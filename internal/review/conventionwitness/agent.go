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

// witnessStageKey is the stage_models / ensemble key for the convention
// witness (Q7). Kept local because this package cannot import internal/review
// (which imports it); it must equal review.StageWitness.
const witnessStageKey = "witness"

// Verdict is the witness's classification of one finding.
type Verdict string

const (
	// VerdictContradictsFinding — the repo's own evidence *contradicts* what
	// the finding asks for: the rest of the repo doesn't do it (sibling files
	// lack the tests/docs/token, prior PRs shipped without them). The finding
	// is technically reasonable but asks the author to exceed the repo's own
	// habit, so the arbiter is willing to demote or suppress here.
	//
	// Q3.8: this was previously named "congruent" (from the finding's
	// perspective: congruent with the repo's under-coverage), which read
	// backwards to most people. The new name is from the evidence's
	// perspective. NormalizeVerdict still parses the old "congruent" spelling.
	VerdictContradictsFinding Verdict = "contradicts_finding"
	// VerdictSupportsFinding — the repo's own evidence *supports* the finding:
	// the rest of the repo already does what it asks (sibling tests/docs
	// exist, prior PRs added them, the token is present in most siblings), and
	// this PR bucks the trend. The arbiter should keep the finding.
	//
	// Q3.8: previously named "divergent" (this PR diverges from the repo's
	// habit). NormalizeVerdict still parses the old "divergent" spelling.
	VerdictSupportsFinding Verdict = "supports_finding"
	// VerdictUnknown — the evidence pack lacks the signal needed to decide.
	VerdictUnknown Verdict = "unknown"
)

// NormalizeVerdict maps free-text values to a canonical Verdict (or "" if
// unknown shape). It accepts both the current terminology
// (supports_finding / contradicts_finding) and the pre-Q3.8 spelling
// (divergent / congruent) plus their synonyms, so a prompt override or a model
// trained on the old vocabulary still parses (the compat alias required by
// Q3.8).
func NormalizeVerdict(s string) Verdict {
	switch strings.ToLower(strings.TrimSpace(s)) {
	// contradicts_finding (was "congruent"): repo does NOT do what the finding
	// asks → arbiter may soften.
	case "contradicts_finding", "contradicts", "congruent", "agrees", "matches":
		return VerdictContradictsFinding
	// supports_finding (was "divergent"): repo DOES do what the finding asks;
	// this PR bucks the trend → arbiter keeps.
	case "supports_finding", "supports", "divergent", "diverges", "conflicts":
		return VerdictSupportsFinding
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
	// Q7: route the witness to its own model when configured
	// (stage_models["witness"] / "default"); a no-op clone when unrouted.
	// Running the witness on a different model family than the specialist it
	// audits decorrelates their hallucinations. Applied here so every caller
	// (runner, evals) picks up witness routing uniformly.
	cfg = cfg.ForStage(witnessStageKey)
	system, err := loadPrompt()
	if err != nil {
		return Result{Err: err}
	}
	// Q3.5: pass the review intensity through so the witness can calibrate
	// how much scrutiny to apply when the evidence is ambiguous. cfg.ForStage
	// preserves ReviewStrictness (it only swaps the model), so this is the
	// user's chosen level.
	user := buildUserPrompt(pr, findings, evidence, cfg.ReviewStrictness)
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
				string(VerdictSupportsFinding), string(VerdictContradictsFinding), string(VerdictUnknown),
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

func buildUserPrompt(pr PrWideRef, findings []FindingInput, evidence string, strictness aiconfig.ReviewStrictness) string {
	var b strings.Builder
	if pr.Repository != "" {
		fmt.Fprintf(&b, "PR: %s#%d\n", pr.Repository, pr.Number)
	} else {
		fmt.Fprintf(&b, "PR: #%d\n", pr.Number)
	}
	if pr.Title != "" {
		b.WriteString("Title: " + pr.Title + "\n")
	}
	// Q3.5: calibrate the witness's scrutiny to the chosen intensity. Empty
	// at the default (balanced) level, so a balanced run's witness prompt is
	// byte-identical to pre-Q3.5.
	if sb := strictnessScrutinyBlock(strictness); sb != "" {
		b.WriteString("\n")
		b.WriteString(sb)
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

// strictnessScrutinyBlock returns a short "Review intensity" section (Q3.5)
// that tells the witness how much benefit of the doubt to give a finding when
// the evidence is ambiguous. It returns "" for the default (balanced) level so
// a balanced run's witness prompt is byte-identical to pre-Q3.5; the embedded
// prompt was authored against the balanced posture, so the section would be
// noise there. The block never asks the witness to invent evidence — it only
// governs how it breaks genuine ties.
func strictnessScrutinyBlock(s aiconfig.ReviewStrictness) string {
	switch s {
	case aiconfig.ReviewStrict:
		return "## Review intensity: strict\n\n" +
			"The reviewer chose the **strict** intensity: they want to see every actionable finding. Apply extra scrutiny before concluding `contradicts_finding` (which invites the arbiter to soften the finding). When the evidence is genuinely ambiguous, prefer `unknown` over `contradicts_finding` so the arbiter keeps the finding. Only assert `contradicts_finding` when the pack clearly shows the repo does not do what the finding asks.\n"
	case aiconfig.ReviewLenient, aiconfig.ReviewCriticalOnly:
		return "## Review intensity: lenient\n\n" +
			"The reviewer chose a **lenient** intensity: they only want higher-severity issues surfaced. When the evidence leans toward the repo not doing what a finding asks, you may conclude `contradicts_finding` on a reasonable (not just overwhelming) reading of the pack, so the arbiter can calibrate the finding away. Still never invent evidence: fall back to `unknown` when the pack is silent.\n"
	default: // balanced — byte-identical to pre-Q3.5
		return ""
	}
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
