package review

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/llmjson"
)

// repairComplete is indirected through a package var purely so tests can
// inject a deterministic completer. Production always uses Complete. Its type
// is the shared ai.CompleteFunc — no per-package inference typedef.
var repairComplete ai.CompleteFunc = Complete

// repairItem is one finding handed to the suggestion-repair model: the
// reviewer's prose plus the numbered post-image lines of the hunk it anchors
// to, so the model only has to pick an anchor line number and write the
// replacement.
type repairItem struct {
	id        int // index into the findings slice
	path      string
	comment   string
	anchorNo  int
	hunkLines []repairHunkLine
}

type repairHunkLine struct {
	no   int
	text string
}

// repairResult is the model's verdict for one item: either a concrete
// {anchorLine, replacement} or a decline.
type repairResult struct {
	AnchorLine  int    `json:"anchor_line"`
	Replacement string `json:"replacement"`
	Decline     bool   `json:"decline"`
}

// repairMissingSuggestions is the batched AI fallback that runs after the
// deterministic gates (validation, relocation, synthesis). For every inline
// finding still missing a usable one-click fix it asks a focused model call
// to pick the correct anchor line and write the replacement, then re-validates
// each repair through the same safety gates a model-authored suggestion goes
// through.
//
// It is strictly fail-open: it only ever ADDS a re-validated suggestion to a
// finding that had none. Any error (no candidates, API failure, malformed
// response, a repair that fails re-validation) leaves the findings exactly as
// they were — a missing suggestion is the status quo and must never block or
// alter the review. The model call only fires when there is at least one
// candidate, so clean specialists cost nothing extra.
// repairMissingSuggestions returns the (possibly updated) findings plus
// telemetry: fired is the number of suggestion-less findings sent to the
// repair model (0 when the model call was skipped entirely), and succeeded is
// the number that came back with a re-validated one-click suggestion. The
// caller surfaces these as Progress events so a run's hidden repair calls are
// observable.
func repairMissingSuggestions(ctx context.Context, cfg *aiconfig.Config, worktree, name string, findings []Finding, files []FileDiff) (out []Finding, fired int, succeeded int) {
	idxs := selectRepairCandidates(findings, files)
	if len(idxs) == 0 {
		return findings, 0, 0
	}
	items := buildRepairItems(findings, files, idxs)
	if len(items) == 0 {
		return findings, 0, 0
	}
	fired = len(items)
	systemPrompt, userPrompt := buildRepairPrompt(name, items)
	resp, err := repairComplete(ctx, cfg, systemPrompt, userPrompt, worktree)
	if err != nil {
		return findings, fired, 0
	}
	results, err := parseRepairResponse(resp)
	if err != nil {
		return findings, fired, 0
	}
	findings, succeeded = applyRepairs(findings, files, results)
	return findings, fired, succeeded
}

// selectRepairCandidates returns the indices of inline findings that lack a
// usable suggestion (none provided, or stripped by an earlier gate) and whose
// anchor lands in a hunk we can quote back to the model.
func selectRepairCandidates(findings []Finding, files []FileDiff) []int {
	var out []int
	for i := range findings {
		f := findings[i]
		if !findingIsInlinePostable(f) {
			continue
		}
		if strings.TrimSpace(f.Suggestion) != "" {
			continue
		}
		file := FindFile(files, f.Path)
		if file == nil {
			continue
		}
		if h, _ := HunkAroundLine(file, f.Line); h == nil {
			continue
		}
		out = append(out, i)
	}
	return out
}

// buildRepairItems gathers the per-finding context (comment + numbered
// post-image hunk lines) for each candidate index.
func buildRepairItems(findings []Finding, files []FileDiff, idxs []int) []repairItem {
	var items []repairItem
	for _, i := range idxs {
		f := findings[i]
		file := FindFile(files, f.Path)
		if file == nil {
			continue
		}
		h, _ := HunkAroundLine(file, f.Line)
		if h == nil {
			continue
		}
		var lines []repairHunkLine
		for _, l := range h.Lines {
			if l.Kind == DiffRemoved || l.NewNo == 0 {
				continue
			}
			lines = append(lines, repairHunkLine{no: l.NewNo, text: l.Text})
		}
		if len(lines) == 0 {
			continue
		}
		items = append(items, repairItem{
			id:        i,
			path:      f.Path,
			comment:   f.Comment,
			anchorNo:  f.Line,
			hunkLines: lines,
		})
	}
	return items
}

// repairSystemPrompt is the focused contract for the suggestion-repair pass.
// It deliberately mirrors the REPLACEMENT-NOT-INSERTION posture of the shared
// SUGGESTION CONTRACT so a repaired suggestion obeys the same rules a
// first-pass one would.
const repairSystemPrompt = `You are a precision patch writer for an automated code review. For each item you are given a reviewer's comment and the numbered post-image lines of the diff hunk it refers to. Your only job is to turn the comment into a GitHub one-click "suggestion" by choosing the exact line to replace and writing its replacement.

REPLACEMENT, NOT INSERTION: a suggestion REPLACES the single line at "anchor_line". The chosen line is deleted and your "replacement" text is inserted in its place; every other line is untouched. If you want a line to survive, reproduce it verbatim (exact indentation) inside "replacement".

Rules:
- "anchor_line" MUST be one of the line numbers shown for that item.
- Choose the line the comment is actually about — not necessarily the item's "current_anchor_line", which may be wrong.
- "replacement" is drop-in valid text at that line: code/config only, no prose, no markdown fences, no placeholders, matching the file's language and indentation. It may span multiple lines (each replaces nothing extra — only the one anchor line is removed).
- Only emit a repair when the comment unambiguously specifies a contiguous fix of 10 lines or fewer. If the fix is multi-file, non-contiguous, ambiguous, or you are unsure it parses cleanly, DECLINE — a wrong suggestion is worse than none.

Return STRICT JSON only, no prose, no fencing:
{"repairs":[{"id":<int>,"anchor_line":<int>,"replacement":"<text>"} | {"id":<int>,"decline":true}]}
Every id you were given must appear exactly once. Escape newlines in "replacement" as \n.`

// buildRepairPrompt renders the batched user message listing each item with
// its comment and numbered hunk lines.
func buildRepairPrompt(name string, items []repairItem) (string, string) {
	var b strings.Builder
	b.WriteString("Specialist: ")
	b.WriteString(name)
	b.WriteString("\n\nRepair the following ")
	b.WriteString(strconv.Itoa(len(items)))
	b.WriteString(" finding(s). For each, the hunk's post-image lines are shown as `NNN| text`.\n\n")
	for _, it := range items {
		b.WriteString("--- item id=")
		b.WriteString(strconv.Itoa(it.id))
		b.WriteString("\n")
		b.WriteString("path: ")
		b.WriteString(it.path)
		b.WriteString("\n")
		b.WriteString("current_anchor_line: ")
		b.WriteString(strconv.Itoa(it.anchorNo))
		b.WriteString("\n")
		b.WriteString("comment: ")
		b.WriteString(strings.ReplaceAll(it.comment, "\n", " "))
		b.WriteString("\n")
		b.WriteString("hunk:\n")
		for _, hl := range it.hunkLines {
			b.WriteString(strconv.Itoa(hl.no))
			b.WriteString("| ")
			b.WriteString(hl.text)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return repairSystemPrompt, b.String()
}

// parseRepairResponse parses the model's JSON into a map keyed by finding id,
// keeping only accepted (non-decline) repairs with a usable anchor and
// replacement. Salvage (fence/extract/comment/triple-quote/trailing-comma) is
// delegated to the shared llmjson ladder.
func parseRepairResponse(raw string) (map[int]repairResult, error) {
	type repairEnvelopeEntry struct {
		ID          int    `json:"id"`
		AnchorLine  int    `json:"anchor_line"`
		Replacement string `json:"replacement"`
		Decline     bool   `json:"decline"`
	}
	type repairEnvelope struct {
		Repairs []repairEnvelopeEntry `json:"repairs"`
	}
	env, err := llmjson.Parse[repairEnvelope](raw)
	if err != nil {
		return nil, fmt.Errorf("repair response: %w", err)
	}
	out := make(map[int]repairResult, len(env.Repairs))
	for _, e := range env.Repairs {
		if e.Decline {
			continue
		}
		if e.AnchorLine <= 0 || strings.TrimSpace(e.Replacement) == "" {
			continue
		}
		out[e.ID] = repairResult{AnchorLine: e.AnchorLine, Replacement: e.Replacement}
	}
	return out, nil
}

// applyRepairs writes each accepted repair onto its finding after the repair
// passes the same safety gates a model-authored suggestion would. A repair
// that names a line outside the hunk, or whose replacement would break the
// file or mismatch the anchor kind, is dropped — the finding stays
// suggestion-less rather than receiving a bad fix.
func applyRepairs(findings []Finding, files []FileDiff, results map[int]repairResult) ([]Finding, int) {
	applied := 0
	for id, r := range results {
		if id < 0 || id >= len(findings) {
			continue
		}
		f := &findings[id]
		if !findingIsInlinePostable(*f) {
			continue
		}
		file := FindFile(files, f.Path)
		if file == nil {
			continue
		}
		h, _ := HunkAroundLine(file, r.AnchorLine)
		if h == nil {
			continue
		}
		if _, anchorText := findAnchorLine(h, r.AnchorLine); strings.TrimSpace(anchorText) == "" {
			continue
		}
		tmp := *f
		tmp.Line = r.AnchorLine
		tmp.Suggestion = r.Replacement
		tmp.AnchorExcerpt = ""
		tmp.SuggestionStrippedReason = ""
		if reason := suggestionBreaksFile(tmp, files); reason != "" {
			continue
		}
		if reason := anchorKindMismatch(tmp, files); reason != "" {
			continue
		}
		if r.AnchorLine != f.Line {
			f.AnchorRelocatedFrom = f.Line
		}
		f.Line = r.AnchorLine
		f.Suggestion = r.Replacement
		f.AnchorExcerpt = ""
		f.SuggestionStrippedReason = ""
		f.SuggestionRepaired = true
		applied++
	}
	return findings, applied
}
