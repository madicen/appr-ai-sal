package review

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/review"
)

// openChallengeForTest sets up a completed overlay focused on the docs agent's
// card and opens the challenge exchange on it, returning the overlay.
func openChallengeForTest(t *testing.T, demoMode bool) *Model {
	t.Helper()
	ro := New(120, 44, false, false, false, nil, demoMode)
	ro.AdoptDraft(tabsTestDraft())
	focusAgentTabForTest(t, ro, review.SpecDocs)
	if ro.phase != phaseApprove {
		t.Fatalf("expected phaseApprove after focusing an agent tab, got %v", ro.phase)
	}
	ro.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if !ro.challengeActive {
		t.Fatal("pressing c should open the challenge exchange")
	}
	return ro
}

func TestChallengeCKeyOpensExchange(t *testing.T) {
	ro := openChallengeForTest(t, false)
	if ro.challengeCardIdx < 0 || ro.challengeCardIdx >= len(ro.cards) {
		t.Fatalf("challengeCardIdx not set to the focused card: %d", ro.challengeCardIdx)
	}
	body := ro.vp.View()
	if !strings.Contains(body, "Challenge this finding") {
		t.Fatalf("challenge body should render its header:\n%s", body)
	}
	// esc closes the exchange and returns to the card view.
	ro.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if ro.challengeActive {
		t.Fatal("esc should close the challenge exchange")
	}
}

// A withdraw decision auto-skips the card, marks it withdrawn-via-challenge,
// and closes the exchange.
func TestChallengeWithdrawAutoSkipsCard(t *testing.T) {
	t.Setenv("APPR_AI_SAL_CACHE_DIR", t.TempDir()) // isolate the fail-open memory write
	ro := openChallengeForTest(t, false)
	idx := ro.challengeCardIdx
	ro.onChallengeDone(ChallengeDoneMsg{
		CardIdx:  idx,
		Question: "This is handled upstream",
		Response: review.ChallengeResponse{Decision: review.ChallengeWithdraw, Justification: "you're right"},
	})
	if ro.challengeActive {
		t.Fatal("a withdraw should close the exchange")
	}
	if ro.cards[idx].state != cardSkipped {
		t.Fatalf("withdrawn card should be auto-skipped, got %v", ro.cards[idx].state)
	}
	if !ro.cards[idx].withdrawnViaChallenge {
		t.Fatal("withdrawn card should be flagged withdrawnViaChallenge")
	}
}

// An uphold keeps the exchange open, records the turn, and applies any revised
// comment/severity to the card.
func TestChallengeUpholdShowsJustificationAndApplies(t *testing.T) {
	ro := openChallengeForTest(t, false)
	idx := ro.challengeCardIdx
	ro.onChallengeDone(ChallengeDoneMsg{
		CardIdx:  idx,
		Question: "Why does this matter?",
		Response: review.ChallengeResponse{
			Decision:        review.ChallengeUphold,
			Justification:   "It drops the cancellation error at line 1.",
			RevisedComment:  "Return ctx.Err() here.",
			RevisedSeverity: review.SeverityError,
		},
	})
	if !ro.challengeActive {
		t.Fatal("an uphold should keep the exchange open for follow-ups")
	}
	if len(ro.challengeTranscript) != 1 {
		t.Fatalf("uphold should record one transcript turn, got %d", len(ro.challengeTranscript))
	}
	if ro.cards[idx].finding.Finding.Comment != "Return ctx.Err() here." {
		t.Fatalf("revised comment should apply to the card: %q", ro.cards[idx].finding.Finding.Comment)
	}
	if ro.cards[idx].finding.Finding.Severity != review.SeverityError {
		t.Fatalf("revised severity should apply to the card: %q", ro.cards[idx].finding.Finding.Severity)
	}
	body := ro.vp.View()
	if !strings.Contains(body, "upheld") {
		t.Fatalf("uphold justification/badge should render:\n%s", body)
	}
}

// A model/transport error is surfaced in the overlay and the card is left
// unchanged (fail-open).
func TestChallengeFailOpenOnError(t *testing.T) {
	ro := openChallengeForTest(t, false)
	idx := ro.challengeCardIdx
	before := ro.cards[idx].state
	ro.onChallengeDone(ChallengeDoneMsg{
		CardIdx: idx,
		Err:     errForTest("network exploded"),
	})
	if !ro.challengeActive {
		t.Fatal("an error should leave the exchange open so the reviewer can retry")
	}
	if ro.cards[idx].state != before {
		t.Fatalf("an error must leave the card unchanged, state changed to %v", ro.cards[idx].state)
	}
	if ro.challengeErr == nil {
		t.Fatal("challengeErr should be set")
	}
	if !strings.Contains(ro.vp.View(), "network exploded") {
		t.Fatalf("error should be shown in the overlay:\n%s", ro.vp.View())
	}
}

// Multi-turn: the demo canned response upholds on the first turn and withdraws
// on the second, so submitting twice grows the transcript then auto-skips.
func TestChallengeMultiTurnDemo(t *testing.T) {
	ro := openChallengeForTest(t, true)
	idx := ro.challengeCardIdx

	// Turn 1: submit a question → the demo canned response upholds.
	ro.challengeInput.SetValue("Please justify this")
	_, cmd := ro.submitChallenge()
	if cmd == nil {
		t.Fatal("submitting a question should return a command")
	}
	msg := cmd()
	done, ok := msg.(ChallengeDoneMsg)
	if !ok {
		t.Fatalf("expected ChallengeDoneMsg, got %T", msg)
	}
	if done.Response.Withdrawn() {
		t.Fatal("demo first turn should uphold")
	}
	ro.onChallengeDone(done)
	if !ro.challengeActive || len(ro.challengeTranscript) != 1 {
		t.Fatalf("after turn 1: active=%v turns=%d", ro.challengeActive, len(ro.challengeTranscript))
	}

	// Turn 2: a follow-up → the demo canned response withdraws and auto-skips.
	t.Setenv("APPR_AI_SAL_CACHE_DIR", t.TempDir())
	ro.challengeInput.SetValue("But it's handled upstream")
	_, cmd = ro.submitChallenge()
	msg = cmd()
	done = msg.(ChallengeDoneMsg)
	if !done.Response.Withdrawn() {
		t.Fatal("demo second turn should withdraw")
	}
	ro.onChallengeDone(done)
	if ro.challengeActive {
		t.Fatal("withdraw on turn 2 should close the exchange")
	}
	if ro.cards[idx].state != cardSkipped || !ro.cards[idx].withdrawnViaChallenge {
		t.Fatalf("withdraw should auto-skip the card: state=%v withdrawn=%v", ro.cards[idx].state, ro.cards[idx].withdrawnViaChallenge)
	}
}

// The demo canned command needs no provider and returns a response offline.
func TestChallengeDemoCommandOffline(t *testing.T) {
	f := review.Finding{Path: "x.go", Line: 1, Comment: "c"}
	cmd := challengeFindingCmd(true, nil, review.SpecDocs, f, "", nil, 0, "why?")
	msg := cmd()
	done, ok := msg.(ChallengeDoneMsg)
	if !ok {
		t.Fatalf("expected ChallengeDoneMsg, got %T", msg)
	}
	if done.Err != nil {
		t.Fatalf("demo command must not error: %v", done.Err)
	}
	if strings.TrimSpace(done.Response.Justification) == "" {
		t.Fatal("demo canned response should carry a justification")
	}
}

// A stale completion (exchange closed / different card) is ignored.
func TestChallengeStaleCompletionIgnored(t *testing.T) {
	ro := openChallengeForTest(t, false)
	ro.onChallengeDone(ChallengeDoneMsg{
		CardIdx:  ro.challengeCardIdx + 99,
		Response: review.ChallengeResponse{Decision: review.ChallengeWithdraw},
	})
	if len(ro.challengeTranscript) != 0 {
		t.Fatal("a mismatched-card completion must be dropped")
	}
	if !ro.challengeActive {
		t.Fatal("a stale completion must not close the active exchange")
	}
}

type testErr string

func (e testErr) Error() string { return string(e) }

func errForTest(s string) error { return testErr(s) }
