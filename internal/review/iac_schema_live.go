package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// iac_schema_live.go upgrades the IaC schema gate (iac_schema_gate.go) from a
// hard-coded ~13-entry deny table to LIVE provider schema when terraform is
// present in the worktree (Q5.c). `terraform providers schema -json` reports
// the exact argument set every resource type accepts, so the gate can catch
// "add argument X to resource Y" false positives across ALL providers/resources
// the repo uses — not just the seed AWS-tagging table.
//
// It is strictly additive and fail-open:
//   - terraform absent, uninitialised, or the call fails/times out → the gate
//     falls back to fallbackUnsupportedHCLArguments (the original table).
//   - the call is expensive, so it runs at most once per worktree and the
//     parsed result is cached for the whole run.

// terraformSchemaTimeout bounds the (expensive) providers-schema call. On
// timeout the gate falls back to the static table.
const terraformSchemaTimeout = 25 * time.Second

// tfSchemaEntry memoises one worktree's parsed provider schema. A nil data map
// after once.Do means "no usable live schema — use the fallback table".
type tfSchemaEntry struct {
	once sync.Once
	data map[string]map[string]bool // resourceType(lower) → accepted arg names(lower)
}

// tfSchemaCache is keyed by cleaned worktree path so repeat gate invocations
// across specialists in one run share a single terraform call.
var tfSchemaCache sync.Map // string → *tfSchemaEntry

// terraformFetchSchema is indirected so tests can inject a canned schema
// without a terraform binary. It returns the parsed resource-arg map, or nil
// when terraform is unavailable / the call fails.
var terraformFetchSchema = fetchTerraformProvidersSchema

// loadTerraformResourceArgs returns the accepted-argument set for rtype from
// the worktree's live provider schema, or nil when the live schema is
// unavailable OR does not describe rtype (either case → the caller falls back
// to the static table). Results are cached per worktree.
func loadTerraformResourceArgs(worktree, rtype string) map[string]bool {
	worktree = strings.TrimSpace(worktree)
	if worktree == "" {
		return nil
	}
	key := filepath.Clean(worktree)
	ev, _ := tfSchemaCache.LoadOrStore(key, &tfSchemaEntry{})
	entry := ev.(*tfSchemaEntry)
	entry.once.Do(func() {
		entry.data = terraformFetchSchema(key)
	})
	if entry.data == nil {
		return nil
	}
	return entry.data[strings.ToLower(strings.TrimSpace(rtype))]
}

// fetchTerraformProvidersSchema runs `terraform providers schema -json` in
// worktree and parses it. Returns nil (never an error) so every failure mode —
// binary missing, module not `terraform init`ed, timeout, unparseable output —
// degrades to the static-table fallback.
func fetchTerraformProvidersSchema(worktree string) map[string]map[string]bool {
	if _, err := exec.LookPath("terraform"); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), terraformSchemaTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "terraform", "providers", "schema", "-json")
	cmd.Dir = worktree
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	schema, err := parseTerraformProvidersSchema(out)
	if err != nil {
		return nil
	}
	return schema
}

// parseTerraformProvidersSchema turns `terraform providers schema -json` output
// into resourceType → accepted-arg set (attributes ∪ nested block names),
// unioned across every provider. Names are lowercased to match the gate's
// candidate extraction. It is a pure function so the live path is testable with
// canned schema JSON (terraform itself is not needed).
func parseTerraformProvidersSchema(b []byte) (map[string]map[string]bool, error) {
	var doc struct {
		ProviderSchemas map[string]struct {
			ResourceSchemas map[string]struct {
				Block struct {
					Attributes map[string]json.RawMessage `json:"attributes"`
					BlockTypes map[string]json.RawMessage `json:"block_types"`
				} `json:"block"`
			} `json:"resource_schemas"`
		} `json:"provider_schemas"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	out := map[string]map[string]bool{}
	for _, ps := range doc.ProviderSchemas {
		for rtype, rs := range ps.ResourceSchemas {
			key := strings.ToLower(strings.TrimSpace(rtype))
			set := out[key]
			if set == nil {
				set = map[string]bool{}
				out[key] = set
			}
			for a := range rs.Block.Attributes {
				set[strings.ToLower(a)] = true
			}
			for bt := range rs.Block.BlockTypes {
				set[strings.ToLower(bt)] = true
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("terraform providers schema: no resource schemas found")
	}
	return out, nil
}

// firstSchemaRejectedArg returns the first argument the finding proposes adding
// that the enclosing resource type does not accept, together with whether the
// determination came from LIVE provider schema (true) or the static fallback
// table (false). Empty arg means "nothing to strip".
//
// Precedence: when live schema describes rtype, its allow-list is authoritative
// (an argument absent from it is rejected). Otherwise the static deny table is
// consulted. This is the Q5.c "consult live schema when available, fall back to
// the table" behaviour.
func firstSchemaRejectedArg(f Finding, rtype, worktree string) (arg string, live bool) {
	proposed := proposedArguments(f)
	if len(proposed) == 0 {
		return "", false
	}
	if allowed := loadTerraformResourceArgs(worktree, rtype); allowed != nil {
		for _, a := range proposed {
			if !allowed[a] {
				return a, true
			}
		}
		// Resource is described by live schema and accepts every proposed
		// argument → definitively nothing to strip.
		return "", true
	}
	if unsupported, ok := fallbackUnsupportedHCLArguments[rtype]; ok {
		if a := firstUnsupportedArg(f, unsupported); a != "" {
			return a, false
		}
	}
	return "", false
}
