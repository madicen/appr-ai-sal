package model

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/commands"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	reviewtab "github.com/madicen/appr-ai-sal/internal/tui/tabs/review"
)

// queueFixtureModel returns a list-mode model with two canned PRs so the queue
// has a batch to run.
func queueFixtureModel(t *testing.T) *Model {
	t.Helper()
	zone.NewGlobal()
	m := New(Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(data.PRListMsg{PRs: []gh.PR{
		{Number: 1, Title: "First", Repository: "o/r", Owner: "o", Repo: "r", Author: "a"},
		{Number: 2, Title: "Second", Repository: "o/r", Owner: "o", Repo: "r", Author: "b"},
	}})
	return m
}

// Phase 5 item 10: A runs the review pipeline over every listed PR, one after
// another. The queue only advances to the next PR when the current run's
// progress channel closes — so exactly one review.Run is ever in flight and
// R2's per-run inference semaphore is respected without extra coordination.
func TestQueueRunsAllPRsSequentially(t *testing.T) {
	m := queueFixtureModel(t)

	cmd := m.startQueueCmd()
	if !m.queue.active {
		t.Fatal("A should activate the queue")
	}
	if len(m.queue.refs) != 2 {
		t.Fatalf("queue should snapshot both listed PRs, got %d", len(m.queue.refs))
	}
	if m.queue.idx != 0 {
		t.Fatalf("queue should start at PR 0, got %d", m.queue.idx)
	}
	if cmd == nil {
		t.Fatal("starting the queue should kick off the first PR's run")
	}

	// PR 1's run starts and installs its progress channel.
	ch1 := make(chan review.Progress)
	out, _ := m.Update(data.QueueReviewStartedMsg{Ref: m.queue.refs[0], Ch: ch1})
	m = out.(*Model)
	if m.queue.ch == nil {
		t.Fatal("a started run should install its progress channel")
	}

	// A progress event keeps the queue on PR 1 (no premature advance).
	out, _ = m.Update(data.QueueProgressMsg{Stage: "specialists"})
	m = out.(*Model)
	if m.queue.idx != 0 {
		t.Fatalf("progress must not advance the queue, idx=%d", m.queue.idx)
	}
	if m.queue.stage != "specialists" {
		t.Fatalf("progress should update the visible stage, got %q", m.queue.stage)
	}

	// PR 1 finishes (channel closed) → advance to PR 2.
	out, cmd2 := m.Update(data.QueueReviewClosedMsg{})
	m = out.(*Model)
	if !m.queue.active || m.queue.idx != 1 {
		t.Fatalf("closing PR 1 should advance to PR 2 (active=%v idx=%d)", m.queue.active, m.queue.idx)
	}
	if cmd2 == nil {
		t.Fatal("advancing should start PR 2's run")
	}

	// PR 2 finishes → the whole batch is done and the queue tears down.
	out, _ = m.Update(data.QueueReviewClosedMsg{})
	m = out.(*Model)
	if m.queue.active {
		t.Fatal("closing the last PR should finish the queue")
	}
}

// A run that fails to start counts as failed and the queue advances rather than
// aborting the batch.
func TestQueueAdvancesPastStartFailure(t *testing.T) {
	m := queueFixtureModel(t)
	m.startQueueCmd()

	out, cmd := m.Update(data.QueueReviewErrMsg{Ref: m.queue.refs[0], Err: errString("no gh")})
	m = out.(*Model)
	if m.queue.idx != 1 {
		t.Fatalf("a failed start should advance to the next PR, idx=%d", m.queue.idx)
	}
	if cmd == nil {
		t.Fatal("the queue should start the next PR after a failure")
	}
	if m.queue.failed != 1 {
		t.Fatalf("a failed start should be tallied, failed=%d", m.queue.failed)
	}
}

// Pressing A while a queue is running cancels it.
func TestQueueSecondPressCancels(t *testing.T) {
	m := queueFixtureModel(t)
	m.startQueueCmd()
	if !m.queue.active {
		t.Fatal("queue should be active after the first A")
	}
	if cmd := m.startQueueCmd(); cmd != nil {
		t.Fatal("a second A should cancel (return no start command)")
	}
	if m.queue.active {
		t.Fatal("a second A should cancel the running queue")
	}
}

// The queue is a no-op when nothing is listed.
func TestQueueNoopWhenListEmpty(t *testing.T) {
	zone.NewGlobal()
	m := New(Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if cmd := m.startQueueCmd(); cmd != nil {
		t.Fatal("queueing an empty list should be a no-op")
	}
	if m.queue.active {
		t.Fatal("an empty list must not activate the queue")
	}
}

// Phase 5 item 3: closing the review overlay cancels the run's context so the
// runner goroutine stops instead of leaking behind the dismissed overlay.
func TestReviewOverlayCloseCancelsContext(t *testing.T) {
	zone.NewGlobal()
	m := New(Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	cancelled := false
	m.reviewCancel = func() { cancelled = true }

	m.Update(reviewtab.CloseMsg{})
	if !cancelled {
		t.Fatal("closing the review overlay should cancel the run's context")
	}
	if m.reviewCancel != nil {
		t.Fatal("the cancel handle should be cleared after cancelling")
	}
}

// The new Phase 5 commands are registered in the palette and gated to the right
// screens (so they surface in ctrl+k and, via their bindings, in ? help).
func TestPhase5CommandsRegistered(t *testing.T) {
	m := New(Options{})
	reg := m.palette

	if _, ok := reg.Find("list.queue-all"); !ok {
		t.Fatal("list.queue-all should be registered")
	}
	if _, ok := reg.Find("copy.pr-url"); !ok {
		t.Fatal("copy.pr-url should be registered")
	}

	listIDs := idSetQ(reg.Enabled(commands.Context{Mode: "list", HasSelection: true}))
	if !listIDs["list.queue-all"] {
		t.Errorf("list.queue-all should be enabled in the list context: %v", listIDs)
	}
	if !listIDs["copy.pr-url"] {
		t.Errorf("copy.pr-url should be enabled in the list context (with a selection): %v", listIDs)
	}

	detailIDs := idSetQ(reg.Enabled(commands.Context{Mode: "detail", HasPR: true}))
	if !detailIDs["copy.pr-url"] {
		t.Errorf("copy.pr-url should be enabled in the detail context (with a PR): %v", detailIDs)
	}
	if detailIDs["list.queue-all"] {
		t.Errorf("list.queue-all should be list-only, got it in detail: %v", detailIDs)
	}
}

func idSetQ(cmds []commands.Command) map[string]bool {
	out := map[string]bool{}
	for _, c := range cmds {
		out[c.ID] = true
	}
	return out
}

type errString string

func (e errString) Error() string { return string(e) }
