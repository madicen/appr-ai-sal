package review

import (
	"context"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// repairCandidateFinding anchors at the "resources:" line (12) of
// unitSuffixDiff with no suggestion — the shape the repair pass targets.
func repairCandidateFinding() Finding {
	return Finding{
		Path:     "deploy/app.yaml",
		Line:     12,
		Side:     "RIGHT",
		Severity: SeverityWarning,
		Comment:  "Use binary units for the memory limit.",
	}
}

func TestSelectRepairCandidates(t *testing.T) {
	files := ParseDiff(unitSuffixDiff)
	withSuggestion := repairCandidateFinding()
	withSuggestion.Suggestion = "        memory: 717Mi"
	prWide := repairCandidateFinding()
	prWide.Path = ""
	prWide.Line = 0
	offHunk := repairCandidateFinding()
	offHunk.Line = 9999

	findings := []Finding{repairCandidateFinding(), withSuggestion, prWide, offHunk}
	got := selectRepairCandidates(findings, files)
	if len(got) != 1 || got[0] != 0 {
		t.Fatalf("expected only index 0 to be a candidate, got %v", got)
	}
}

func TestBuildRepairPromptIncludesNumberedHunkAndComment(t *testing.T) {
	files := ParseDiff(unitSuffixDiff)
	findings := []Finding{repairCandidateFinding()}
	items := buildRepairItems(findings, files, selectRepairCandidates(findings, files))
	if len(items) != 1 {
		t.Fatalf("expected 1 repair item, got %d", len(items))
	}
	_, user := buildRepairPrompt("design", items)
	for _, want := range []string{"id=0", "Use binary units for the memory limit.", "13| ", "memory: 717M", "current_anchor_line: 12"} {
		if !strings.Contains(user, want) {
			t.Fatalf("repair prompt missing %q:\n%s", want, user)
		}
	}
}

func TestParseRepairResponseAcceptAndDecline(t *testing.T) {
	raw := `{"repairs":[{"id":0,"anchor_line":13,"replacement":"        memory: 717Mi"},{"id":1,"decline":true},{"id":2,"anchor_line":0,"replacement":"x"}]}`
	res, err := parseRepairResponse(raw)
	if err != nil {
		t.Fatalf("parseRepairResponse: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 accepted repair (decline and anchor<=0 dropped), got %d: %v", len(res), res)
	}
	r, ok := res[0]
	if !ok || r.AnchorLine != 13 || strings.TrimSpace(r.Replacement) != "memory: 717Mi" {
		t.Fatalf("unexpected repair result: %+v", res)
	}
}

func TestParseRepairResponseToleratesFencing(t *testing.T) {
	raw := "```json\n{\"repairs\":[{\"id\":0,\"anchor_line\":13,\"replacement\":\"        memory: 717Mi\"}]}\n```"
	res, err := parseRepairResponse(raw)
	if err != nil {
		t.Fatalf("parseRepairResponse (fenced): %v", err)
	}
	if _, ok := res[0]; !ok {
		t.Fatalf("expected fenced JSON to parse, got %v", res)
	}
}

func TestApplyRepairsRelocatesAndFlags(t *testing.T) {
	files := ParseDiff(unitSuffixDiff)
	findings := []Finding{repairCandidateFinding()}
	results := map[int]repairResult{0: {AnchorLine: 13, Replacement: "        memory: 717Mi"}}
	out := applyRepairs(findings, files, results)
	if out[0].Line != 13 {
		t.Fatalf("expected anchor moved to line 13, got %d", out[0].Line)
	}
	if out[0].AnchorRelocatedFrom != 12 {
		t.Fatalf("expected AnchorRelocatedFrom=12, got %d", out[0].AnchorRelocatedFrom)
	}
	if strings.TrimSpace(out[0].Suggestion) != "memory: 717Mi" {
		t.Fatalf("expected repaired suggestion, got %q", out[0].Suggestion)
	}
	if !out[0].SuggestionRepaired {
		t.Fatalf("SuggestionRepaired should be set on a repaired finding")
	}
}

func TestApplyRepairsRollsBackNoOpReplacement(t *testing.T) {
	files := ParseDiff(unitSuffixDiff)
	findings := []Finding{repairCandidateFinding()}
	// Replacement equals the anchor line verbatim → suggestionBreaksFile
	// flags a no-op, so the repair must be rejected.
	results := map[int]repairResult{0: {AnchorLine: 13, Replacement: "        memory: 717M"}}
	out := applyRepairs(findings, files, results)
	if strings.TrimSpace(out[0].Suggestion) != "" {
		t.Fatalf("no-op repair must be rolled back, got suggestion %q", out[0].Suggestion)
	}
	if out[0].SuggestionRepaired {
		t.Fatalf("SuggestionRepaired must stay false on a rejected repair")
	}
	if out[0].Line != 12 {
		t.Fatalf("rejected repair must not move the anchor, got Line=%d", out[0].Line)
	}
}

func TestApplyRepairsRollsBackAnchorKindMismatch(t *testing.T) {
	files := ParseDiff(screenshotIngressPipelineDiff)
	f := Finding{
		Path:     "internal/ingress/ingress_pipeline.go",
		Line:     76, // a comment-only line
		Side:     "RIGHT",
		Severity: SeverityWarning,
		Comment:  "The function name should be in snake_case according to naming conventions.",
	}
	results := map[int]repairResult{0: {AnchorLine: 76, Replacement: `ingestValidatorFunc{N: "x", F: ingressStripHTML}`}}
	out := applyRepairs([]Finding{f}, files, results)
	if out[0].Suggestion != "" {
		t.Fatalf("anchor-kind mismatch repair must be rolled back, got %q", out[0].Suggestion)
	}
	if out[0].SuggestionRepaired {
		t.Fatalf("SuggestionRepaired must stay false on a rolled-back repair")
	}
}

func TestRepairMissingSuggestionsAppliesRepair(t *testing.T) {
	prev := repairComplete
	defer func() { repairComplete = prev }()
	calls := 0
	repairComplete = func(ctx context.Context, cfg *aiconfig.Config, system, user, worktree string) (string, error) {
		calls++
		return `{"repairs":[{"id":0,"anchor_line":13,"replacement":"        memory: 717Mi"}]}`, nil
	}
	files := ParseDiff(unitSuffixDiff)
	findings := []Finding{repairCandidateFinding()}
	out := repairMissingSuggestions(context.Background(), aiconfig.DefaultConfig(), "", "design", findings, files)
	if calls != 1 {
		t.Fatalf("expected exactly 1 model call, got %d", calls)
	}
	if !out[0].SuggestionRepaired || strings.TrimSpace(out[0].Suggestion) != "memory: 717Mi" {
		t.Fatalf("expected repaired suggestion, got repaired=%v suggestion=%q", out[0].SuggestionRepaired, out[0].Suggestion)
	}
}

func TestRepairMissingSuggestionsSkipsWhenNoCandidates(t *testing.T) {
	prev := repairComplete
	defer func() { repairComplete = prev }()
	calls := 0
	repairComplete = func(ctx context.Context, cfg *aiconfig.Config, system, user, worktree string) (string, error) {
		calls++
		return "{}", nil
	}
	files := ParseDiff(unitSuffixDiff)
	f := repairCandidateFinding()
	f.Suggestion = "        memory: 717Mi" // already has one
	out := repairMissingSuggestions(context.Background(), aiconfig.DefaultConfig(), "", "design", []Finding{f}, files)
	if calls != 0 {
		t.Fatalf("no candidates should mean no model call, got %d calls", calls)
	}
	if strings.TrimSpace(out[0].Suggestion) != "memory: 717Mi" {
		t.Fatalf("existing suggestion should be untouched, got %q", out[0].Suggestion)
	}
}

func TestRepairMissingSuggestionsFailsOpenOnError(t *testing.T) {
	prev := repairComplete
	defer func() { repairComplete = prev }()
	repairComplete = func(ctx context.Context, cfg *aiconfig.Config, system, user, worktree string) (string, error) {
		return "", context.DeadlineExceeded
	}
	files := ParseDiff(unitSuffixDiff)
	findings := []Finding{repairCandidateFinding()}
	out := repairMissingSuggestions(context.Background(), aiconfig.DefaultConfig(), "", "design", findings, files)
	if strings.TrimSpace(out[0].Suggestion) != "" || out[0].SuggestionRepaired {
		t.Fatalf("API error must leave findings unchanged, got suggestion=%q repaired=%v", out[0].Suggestion, out[0].SuggestionRepaired)
	}
}

func TestReviewCommentBodyDisclosesRepairedSuggestion(t *testing.T) {
	f := repairCandidateFinding()
	f.Line = 13
	f.Suggestion = "        memory: 717Mi"
	f.SuggestionRepaired = true
	body := ReviewCommentBody("design", f)
	if !strings.Contains(body, "suggestion-repair pass") {
		t.Fatalf("posted body must disclose a repaired suggestion, got:\n%s", body)
	}
	if !strings.Contains(body, "```suggestion") {
		t.Fatalf("posted body must still contain the suggestion block, got:\n%s", body)
	}
}
