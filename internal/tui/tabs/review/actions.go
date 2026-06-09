package review

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
)

// VibeCoachDoneMsg is delivered when the TUI's lazy vibe-coach re-run
// (kicked off by enterSummary after the user changed skips) completes.
// The atSkipHash captures the state of d.UserSkipPostKeys when the call
// was issued so a stale completion (user has since changed skips again)
// doesn't overwrite a newer in-flight result.
type VibeCoachDoneMsg struct {
	Result      *review.VibeCoachResult
	AtSkipHash  string
	RequestedAt time.Time
}

// skipSetHash returns a stable hash of the user-skip set so enterSummary
// can decide whether to re-run vibe-coach. Empty set hashes to "".
func skipSetHash(keys map[string]struct{}) string {
	if len(keys) == 0 {
		return ""
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	sum := sha256.Sum256([]byte(strings.Join(out, "\n")))
	return hex.EncodeToString(sum[:])
}

// enterSummary is the canonical transition into phaseSummary. It syncs
// the user's skips onto the draft, then either:
//
//   - lands directly in phaseSummary when no LLM refresh is needed
//     (no aiConfig, identical skip set as last run), or
//   - sets phaseGeneratingSummary and returns a tea.Cmd that runs
//     vibe-coach against the final finding set off the UI thread.
//
// Callers should include the returned cmd in their tea.Batch. Safe to
// call repeatedly — coachInFlight guards against double-issue.
func (m *Model) enterSummary() tea.Cmd {
	m.syncUserSkipsToDraft()
	// First decide whether the summary tab should show its confirm-approve
	// sub-state instead of the full post-summary form. This subsumes the
	// old advanceCard routing: no findings at all, an APPROVE verdict, or
	// the user having skipped every objection they disagree with.
	_, posted, skipped := m.tallyCardKinds()
	switch {
	case m.noFindingsApprove:
		m.approveAfterSkipDisagree = false
		m.setSummaryPhase(phaseConfirmApprove)
		return nil
	case m.draft != nil && m.draft.PostEvent() != "APPROVE" && posted == 0 && skipped > 0:
		// Skipped every suggested inline (posted none) while the AI did
		// not recommend approve — offer GitHub APPROVE before the long
		// summary path.
		m.approveAfterSkipDisagree = true
		m.setSummaryPhase(phaseConfirmApprove)
		return nil
	case m.draft != nil && m.draft.PostEvent() == "APPROVE" && len(m.cards) > 0:
		m.approveAfterSkipDisagree = false
		m.setSummaryPhase(phaseConfirmApprove)
		return nil
	}
	m.approveAfterSkipDisagree = false

	hash := ""
	if m.draft != nil {
		hash = skipSetHash(m.draft.UserSkipPostKeys)
	}
	// If vibe-coach is already current for this skip set, just flip
	// the phase and let the user see the cached summary. This is the
	// common case on re-entry (user backs out, doesn't change skips,
	// re-enters).
	if m.draft != nil && m.draft.VibeCoach != nil && hash == m.lastCoachHash && m.coachErr == nil {
		m.setSummaryPhase(phaseSummary)
		return nil
	}
	// No AI config (tests / dev) → just show the summary with whatever
	// is on the draft. Production always passes a config.
	if m.aiConfig == nil || m.draft == nil {
		m.setSummaryPhase(phaseSummary)
		return nil
	}
	// Already running a vibe-coach call → don't issue a second one.
	if m.coachInFlight {
		m.setSummaryPhase(phaseGeneratingSummary)
		return nil
	}
	m.coachInFlight = true
	m.coachErr = nil
	m.setSummaryPhase(phaseGeneratingSummary)
	return runVibeCoachCmd(m.draft, m.aiConfig, hash)
}

// setSummaryPhase records a summary-tab sub-state and, when the summary
// tab is focused, mirrors it onto m.phase so the renderer/help/title pick
// it up. Callers use this instead of writing m.phase directly so the
// dual phase/tab state stays consistent.
func (m *Model) setSummaryPhase(p overlayPhase) {
	if m.onSummaryTab() {
		m.phase = p
	}
	m.vp.GotoTop()
	m.rebuildBody()
}

// runVibeCoachCmd kicks off vibe-coach against the draft's post-skip
// finding set on a background goroutine. The atSkipHash is echoed back
// in the done-msg so the receiver can drop stale results.
func runVibeCoachCmd(d *review.Draft, cfg *aiconfig.Config, atSkipHash string) tea.Cmd {
	requestedAt := time.Now()
	return func() tea.Msg {
		res := review.RunVibeCoachForDraft(context.Background(), cfg, d, nil)
		return VibeCoachDoneMsg{Result: res, AtSkipHash: atSkipHash, RequestedAt: requestedAt}
	}
}

// syncUserSkipsToDraft copies skipped approval-card findings onto the draft so
// RenderBody and FlatPostableFindingsForPost exclude them from the GitHub summary.
func (m *Model) syncUserSkipsToDraft() {
	if m.draft == nil {
		return
	}
	m.draft.UserSkipPostKeys = nil
	for _, c := range m.cards {
		if c.state != cardSkipped {
			continue
		}
		k := review.FindingSuppressionKey(c.finding.Specialist, c.finding.Finding)
		if m.draft.UserSkipPostKeys == nil {
			m.draft.UserSkipPostKeys = make(map[string]struct{})
		}
		m.draft.UserSkipPostKeys[k] = struct{}{}
	}
}

// advanceCard moves to the next pending finding within the currently
// focused agent tab after the user posts or skips one. It stays inside
// the agent — the summary is reached by selecting the summary tab, not by
// running off the end of a flat finding list. Returns nil (no phase
// transition).
func (m *Model) advanceCard() tea.Cmd {
	idxs := m.agentCardIndices(m.activeAgent())
	// Next pending card after the current one.
	for _, gi := range idxs {
		if gi > m.idx && m.cards[gi].state == cardPending {
			m.idx = gi
			m.vp.GotoTop()
			return nil
		}
	}
	// Wrap: first pending card anywhere in this agent.
	for _, gi := range idxs {
		if m.cards[gi].state == cardPending {
			m.idx = gi
			m.vp.GotoTop()
			return nil
		}
	}
	// Nothing left pending for this agent — keep the focus on the last
	// card so the reviewer still sees its resolved badge.
	m.vp.GotoTop()
	return nil
}

func (m *Model) actPostCurrent() (tea.Model, tea.Cmd) {
	if m.existingCommentsLoading || m.idx < 0 || m.idx >= len(m.cards) || m.draft == nil || m.draft.PR == nil {
		return m, nil
	}
	cur := &m.cards[m.idx]
	if cur.state == cardAlreadyOnPR {
		advCmd := m.advanceCard()
		m.rebuildBody()
		return m, advCmd
	}
	// Local pre-flight: if we couldn't anchor this finding to any hunk in the
	// parsed diff, GitHub's reviews/comments endpoints will reject it with
	// "pull_request_review_thread.line could not be resolved". Catch it here
	// so the user gets an actionable, local explanation instead of a 422.
	if !m.dryRun && cur.hunk == nil {
		cur.state = cardError
		cur.err = fmt.Errorf("can't post inline: %s:%d isn't on a hunk in the current PR diff (line may have moved or been removed). Press F to post as a file-level comment, R to refresh the PR, or s to skip this finding.",
			cur.finding.Finding.Path, cur.finding.Finding.Line)
		m.rebuildBody()
		return m, nil
	}
	cmd := data.PostSingleFindingCmd(m.draft.Ref, m.draft.PR, cur.finding.Specialist, cur.finding.Finding, m.dryRun)
	return m, cmd
}

// actPostCurrentFileLevel is the file-level fallback: post the current
// finding as a subject_type=file comment instead of an inline one. It
// applies in two situations:
//
//   - The card is in cardError state because actPostCurrent's local
//     pre-flight (cur.hunk == nil) or GitHub's line-resolution returned
//     "line could not be resolved". This is the canonical case the F
//     hotkey was added for.
//
//   - The card is in cardPending state but we never anchored a hunk in
//     the first place (rare — typically PR-wide style findings that the
//     model emitted with a path + line that no longer exists). The user
//     can press F preemptively without forcing the "(no hunk located)"
//     warning into an explicit error first.
//
// In every other state — already on the PR, already posted, already
// skipped, or pending with a valid hunk — F is a no-op (the inline
// post is still the right choice and the reviewer should press y).
func (m *Model) actPostCurrentFileLevel() (tea.Model, tea.Cmd) {
	if m.existingCommentsLoading || m.idx < 0 || m.idx >= len(m.cards) || m.draft == nil || m.draft.PR == nil {
		return m, nil
	}
	cur := &m.cards[m.idx]
	switch cur.state {
	case cardError:
		// Allowed — fall through.
	case cardPending:
		if cur.hunk != nil {
			// The inline post is still viable; ignore F to avoid
			// silently downgrading a perfectly-anchored finding.
			return m, nil
		}
	default:
		return m, nil
	}
	cur.fileLevelPost = true
	cmd := data.PostSingleFindingFileLevelCmd(m.draft.Ref, m.draft.PR, cur.finding.Specialist, cur.finding.Finding, m.dryRun)
	return m, cmd
}

// actRefreshPR re-fetches the PR view (head SHA) and unified diff so the
// overlay can re-anchor each pending card to the new diff. It's the recovery
// path for "PR head moved" and "line could not be resolved" errors.
func (m *Model) actRefreshPR() (tea.Model, tea.Cmd) {
	if m.refreshing || m.draft == nil {
		return m, nil
	}
	m.refreshing = true
	m.refreshNote = ""
	m.rebuildBody()
	return m, data.RefreshPRCmd(m.draft.Ref, m.demoMode)
}

// applyPRRefresh adopts a freshly fetched PR + diff, re-anchors every pending
// approval card to the new diff, and clears any stale per-card / summary
// error state so the user can immediately retry. Findings whose original line
// is no longer on a hunk are flagged as cardError with a local message —
// GitHub would reject those anyway.
func (m *Model) applyPRRefresh(pr *gh.PR, diff string) {
	m.refreshing = false
	if m.draft == nil || pr == nil {
		return
	}
	wasSHA := ""
	if m.draft.PR != nil {
		wasSHA = m.draft.PR.HeadSHA
	}
	m.draft.PR = pr
	m.draft.Diff = diff
	m.files = review.ParseDiff(diff)

	unanchored := 0
	for i := range m.cards {
		c := &m.cards[i]
		// For cards the reviewer already acted on (posted / skipped /
		// already-on-PR), the anchor still gets recomputed so the
		// inline hunk snippet stays accurate, but we do NOT try to
		// re-anchor via the model's AnchorExcerpt — moving an already-
		// posted comment's local "anchor" silently would be misleading
		// (the comment on GitHub stays where it was). For pending /
		// error cards we DO try the excerpt relocation because the
		// reviewer is about to post and we want the best available
		// line, mirroring AdoptDraft's behaviour.
		switch c.state {
		case cardPosted, cardSkipped, cardAlreadyOnPR:
			c.file = review.FindFile(m.files, c.finding.Finding.Path)
			if c.file != nil {
				c.hunk, _ = review.HunkAroundLine(c.file, c.finding.Finding.Line)
			} else {
				c.hunk = nil
			}
			continue
		}
		anchorCardToDiff(c, m.files)
		if c.state == cardError {
			c.state = cardPending
			c.err = nil
		}
		if c.hunk == nil {
			unanchored++
		}
	}
	// Reset the summary-phase error so the user can re-attempt the post.
	m.summaryErr = nil

	switch {
	case wasSHA != "" && wasSHA != pr.HeadSHA && unanchored > 0:
		m.refreshNote = fmt.Sprintf("PR refreshed · head %s → %s · %d finding(s) no longer anchor to a hunk on the new diff (skip them or edit on GitHub).",
			shortSHA(wasSHA), shortSHA(pr.HeadSHA), unanchored)
	case wasSHA != "" && wasSHA != pr.HeadSHA:
		m.refreshNote = fmt.Sprintf("PR refreshed · head %s → %s · all findings still anchor to the new diff.",
			shortSHA(wasSHA), shortSHA(pr.HeadSHA))
	case unanchored > 0:
		m.refreshNote = fmt.Sprintf("PR refreshed · head unchanged · %d finding(s) don't anchor to a hunk on the current diff.", unanchored)
	default:
		m.refreshNote = "PR refreshed · head unchanged · all findings re-anchored."
	}
	m.rebuildBody()
}

// shortSHA returns the first 7 chars of a SHA, or s if shorter.
func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func (m *Model) actSkipCurrent() (tea.Model, tea.Cmd) {
	if m.idx < 0 || m.idx >= len(m.cards) {
		return m, nil
	}
	if m.cards[m.idx].state == cardAlreadyOnPR {
		advCmd := m.advanceCard()
		m.rebuildBody()
		return m, advCmd
	}
	m.cards[m.idx].state = cardSkipped
	advCmd := m.advanceCard()
	m.rebuildBody()
	return m, advCmd
}

// actNext / actPrev move the focused finding within the active agent tab.
func (m *Model) actNext() (tea.Model, tea.Cmd) {
	idxs := m.agentCardIndices(m.activeAgent())
	pos := positionOf(idxs, m.idx)
	if pos >= 0 && pos < len(idxs)-1 {
		m.idx = idxs[pos+1]
		m.vp.GotoTop()
		m.rebuildBody()
	}
	return m, nil
}

func (m *Model) actPrev() (tea.Model, tea.Cmd) {
	idxs := m.agentCardIndices(m.activeAgent())
	pos := positionOf(idxs, m.idx)
	if pos > 0 {
		m.idx = idxs[pos-1]
		m.vp.GotoTop()
		m.rebuildBody()
	}
	return m, nil
}

// positionOf returns the position of v in s, or -1.
func positionOf(s []int, v int) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func (m *Model) actPostSummary() (tea.Model, tea.Cmd) {
	if m.draft == nil || m.draft.PR == nil {
		return m, nil
	}
	m.syncUserSkipsToDraft()
	// The summary phase posts a body-only review with the verdict event
	// (REQUEST_CHANGES or COMMENT). The approve verdict is handled by
	// actPostApprove which lives in its own phase.
	return m, data.PostReviewWithVerdictCmd(m.draft.Ref, m.draft, m.dryRun, m.demoMode, "")
}

// actPostApprove posts a GitHub review with event=APPROVE and an empty body.
// Reachable only from phaseConfirmApprove (verdict=approve, user clicks Approve).
//
// In the no-findings auto-approve flow (m.noFindingsApprove) the underlying
// Draft.RenderBodyForEvent attaches the "no issues found by any agent"
// rendered body to the APPROVE post so the GitHub review explains the
// thumbs-up. The "Approve only" sibling action (actPostApproveOnly) bypasses
// that and posts APPROVE with an explicit empty body for reviewers who don't
// want any review text published alongside the approval.
func (m *Model) actPostApprove() (tea.Model, tea.Cmd) {
	if m.draft == nil || m.draft.PR == nil {
		return m, nil
	}
	return m, data.PostReviewWithVerdictCmd(m.draft.Ref, m.draft, m.dryRun, m.demoMode, "APPROVE")
}

// actPostApproveOnly posts a content-free GitHub APPROVE — event=APPROVE with
// an explicit empty body, bypassing the "no issues found by any agent"
// summary that actPostApprove would otherwise attach in the no-findings
// auto-approve flow. Reachable from phaseConfirmApprove when
// m.noFindingsApprove is set (the only state where actPostApprove would
// produce a non-empty body) and from phaseSummary as the "Approve only"
// button there, where APPROVE always means "no body" already so the two
// paths are equivalent.
func (m *Model) actPostApproveOnly() (tea.Model, tea.Cmd) {
	if m.draft == nil || m.draft.PR == nil {
		return m, nil
	}
	return m, data.PostApproveBareCmd(m.draft.Ref, m.draft, m.dryRun, m.demoMode)
}

// summaryPhaseOfferApproveWithoutSummary reports whether the summary step
// should *nudge* the user toward submitting a body-less APPROVE — i.e.
// surface the contextual "you posted no inline comments this session, you
// can approve without publishing the summary" hint paragraph. That applies
// when the merge verdict is not already APPROVE and this session posted
// no inline comments — e.g. the user skipped every suggestion, had no
// postable inlines, or only findings already on the PR.
//
// This governs only the suggestion text. The "Approve only (a)" button
// itself is always available at phaseSummary (see
// summaryPhaseAllowApproveOnly) — the human reviewer must always be able
// to override the AI's verdict and approve.
func (m *Model) summaryPhaseOfferApproveWithoutSummary() bool {
	if m.draft == nil || m.draft.PR == nil {
		return false
	}
	if m.draft.PostEvent() == "APPROVE" {
		return false
	}
	onPR, sessPosted, skippedOnly := m.tallyCardKinds()
	if sessPosted > 0 {
		return false
	}
	if len(m.cards) == 0 {
		return true
	}
	return skippedOnly > 0 || onPR+skippedOnly == len(m.cards)
}

// summaryPhaseAllowApproveOnly reports whether the "Approve only" button
// should be rendered (and the corresponding `a` key + click zone be live)
// at phaseSummary.
//
// The button is intentionally always available so the human reviewer can
// approve at any time — whether they posted inline comments, skipped them
// all, or never had any to start with. The previous gate
// (summaryPhaseOfferApproveWithoutSummary) hid the button after a single
// inline post, which conflated "we suggest this path" with "this path is
// allowed"; the user-facing principle is that the GitHub approval signal
// always represents the human's own judgement, so the option to approve
// must always be reachable from the final review screen.
//
// Only a missing draft/PR disables it.
func (m *Model) summaryPhaseAllowApproveOnly() bool {
	if m.draft == nil || m.draft.PR == nil {
		return false
	}
	return true
}
