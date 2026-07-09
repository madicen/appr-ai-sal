package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/appdirs"
	"github.com/madicen/appr-ai-sal/internal/applog"
)

// registry_user.go loads user-defined specialists from disk and merges them
// into the registry (see registry.go). A user specialist is a pair of files
// under <ConfigDir>/specialists/:
//
//	<name>.json  — the serializable SpecialistSpec (see userSpecJSON)
//	<name>.md    — the system prompt (or the file named by prompt_file)
//
// The prompt-override loader (prompts.go) already proved demand for user
// customization; this lets users add whole new lanes (e.g. performance, i18n)
// without rebuilding the binary. Adding a specialist is: drop in a .json + .md.
//
// Loading is FAIL-OPEN: a malformed or incomplete user spec logs a warning via
// applog and is skipped. It never aborts registry construction, so a bad file
// can never crash a review run.

// UserSpecialistsSubdir is the ConfigDir subdirectory scanned for user-defined
// specialist .json/.md pairs.
const UserSpecialistsSubdir = "specialists"

// userSpecJSON is the on-disk, serializable subset of a SpecialistSpec — the
// Go-only fields (compiled regexes, embedded-prompt refs) are omitted. Unknown
// gate/input names are dropped with a warning rather than failing the load.
type userSpecJSON struct {
	Name          string   `json:"name"`
	Kind          string   `json:"kind"` // "code" | "pr-wide"
	PromptFile    string   `json:"prompt_file,omitempty"`
	Inputs        []string `json:"inputs,omitempty"`
	Gates         []string `json:"gates,omitempty"`
	LanePriority  *int     `json:"lane_priority,omitempty"`
	ArbiterPolicy struct {
		Suppressible bool `json:"suppressible"`
		Demotable    bool `json:"demotable"`
	} `json:"arbiter_policy"`
	Witnessable    bool   `json:"witnessable,omitempty"`
	SeverityLadder string `json:"severity_ladder,omitempty"`
	PRScope        string `json:"pr_scope,omitempty"` // "", "whole-pr", "thread-anchored"
}

// loadUserSpecialists scans <ConfigDir>/specialists for *.json spec files and
// returns the valid ones as SpecialistSpecs. Never returns an error: every
// failure path logs a warning and skips the offending spec (fail-open).
func loadUserSpecialists() []SpecialistSpec {
	dir := filepath.Join(appdirs.ConfigDir(), UserSpecialistsSubdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		// No specialists dir (the common case) or an unreadable one — either
		// way there is simply nothing to merge; only note the unusual case.
		if !os.IsNotExist(err) {
			applog.Warn("user specialists: cannot read dir; skipping", "dir", dir, "err", err)
		}
		return nil
	}
	var out []SpecialistSpec
	seen := map[string]bool{}
	// Sort for deterministic load order (and deterministic collision reporting).
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, fname := range names {
		spec, ok := loadUserSpecialist(dir, fname)
		if !ok {
			continue
		}
		key := specKey(spec.Name)
		if seen[key] {
			applog.Warn("user specialists: duplicate name; skipping", "name", spec.Name, "file", fname)
			continue
		}
		seen[key] = true
		out = append(out, spec)
	}
	return out
}

// loadUserSpecialist parses one <dir>/<fname> spec (+ its prompt file) into a
// SpecialistSpec. Returns ok=false (with a warning already logged) on any
// problem so the caller can skip it.
func loadUserSpecialist(dir, fname string) (SpecialistSpec, bool) {
	path := filepath.Join(dir, fname)
	raw, err := os.ReadFile(path)
	if err != nil {
		applog.Warn("user specialist: cannot read spec; skipping", "file", path, "err", err)
		return SpecialistSpec{}, false
	}
	var js userSpecJSON
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&js); err != nil {
		// Retry leniently: an unknown field should warn, not reject, so a
		// forward-compatible spec still loads. Re-parse without the strict
		// flag; only a genuine syntax/type error rejects the spec.
		if lenientErr := json.Unmarshal(raw, &js); lenientErr != nil {
			applog.Warn("user specialist: malformed JSON; skipping", "file", path, "err", lenientErr)
			return SpecialistSpec{}, false
		}
		applog.Warn("user specialist: unknown field(s) ignored", "file", path, "detail", err.Error())
	}

	name := specKey(js.Name)
	if name == "" {
		applog.Warn("user specialist: missing name; skipping", "file", path)
		return SpecialistSpec{}, false
	}
	if _, isBuiltin := builtinSpecByName(name); isBuiltin {
		applog.Warn("user specialist: name collides with a built-in; skipping", "name", name, "file", path)
		return SpecialistSpec{}, false
	}

	kind, ok := parseKind(js.Kind)
	if !ok {
		applog.Warn("user specialist: invalid kind; skipping", "name", name, "kind", js.Kind, "file", path)
		return SpecialistSpec{}, false
	}

	// Resolve and load the prompt. Default to <name>.md alongside the JSON.
	promptFile := strings.TrimSpace(js.PromptFile)
	if promptFile == "" {
		promptFile = strings.TrimSuffix(fname, filepath.Ext(fname)) + ".md"
	}
	// Keep prompt files inside the specialists dir (no path traversal).
	promptFile = filepath.Base(promptFile)
	promptBytes, err := os.ReadFile(filepath.Join(dir, promptFile))
	if err != nil {
		applog.Warn("user specialist: cannot read prompt; skipping", "name", name, "prompt", promptFile, "file", path, "err", err)
		return SpecialistSpec{}, false
	}
	prompt := strings.TrimRight(string(promptBytes), "\n")
	if strings.TrimSpace(prompt) == "" {
		applog.Warn("user specialist: empty prompt; skipping", "name", name, "prompt", promptFile)
		return SpecialistSpec{}, false
	}
	if ladder := strings.TrimSpace(js.SeverityLadder); ladder != "" {
		// Built-ins bake their ladder into the .md; user specs get theirs
		// appended so the model sees it without editing the prompt file.
		prompt += "\n\nSeverity ladder (for this specialist):\n" + ladder + "\n"
	}

	lane := 50 // between the built-in lanes and the unknown-agent sentinel (99)
	if js.LanePriority != nil {
		lane = *js.LanePriority
	}

	spec := SpecialistSpec{
		Name:           name,
		Kind:           kind,
		PromptSource:   PromptRef{Name: name, Embedded: false},
		Inputs:         parseInputs(js.Inputs, name, path),
		Gates:          parseGates(js.Gates, name, path),
		LanePriority:   lane,
		ArbiterPolicy:  ArbiterPolicy{Suppressible: js.ArbiterPolicy.Suppressible, Demotable: js.ArbiterPolicy.Demotable},
		Witnessable:    js.Witnessable,
		SeverityLadder: strings.TrimSpace(js.SeverityLadder),
		PRScope:        parsePRScope(js.PRScope, kind),
		userDefined:    true,
		prompt:         prompt,
	}
	// A user spec that opts into the actionability gate gets a combined
	// docs+testing deficiency pattern so the gate can actually fire.
	if spec.hasGate(GateActionability) {
		spec.deficiencyPattern = combinedDeficiencyRe
	}
	return spec, true
}

// builtinSpecByName returns the built-in spec for a normalized name.
func builtinSpecByName(name string) (SpecialistSpec, bool) {
	for _, s := range builtinSpecs {
		if specKey(s.Name) == name {
			return s, true
		}
	}
	return SpecialistSpec{}, false
}

func parseKind(s string) (Kind, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(KindCode), "":
		// Default to code — the common case for a new lane.
		return KindCode, true
	case string(KindPRWide), "pr_wide", "prwide":
		return KindPRWide, true
	}
	return "", false
}

func parseInputs(vals []string, name, file string) InputSet {
	var out InputSet
	for _, v := range vals {
		switch Input(strings.ToLower(strings.TrimSpace(v))) {
		case InputDiff:
			out = append(out, InputDiff)
		case InputEvidence:
			out = append(out, InputEvidence)
		case InputChecks:
			out = append(out, InputChecks)
		case InputThreads:
			out = append(out, InputThreads)
		case InputDiscussion:
			out = append(out, InputDiscussion)
		default:
			applog.Warn("user specialist: unknown input ignored", "name", name, "input", v, "file", file)
		}
	}
	if !out.Has(InputDiff) {
		// Every review agent sees the diff; guarantee it.
		out = append(InputSet{InputDiff}, out...)
	}
	return out
}

func parseGates(vals []string, name, file string) []Gate {
	var out []Gate
	for _, v := range vals {
		switch Gate(strings.ToLower(strings.TrimSpace(v))) {
		case GateActionability:
			out = append(out, GateActionability)
		default:
			applog.Warn("user specialist: unknown gate ignored", "name", name, "gate", v, "file", file)
		}
	}
	return out
}

func parsePRScope(s string, kind Kind) PRScope {
	if kind != KindPRWide {
		return ScopeInline
	}
	switch PRScope(strings.ToLower(strings.TrimSpace(s))) {
	case ScopeWholePR:
		return ScopeWholePR
	case ScopeThreadAnchored:
		return ScopeThreadAnchored
	default:
		return ScopeInline
	}
}
