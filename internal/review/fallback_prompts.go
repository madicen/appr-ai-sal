package review

import (
	"fmt"
	"strings"
)

// synthesizeFallbackPrompts builds one AuthorPrompt per error/critical
// finding that survives the user's review and the repo arbiter but isn't
// covered by any of the model's surviving prompts.
//
// "Covered" means the finding appears in some surviving prompt's
// finding_refs. "Blocking" means severity error or critical. Inline
// findings that already carry a one-click GitHub `suggestion` block on the
// diff are considered self-actionable and are not synthesized for —
// duplicating them as paste-ready prompts would just add noise.
//
// Returned prompts have an empty FindingRefs so they survive any future
// filter pass unconditionally; the agent_prompt body quotes the finding's
// location and comment so the author's AI gets the same context the human
// reviewer has.
func synthesizeFallbackPrompts(d *Draft, surviving []AuthorPrompt) []AuthorPrompt {
	if d == nil {
		return nil
	}
	covered := coveredFindingKeys(surviving)
	var out []AuthorPrompt

	for _, ff := range d.FlatPostableFindingsForPost() {
		f := ff.Finding
		if f.Severity != SeverityError && f.Severity != SeverityCritical {
			continue
		}
		if SuggestionPostsToGitHub(f) {
			continue
		}
		key := suppressionKey(ff.Specialist, f.Path, f.Line, f.Side)
		if _, ok := covered[key]; ok {
			continue
		}
		out = append(out, fallbackPromptForInline(ff.Specialist, f))
	}

	for _, s := range d.Specialists {
		if s.Err != nil {
			continue
		}
		for _, f := range generalFindings(s.Findings) {
			if f.Severity != SeverityError && f.Severity != SeverityCritical {
				continue
			}
			key := suppressionKey(s.Specialist, f.Path, f.Line, f.Side)
			if _, ok := covered[key]; ok {
				continue
			}
			out = append(out, fallbackPromptForGeneral(s.Specialist, f))
		}
	}
	return out
}

// coveredFindingKeys collects the suppressionKey for every finding bundled
// into any of the supplied prompts via finding_refs. Used by
// synthesizeFallbackPrompts to skip blockers the model already addressed.
func coveredFindingKeys(prompts []AuthorPrompt) map[string]struct{} {
	out := map[string]struct{}{}
	for _, p := range prompts {
		for _, ref := range p.FindingRefs {
			out[suppressionKey(ref.Specialist, ref.Path, ref.Line, ref.Side)] = struct{}{}
		}
	}
	return out
}

// fallbackPromptForInline constructs a paste-ready AuthorPrompt for an
// uncovered inline blocking finding. The body names the file and line so
// the author's AI can navigate directly, and quotes the specialist's
// comment verbatim — synthesizers can't invent acceptance criteria the
// reviewer didn't write, so the safest move is to surface the original
// language.
func fallbackPromptForInline(specialist string, f Finding) AuthorPrompt {
	title := fmt.Sprintf("%s: address %s:%d", specialist, f.Path, f.Line)
	body := fmt.Sprintf("In `%s` around line %d, the %s specialist flagged a %s-severity issue:\n\n%s\n\nFix the file at that location so the issue no longer applies.",
		f.Path, f.Line, specialist, string(f.Severity), strings.TrimSpace(f.Comment))
	return AuthorPrompt{
		Title:       title,
		Rationale:   fmt.Sprintf("Auto-generated from a %s-severity %s finding the vibe-coach did not bundle into a paste-ready prompt.", string(f.Severity), specialist),
		AgentPrompt: body,
	}
}

// fallbackPromptForGeneral constructs a paste-ready AuthorPrompt for an
// uncovered PR-wide blocking finding. The location context is "the
// repository" rather than a specific file because the specialist did not
// anchor the finding to a line.
func fallbackPromptForGeneral(specialist string, f Finding) AuthorPrompt {
	title := fmt.Sprintf("%s: %s", specialist, firstSentence(f.Comment))
	body := fmt.Sprintf("Repository-wide note from the %s specialist (%s severity):\n\n%s\n\nIdentify what in this PR's diff triggers the note above and address it.",
		specialist, string(f.Severity), strings.TrimSpace(f.Comment))
	return AuthorPrompt{
		Title:       title,
		Rationale:   fmt.Sprintf("Auto-generated from a PR-wide %s-severity %s finding the vibe-coach did not bundle into a paste-ready prompt.", string(f.Severity), specialist),
		AgentPrompt: body,
	}
}

// firstSentence returns the first sentence of s (up to the first '.', '!',
// '?' followed by space or end-of-string), trimmed and capped at ~80
// characters so it works as a prompt title.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for i, r := range s {
		if r == '.' || r == '!' || r == '?' {
			next := i + 1
			if next >= len(s) || s[next] == ' ' || s[next] == '\n' {
				s = s[:i]
				break
			}
		}
	}
	if len(s) > 80 {
		s = strings.TrimSpace(s[:80]) + "…"
	}
	return s
}

// filterAuthorPrompts returns the prompts that should still render plus the
// titles of those that were dropped because every referenced finding was
// arbiter-suppressed or user-skipped. Prompts with no finding_refs are kept
// unconditionally (legacy outputs / general advice).
func filterAuthorPrompts(d *Draft, prompts []AuthorPrompt) (kept []AuthorPrompt, droppedTitles []string) {
	if len(prompts) == 0 {
		return nil, nil
	}
	for _, p := range prompts {
		if isAuthorPromptAlive(d, p) {
			kept = append(kept, p)
			continue
		}
		title := strings.TrimSpace(p.Title)
		if title == "" {
			title = "untitled"
		}
		droppedTitles = append(droppedTitles, title)
	}
	return kept, droppedTitles
}

// isAuthorPromptAlive returns true when an AuthorPrompt should still render.
//
// Rules:
//   - No finding_refs → keep (general prompt, no specific anchor to filter
//     against).
//   - At least one ref still appears in d.FlatPostableFindingsForPost (i.e.
//     an inline ref that is not arbiter-suppressed and not user-skipped) →
//     keep.
//   - At least one ref points to a PR-wide finding (path empty, line 0)
//     that the specialist actually emitted → keep. PR-wide findings can't
//     be arbiter-suppressed or user-skipped, so a prompt that addresses
//     one is still actionable even if its inline siblings were filtered.
//     Without this, a vibe-coach prompt that bundles "fix this inline +
//     fix this PR-wide README issue" silently disappears the moment the
//     inline finding is suppressed, which is exactly the failure mode the
//     screenshot showed (request_changes verdict, zero rendered prompts,
//     PR-wide testing error stranded in the notes section).
//   - Every ref was suppressed or skipped → drop.
func isAuthorPromptAlive(d *Draft, p AuthorPrompt) bool {
	if len(p.FindingRefs) == 0 {
		return true
	}
	if d == nil {
		return true
	}
	live := map[string]struct{}{}
	for _, ff := range d.FlatPostableFindingsForPost() {
		live[suppressionKey(ff.Specialist, ff.Finding.Path, ff.Finding.Line, ff.Finding.Side)] = struct{}{}
	}
	prWide := prWideFindingKeys(d)
	for _, ref := range p.FindingRefs {
		k := suppressionKey(ref.Specialist, ref.Path, ref.Line, ref.Side)
		if _, ok := live[k]; ok {
			return true
		}
		if isPRWideRef(ref) {
			if _, ok := prWide[k]; ok {
				return true
			}
		}
	}
	return false
}

// isPRWideRef reports whether a finding_ref points to a PR-wide finding
// (no path, no line). PR-wide refs are never inline-suppressible, so they
// are tracked separately from FlatPostableFindingsForPost in
// isAuthorPromptAlive.
func isPRWideRef(ref FindingRef) bool {
	return strings.TrimSpace(ref.Path) == "" && ref.Line == 0
}

// prWideFindingKeys indexes every PR-wide finding the specialists actually
// emitted (with non-empty Comment), keyed the same way as inline findings
// so isAuthorPromptAlive can match a finding_ref against them. The
// suppression-key shape is reused even though PR-wide entries can't be
// suppressed — the key is just a convenient identity.
func prWideFindingKeys(d *Draft) map[string]struct{} {
	out := map[string]struct{}{}
	if d == nil {
		return out
	}
	for _, s := range d.Specialists {
		if s.Err != nil {
			continue
		}
		for _, f := range generalFindings(s.Findings) {
			out[suppressionKey(s.Specialist, f.Path, f.Line, f.Side)] = struct{}{}
		}
	}
	return out
}
