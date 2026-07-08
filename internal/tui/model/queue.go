package model

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
)

// queue.go implements Phase 5 item 10: press A on the PR list to run the AI
// review pipeline on every currently-listed PR, one after another. Sequential
// execution is the whole point — only one review.Run is ever in flight, so
// R2's per-run inference semaphore already caps concurrency without any extra
// coordination (a second run never starts until the first channel closes).
//
// The queue runs the pipelines to completion (the runner persists its B2 draft
// cache on finish, so a later interactive open of the PR gets a warm
// incremental re-review) and reports live progress in the list title. It never
// posts anything — batch-reviewing populates the caches; the human still walks
// each PR's approval flow interactively afterwards.

// queueState is the root model's sequential-review-queue bookkeeping.
type queueState struct {
	// active is true from the A press until the last PR's run closes (or the
	// user cancels). While active the list title shows queue progress.
	active bool
	// refs is the snapshot of PRs to review, captured when the queue started
	// so a background list refresh can't reshuffle it mid-run.
	refs []gh.Ref
	// idx is the index of the PR currently running (0-based).
	idx int
	// done / failed tally completed / start-failed PRs for the summary.
	done   int
	failed int
	// stage is the latest progress stage label for the running PR, shown in
	// the title so the user sees forward motion.
	stage string
	// ch is the running PR's progress channel (nil between PRs).
	ch <-chan review.Progress
	// cancel cancels the current run's context; invoked on the next-PR
	// transition and on an explicit queue cancel.
	cancel context.CancelFunc
}

// cancelReview cancels the interactive review run's context (Phase 5 item 3)
// if one is in flight, then clears the handle. Safe to call when no run is
// active. Called when the review overlay closes and before a new run starts so
// the runner goroutine stops promptly instead of leaking.
func (m *Model) cancelReview() {
	if m.reviewCancel != nil {
		m.reviewCancel()
		m.reviewCancel = nil
	}
}

// reviewCompleteNotifyCmd returns the run-completion notification for an
// interactive review run (Phase 5 item 3). It rings the bell + fires an OSC-9
// desktop notification naming the PR. Skipped in demo mode (recordings stay
// quiet) and when there's no PR to name.
func (m *Model) reviewCompleteNotifyCmd() tea.Cmd {
	if m.opts.Demo {
		return nil
	}
	ref := ""
	switch {
	case m.draft != nil && m.draft.PR != nil:
		ref = m.draft.Ref.String()
	case m.currentPR != nil:
		ref = gh.Ref{Owner: m.currentPR.Owner, Repo: m.currentPR.Repo, Number: m.currentPR.Number}.String()
	}
	if ref == "" {
		return nil
	}
	return util.NotifyCompleteCmd("appr-ai-sal: review finished for " + ref)
}

// listedRefs returns the refs of the PRs currently visible in the list (after
// the active search filter), in display order. Empty when nothing is listed.
func (m *Model) listedRefs() []gh.Ref {
	items := m.list.Items()
	refs := make([]gh.Ref, 0, len(items))
	for _, it := range items {
		pi, ok := it.(prItem)
		if !ok {
			continue
		}
		refs = append(refs, gh.Ref{Owner: pi.pr.Owner, Repo: pi.pr.Repo, Number: pi.pr.Number})
	}
	return refs
}

// startQueueCmd begins (or cancels) the review queue over the currently-listed
// PRs. Pressing A while a queue is already running cancels it. With no listed
// PRs it is a no-op. Returns the command that kicks off the first PR's run.
func (m *Model) startQueueCmd() tea.Cmd {
	if m.queue.active {
		m.cancelQueue()
		return nil
	}
	refs := m.listedRefs()
	if len(refs) == 0 {
		return nil
	}
	m.queue = queueState{active: true, refs: refs}
	m.updateListTitle()
	return m.startQueueItemCmd()
}

// startQueueItemCmd starts the review run for the queue's current index,
// installing a fresh cancellable context. Returns nil when the queue has run
// off the end (caller should have finished it first).
func (m *Model) startQueueItemCmd() tea.Cmd {
	if m.queue.idx < 0 || m.queue.idx >= len(m.queue.refs) {
		return nil
	}
	ref := m.queue.refs[m.queue.idx]
	ctx, cancel := context.WithCancel(context.Background())
	m.queue.cancel = cancel
	m.queue.stage = "starting"
	m.updateListTitle()
	return data.StartQueueReviewCmd(ctx, ref, m.opts.AIConfig, m.opts.Demo)
}

// advanceQueue cancels the just-finished run's context, steps to the next PR,
// and returns the command to start it — or, when the batch is done, finishes
// the queue and returns the completion notification.
func (m *Model) advanceQueue() tea.Cmd {
	if m.queue.cancel != nil {
		m.queue.cancel()
		m.queue.cancel = nil
	}
	m.queue.ch = nil
	m.queue.idx++
	if m.queue.idx >= len(m.queue.refs) {
		return m.finishQueue()
	}
	m.queue.stage = "starting"
	m.updateListTitle()
	return m.startQueueItemCmd()
}

// finishQueue tears the queue down, restores the list title, and fires the
// completion notification (skipped in demo mode). Returns the notification cmd.
func (m *Model) finishQueue() tea.Cmd {
	total := len(m.queue.refs)
	done, failed := m.queue.done, m.queue.failed
	m.queue = queueState{}
	m.updateListTitle()
	if m.opts.Demo {
		return nil
	}
	msg := fmt.Sprintf("appr-ai-sal: queue finished — %d/%d PRs reviewed", done, total)
	if failed > 0 {
		msg += fmt.Sprintf(" (%d failed to start)", failed)
	}
	return util.NotifyCompleteCmd(msg)
}

// cancelQueue aborts a running queue: cancel the current run's context and
// reset the state. Restores the list title.
func (m *Model) cancelQueue() {
	if m.queue.cancel != nil {
		m.queue.cancel()
	}
	m.queue = queueState{}
	m.updateListTitle()
}

// queueTitle returns the list-title suffix describing queue progress, or "" when
// no queue is active. Rendered by updateListTitle so the running PR, position,
// and remaining count are always visible.
func (m *Model) queueTitle() string {
	if !m.queue.active || len(m.queue.refs) == 0 {
		return ""
	}
	pos := m.queue.idx + 1
	if pos > len(m.queue.refs) {
		pos = len(m.queue.refs)
	}
	cur := m.queue.refs[m.queue.idx%len(m.queue.refs)]
	remaining := len(m.queue.refs) - pos
	stage := m.queue.stage
	if stage == "" {
		stage = "…"
	}
	return fmt.Sprintf("  ▸ queue %d/%d · %s · %s · %d remaining (A cancel)",
		pos, len(m.queue.refs), cur.String(), stage, remaining)
}
