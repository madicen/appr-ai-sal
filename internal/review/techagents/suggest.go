package techagents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/review/repocontext"
)

// ErrNoRepoAccess is returned by Suggest when no local clone or worktree is
// available to analyse. Suggestion needs the repo's actual files (manifests,
// configs) to detect technologies; unlike Generate it does NOT fall back to
// an empty temp dir, since that would yield no useful candidates.
var ErrNoRepoAccess = errors.New("techagents: no local clone or worktree available to analyse; configure a local clone path for this repo")

// Candidate is one technology the suggester proposes for a repo. It mirrors
// the inputs the generator needs (Tech, Label, Seed) plus a short Rationale
// describing the evidence the model keyed on, shown to the user during the
// approve/deny step.
type Candidate struct {
	Tech      string `json:"tech"`
	Label     string `json:"label"`
	Seed      string `json:"seed"`
	Rationale string `json:"rationale"`
}

// SuggestOpts collects inputs for a single suggestion pass. Existing lists
// canonical tech keys already configured for the repo so the suggester can
// drop duplicates the user already has.
type SuggestOpts struct {
	AICfg    *aiconfig.Config
	RC       *repoconfig.Config
	Owner    string
	Repo     string
	Worktree string // optional; LocalRoot is used when empty
	Complete CompleteFunc
	Existing []string
}

// Suggest analyses the repo and returns a deduped list of technology
// candidates the user can approve. It does NOT persist anything; approved
// candidates are fed back through Generate + SaveAgent by the caller.
func Suggest(ctx context.Context, opts SuggestOpts) ([]Candidate, error) {
	if opts.Complete == nil {
		return nil, fmt.Errorf("techagents.Suggest: Complete is required")
	}
	if opts.AICfg == nil {
		return nil, fmt.Errorf("techagents.Suggest: AICfg is required")
	}
	owner := strings.ToLower(strings.TrimSpace(opts.Owner))
	repo := strings.ToLower(strings.TrimSpace(opts.Repo))
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("techagents.Suggest: empty owner/repo")
	}

	rc := opts.RC
	if rc == nil {
		rc = repoconfig.Default()
	}

	worktree := strings.TrimSpace(opts.Worktree)
	if worktree == "" {
		worktree = rc.LocalRootFor(owner, repo)
	}
	if !isDir(worktree) {
		return nil, ErrNoRepoAccess
	}

	bundle, err := repocontext.Build(ctx, repocontext.Options{
		Worktree:         worktree,
		LocalRoot:        rc.LocalRootFor(owner, repo),
		MaxBytes:         rc.MaxBytes,
		IncludeManifests: true,
	})
	if err != nil {
		return nil, fmt.Errorf("techagents.Suggest: build context: %w", err)
	}
	if strings.TrimSpace(bundle) == "" {
		return nil, ErrNoRepoAccess
	}

	system, err := loadSuggesterPrompt()
	if err != nil {
		return nil, err
	}
	user := buildSuggesterUserPrompt(owner, repo, bundle)

	out, err := opts.Complete(ctx, opts.AICfg, system, user, worktree)
	if err != nil {
		return nil, fmt.Errorf("complete tech suggestions: %w", err)
	}
	cands, err := parseCandidates(out)
	if err != nil {
		return nil, fmt.Errorf("techagents.Suggest: parse: %w", err)
	}
	return dedupeCandidates(cands, opts.Existing), nil
}

func isDir(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// parseCandidates extracts the JSON candidate array from noisy model output.
// It tolerates a leading ```json fence and surrounding prose.
func parseCandidates(raw string) ([]Candidate, error) {
	s := strings.TrimSpace(raw)
	var cands []Candidate
	if err := json.Unmarshal([]byte(s), &cands); err == nil {
		return cands, nil
	}
	if arr := extractJSONArray(s); arr != "" {
		if err := json.Unmarshal([]byte(arr), &cands); err == nil {
			return cands, nil
		}
	}
	return nil, fmt.Errorf("no JSON array found in model output")
}

// extractJSONArray returns the first balanced top-level [...] block in s,
// skipping brackets that appear inside JSON strings.
func extractJSONArray(s string) string {
	start := strings.IndexByte(s, '[')
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// dedupeCandidates canonicalises tech keys, drops entries with empty keys,
// removes duplicates within the list, and excludes any tech already present
// in existing (canonicalised).
func dedupeCandidates(in []Candidate, existing []string) []Candidate {
	skip := map[string]struct{}{}
	for _, e := range existing {
		if c := CanonicalTech(e); c != "" {
			skip[c] = struct{}{}
		}
	}
	seen := map[string]struct{}{}
	out := make([]Candidate, 0, len(in))
	for _, c := range in {
		key := CanonicalTech(c.Tech)
		if key == "" {
			// Fall back to the label when tech is missing/garbage.
			key = CanonicalTech(c.Label)
		}
		if key == "" {
			continue
		}
		if _, ok := skip[key]; ok {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		label := strings.TrimSpace(c.Label)
		if label == "" {
			label = key
		}
		out = append(out, Candidate{
			Tech:      key,
			Label:     label,
			Seed:      strings.TrimSpace(c.Seed),
			Rationale: strings.TrimSpace(c.Rationale),
		})
	}
	return out
}

func loadSuggesterPrompt() (string, error) {
	if override, ok, err := readSuggesterOverride(); err != nil {
		return "", err
	} else if ok {
		return override, nil
	}
	b, err := fs.ReadFile(promptFS, "prompts/tech-suggester.md")
	if err != nil {
		return "", fmt.Errorf("load tech-suggester prompt: %w", err)
	}
	return string(b), nil
}

// SuggesterPromptOverridePath is where users may write a custom suggester
// prompt to replace the embedded one.
func SuggesterPromptOverridePath() string {
	return filepath.Join(configDir(), "prompts", "tech-suggester.md")
}

func readSuggesterOverride() (string, bool, error) {
	p := SuggesterPromptOverridePath()
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read override %s: %w", p, err)
	}
	return string(b), true, nil
}

func buildSuggesterUserPrompt(owner, repo, bundle string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Repository: %s/%s\n\n", owner, repo)
	b.WriteString("Propose the technologies worth a dedicated review brief for this repository, grounded only in the bundle below. Return ONLY the JSON array described in your system instructions.\n\n")
	b.WriteString("## Repository convention + manifest bundle (auto-harvested)\n\n")
	if strings.TrimSpace(bundle) == "" {
		b.WriteString("_(no files were found under the worktree.)_\n")
	} else {
		b.WriteString(bundle)
		b.WriteString("\n")
	}
	return b.String()
}
