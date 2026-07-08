// Package conventionwitness runs a per-finding evidence pass between the
// code-reviewing specialists and the repo arbiter. For each testing, docs,
// or tech finding it answers a single question — "does the rest of this repo
// do what this finding asks for?" — using the auto-harvested per-PR evidence
// pack. The arbiter consumes the verdicts to decide whether to suppress,
// demote, or keep each finding.
package conventionwitness

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/appdirs"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/llmjson"
	"github.com/madicen/appr-ai-sal/internal/review/findingkey"
)

//go:embed prompts
var promptFS embed.FS

// Verdict is the witness's classification of one finding.
type Verdict string

const (
	// VerdictCongruent — the finding aligns with the repo's *existing
	// under-coverage*: the rest of the repo doesn't do what the finding
	// asks for. The arbiter is willing to demote or suppress here.
	VerdictCongruent Verdict = "congruent"
	// VerdictDivergent — the rest of the repo does what the finding asks
	// for; this PR is bucking the trend. The arbiter should keep the finding.
	VerdictDivergent Verdict = "divergent"
	// VerdictUnknown — the evidence pack lacks the signal needed to decide.
	VerdictUnknown Verdict = "unknown"
)

// NormalizeVerdict maps free-text values to a canonical Verdict (or "" if
// unknown shape).
func NormalizeVerdict(s string) Verdict {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "congruent", "agrees", "matches":
		return VerdictCongruent
	case "divergent", "diverges", "conflicts":
		return VerdictDivergent
	case "unknown", "unsure", "no_evidence", "insufficient":
		return VerdictUnknown
	}
	return ""
}

// Witness is one per-finding verdict + supporting one-line citation.
type Witness struct {
	Specialist string  `json:"specialist"`
	Path       string  `json:"path"`
	Line       int     `json:"line"`
	Side       string  `json:"side,omitempty"`
	Verdict    Verdict `json:"verdict"`
	Citation   string  `json:"citation,omitempty"`
}

// FindingInput is one input finding the witness should classify. Mirrors
// the shape of review.Finding but kept local so this package has no import
// dependency on review (which would be a cycle).
type FindingInput struct {
	Specialist string `json:"specialist"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Side       string `json:"side,omitempty"`
	Severity   string `json:"severity"`
	Comment    string `json:"comment"`
}

// Result wraps the witness call's output and any error.
type Result struct {
	Witnesses []Witness
	Err       error
}

// PrWideRef summarises the pull request the witnesses are evaluating.
type PrWideRef struct {
	Repository string
	Number     int
	Title      string
}

// Run calls the LLM with the convention-witness prompt and returns one
// Witness per input finding. Empty input returns an empty Result with no
// LLM call. The agent is invoked even when evidence is empty; the prompt
// instructs the model to emit `unknown` verdicts in that case so the
// arbiter still has a row per finding.
func Run(ctx context.Context, cfg *aiconfig.Config, complete ai.CompleteFunc, worktree string, pr PrWideRef, findings []FindingInput, evidence string) Result {
	if len(findings) == 0 {
		return Result{Witnesses: nil}
	}
	if complete == nil {
		return Result{Err: fmt.Errorf("conventionwitness.Run: nil complete func")}
	}
	system, err := loadPrompt()
	if err != nil {
		return Result{Err: err}
	}
	user := buildUserPrompt(pr, findings, evidence)
	// The witness emits strict JSON; opt into native JSON mode and hand the
	// per-agent schema to schema-capable providers (Gemini responseSchema).
	// The injected complete func (review.Complete) reads both off the context
	// and requests json_object / responseMimeType / responseSchema where the
	// provider supports it; schema-less providers ignore the schema.
	out, err := complete(ai.WithJSONSchema(ai.WithJSONMode(ctx), witnessSchema()), cfg, system, user, worktree)
	if err != nil {
		return Result{Err: fmt.Errorf("convention witness: %w", err)}
	}
	parsed, err := parseWitnessJSON(out)
	if err != nil {
		return Result{Err: fmt.Errorf("convention witness: parse: %w (raw: %s)", err, truncate(out, 400))}
	}
	cleaned := normalizeAndAlign(parsed.Witnesses, findings)
	return Result{Witnesses: cleaned}
}

type witnessJSON struct {
	Witnesses []Witness `json:"witnesses"`
}

// witnessSchema is the per-agent JSON schema for the witness output (R5),
// kept to the OpenAPI-3.0 subset Gemini's responseSchema accepts (no $schema,
// no additionalProperties). The verdict enum is sourced from the canonical
// Verdict constants so it can never drift from NormalizeVerdict. Schema-less
// JSON providers ignore it and use plain json_object mode.
var witnessSchema = sync.OnceValue(func() json.RawMessage {
	witness := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"specialist": map[string]any{"type": "string"},
			"path":       map[string]any{"type": "string"},
			"line":       map[string]any{"type": "integer"},
			"side":       map[string]any{"type": "string", "enum": []string{"LEFT", "RIGHT"}},
			"verdict": map[string]any{"type": "string", "enum": []string{
				string(VerdictCongruent), string(VerdictDivergent), string(VerdictUnknown),
			}},
			"citation": map[string]any{"type": "string"},
		},
		"required": []string{"specialist", "verdict"},
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"witnesses": map[string]any{"type": "array", "items": witness},
		},
		"required": []string{"witnesses"},
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	return b
})

func parseWitnessJSON(s string) (*witnessJSON, error) {
	v, err := llmjson.Parse[witnessJSON](s)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// normalizeAndAlign drops witnesses that don't reference an input finding
// and ensures a witness is emitted for every input (defaulting to
// VerdictUnknown when the model omitted one).
func normalizeAndAlign(out []Witness, in []FindingInput) []Witness {
	byKey := map[string]Witness{}
	for _, w := range out {
		side := strings.ToUpper(strings.TrimSpace(w.Side))
		if side == "" {
			side = "RIGHT"
		}
		k := findingkey.New(w.Specialist, w.Path, w.Line, w.Side).String()
		v := NormalizeVerdict(string(w.Verdict))
		if v == "" {
			v = VerdictUnknown
		}
		byKey[k] = Witness{
			Specialist: strings.ToLower(strings.TrimSpace(w.Specialist)),
			Path:       strings.TrimSpace(w.Path),
			Line:       w.Line,
			Side:       side,
			Verdict:    v,
			Citation:   strings.TrimSpace(w.Citation),
		}
	}
	aligned := make([]Witness, 0, len(in))
	for _, f := range in {
		side := strings.ToUpper(strings.TrimSpace(f.Side))
		if side == "" {
			side = "RIGHT"
		}
		k := findingkey.New(f.Specialist, f.Path, f.Line, f.Side).String()
		if w, ok := byKey[k]; ok {
			aligned = append(aligned, w)
			continue
		}
		aligned = append(aligned, Witness{
			Specialist: strings.ToLower(strings.TrimSpace(f.Specialist)),
			Path:       strings.TrimSpace(f.Path),
			Line:       f.Line,
			Side:       side,
			Verdict:    VerdictUnknown,
			Citation:   "(no witness emitted by the model)",
		})
	}
	return aligned
}

func buildUserPrompt(pr PrWideRef, findings []FindingInput, evidence string) string {
	var b strings.Builder
	if pr.Repository != "" {
		fmt.Fprintf(&b, "PR: %s#%d\n", pr.Repository, pr.Number)
	} else {
		fmt.Fprintf(&b, "PR: #%d\n", pr.Number)
	}
	if pr.Title != "" {
		b.WriteString("Title: " + pr.Title + "\n")
	}
	b.WriteString("\n## Per-PR evidence pack\n\n")
	body := strings.TrimSpace(evidence)
	if body == "" {
		b.WriteString("_(no evidence harvested for this PR — emit `unknown` verdicts.)_\n\n")
	} else {
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	b.WriteString("## Findings to classify (one witness per finding)\n\n")
	encoded, _ := json.MarshalIndent(struct {
		Findings []FindingInput `json:"findings"`
	}{findings}, "", "  ")
	b.WriteString("```json\n")
	b.Write(encoded)
	b.WriteString("\n```\n\n")
	b.WriteString("Return only the JSON object specified in your system instructions.")
	return b.String()
}

func loadPrompt() (string, error) {
	if override, ok, err := readOverride(); err != nil {
		return "", err
	} else if ok {
		return override, nil
	}
	b, err := fs.ReadFile(promptFS, "prompts/convention-witness.md")
	if err != nil {
		return "", fmt.Errorf("load convention-witness prompt: %w", err)
	}
	return string(b), nil
}

// OverridePath returns the user-writable path that overrides the embedded prompt.
func OverridePath() string {
	return filepath.Join(appdirs.ConfigDir(), "prompts", "convention-witness.md")
}

func readOverride() (string, bool, error) {
	p := OverridePath()
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read override %s: %w", p, err)
	}
	return string(b), true, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// FormatMarkdown renders the witnesses as a markdown bullet list for
// inclusion in the arbiter prompt. Returns "" for empty input.
func FormatMarkdown(witnesses []Witness) string {
	if len(witnesses) == 0 {
		return ""
	}
	var b strings.Builder
	for _, w := range witnesses {
		side := w.Side
		if side == "" {
			side = "RIGHT"
		}
		fmt.Fprintf(&b, "- [%s] %s `%s:%d` side=%s — verdict: **%s**",
			w.Specialist, w.Verdict, w.Path, w.Line, side, w.Verdict)
		if w.Citation != "" {
			fmt.Fprintf(&b, "; citation: %s", w.Citation)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// _ keeps gh imported for the PrWideRef typedef stability if callers want
// to wire in a real PR. Currently PrWideRef is independent of gh.PR but
// future extensions may use gh fields directly.
var _ = gh.PR{}
