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
//     (no aiConfig, identical skip set as last run, or peruse mode
//     with an already-fresh draft), or
//   - sets phaseGeneratingSummary and returns a tea.Cmd that runs
//     vibe-coach against the final finding set off the UI thread.
//
// Callers should include the returned cmd in their tea.Batch. Safe to
// call repeatedly — coachInFlight guards against double-issue.
func (m *Model) enterSummary() tea.Cmd {
	m.syncUserSkipsToDraft()
	hash := ""
	if m.draft != nil {
		hash = skipSetHash(m.draft.UserSkipPostKeys)
	}
	// If vibe-coach is already current for this skip set, just flip
	// the phase and let the user see the cached summary. This is the
	// common case on re-entry (user backs out, doesn't change skips,
	// re-enters).
	if m.draft != nil && m.draft.VibeCoach != nil && hash == m.lastCoachHash && m.coachErr == nil {
		m.phase = phaseSummary
		m.vp.GotoTop()
		m.rebuildBody()
		return nil
	}
	// No AI config (tests / dev) → just show the summary with whatever
	// is on the draft. Production always passes a config.
	if m.aiConfig == nil || m.draft == nil {
		m.phase = phaseSummary
		m.vp.GotoTop()
		m.rebuildBody()
		return nil
	}
	// Already running a vibe-coach call → don't issue a second one.
	if m.coachInFlight {
		m.phase = phaseGeneratingSummary
		m.rebuildBody()
		return nil
	}
	m.coachInFlight = true
	m.coachErr = nil
	m.phase = phaseGeneratingSummary
	m.vp.GotoTop()
	m.rebuildBody()
	return runVibeCoachCmd(m.draft, m.aiConfig, hash)
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

// advanceCard moves to the next pending card. When the cards are
// exhausted it transitions into the next phase (confirmApprove or, via
// enterSummary, the summary phase — re-running vibe-coach first only
// when the user's skip set differs from the pipeline-time run).
// Returns the tea.Cmd that callers must include in their batch.
//
// Note: PostEvent() may currently return a stale verdict (vibe-coach
// hasn't re-run yet against the final skip set). That's fine — the
// confirmApprove vs summary decision is conservative: if the pre-skip
// verdict was APPROVE, the user wanted approval and the post-skip set
// can only have shrunk, so APPROVE is still safe. If it was anything
// else, we route through summary, where the VibeCoachDoneMsg handler
// can re-evaluate.
func (m *Model) advanceCard() tea.Cmd {
	if m.idx < len(m.cards) {
		m.idx++
	}
	if m.idx >= len(m.cards) {
		_, posted, skipped := m.tallyCardKinds()
		// User skipped every suggestion (posted no inline comments) but the AI
		// did not recommend approve — treat as disagreeing with the objections
		// and offer GitHub APPROVE before the long summary path.
		skipDisagree := m.draft != nil && m.draft.PostEvent() != "APPROVE" && posted == 0 && skipped > 0
		// Peruse mode never offers approval shortcuts — we only show
		// the rendered summary so the user can read it.
		if m.peruse {
			skipDisagree = false
		}
		m.approveAfterSkipDisagree = skipDisagree
		switch {
		case skipDisagree:
			m.phase = phaseConfirmApprove
			m.vp.GotoTop()
			return nil
		case !m.peruse && m.draft != nil && m.draft.PostEvent() == "APPROVE":
			m.approveAfterSkipDisagree = false
			m.phase = phaseConfirmApprove
			m.vp.GotoTop()
			return nil
		default:
			m.approveAfterSkipDisagree = false
			return m.enterSummary()
		}
	}
	m.vp.GotoTop()
	return nil
}

func (m *Model) actPostCurrent() (tea.Model, tea.Cmd) {
	if m.peruse {
		return m.flashPeruse("peruse mode — no posting; use ←/→ to navigate, f to jump to summary, q to exit")
	}
	if m.existingCommentsLoading || m.idx >= len(m.cards) || m.draft == nil || m.draft.PR == nil {
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
	if m.peruse {
		return m.flashPeruse("peruse mode — no posting; use ←/→ to navigate, f to jump to summary, q to exit")
	}
	if m.existingCommentsLoading || m.idx >= len(m.cards) || m.draft == nil || m.draft.PR == nil {
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
	return m, data.RefreshPRCmd(m.draft.Ref)
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
	if m.peruse {
		return m.flashPeruse("peruse mode — no skipping; use ←/→ to navigate, f to jump to summary, q to exit")
	}
	if m.idx >= len(m.cards) {
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

func (m *Model) actNext() (tea.Model, tea.Cmd) {
	if m.idx < len(m.cards)-1 {
		m.idx++
		m.vp.GotoTop()
		m.rebuildBody()
	}
	return m, nil
}

func (m *Model) actPrev() (tea.Model, tea.Cmd) {
	if m.idx > 0 {
		m.idx--
		m.vp.GotoTop()
		m.rebuildBody()
	}
	return m, nil
}

func (m *Model) actPostSummary() (tea.Model, tea.Cmd) {
	if m.peruse {
		return m.flashPeruse("peruse mode — no posting; q to exit without sending anything")
	}
	if m.draft == nil || m.draft.PR == nil {
		return m, nil
	}
	m.syncUserSkipsToDraft()
	// The summary phase posts a body-only review with the verdict event
	// (REQUEST_CHANGES or COMMENT). The approve verdict is handled by
	// actPostApprove which lives in its own phase.
	return m, data.PostReviewWithVerdictCmd(m.draft.Ref, m.draft, m.dryRun, "")
}

// actPostApprove posts a GitHub review with event=APPROVE and an empty body.
// Reachable only from phaseConfirmApprove (verdict=approve, user clicks Approve).
func (m *Model) actPostApprove() (tea.Model, tea.Cmd) {
	if m.peruse {
		return m.flashPeruse("peruse mode — no approving; q to exit without sending anything")
	}
	if m.draft == nil || m.draft.PR == nil {
		return m, nil
	}
	return m, data.PostReviewWithVerdictCmd(m.draft.Ref, m.draft, m.dryRun, "APPROVE")
}

// flashPeruse records a one-frame help-line hint to surface why an
// action key was ignored in peruse mode, then triggers a rebuild so
// the hint is visible. Returns the no-op (m, nil) tuple every caller
// uses, so it's an inline-friendly bail-out.
func (m *Model) flashPeruse(hint string) (tea.Model, tea.Cmd) {
	m.peruseHint = hint
	m.rebuildBody()
	return m, nil
}

// summaryPhaseOfferApproveWithoutSummary reports whether we should offer GitHub
// APPROVE with an empty body from the summary step. That applies when the merge
// verdict is not already APPROVE and this session posted no inline comments —
// e.g. the user skipped every suggestion, had no postable inlines, or only
// findings already on the PR.
func (m *Model) summaryPhaseOfferApproveWithoutSummary() bool {
	if m.peruse {
		// Peruse never offers approval shortcuts — the whole point is
		// "look without committing".
		return false
	}
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
