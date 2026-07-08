package review

import (
	"context"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/demo"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
)

// challenge.go is the TUI half of B4 "chat-with-specialist (challenge this
// finding)". The backend (review.ChallengeFinding) is TUI-free; here we open a
// scoped overlay from a finding card, drive a multi-turn exchange through a
// tea.Cmd (like the vibe-coach re-run), and render the transcript. On a
// withdraw decision the card is auto-skipped and the outcome is folded into
// reviewer memory (B1); on uphold the strengthened justification is shown (and
// any revised comment/severity applied to the card).

// ChallengeDoneMsg is delivered when one scoped challenge call
// (challengeFindingCmd) completes. CardIdx / Question echo the request so a
// stale completion (the reviewer navigated away or closed the exchange) is
// dropped instead of mutating the wrong card. Err is non-nil on a failed call:
// the overlay shows it and leaves the card unchanged (fail-open).
type ChallengeDoneMsg struct {
	CardIdx  int
	Question string
	Response review.ChallengeResponse
	Err      error
}

// OverlayBound marks ChallengeDoneMsg for the root's generic overlay forwarder
// (data.ForwardToOverlay), exactly like VibeCoachDoneMsg — the goroutine's
// response only means something to the overlay, and routing it generically
// means a future root refactor can't strand the challenge exchange.
func (ChallengeDoneMsg) OverlayBound() {}

var _ data.ForwardToOverlay = ChallengeDoneMsg{}

// challengeFindingCmd runs one scoped challenge call off the UI thread and
// returns a ChallengeDoneMsg. In demo mode it returns a canned, offline
// response so the feature is demoable/VHS-recordable without a live model;
// otherwise it calls review.ChallengeFinding (routed as the "challenge" stage,
// respecting R1 usage + R2 concurrency in the ai layer).
func challengeFindingCmd(demoMode bool, cfg *aiconfig.Config, specialist string, f review.Finding, hunk string, transcript []review.ChallengeTurn, cardIdx int, question string) tea.Cmd {
	// Snapshot the transcript so a later mutation on the UI thread can't race
	// the goroutine reading it.
	tr := append([]review.ChallengeTurn(nil), transcript...)
	return func() tea.Msg {
		if demoMode {
			return ChallengeDoneMsg{
				CardIdx:  cardIdx,
				Question: question,
				Response: demo.CannedChallengeResponse(specialist, f, tr, question),
			}
		}
		resp, err := review.ChallengeFinding(context.Background(), cfg, specialist, f, hunk, tr, question)
		return ChallengeDoneMsg{CardIdx: cardIdx, Question: question, Response: resp, Err: err}
	}
}

// actOpenChallenge opens the scoped challenge exchange for the focused card. It
// is a no-op unless the review is done, an inline card with a finding is
// focused, and no challenge is already open.
func (m *Model) actOpenChallenge() (tea.Model, tea.Cmd) {
	if m.challengeActive || !m.done {
		return m, nil
	}
	if m.idx < 0 || m.idx >= len(m.cards) {
		return m, nil
	}
	if strings.TrimSpace(m.cards[m.idx].finding.Finding.Comment) == "" {
		return m, nil
	}
	m.challengeActive = true
	m.challengeCardIdx = m.idx
	m.challengeTranscript = nil
	m.challengeErr = nil
	m.challengeInFlight = false
	m.challengeInput = m.newChallengeInput()
	m.vp.GotoTop()
	m.rebuildBody()
	return m, m.challengeInput.Focus()
}

// newChallengeInput builds the textarea for the reviewer's question, sized to
// the current viewport.
func (m *Model) newChallengeInput() textarea.Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 4000
	ta.Placeholder = "Ask the specialist to justify or reconsider this finding…"
	ta.SetWidth(max(20, m.vp.Width-2))
	ta.SetHeight(3)
	return ta
}

// closeChallenge tears the exchange down and returns to the normal card view.
// The card keeps whatever state the exchange left it in (unchanged on uphold,
// skipped on withdraw).
func (m *Model) closeChallenge() (tea.Model, tea.Cmd) {
	m.challengeActive = false
	m.challengeInput.Blur()
	m.challengeTranscript = nil
	m.challengeErr = nil
	m.challengeInFlight = false
	m.vp.GotoTop()
	m.rebuildBody()
	return m, nil
}

// handleChallengeKey owns keyboard input while the challenge exchange is open.
// esc/q closes it; ctrl+s submits the current question (a scoped call); every
// other key is forwarded to the textarea so the reviewer can type freely
// (including tab and enter for newlines).
func (m *Model) handleChallengeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.closeChallenge()
	case "ctrl+c":
		return m.closeChallenge()
	case "ctrl+s":
		return m.submitChallenge()
	}
	if m.challengeInFlight {
		// A call is in flight — swallow edits until it returns so the
		// transcript and input stay consistent.
		return m, nil
	}
	var cmd tea.Cmd
	m.challengeInput, cmd = m.challengeInput.Update(msg)
	m.rebuildBody()
	return m, cmd
}

// submitChallenge fires one scoped challenge call for the focused card carrying
// the running transcript. It is a no-op on an empty question or while a call is
// already in flight.
func (m *Model) submitChallenge() (tea.Model, tea.Cmd) {
	if m.challengeInFlight {
		return m, nil
	}
	question := strings.TrimSpace(m.challengeInput.Value())
	if question == "" {
		return m, nil
	}
	idx := m.challengeCardIdx
	if idx < 0 || idx >= len(m.cards) {
		return m.closeChallenge()
	}
	card := m.cards[idx]
	m.challengeInFlight = true
	m.challengeErr = nil
	hunk := challengeHunkText(card.hunk)
	cmd := challengeFindingCmd(m.demoMode, m.aiConfig, card.finding.Specialist, card.finding.Finding, hunk, m.challengeTranscript, idx, question)
	m.rebuildBody()
	return m, cmd
}

// onChallengeDone folds a completed scoped call into the exchange. A stale
// completion (exchange closed, or a different card focused) is dropped. On
// error the message is surfaced and the card is left unchanged. On success the
// turn is appended to the transcript; a withdraw auto-skips the card (recording
// the negative signal into reviewer memory) and closes the exchange, while an
// uphold keeps it open and applies any revised comment/severity.
func (m *Model) onChallengeDone(msg ChallengeDoneMsg) tea.Cmd {
	if !m.challengeActive || msg.CardIdx != m.challengeCardIdx {
		return nil
	}
	m.challengeInFlight = false
	if msg.Err != nil {
		m.challengeErr = msg.Err
		m.rebuildBody()
		return nil
	}
	m.challengeErr = nil
	m.challengeTranscript = append(m.challengeTranscript, review.ChallengeTurn{
		Question: msg.Question,
		Response: msg.Response,
	})
	// Clear the input for the next follow-up turn.
	m.challengeInput.Reset()

	idx := msg.CardIdx
	if idx < 0 || idx >= len(m.cards) {
		return nil
	}
	if msg.Response.Withdrawn() {
		return m.applyChallengeWithdrawal(idx)
	}
	m.applyChallengeUphold(idx, msg.Response)
	m.rebuildBody()
	return nil
}

// applyChallengeWithdrawal auto-skips the withdrawn card, records the negative
// signal into reviewer memory (B1, fail-open, skipped in demo mode so a demo
// never writes to the user's cache), closes the exchange, and advances to the
// next pending card — the same terminal move a manual skip makes.
func (m *Model) applyChallengeWithdrawal(idx int) tea.Cmd {
	card := &m.cards[idx]
	card.state = cardSkipped
	card.withdrawnViaChallenge = true
	// B1: a specialist withdrawing its own finding under challenge is a strong
	// negative signal for that pattern. Fold it in now (not at post time) so it
	// counts even if the reviewer never posts. Not in demo mode.
	if !m.demoMode && m.draft != nil {
		review.RecordChallengeWithdrawal(m.draft.PR, card.finding.Specialist, card.finding.Finding)
	}
	m.challengeActive = false
	m.challengeInput.Blur()
	advCmd := m.advanceCard()
	m.rebuildBody()
	return tea.Batch(advCmd, m.scheduleSessionSave())
}

// applyChallengeUphold applies an upheld response's optional revisions to the
// card's finding: a revised comment replaces the posted comment, a revised
// severity re-grades it. Both are advisory (the reviewer can still skip/post);
// an empty field leaves the original untouched.
func (m *Model) applyChallengeUphold(idx int, resp review.ChallengeResponse) {
	card := &m.cards[idx]
	if c := strings.TrimSpace(resp.RevisedComment); c != "" {
		card.finding.Finding.Comment = c
	}
	if resp.RevisedSeverity != "" {
		card.finding.Finding.Severity = resp.RevisedSeverity
	}
}

// challengeHunkText renders a diff hunk to the compact, marker-prefixed text
// the challenge call shows the specialist (mirrors what specialists see in the
// diff). Returns "" when there's no hunk — ChallengeFinding tolerates a missing
// hunk (the finding + question still carry the exchange).
func challengeHunkText(h *review.Hunk) string {
	if h == nil {
		return ""
	}
	var b strings.Builder
	if hdr := strings.TrimSpace(h.Header); hdr != "" {
		b.WriteString(hdr + "\n")
	}
	for _, ln := range h.Lines {
		switch ln.Kind {
		case review.DiffAdded:
			b.WriteString("+ " + strconv.Itoa(ln.NewNo) + "| " + ln.Text + "\n")
		case review.DiffRemoved:
			b.WriteString("- " + strconv.Itoa(ln.OldNo) + "| " + ln.Text + "\n")
		case review.DiffNoNewline:
			b.WriteString("  " + ln.Text + "\n")
		default:
			b.WriteString("  " + strconv.Itoa(ln.NewNo) + "| " + ln.Text + "\n")
		}
	}
	return b.String()
}

// renderChallenge draws the scoped challenge exchange: the finding under
// challenge, the multi-turn transcript, the input textarea (or an in-flight
// spinner), any error, and the key hints. It replaces the normal card detail
// while the exchange is open.
func (m *Model) renderChallenge(rowW int) string {
	var b strings.Builder
	b.WriteString(styles.BoldStyle.Render("Challenge this finding") + "\n")
	b.WriteString(styles.DimStyle.Render("Ask the specialist to justify or reconsider it. It will withdraw the finding (auto-skipped) or uphold it with a stronger case.") + "\n\n")

	if m.challengeCardIdx >= 0 && m.challengeCardIdx < len(m.cards) {
		cur := m.cards[m.challengeCardIdx]
		b.WriteString(styles.RenderTag(cur.finding.Specialist) + "  ")
		loc := cur.finding.Finding.Path
		if cur.finding.Finding.Line > 0 {
			loc += ":" + strconv.Itoa(cur.finding.Finding.Line)
		}
		b.WriteString(styles.BoldStyle.Render(loc) + "  ")
		b.WriteString(styles.RenderSeverity(string(cur.finding.Finding.Severity)) + "\n")
		b.WriteString(util.RenderMarkdownIndented(strings.TrimSpace(cur.finding.Finding.Comment), rowW, 2) + "\n\n")
	}

	// Transcript of the exchange so far.
	for i, t := range m.challengeTranscript {
		b.WriteString(styles.DimStyle.Render("You:") + "\n")
		b.WriteString(util.RenderMarkdownIndented(strings.TrimSpace(t.Question), rowW, 2) + "\n")
		var badge string
		if t.Response.Withdrawn() {
			badge = styles.OkStyle.Render("↩ withdrawn")
		} else {
			badge = styles.WarnStyle.Render("⬆ upheld")
		}
		b.WriteString(styles.RenderTag(m.challengeSpecialistName()) + " " + badge + "\n")
		if j := strings.TrimSpace(t.Response.Justification); j != "" {
			b.WriteString(util.RenderMarkdownIndented(j, rowW, 2) + "\n")
		}
		if !t.Response.Withdrawn() {
			if rc := strings.TrimSpace(t.Response.RevisedComment); rc != "" {
				b.WriteString("  " + styles.DimStyle.Render("(revised comment applied to the card)") + "\n")
			}
			if t.Response.RevisedSeverity != "" {
				b.WriteString("  " + styles.DimStyle.Render("(revised severity: "+string(t.Response.RevisedSeverity)+")") + "\n")
			}
		}
		if i < len(m.challengeTranscript)-1 {
			b.WriteString("\n")
		}
	}
	if len(m.challengeTranscript) > 0 {
		b.WriteString("\n")
	}

	if m.challengeErr != nil {
		b.WriteString(styles.ErrStyle.Render("✗ challenge failed: "+m.challengeErr.Error()) + "\n")
		b.WriteString(styles.DimStyle.Render("The finding is unchanged. Edit your message and press ctrl+s to retry, or esc to close.") + "\n\n")
	}

	if m.challengeInFlight {
		b.WriteString(styles.DimStyle.Render("⟳ asking the "+m.challengeSpecialistName()+" specialist… "+m.sp.View()) + "\n\n")
	} else {
		b.WriteString(styles.DimStyle.Render("Your message:") + "\n")
		b.WriteString(m.challengeInput.View() + "\n\n")
	}

	b.WriteString(styles.DimStyle.Render("ctrl+s send · esc close" + m.challengeFollowUpHint()))
	b.WriteString("\n")
	return b.String()
}

// challengeSpecialistName returns the specialist name of the card under
// challenge, for the transcript/hint labels.
func (m *Model) challengeSpecialistName() string {
	if m.challengeCardIdx >= 0 && m.challengeCardIdx < len(m.cards) {
		return m.cards[m.challengeCardIdx].finding.Specialist
	}
	return "specialist"
}

// challengeFollowUpHint adds a "· ask a follow-up" note once at least one turn
// has completed, so the reviewer knows the exchange is multi-turn.
func (m *Model) challengeFollowUpHint() string {
	if len(m.challengeTranscript) > 0 {
		return " · type a follow-up to continue"
	}
	return ""
}
