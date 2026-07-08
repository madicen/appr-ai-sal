package model_test

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/madicen/appr-ai-sal/internal/tui/model"
	"github.com/madicen/appr-ai-sal/internal/tui/tuitest"
)

// teatest end-to-end flows (Phase 5 item 11) that drive the real root
// tea.Model in demo mode. Everything is hermetic: demo mode returns canned
// PRs / diffs / a scripted review with no gh CLI, no network, and no AI
// provider. Each flow forces a monochrome Ascii profile + a fixed 120×40
// terminal so the frames teatest scans are stable plain text.
//
// Flows only WaitFor early, stable signals (a header, an overlay title, the
// first review stage) rather than waiting out the demo's ~28s scripted run,
// so they finish in well under a second and never flake on timing.

const (
	flowW = 120
	flowH = 40
	// waitTimeout is deliberately generous: the signals we wait for appear
	// within a frame or two, so a large ceiling only matters if a machine is
	// heavily loaded — it never adds latency to the happy path.
	waitTimeout = 5 * time.Second
)

// newFlowModel builds a demo-mode root model wired for teatest.
func newFlowModel(t *testing.T) *teatest.TestModel {
	t.Helper()
	tuitest.ForceMonochrome(t)
	m := model.New(model.Options{Demo: true, DryRun: true})
	return teatest.NewTestModel(t, m, teatest.WithInitialTermSize(flowW, flowH))
}

// waitForText blocks until the cumulative program output contains sub.
func waitForText(t *testing.T, tm *teatest.TestModel, sub string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte(sub))
	}, teatest.WithDuration(waitTimeout))
}

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// quit tells the program to terminate and waits for its goroutine to exit so
// the test's teatest cleanup doesn't block. We use tm.Quit (a direct tea.Quit)
// rather than a ctrl+c key so it works from any screen — including while a
// modal overlay owns the keyboard and would otherwise swallow ctrl+c.
func quit(t *testing.T, tm *teatest.TestModel) {
	t.Helper()
	if err := tm.Quit(); err != nil {
		t.Fatalf("quit program: %v", err)
	}
	tm.WaitFinished(t, teatest.WithFinalTimeout(waitTimeout))
}

// NOTE: teatest.WaitFor consumes the output stream, and separate WaitFor
// calls do not share an accumulated buffer. So each waitForText must target
// content produced *after* the previous one returned — i.e. one wait per
// state change, checking a signal unique to the new frame.

// TestFlowListToDetail: the demo list loads, and pressing enter on the
// highlighted PR opens its detail page (list → detail).
func TestFlowListToDetail(t *testing.T) {
	tm := newFlowModel(t)
	waitForText(t, tm, "Replace flat file list") // #318, first after sort
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	// The detail header carries the "owner/repo#N" string only the detail
	// page renders.
	waitForText(t, tm, "madicen/appr-ai-sal#318")
	quit(t, tm)
}

// TestFlowHelpOverlay: `?` opens the full-screen help reference and esc
// dismisses it back to the queue.
func TestFlowHelpOverlay(t *testing.T) {
	tm := newFlowModel(t)
	waitForText(t, tm, "review queue")
	tm.Send(keyRunes("?"))
	waitForText(t, tm, "Keyboard shortcuts")
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	quit(t, tm)
}

// TestFlowCommandPalette: ctrl+k opens the fuzzy command palette and esc
// dismisses it.
func TestFlowCommandPalette(t *testing.T) {
	tm := newFlowModel(t)
	waitForText(t, tm, "review queue")
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlK})
	waitForText(t, tm, "Command palette")
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	quit(t, tm)
}

// TestFlowStartReview: open a PR, press r to start the demo review, and see
// the running overlay come up (detail → run). We wait only for the first
// stage so the flow doesn't sit through the scripted run; esc then closes the
// overlay (cancelling the demo runner's context) before quitting.
func TestFlowStartReview(t *testing.T) {
	tm := newFlowModel(t)
	waitForText(t, tm, "Replace flat file list")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitForText(t, tm, "madicen/appr-ai-sal#318")
	tm.Send(keyRunes("r"))
	waitForText(t, tm, "Review in progress")
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	quit(t, tm)
}

// TestFlowListFilter: pressing f cycles the review-queue filter and the list
// title reflects the new scope (triage-style list filter).
func TestFlowListFilter(t *testing.T) {
	tm := newFlowModel(t)
	waitForText(t, tm, "review requested (@me, incl. teams)")
	tm.Send(keyRunes("f"))
	waitForText(t, tm, "you are explicitly requested")
	quit(t, tm)
}
