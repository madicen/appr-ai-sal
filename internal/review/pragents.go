package review

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
)

// PR-agent names. These are a second family of review agents that evaluate the
// pull request at the whole-PR / metadata level (title, description, CI checks,
// discussion threads, scope) rather than line-by-line over the diff like the
// code specialists in AllSpecialists. They reuse the same SpecialistResult ->
// Finding -> posted-review pipeline, so their feedback is postable just like a
// specialist's. Each name maps to an embedded prompt in prompts/<name>.md.
const (
	SpecDescription = "description"
	SpecChecks      = "checks"
	SpecDiscussion  = "discussion"
	SpecScope       = "scope"
)

// AllPRAgents is the ordered set of PR-level agents. Description and Scope read
// only the title/body/diff; Checks reads the CI rollup; Discussion reads the
// unresolved review threads and conversation.
var AllPRAgents = []string{
	SpecDescription,
	SpecChecks,
	SpecDiscussion,
	SpecScope,
}

// IsPRAgent reports whether name is one of the PR-level agents.
func IsPRAgent(name string) bool {
	switch name {
	case SpecDescription, SpecChecks, SpecDiscussion, SpecScope:
		return true
	}
	return false
}

// PRAgentInput carries the PR-level signals the agents reason over. The runner
// fetches these once and shares them across all four agents. Any field may be
// nil/empty when the corresponding fetch failed or returned nothing — each
// agent prompt is written to degrade gracefully.
type PRAgentInput struct {
	Checks     *gh.ChecksReport
	Threads    []gh.ReviewThread
	Discussion []gh.DiscussionEvent
}

// runPRAgent runs a single PR-level agent over the PR metadata and diff and
// returns its findings. It mirrors runReviewSpecialist: load the prompt, build
// the user message, call the model, parse strict JSON, and run the shared
// finding-validation gates. PR agents emit mostly PR-wide findings (path "",
// line 0) which the anchor gates leave untouched, but an agent may still anchor
// a concrete fix to a changed line (e.g. the Checks agent), so the same gates
// apply.
func runPRAgent(ctx context.Context, cfg *aiconfig.Config, name string, worktree string, pr *gh.PR, diff string, in PRAgentInput) SpecialistResult {
	res := SpecialistResult{Specialist: name, Findings: []Finding{}}

	systemPrompt, err := SpecialistPrompt(name)
	if err != nil {
		res.Err = err
		return res
	}

	userPrompt := buildPRAgentUserPrompt(name, pr, diff, in, cfg.ReviewStrictness)
	// PR agents do not inject repo/lang/tech briefs; pass hasRepoContext=false
	// so non-Claude backends get the diff-only tooling hint.
	systemPrompt, userPrompt = augmentPromptsForProvider(ai.CapabilitiesFor(cfg).RepoTools, systemPrompt, userPrompt, false)

	out, err := completeJSON(ctx, cfg, systemPrompt, userPrompt, worktree)
	if err != nil {
		res.Err = err
		return res
	}

	parsed, err := parseSpecialistJSON(out)
	if err != nil {
		res.Err = fmt.Errorf("parse pr-agent output: %w (raw: %s)", err, truncate(out, 500))
		return res
	}
	res.Summary = parsed.Summary
	if parsed.Findings != nil {
		floor := MinSeverityForStrictness(cfg.ReviewStrictness)
		res.Findings = FilterFindingsBySeverity(parsed.Findings, floor)
		// Keep each PR agent in its lane before any anchor/suggestion work:
		// description/scope are whole-PR judgments (force PR-wide) and the
		// discussion agent may only anchor to an actual review thread. This
		// runs first so we never waste a repair call on a finding we are
		// about to drop or de-anchor. See constrainPRAgentScope.
		res.Findings = constrainPRAgentScope(name, res.Findings, in.Threads)
		if name == SpecDiscussion {
			// Backstop: when the PR author had the last word in an unresolved
			// thread (e.g. "it's already there" with a link), the concern is
			// disputed, not unaddressed. Demote a "not addressed" finding so
			// it doesn't block on the author's own rebuttal. See
			// downrankAuthorRebuttedThreads.
			res.Findings = downrankAuthorRebuttedThreads(pr, res.Findings, in.Threads)
		}
		parsedFiles := ParseDiff(diff)
		res.Findings = validateAndPruneSuggestions(res.Findings, parsedFiles)
		res.Findings = validateAnchorKind(res.Findings, parsedFiles)
		res.Findings = validateAnchorExcerpt(res.Findings, parsedFiles)
		res.Findings = validateNamingConvention(res.Findings)
		res.Findings = synthesizeSuggestions(res.Findings, parsedFiles)
		res.Findings, res.RepairFired, res.RepairSucceeded = repairMissingSuggestions(ctx, cfg, worktree, name, res.Findings, parsedFiles)
		res.Findings = FilterFindingsBySeverity(res.Findings, floor)
	}
	return res
}

// constrainPRAgentScope is a deterministic guard that keeps PR-level agents
// from drifting into line-by-line code review (the recurring failure where the
// discussion/description/scope agents file a code-style finding the code
// specialists already own). Prompt text alone does not stop this, so we enforce
// it structurally:
//
//   - description, scope: inherently whole-PR judgments. Any inline finding is
//     forced PR-wide (path "", line 0) — the prose survives as a PR-wide note
//     but it no longer posts as an inline code comment.
//   - discussion: may only anchor to a line that belongs to an unresolved
//     review thread. An inline finding that maps to no such thread is code
//     review the agent is told not to do, so it is dropped entirely. PR-wide
//     discussion findings (conversation asks) are kept.
//   - checks (and anything else): unchanged — Checks legitimately anchors a fix
//     to a failing-check line.
func constrainPRAgentScope(name string, findings []Finding, threads []gh.ReviewThread) []Finding {
	switch name {
	case SpecDescription, SpecScope:
		for i := range findings {
			f := &findings[i]
			if findingIsInlinePostable(*f) {
				f.Path = ""
				f.Line = 0
				f.Side = ""
				f.Suggestion = ""
				f.AnchorExcerpt = ""
				f.SuggestionStrippedReason = ""
			}
		}
		return findings
	case SpecDiscussion:
		anchors := unresolvedThreadAnchors(threads)
		out := findings[:0]
		for _, f := range findings {
			if !findingIsInlinePostable(f) {
				out = append(out, f) // PR-wide conversation asks are fine.
				continue
			}
			if anchors[threadAnchorKey(f.Path, f.Line)] {
				out = append(out, f)
			}
			// else: inline finding with no matching thread → code-review
			// drift; drop it.
		}
		return out
	default:
		return findings
	}
}

// downrankAuthorRebuttedThreads demotes discussion findings that track an
// unresolved thread in which the PR author already had the last word —
// replying that the ask is already satisfied (e.g. "already there" with a
// link), done elsewhere, or otherwise disputing it — without a reviewer
// pushing back afterwards.
//
// This is the provider-agnostic backstop for the false positive where the
// discussion agent judges "addressed" strictly from the diff and ignores the
// author's textual reply: the reviewer asked to update CODEOWNERS, the author
// replied that it already exists (so nothing shows in the diff), yet the agent
// still filed a "not addressed" finding. The author's reply is always in the
// agent's text context, but an HTTP-only provider (e.g. Gemini) can't open the
// referenced file to confirm, and the prompt's diff-centric framing leads the
// model to disregard the rebuttal — so we enforce the calibration here.
//
// The thread is genuinely unresolved on GitHub, so we do NOT drop the finding
// outright (the reviewer may still want to confirm and click Resolve); we
// demote it to info with an explanatory note. Under balanced/strict floors the
// info finding falls away on its own; at the lowest floor it stays visible as
// an "awaiting resolution" nudge rather than a blocking "unaddressed" claim.
//
// Matching is deliberately conservative — a finding is only demoted when it
// clearly references a rebutted thread:
//   - an inline finding anchored to the thread's (path, line), or
//   - a finding whose comment contains the thread's "path:line" location, or
//   - a finding whose comment @-mentions the reviewer who opened the thread.
//
// A thread only counts as rebutted when its opening comment is from someone
// OTHER than the PR author (a real reviewer ask) and its most recent comment
// is from the PR author. Resolved threads and author-opened threads are
// ignored. Returns the same slice for ergonomics.
func downrankAuthorRebuttedThreads(pr *gh.PR, findings []Finding, threads []gh.ReviewThread) []Finding {
	if pr == nil {
		return findings
	}
	author := normalizeLogin(pr.Author)
	if author == "" {
		return findings
	}
	var marks []rebuttedThread
	for _, t := range threads {
		if t.IsResolved || len(t.Comments) < 2 {
			continue
		}
		opener := normalizeLogin(t.Comments[0].Author)
		last := normalizeLogin(t.Comments[len(t.Comments)-1].Author)
		if opener == "" || opener == author || last != author {
			continue
		}
		path, line := threadAnchor(t)
		loc := path
		if path != "" && line > 0 {
			loc = path + ":" + strconv.Itoa(line)
		}
		marks = append(marks, rebuttedThread{path: path, line: line, loc: loc, opener: opener})
	}
	if len(marks) == 0 {
		return findings
	}
	for i := range findings {
		f := &findings[i]
		if f.Severity != SeverityWarning && f.Severity != SeverityError {
			continue
		}
		if !findingReferencesRebutted(*f, marks) {
			continue
		}
		f.Severity = SeverityInfo
		f.ActionabilityNote = "PR author replied in this thread asserting the request is already satisfied; treat as awaiting resolution, not unaddressed"
	}
	return findings
}

// rebuttedThread is one unresolved thread the PR author had the last word in.
// It carries the anchor (path/line and the rendered "path:line" location) and
// the reviewer who opened it, so findingReferencesRebutted can match a finding
// to it by anchor, by location string, or by @-mention of the opener.
type rebuttedThread struct {
	path   string
	line   int
	loc    string // "path:line" when line > 0, else path
	opener string // normalized login of the reviewer who opened the thread
}

// findingReferencesRebutted reports whether a discussion finding clearly maps
// to one of the author-rebutted threads. See downrankAuthorRebuttedThreads for
// the matching rules.
func findingReferencesRebutted(f Finding, marks []rebuttedThread) bool {
	comment := strings.ToLower(f.Comment)
	for _, m := range marks {
		if findingIsInlinePostable(f) && m.path != "" && m.line > 0 &&
			filepathToSlashEqual(f.Path, m.path) && f.Line == m.line {
			return true
		}
		if m.loc != "" && strings.Contains(comment, strings.ToLower(m.loc)) {
			return true
		}
		if m.opener != "" && strings.Contains(comment, "@"+m.opener) {
			return true
		}
	}
	return false
}

func filepathToSlashEqual(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// normalizeLogin lowercases a GitHub login and strips a leading "@" so author
// comparisons are stable regardless of how the login was captured.
func normalizeLogin(s string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "@"))
}

// threadAnchor returns the (path, line) the thread is anchored to, taking the
// first comment that carries a path. Line is 0 when none is known.
func threadAnchor(t gh.ReviewThread) (string, int) {
	for _, c := range t.Comments {
		if strings.TrimSpace(c.Path) != "" {
			return c.Path, c.Line
		}
	}
	return "", 0
}

// unresolvedThreadAnchors returns the set of "path:line" anchors that belong to
// an unresolved review thread, so the discussion agent's inline findings can be
// validated against real reviewer feedback.
func unresolvedThreadAnchors(threads []gh.ReviewThread) map[string]bool {
	anchors := make(map[string]bool)
	for _, t := range threads {
		if t.IsResolved {
			continue
		}
		for _, c := range t.Comments {
			if c.Path == "" || c.Line <= 0 {
				continue
			}
			anchors[threadAnchorKey(c.Path, c.Line)] = true
		}
	}
	return anchors
}

func threadAnchorKey(path string, line int) string {
	return path + ":" + strconv.Itoa(line)
}

// prAgentIntro frames the task for one PR-level agent. The bulk of each agent's
// behaviour lives in its prompts/<name>.md system prompt; this intro just
// orients the model on what data it has.
const prAgentIntro = "You are reviewing a pull request as a whole, not line by line. The PR's head branch is checked out in the working directory and you may read files for extra context, but focus strictly on the single aspect described in the system prompt above. Base your judgement on the PR metadata, the unified diff, and the sections in this message.\n\n"

func buildPRAgentUserPrompt(name string, pr *gh.PR, diff string, in PRAgentInput, strict aiconfig.ReviewStrictness) string {
	var b strings.Builder
	b.WriteString(prAgentIntro)
	b.WriteString("PR: " + pr.Repository + "#")
	fmt.Fprintf(&b, "%d", pr.Number)
	b.WriteString("\nTitle: " + pr.Title + "\n")
	b.WriteString("Author: " + pr.Author + "\n")
	b.WriteString("Base: " + pr.BaseRef + " → Head: " + pr.HeadRef + "\n")
	fmt.Fprintf(&b, "Changed files: %d (+%d / -%d)\n\n", pr.ChangedFiles, pr.Additions, pr.Deletions)

	b.WriteString("PR description:\n")
	if strings.TrimSpace(pr.Body) != "" {
		b.WriteString(pr.Body)
	} else {
		b.WriteString("(no description provided)")
	}
	b.WriteString("\n\n")

	b.WriteString(strictnessBlockForSpecialists(strict))

	switch name {
	case SpecChecks:
		b.WriteString(formatChecksSection(in.Checks))
	case SpecDiscussion:
		b.WriteString(formatDiscussionSection(in.Threads, in.Discussion))
	}

	b.WriteString("Unified diff (line numbers in `+` hunks are the lines you cite in findings, with side=\"RIGHT\"):\n\n")
	b.WriteString("```diff\n")
	b.WriteString(diff)
	b.WriteString("\n```\n\n")
	b.WriteString(reviewOutputContract)
	return b.String()
}

// formatChecksSection renders the CI checks rollup for the Checks agent,
// surfacing failing/erroring runs first with their title, output summary, and
// annotations so the model can propose concrete fixes.
func formatChecksSection(report *gh.ChecksReport) string {
	if report == nil {
		return "## CI checks\n\nCheck status could not be loaded for this PR. If you have no other signal, say so and emit no findings.\n\n"
	}
	if len(report.Runs) == 0 {
		return "## CI checks\n\nNo CI checks are reported for the head commit. Treat this as \"no check signal\" rather than a failure.\n\n"
	}
	var b strings.Builder
	b.WriteString("## CI checks\n\n")
	rollup := strings.TrimSpace(report.RollupState)
	if rollup == "" {
		rollup = "UNKNOWN"
	}
	b.WriteString("Rollup state: " + rollup + "\n\n")

	var failing, passing []gh.CheckRun
	for _, r := range report.Runs {
		if checkRunFailing(r) {
			failing = append(failing, r)
		} else {
			passing = append(passing, r)
		}
	}

	if len(failing) == 0 {
		b.WriteString("No checks are currently failing. If nothing is failing, emit no findings.\n\n")
	} else {
		fmt.Fprintf(&b, "Failing / erroring checks (%d):\n\n", len(failing))
		for _, r := range failing {
			state := checkRunState(r)
			fmt.Fprintf(&b, "- %s [%s]\n", checkRunName(r), state)
			if t := strings.TrimSpace(r.Title); t != "" {
				b.WriteString("  Title: " + t + "\n")
			}
			if s := strings.TrimSpace(r.Summary); s != "" {
				b.WriteString("  Output: " + truncate(collapseWhitespace(s), 1200) + "\n")
			}
			for _, a := range r.Annotations {
				fmt.Fprintf(&b, "  Annotation [%s] %s:%d — %s\n", a.Level, a.Path, a.Line, truncate(collapseWhitespace(a.Message), 400))
			}
			if u := strings.TrimSpace(r.DetailsURL); u != "" {
				b.WriteString("  Details: " + u + "\n")
			}
			b.WriteString("\n")
		}
	}

	if len(passing) > 0 {
		names := make([]string, 0, len(passing))
		for _, r := range passing {
			names = append(names, checkRunName(r))
		}
		b.WriteString("Passing / other checks: " + strings.Join(names, ", ") + "\n\n")
	}
	return b.String()
}

// formatDiscussionSection renders unresolved review threads (the suggestions
// that may still need addressing) plus the top-level conversation for the
// Discussion agent.
func formatDiscussionSection(threads []gh.ReviewThread, discussion []gh.DiscussionEvent) string {
	var b strings.Builder
	b.WriteString("## Discussion\n\n")

	var unresolved []gh.ReviewThread
	for _, t := range threads {
		if !t.IsResolved && len(t.Comments) > 0 {
			unresolved = append(unresolved, t)
		}
	}

	if len(unresolved) == 0 {
		b.WriteString("Unresolved review threads: none. There are no open inline suggestions to check against the diff.\n\n")
	} else {
		fmt.Fprintf(&b, "Unresolved review threads (%d). For each, judge from the unified diff below whether the reviewer's concern has been addressed in code, and file a finding only when it appears NOT addressed:\n\n", len(unresolved))
		for i, t := range unresolved {
			loc := threadLocation(t)
			outdated := ""
			if t.IsOutdated {
				outdated = " (outdated — the anchored code has changed since this comment)"
			}
			fmt.Fprintf(&b, "Thread %d — %s%s\n", i+1, loc, outdated)
			for _, c := range t.Comments {
				author := c.Author
				if author == "" {
					author = "reviewer"
				}
				b.WriteString("  @" + author + ": " + truncate(collapseWhitespace(c.Body), 800) + "\n")
			}
			b.WriteString("\n")
		}
	}

	if len(discussion) > 0 {
		b.WriteString("Top-level conversation (issue comments and review summaries):\n\n")
		for _, e := range discussion {
			author := e.Author
			if author == "" {
				author = "participant"
			}
			tag := "commented"
			if e.Kind == gh.DiscussionReview && strings.TrimSpace(e.Verdict) != "" {
				tag = strings.ToLower(e.Verdict)
			}
			fmt.Fprintf(&b, "- @%s %s: %s\n", author, tag, truncate(collapseWhitespace(e.Body), 600))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func threadLocation(t gh.ReviewThread) string {
	for _, c := range t.Comments {
		if strings.TrimSpace(c.Path) != "" {
			if c.Line > 0 {
				return fmt.Sprintf("%s:%d", c.Path, c.Line)
			}
			return c.Path
		}
	}
	return "general"
}

// checkRunFailing reports whether a check run is in a state that warrants a
// fix suggestion (failed/errored/cancelled/etc). Pending and successful runs
// are not failing.
func checkRunFailing(r gh.CheckRun) bool {
	switch checkRunState(r) {
	case "FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE":
		return true
	}
	return false
}

func checkRunState(r gh.CheckRun) string {
	s := strings.TrimSpace(r.Conclusion)
	if s == "" {
		s = strings.TrimSpace(r.State)
	}
	return strings.ToUpper(s)
}

func checkRunName(r gh.CheckRun) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed check)"
	}
	if app := strings.TrimSpace(r.App); app != "" {
		return name + " · " + app
	}
	return name
}

// collapseWhitespace flattens runs of whitespace (including newlines) into
// single spaces so multi-line CI output / comment bodies render as compact,
// scannable single lines inside the prompt.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// sortedPRAgentResults returns results ordered to match AllPRAgents so the
// pipeline and overlay see a deterministic order regardless of completion
// timing under parallel dispatch.
func sortedPRAgentResults(results []SpecialistResult) []SpecialistResult {
	order := map[string]int{}
	for i, n := range AllPRAgents {
		order[n] = i
	}
	out := append([]SpecialistResult(nil), results...)
	sort.SliceStable(out, func(i, j int) bool {
		return order[out[i].Specialist] < order[out[j].Specialist]
	})
	return out
}
