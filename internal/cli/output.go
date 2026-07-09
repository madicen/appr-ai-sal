package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/review"
)

// ---------------------------------------------------------------------------
// NDJSON progress (stderr)
// ---------------------------------------------------------------------------

// progressJSON is the stable, documented per-event shape streamed to stderr as
// NDJSON. It is intentionally a small projection of review.Progress (which
// carries rich in-memory pointers) so the wire contract stays stable across
// engine refactors.
type progressJSON struct {
	Stage      string     `json:"stage"`
	Detail     string     `json:"detail,omitempty"`
	Error      string     `json:"error,omitempty"`
	Specialist string     `json:"specialist,omitempty"`
	Verdict    string     `json:"verdict,omitempty"`
	Usage      *usageJSON `json:"usage,omitempty"`
}

// usageJSON is the stable usage/cost projection of review.RunUsage.
type usageJSON struct {
	Calls        int     `json:"calls"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
	CostKnown    bool    `json:"cost_known"`
	WallClockMS  int64   `json:"wall_clock_ms"`
}

func usageToJSON(u *review.RunUsage) *usageJSON {
	if u == nil {
		return nil
	}
	return &usageJSON{
		Calls:        u.Calls,
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		CostUSD:      u.CostUSD,
		CostKnown:    u.CostKnown,
		WallClockMS:  u.WallClock.Milliseconds(),
	}
}

// emitProgress writes one NDJSON progress object to w (stderr). A marshal
// failure is swallowed — progress is diagnostic, never fatal to the run.
func emitProgress(w io.Writer, p review.Progress) {
	ev := progressJSON{
		Stage:  p.Stage,
		Detail: p.Detail,
	}
	if p.Err != nil {
		ev.Error = p.Err.Error()
	}
	if p.Result != nil {
		ev.Specialist = p.Result.Specialist
	}
	if p.Vibe != nil && p.Vibe.Verdict != "" {
		ev.Verdict = review.NormalizeVibeVerdict(p.Vibe.Verdict)
	}
	if p.Arbiter != nil && p.Arbiter.EffectiveVerdict != "" {
		ev.Verdict = review.NormalizeVibeVerdict(p.Arbiter.EffectiveVerdict)
	}
	ev.Usage = usageToJSON(p.Usage)

	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	fmt.Fprintln(w, string(b))
}

// ---------------------------------------------------------------------------
// Final result (stdout)
// ---------------------------------------------------------------------------

// reviewResultJSON is the stable object written to stdout with --json. It is a
// clean projection of the final review.Draft so it pipes into `jq` without
// leaking internal engine types; `body` is the authoritative rendered review
// markdown.
type reviewResultJSON struct {
	Ref              string        `json:"ref"`
	Verdict          string        `json:"verdict"`           // reconciled = what gets posted
	EffectiveVerdict string        `json:"effective_verdict"` // raw effective verdict
	PostEvent        string        `json:"post_event"`        // APPROVE | COMMENT | REQUEST_CHANGES
	Summary          string        `json:"summary"`
	Body             string        `json:"body"`
	Findings         []findingJSON `json:"findings"`
	Degraded         []string      `json:"degraded,omitempty"`
	Usage            *usageJSON    `json:"usage,omitempty"`
	Post             *postResult   `json:"post,omitempty"`
}

// findingJSON is one finding in the result. Inline findings carry path/line;
// PR-wide findings have inline=false and no anchor.
type findingJSON struct {
	Specialist string `json:"specialist"`
	Path       string `json:"path,omitempty"`
	Line       int    `json:"line,omitempty"`
	Severity   string `json:"severity"`
	Comment    string `json:"comment"`
	Suggestion string `json:"suggestion,omitempty"`
	Inline     bool   `json:"inline"`
}

// buildResult projects the final Draft into the stable output object.
func buildResult(draft *review.Draft, post *postResult) reviewResultJSON {
	res := reviewResultJSON{
		Ref:              draft.Ref.String(),
		Verdict:          review.NormalizeVibeVerdict(draft.ReconciledMergeVerdict()),
		EffectiveVerdict: review.NormalizeVibeVerdict(draft.EffectiveMergeVerdict()),
		PostEvent:        draft.PostEvent(),
		Body:             draft.RenderBody(),
		Post:             post,
	}
	if draft.VibeCoach != nil {
		res.Summary = strings.TrimSpace(draft.VibeCoach.Summary)
	}
	// Inline findings that would actually post (after arbiter suppressions /
	// user skips), plus PR-wide/general findings from every specialist.
	for _, ff := range draft.FlatPostableFindingsForPost() {
		res.Findings = append(res.Findings, findingJSON{
			Specialist: ff.Specialist,
			Path:       ff.Finding.Path,
			Line:       ff.Finding.Line,
			Severity:   string(ff.Finding.Severity),
			Comment:    strings.TrimSpace(ff.Finding.Comment),
			Suggestion: strings.TrimSpace(ff.Finding.Suggestion),
			Inline:     true,
		})
	}
	for _, s := range draft.Specialists {
		if s.Err != nil {
			continue
		}
		for _, f := range s.Findings {
			if strings.TrimSpace(f.Path) != "" && f.Line > 0 {
				continue // inline, already covered above
			}
			if strings.TrimSpace(f.Comment) == "" {
				continue
			}
			res.Findings = append(res.Findings, findingJSON{
				Specialist: s.Specialist,
				Severity:   string(f.Severity),
				Comment:    strings.TrimSpace(f.Comment),
				Inline:     false,
			})
		}
	}
	if failed, skipped := draft.DegradedStages(); len(failed)+len(skipped) > 0 {
		res.Degraded = append(res.Degraded, failed...)
		res.Degraded = append(res.Degraded, skipped...)
	}
	return res
}

// writeResult writes the final review result to stdout — JSON with --json,
// otherwise a compact human summary. Stdout carries ONLY this so the JSON
// output pipes cleanly into `jq`.
func writeResult(w io.Writer, fl *reviewFlags, draft *review.Draft, post *postResult) {
	res := buildResult(draft, post)
	if fl.json {
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			fmt.Fprintf(w, "{\"error\":%q}\n", err.Error())
			return
		}
		fmt.Fprintln(w, string(b))
		return
	}
	writeHumanSummary(w, res)
}

func writeHumanSummary(w io.Writer, res reviewResultJSON) {
	fmt.Fprintf(w, "Review of %s\n", res.Ref)
	verdict := verdictLabel(res.Verdict)
	if em := review.VerdictEmoji(review.NormalizeVibeVerdict(res.Verdict)); em != "" {
		verdict = em + " " + verdict
	}
	fmt.Fprintf(w, "Verdict: %s → GitHub event %s\n", verdict, res.PostEvent)
	if res.Summary != "" {
		fmt.Fprintf(w, "\n%s\n", res.Summary)
	}
	if len(res.Findings) > 0 {
		fmt.Fprintf(w, "\nFindings: %s\n", severityCounts(res.Findings))
		for _, f := range res.Findings {
			loc := "(PR-wide)"
			if f.Inline {
				loc = fmt.Sprintf("%s:%d", f.Path, f.Line)
			}
			fmt.Fprintf(w, "  [%s] %s · %s — %s\n", strings.ToUpper(f.Severity), f.Specialist, loc, firstLine(f.Comment))
		}
	}
	if len(res.Degraded) > 0 {
		fmt.Fprintf(w, "\n⚠ Partial review — degraded stages: %s\n", strings.Join(res.Degraded, ", "))
	}
	if res.Post != nil {
		if res.Post.DryRun {
			fmt.Fprintf(w, "\nDry-run: %d payload(s) previewed (event %s); nothing posted.\n", len(res.Post.Previews), res.Post.Event)
		} else {
			fmt.Fprintf(w, "\nPosted: %d comment(s), %d reply(ies), body=%v (event %s), %d failed.\n",
				res.Post.Posted, res.Post.Replies, res.Post.BodyPost, res.Post.Event, res.Post.Failed)
		}
	}
}

// severityCounts renders "1 critical · 2 error · 3 warning · 1 info" in fixed
// severity order, skipping empty buckets, so the findings header reads as a
// triage summary instead of a bare total.
func severityCounts(findings []findingJSON) string {
	counts := map[string]int{}
	for _, f := range findings {
		counts[strings.ToLower(strings.TrimSpace(f.Severity))]++
	}
	var parts []string
	for _, sev := range []string{"critical", "error", "warning", "info"} {
		if n := counts[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, sev))
			delete(counts, sev)
		}
	}
	// Unknown severities still get counted rather than silently dropped;
	// sorted so the line is deterministic.
	var rest []string
	for sev := range counts {
		rest = append(rest, sev)
	}
	sort.Strings(rest)
	for _, sev := range rest {
		label := sev
		if label == "" {
			label = "unrated"
		}
		parts = append(parts, fmt.Sprintf("%d %s", counts[sev], label))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%d", len(findings))
	}
	return strings.Join(parts, " · ")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
