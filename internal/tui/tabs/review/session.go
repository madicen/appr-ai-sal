package review

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
)

// session.go implements the TUI half of U2 (draft persistence & resume): it
// serializes the overlay's decision layer (per-card state + cursor + focused
// tab) alongside a snapshot of the completed Draft, so quitting mid-approval
// can be resumed later WITHOUT re-running the LLM pipeline.
//
// Persistence rules:
//   - Only after the pipeline finished (m.done): there is nothing resumable
//     before the Draft is adopted.
//   - Never in demo mode: demo recordings must stay self-contained and
//     reproducible; a stray session file on disk would leak between runs.
//   - Debounced: decision toggles schedule a save via a tea.Tick so a burst of
//     keystrokes collapses to one atomic write; quitting flushes synchronously
//     so the last decision is never lost.
//   - Cleared on a real post (MarkSummaryPosted): the run is done, so the
//     session must not be offered for resume afterwards.

// sessionSaveDebounce is how long a decision change waits before the coalesced
// atomic write fires. Long enough to batch a rapid y/y/y/s burst, short enough
// that a quit-before-flush window is negligible (and quit flushes anyway).
const sessionSaveDebounce = 400 * time.Millisecond

// sessionSaveMsg is the debounced save trigger. seq lets a later schedule
// supersede an earlier pending tick so only the final state in a burst is
// written. It implements data.ForwardToOverlay so the root routes it back to
// the overlay without a bespoke case.
type sessionSaveMsg struct{ seq int }

// OverlayBound marks sessionSaveMsg for generic overlay routing (see
// data.ForwardToOverlay).
func (sessionSaveMsg) OverlayBound() {}

var _ data.ForwardToOverlay = sessionSaveMsg{}

// cardIdentity returns a stable key for one approval card so a persisted
// decision re-attaches to the right card after the Draft is rehydrated. Normal
// cards use the finding suppression key; demoted and memory-suppressed cards
// live on side-lists and are namespaced so they never collide with a normal
// card for the same finding.
func (m *Model) cardIdentity(c approvalCard) string {
	base := review.FindingSuppressionKey(c.finding.Specialist, c.finding.Finding)
	switch {
	case c.memorySuppressed:
		return fmt.Sprintf("memory:%d:%s", c.memorySuppIdx, base)
	case c.demoted:
		return "demoted:" + review.DemotedFindingKey(c.finding.Specialist, c.finding.Finding)
	default:
		return base
	}
}

// decisionString maps a card state to its persisted token.
func decisionString(s approvalCardState) string {
	switch s {
	case cardPosted:
		return "posted"
	case cardSkipped:
		return "skipped"
	case cardError:
		return "error"
	case cardAlreadyOnPR:
		return "already_on_pr"
	default:
		return "pending"
	}
}

// parseCardDecision maps a persisted token back to a card state. An "error"
// token restores as pending: the failed post attempt's error message is not
// persisted and the finding is still eligible to post, so the reviewer sees a
// clean, retryable card rather than a dangling error with no detail.
func parseCardDecision(s string) approvalCardState {
	switch s {
	case "posted":
		return cardPosted
	case "skipped":
		return cardSkipped
	case "already_on_pr":
		return cardAlreadyOnPR
	default:
		return cardPending
	}
}

// sessionHeadSHA returns the head SHA the session is keyed by, or "" when it
// can't be determined (no draft / PR / SHA) — in which case persistence is a
// no-op, matching B2's "nothing to key on" guard.
func (m *Model) sessionHeadSHA() string {
	if m.draft == nil || m.draft.PR == nil {
		return ""
	}
	return strings.TrimSpace(m.draft.PR.HeadSHA)
}

// sessionPersistable reports whether the overlay is in a state worth
// persisting: pipeline finished, not demo mode, and a usable SHA key exists.
func (m *Model) sessionPersistable() bool {
	return m.done && !m.demoMode && m.sessionHeadSHA() != ""
}

// collectSession snapshots the current overlay into a review.SessionState.
// Returns nil when there is nothing to key on.
func (m *Model) collectSession() *review.SessionState {
	sha := m.sessionHeadSHA()
	if sha == "" {
		return nil
	}
	decisions := make([]review.CardDecision, 0, len(m.cards))
	for _, c := range m.cards {
		d := review.CardDecision{
			Key:        m.cardIdentity(c),
			Decision:   decisionString(c.state),
			Resurfaced: c.memorySuppressed && c.state != cardSkipped,
		}
		// Phase 5 item 2: persist an edited comment so a U2 resume restores the
		// reviewer's own words (the Draft snapshot carries the model's original
		// comment; the per-card edit lives only here).
		if c.edited {
			d.EditedBody = c.finding.Finding.Comment
		}
		decisions = append(decisions, d)
	}
	return &review.SessionState{
		HeadSHA:   sha,
		Ref:       m.draft.Ref,
		Draft:     m.draft.SessionSnapshot(),
		Cursor:    m.idx,
		ActiveTab: m.activeTab,
		Decisions: decisions,
		CoachHash: m.lastCoachHash,
	}
}

// scheduleSessionSave marks the session dirty and returns a debounced save
// tick. Returns nil (no scheduling) when the overlay isn't in a persistable
// state, so callers can unconditionally batch it into their tea.Cmd.
func (m *Model) scheduleSessionSave() tea.Cmd {
	if !m.sessionPersistable() {
		return nil
	}
	m.sessionDirty = true
	m.sessionSaveSeq++
	seq := m.sessionSaveSeq
	return tea.Tick(sessionSaveDebounce, func(time.Time) tea.Msg { return sessionSaveMsg{seq: seq} })
}

// persistSession writes the current session to the draft-cache dir (atomic,
// fail-open). Used both by the debounce handler and as a synchronous flush on
// quit. A no-op when not persistable.
func (m *Model) persistSession() {
	if !m.sessionPersistable() {
		return
	}
	s := m.collectSession()
	if s == nil {
		return
	}
	// Fail-open: a write failure must never break the approval flow (B2's
	// contract). The next decision toggle retries.
	_ = review.NewDraftCache().SaveSession(s)
	m.sessionDirty = false
}

// clearSession removes any persisted session for this PR's head SHA. Called
// after a successful post: the run is complete and must not be offered for
// resume. Fail-open.
func (m *Model) clearSession() {
	sha := m.sessionHeadSHA()
	if sha == "" || m.demoMode {
		return
	}
	review.NewDraftCache().ClearSession(m.draft.Ref, sha)
}

// closeCmd flushes any pending session write synchronously, then emits the
// CloseMsg that tears the overlay down. Used everywhere the overlay closes so
// the last decision is persisted even if the debounce tick hasn't fired.
func (m *Model) closeCmd() tea.Cmd {
	m.persistSession()
	return func() tea.Msg { return CloseMsg{} }
}

// codeSpecialistNames returns the code-specialist names present in the draft
// (in draft order), used to narrow the resumed overlay's tab layout to the same
// specialists the original run used. PR agents live on their own side of the
// tab bar (AllPRAgents) so they're filtered out here.
func codeSpecialistNames(d *review.Draft) []string {
	if d == nil {
		return nil
	}
	valid := make(map[string]bool, len(review.AllSpecialists))
	for _, n := range review.AllSpecialists {
		valid[n] = true
	}
	var out []string
	for _, sr := range d.Specialists {
		if valid[sr.Specialist] {
			out = append(out, sr.Specialist)
		}
	}
	return out
}

// applySession overlays a restored session's decisions, cursor, and focused tab
// onto an overlay whose Draft has already been adopted. It returns any tea.Cmd
// from focusing the saved tab (normally nil — the persisted CoachHash keeps the
// summary's vibe-coach cache warm so no LLM re-run is issued).
func (m *Model) applySession(s *review.SessionState) tea.Cmd {
	if s == nil {
		return nil
	}
	byKey := make(map[string]review.CardDecision, len(s.Decisions))
	for _, d := range s.Decisions {
		byKey[d.Key] = d
	}
	for i := range m.cards {
		if d, ok := byKey[m.cardIdentity(m.cards[i])]; ok {
			m.cards[i].state = parseCardDecision(d.Decision)
			// Phase 5 item 2: reapply a persisted comment edit onto the
			// rehydrated card so the posted body is the reviewer's edited text.
			if strings.TrimSpace(d.EditedBody) != "" {
				m.cards[i].finding.Finding.Comment = d.EditedBody
				m.cards[i].edited = true
			}
		}
	}
	// Restore the vibe-coach cache validity so re-entering the summary with the
	// same skip set reuses the persisted result instead of re-running the LLM.
	m.lastCoachHash = s.CoachHash
	tab := s.ActiveTab
	if tab < 0 || tab >= len(m.tabs) {
		tab = 0
	}
	cmd := m.focusTab(tab)
	if s.Cursor >= 0 && s.Cursor < len(m.cards) {
		m.idx = s.Cursor
	}
	m.vp.GotoTop()
	m.rebuildBody()
	return cmd
}

// NewResumed builds a review overlay rehydrated from a saved session: it adopts
// the snapshot Draft (no LLM run) and overlays the persisted decision layer,
// cursor, and focused tab. The returned cmd MUST be included in the caller's
// tea.Batch (it may focus the summary tab). Falls back to a bare overlay when
// the session or its draft snapshot is missing.
func NewResumed(screenW, screenH int, dryRun, specialistsParallel, repoExpertsParallel bool, cfg *aiconfig.Config, demoMode bool, s *review.SessionState) (*Model, tea.Cmd) {
	m := New(screenW, screenH, dryRun, specialistsParallel, repoExpertsParallel, cfg, demoMode)
	if s == nil || s.Draft == nil {
		return m, nil
	}
	d := s.Draft.Restore()
	if specs := codeSpecialistNames(d); len(specs) > 0 {
		m.SetSpecialists(specs)
	}
	adoptCmd := m.AdoptDraft(d)
	applyCmd := m.applySession(s)
	return m, tea.Batch(adoptCmd, applyCmd)
}
