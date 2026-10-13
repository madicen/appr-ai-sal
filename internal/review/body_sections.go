package review

import (
	"fmt"
	"strconv"
	"strings"
)

// BodySectionKind classifies a unit of the GitHub summary body.
type BodySectionKind string

const (
	BodySectionHeadline       BodySectionKind = "headline"
	BodySectionDiffTruncation BodySectionKind = "diff_truncation"
	BodySectionCaution        BodySectionKind = "caution"
	BodySectionStats          BodySectionKind = "stats"
	BodySectionPromptTopic    BodySectionKind = "prompt_topic"
	BodySectionPromptNote     BodySectionKind = "prompt_note" // arbiter/skip dropped-prompt disclosure
	BodySectionPRWideNote     BodySectionKind = "pr_wide_note"
	BodySectionDemotedNote    BodySectionKind = "demoted_note"
	BodySectionRepoExperts    BodySectionKind = "repo_experts"
	BodySectionProcess        BodySectionKind = "process"
)

// BodySection is one ordered, individually includable/editable unit of the
// summary body. Markdown is the fragment ComposeBody joins into the post.
type BodySection struct {
	ID       string
	Kind     BodySectionKind
	Title    string // TUI label
	Markdown string // fragment joined into the GitHub body
	Fixed    bool   // true only for Caution

	// synthesized marks a prompt topic auto-built from a blocking finding the
	// vibe-coach didn't bundle; ComposeBody uses it to size the disclosure line.
	synthesized bool
}

// BodySectionOverride is a reviewer's per-section choice. A missing override
// means "included, unedited".
type BodySectionOverride struct {
	Included bool
	Edited   string // empty means use section.Markdown

	// BaseMarkdown is the section's generated Markdown when the override was
	// created. The TUI drops the override once the live section's Markdown no
	// longer matches, so positional IDs can't carry edits onto different
	// content after a re-sync. Empty means "not tracked" (never dropped).
	BaseMarkdown string
}

const (
	prWideNotesHeading = "### PR-wide notes\n\n" +
		"_Not tied to a single changed line — all other findings are inline comments on the diff._\n\n"
	demotedNotesHeading = "### PR-wide notes — included despite demotion\n\n" +
		"_The repo arbiter demoted these below the review threshold; the reviewer chose to surface them anyway._\n\n"
	promptsHeading = "### 🤖 Prompt for your AI assistant\n\n"
)

type promptTopic struct {
	title       string
	markdown    string
	synthesized bool
}

// vibeCoachPromptParts returns the paste-ready prompt topics (kept +
// synthesized, each one combineAuthorPrompts segment) and the optional
// dropped-prompts disclosure note.
//
// Prompts are filtered against the draft: any prompt whose finding_refs are
// all arbiter-suppressed or user-skipped is dropped, so the rendered review
// only suggests AI prompts for findings that actually made it through to
// GitHub. Legacy / general prompts (no finding_refs) are kept unconditionally.
// After filtering, uncovered error/critical findings get synthesized fallback
// prompts so the author always has a paste-ready instruction per blocker.
func vibeCoachPromptParts(d *Draft, vc *VibeCoachResult) (topics []promptTopic, note string) {
	if vc == nil || vc.Err != nil {
		return nil, ""
	}
	kept, droppedTitles := filterAuthorPrompts(d, vc.Prompts)
	synthesized := synthesizeFallbackPrompts(d, kept)
	rendered := append([]AuthorPrompt(nil), kept...)
	rendered = append(rendered, synthesized...)
	for i, p := range rendered {
		seg := combineAuthorPrompts([]AuthorPrompt{p})
		if seg == "" {
			continue
		}
		title := strings.TrimSpace(p.Title)
		if title == "" {
			title = "Prompt topic " + strconv.Itoa(i+1)
		}
		topics = append(topics, promptTopic{
			title:       title,
			markdown:    seg,
			synthesized: i >= len(kept),
		})
	}

	// Disclosure: the human reader needs to know that some follow-up prompts
	// were silently dropped because every inline finding they bundled was
	// suppressed or skipped — otherwise the verdict / summary above can look
	// mismatched ("request changes" + zero prompts). The wording is
	// deliberately scoped to "inline findings the prompt pointed to" so the
	// reader doesn't conclude that *every* finding was dropped.
	switch {
	case len(droppedTitles) == 0:
	case len(topics) == 0:
		note = fmt.Sprintf("_%s dropped because the inline findings they pointed to were all suppressed by the repo arbiter or skipped during review. The verdict above is based on the broader review (PR-wide notes and repo-expert panel below, if any), not just those inline findings._\n\n",
			countLabel(len(droppedTitles), "The 1 paste-ready follow-up prompt was", "All %d paste-ready follow-up prompts were"))
	default:
		note = fmt.Sprintf("_%s dropped because the inline findings they pointed to were all suppressed or skipped: %s._\n\n",
			countLabel(len(droppedTitles), "1 paste-ready follow-up prompt was", "%d paste-ready follow-up prompts were"),
			joinQuoted(droppedTitles))
	}
	return topics, note
}

// BodySections splits the summary body into ordered units. ComposeBody with
// nil overrides reproduces the full body. It does not cover the no-findings
// special case, which RenderBody handles itself.
func (d *Draft) BodySections() []BodySection {
	if d == nil {
		return nil
	}

	type agentErr struct{ name, msg string }
	var failures []agentErr
	var skippedStages []string
	type note struct {
		specialist string
		f          Finding
	}
	var prWide []note

	for _, s := range d.Specialists {
		switch s.EffectiveOutcome() {
		case OutcomeSkipped:
			// Never ran (circuit breaker aborted the remaining stages). Listed
			// separately from failures so the reader knows the review is
			// partial rather than that the agent errored.
			skippedStages = append(skippedStages, s.Specialist)
			continue
		case OutcomeFailed:
			msg := "failed after retries"
			if s.Err != nil {
				msg = s.Err.Error()
			}
			failures = append(failures, agentErr{s.Specialist, msg})
			continue
		}
		for _, f := range generalFindings(s.Findings) {
			if d.generalFindingSuppressed(s.Specialist, f) {
				continue
			}
			prWide = append(prWide, note{s.Specialist, f})
		}
	}

	// Demoted PR-wide findings the reviewer opted to include despite the
	// arbiter demoting them below the strictness floor. Inline demoted
	// findings post through the opt-in card flow, not here.
	var demotedIncluded []note
	for _, ff := range d.DemotedHidden {
		if findingIsInlinePostable(ff.Finding) {
			continue
		}
		if strings.TrimSpace(ff.Finding.Comment) == "" {
			continue
		}
		if !d.DemotedPostingEnabled(ff.Specialist, ff.Finding) {
			continue
		}
		demotedIncluded = append(demotedIncluded, note{ff.Specialist, ff.Finding})
	}

	vcDisp := d.effectiveVibeCoach()
	vcOK := vcDisp != nil && vcDisp.Err == nil
	verdict := ""
	if vcOK {
		verdict = NormalizeVibeVerdict(vcDisp.Verdict)
	}

	var out []BodySection

	// 1. Headline (verdict folded in), downgrade note, TL;DR.
	var head strings.Builder
	head.WriteString(bodyHeadline(verdict))
	if vcOK {
		if n := strings.TrimSpace(d.VerdictReconciliationNote()); n != "" {
			head.WriteString("> " + n + "\n\n")
		}
		if s := strings.TrimSpace(vcDisp.Summary); s != "" {
			head.WriteString(s + "\n\n")
		}
	}
	out = append(out, BodySection{ID: "headline", Kind: BodySectionHeadline, Title: "Headline & summary", Markdown: head.String()})

	// 2. Warnings and the merged AI disclosure / human-review caution.
	if trunc := d.diffTruncationNote(); trunc != "" {
		out = append(out, BodySection{ID: "diff_truncation", Kind: BodySectionDiffTruncation, Title: "Diff truncation warning", Markdown: trunc})
	}
	out = append(out, BodySection{ID: "caution", Kind: BodySectionCaution, Title: "AI disclosure (required)", Markdown: bodyCaution, Fixed: true})

	// 3. One-line orientation stats (+ request-changes-without-prompts warning).
	stats := bodyStatsLine(
		len(d.FlatPostableFindingsForPost()),
		len(prWide)+len(demotedIncluded),
		len(failures)+len(skippedStages))
	if vcOK && vcDisp.RequestChangesWithoutPrompts {
		stats += "**Warning:** Verdict is **request changes**, but no paste-ready AI prompts were returned — rely on the inline comments on the diff and the PR-wide notes below (if any).\n\n"
	}
	if stats != "" {
		out = append(out, BodySection{ID: "stats", Kind: BodySectionStats, Title: "Stats", Markdown: stats})
	}

	// 4. The author's main action item: the paste-ready prompt topics.
	if vcOK {
		topics, promptNote := vibeCoachPromptParts(d, vcDisp)
		for i, t := range topics {
			out = append(out, BodySection{
				ID:          "prompt:" + strconv.Itoa(i),
				Kind:        BodySectionPromptTopic,
				Title:       "Prompt: " + t.title,
				Markdown:    t.markdown,
				synthesized: t.synthesized,
			})
		}
		if promptNote != "" {
			out = append(out, BodySection{ID: "prompt_note", Kind: BodySectionPromptNote, Title: "Dropped prompts note", Markdown: promptNote})
		}
	}

	// 5. Substantive findings that live in the body rather than on the diff.
	// Indices are per specialist so one specialist's findings changing does
	// not shift the IDs (and thus reviewer overrides) of another's.
	prWideIdx := map[string]int{}
	for _, item := range prWide {
		i := prWideIdx[item.specialist]
		prWideIdx[item.specialist]++
		out = append(out, BodySection{
			ID:       "prwide:" + item.specialist + ":" + strconv.Itoa(i),
			Kind:     BodySectionPRWideNote,
			Title:    "PR-wide note · " + item.specialist,
			Markdown: "- **" + string(item.f.Severity) + " · " + item.specialist + ":** " + item.f.Comment + "\n",
		})
	}
	demotedIdx := map[string]int{}
	for _, item := range demotedIncluded {
		i := demotedIdx[item.specialist]
		demotedIdx[item.specialist]++
		out = append(out, BodySection{
			ID:       "demoted:" + item.specialist + ":" + strconv.Itoa(i),
			Kind:     BodySectionDemotedNote,
			Title:    "Demoted note · " + item.specialist,
			Markdown: "- **" + item.specialist + " _(demoted by repo arbiter)_:** " + item.f.Comment + "\n",
		})
	}

	// 6. Repo expert panel.
	if d.RepoArbiter != nil {
		if panel := repoExpertPanelSection(d); panel != "" {
			out = append(out, BodySection{ID: "repo_experts", Kind: BodySectionRepoExperts, Title: "Repo expert panel", Markdown: panel})
		}
	}

	// 7. Process metadata, collapsed. The stats line already flags a degraded
	// run; the details block carries the specifics.
	userSkips := len(d.UserSkipPostKeys)
	if len(failures)+len(skippedStages) > 0 || userSkips > 0 {
		var sum []string
		if len(failures) > 0 {
			sum = append(sum, countLabel(len(failures), "1 agent failed", "%d agents failed"))
		}
		if len(skippedStages) > 0 {
			sum = append(sum, countLabel(len(skippedStages), "1 stage skipped", "%d stages skipped"))
		}
		if userSkips > 0 {
			sum = append(sum, countLabel(userSkips, "1 suggestion skipped by reviewer", "%d suggestions skipped by reviewer"))
		}
		var b strings.Builder
		b.WriteString("<details>\n<summary>⚙️ Review process details — " + strings.Join(sum, " · ") + "</summary>\n\n")
		if len(failures) > 0 {
			b.WriteString("### Agent failures _(failed after retries)_\n\n")
			for _, e := range failures {
				b.WriteString("- **" + e.name + ":** _" + e.msg + "_\n")
			}
			b.WriteString("\n")
		}
		if len(skippedStages) > 0 {
			b.WriteString("### Stages skipped _(run aborted early)_\n\n")
			b.WriteString("_The run's circuit breaker stopped these agents before they ran, so this review is partial — review the areas they cover manually._\n\n")
			for _, name := range skippedStages {
				b.WriteString("- **" + name + "**\n")
			}
			b.WriteString("\n")
		}
		if userSkips > 0 {
			b.WriteString("### Reviewer choices\n\n")
			fmt.Fprintf(&b, "_%s skipped during review — not included in this GitHub post._\n\n",
				countLabel(userSkips, "1 inline suggestion", "%d inline suggestions"))
		}
		b.WriteString("</details>\n")
		out = append(out, BodySection{ID: "process", Kind: BodySectionProcess, Title: "Review process details", Markdown: b.String()})
	}

	return out
}

// sectionIncluded reports whether s survives the overrides. Fixed sections are
// always included; a missing override defaults to included.
func sectionIncluded(s BodySection, overrides map[string]BodySectionOverride) bool {
	if s.Fixed {
		return true
	}
	ov, ok := overrides[s.ID]
	if !ok {
		return true
	}
	return ov.Included
}

// sectionMarkdown returns the effective fragment for an included section:
// the reviewer's edit when non-empty (never for Fixed), else the original.
// Edited fragments are normalised to end in exactly one blank line (bullets:
// one newline) so neighbouring fragments can't run together.
func sectionMarkdown(s BodySection, overrides map[string]BodySectionOverride) string {
	if s.Fixed {
		return s.Markdown
	}
	ov, ok := overrides[s.ID]
	if !ok || strings.TrimSpace(ov.Edited) == "" {
		return s.Markdown
	}
	edited := strings.TrimRight(ov.Edited, " \t\r\n")
	switch s.Kind {
	case BodySectionPRWideNote, BodySectionDemotedNote:
		return edited + "\n"
	default:
		return edited + "\n\n"
	}
}

// ComposeBody joins sections into the GitHub summary markdown, honouring
// per-section include/edit overrides (nil overrides = everything included,
// unedited). Group wrappers (prompt heading + fence, PR-wide note headings)
// are recomputed from the surviving members.
func ComposeBody(sections []BodySection, overrides map[string]BodySectionOverride) string {
	var b strings.Builder
	var promptsDone, prWideHeaded, demotedHeaded bool

	for _, s := range sections {
		switch s.Kind {
		case BodySectionPromptTopic:
			if promptsDone {
				continue
			}
			promptsDone = true
			var segs []string
			synth := 0
			for _, t := range sections {
				if t.Kind != BodySectionPromptTopic || !sectionIncluded(t, overrides) {
					continue
				}
				seg := strings.TrimSpace(sectionMarkdown(t, overrides))
				if seg == "" {
					continue
				}
				segs = append(segs, seg)
				if t.synthesized {
					synth++
				}
			}
			if len(segs) == 0 {
				continue
			}
			b.WriteString(promptsHeading)
			if len(segs) <= 1 {
				b.WriteString("_Paste the fenced block below into your coding assistant (Cursor, Claude Code, etc.)._\n\n")
			} else {
				fmt.Fprintf(&b, "_Paste the fenced block below into your coding assistant (Cursor, Claude Code, etc.) — **%d distinct topics** separated by `---`._\n\n", len(segs))
			}
			if synth > 0 {
				fmt.Fprintf(&b, "_%s auto-built from blocking findings the vibe-coach didn't bundle into a paste-ready prompt; the wording is verbatim from the specialist's comment._\n\n",
					countLabel(synth, "1 topic was", "%d topics were"))
			}
			b.WriteString("```text\n")
			b.WriteString(strings.Join(segs, "\n\n---\n\n"))
			b.WriteString("\n```\n\n")
		case BodySectionPRWideNote:
			if !sectionIncluded(s, overrides) {
				continue
			}
			if !prWideHeaded {
				b.WriteString(prWideNotesHeading)
				prWideHeaded = true
			}
			b.WriteString(sectionMarkdown(s, overrides))
			if nextKindIsNot(sections, s, BodySectionPRWideNote, overrides) {
				b.WriteString("\n")
			}
		case BodySectionDemotedNote:
			if !sectionIncluded(s, overrides) {
				continue
			}
			if !demotedHeaded {
				b.WriteString(demotedNotesHeading)
				demotedHeaded = true
			}
			b.WriteString(sectionMarkdown(s, overrides))
			if nextKindIsNot(sections, s, BodySectionDemotedNote, overrides) {
				b.WriteString("\n")
			}
		default:
			if !sectionIncluded(s, overrides) {
				continue
			}
			b.WriteString(sectionMarkdown(s, overrides))
		}
	}
	return b.String()
}

// nextKindIsNot reports whether no later included section of the given kind
// follows cur, i.e. cur closes its bullet group and needs a trailing blank
// line.
func nextKindIsNot(sections []BodySection, cur BodySection, kind BodySectionKind, overrides map[string]BodySectionOverride) bool {
	after := false
	for _, s := range sections {
		if !after {
			if s.ID == cur.ID {
				after = true
			}
			continue
		}
		if s.Kind == kind && sectionIncluded(s, overrides) {
			return false
		}
	}
	return true
}
