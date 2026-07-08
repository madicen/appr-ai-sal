package detail

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
)

// detail_threads.go implements Phase 5 item 8 "thread browsing": rendering the
// PR's existing inline review comments in the diff, a browsable review-history
// pane, and replying to a thread via the same B3 Backend.ReplyToThread seam.
//
// The inline comments + threads are the ones already fetched for dedup (never
// shown before); we lazily fetch them on the first `t`/`H` press and cache them
// on the model so toggling is instant afterwards.

// currentRef returns the ref for the loaded PR (zero value when none).
func (m *Model) currentRef() gh.Ref {
	if m.currentPR() == nil {
		return gh.Ref{}
	}
	return gh.Ref{Owner: m.currentPR().Owner, Repo: m.currentPR().Repo, Number: m.currentPR().Number}
}

// ensureThreadsCmd fires the existing-comments fetch the first time thread data
// is needed. Returns nil when already loaded / loading or when there's no PR.
func (m *Model) ensureThreadsCmd() tea.Cmd {
	if m.currentPR() == nil || m.threadsLoaded || m.threadsLoading {
		return nil
	}
	m.threadsLoading = true
	return data.FetchThreadsCmd(m.currentRef(), m.host.Demo())
}

// applyThreadsLoaded stores fetched inline comments + threads on the model
// (Phase 5 item 8) and refreshes the diff so any newly-toggled comment
// annotations appear. Called from the root ThreadsLoadedMsg handler.
func (m *Model) applyThreadsLoaded(msg data.ThreadsLoadedMsg) {
	m.prComments = msg.Comments
	m.prThreads = msg.Threads
	m.threadsLoaded = true
	m.threadsLoading = false
	if true {
		m.refreshDetailViews()
	}
}

// toggleThreads flips inline-comment rendering in the diff and lazily fetches
// the comment data on first enable.
func (m *Model) toggleThreads() tea.Cmd {
	m.showThreads = !m.showThreads
	cmd := m.ensureThreadsCmd()
	m.refreshDetailViews()
	return cmd
}

// openReviewHistory switches the centre pane to the browsable review-history
// pane and lazily fetches the thread data.
func (m *Model) openReviewHistory() tea.Cmd {
	m.centerView = centerHistory
	m.focusedPane = paneDiff
	m.historyCursor = 0
	cmd := m.ensureThreadsCmd()
	m.refreshDetailViews()
	return cmd
}

// closeReviewHistory returns from the history pane to the diff.
func (m *Model) closeReviewHistory() {
	m.centerView = centerDiff
	m.replyingTo = ""
	m.refreshDetailViews()
}

// browsableThreads returns the PR's review threads sorted for display: by path
// then line, unresolved before resolved. Threads without comments are dropped.
func (m *Model) browsableThreads() []gh.ReviewThread {
	out := make([]gh.ReviewThread, 0, len(m.prThreads))
	for _, t := range m.prThreads {
		if len(t.Comments) == 0 {
			continue
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ci, cj := out[i].Comments[0], out[j].Comments[0]
		if ci.Path != cj.Path {
			return ci.Path < cj.Path
		}
		return ci.Line < cj.Line
	})
	return out
}

// handleHistoryKey routes keys while the review-history pane is focused: j/k
// move the thread cursor, r starts a reply to the selected thread, esc returns
// to the diff. Returns (handled) so the caller can fall through otherwise.
func (m *Model) handleHistoryKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	threads := m.browsableThreads()
	switch msg.String() {
	case "j", "down":
		if m.historyCursor < len(threads)-1 {
			m.historyCursor++
			m.refreshDetailViews()
		}
		return nil, true
	case "k", "up":
		if m.historyCursor > 0 {
			m.historyCursor--
			m.refreshDetailViews()
		}
		return nil, true
	case "r":
		if m.historyCursor >= 0 && m.historyCursor < len(threads) {
			return m.beginReply(threads[m.historyCursor].ID), true
		}
		return nil, true
	case "esc", "q":
		m.closeReviewHistory()
		return nil, true
	}
	return nil, false
}

// beginReply opens the in-pane reply prompt for a thread. A no-op (with a
// status note) when the thread has no ID — that happens for demo threads /
// payloads that predate the id being requested, where a reply can't be routed.
func (m *Model) beginReply(threadID string) tea.Cmd {
	if strings.TrimSpace(threadID) == "" {
		m.replyStatus = "this thread has no id — reply unavailable (demo / legacy data)"
		m.refreshDetailViews()
		return nil
	}
	ti := textinput.New()
	ti.Placeholder = "reply…"
	ti.Prompt = "↳ "
	ti.CharLimit = 4000
	m.replyInput = ti
	m.replyingTo = threadID
	m.replyStatus = ""
	m.refreshDetailViews()
	return m.replyInput.Focus()
}

// handleReplyKey owns keyboard input while the reply prompt is open: enter
// submits (via data.ReplyToThreadCmd, the B3 seam), esc cancels, everything
// else goes to the text field.
func (m *Model) handleReplyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		body := strings.TrimSpace(m.replyInput.Value())
		threadID := m.replyingTo
		m.replyingTo = ""
		if body == "" {
			m.replyStatus = "reply cancelled (empty)"
			m.refreshDetailViews()
			return m, nil
		}
		m.replyStatus = "posting reply…"
		m.refreshDetailViews()
		return m, data.ReplyToThreadCmd(m.currentRef(), threadID, body, m.host.Demo())
	case tea.KeyEsc:
		m.replyingTo = ""
		m.replyStatus = "reply cancelled"
		m.refreshDetailViews()
		return m, nil
	}
	var cmd tea.Cmd
	m.replyInput, cmd = m.replyInput.Update(msg)
	m.refreshDetailViews()
	return m, cmd
}

// applyThreadReply records the outcome of a reply post (Phase 5 item 8.3).
func (m *Model) applyThreadReply(msg data.ThreadReplyPostedMsg) {
	if msg.Err != nil {
		m.replyStatus = "reply failed: " + msg.Err.Error()
	} else {
		m.replyStatus = "reply posted"
	}
	if true {
		m.refreshDetailViews()
	}
}

// renderHistoryPane renders the browsable review-history pane: a list of the
// PR's inline threads with the selected one expanded, plus the reply prompt /
// status. contentCols is the diff viewport width.
func (m *Model) renderHistoryPane(contentCols int) string {
	contentCols = max(8, contentCols)
	var b strings.Builder
	b.WriteString(styles.BoldStyle.Render("Review history") + "  " +
		styles.DimStyle.Render("j/k move · r reply · esc back") + "\n\n")
	if m.threadsLoading {
		b.WriteString(styles.DimStyle.Render("Loading review threads…") + "\n")
		return b.String()
	}
	threads := m.browsableThreads()
	if len(threads) == 0 {
		b.WriteString(styles.DimStyle.Render("No inline review threads on this PR yet.") + "\n")
		if m.replyStatus != "" {
			b.WriteString("\n" + styles.DimStyle.Render(m.replyStatus) + "\n")
		}
		return b.String()
	}
	if m.historyCursor >= len(threads) {
		m.historyCursor = len(threads) - 1
	}
	for i, t := range threads {
		b.WriteString(renderThreadEntry(t, i == m.historyCursor, contentCols))
		b.WriteString("\n")
	}
	if m.replyingTo != "" {
		b.WriteString("\n" + m.replyInput.View() + "\n")
	}
	if m.replyStatus != "" {
		b.WriteString("\n" + styles.DimStyle.Render(m.replyStatus) + "\n")
	}
	return b.String()
}

// renderThreadEntry renders one review thread: a header line (anchor + state)
// and, when selected, the full comment bodies; unselected threads show only the
// first comment's first line as a preview.
func renderThreadEntry(t gh.ReviewThread, selected bool, contentCols int) string {
	var b strings.Builder
	marker := "  "
	if selected {
		marker = styles.BoldStyle.Render("> ")
	}
	first := t.Comments[0]
	state := stateChip(t)
	header := fmt.Sprintf("%s%s:%d  %s", marker, first.Path, first.Line, state)
	b.WriteString(ansi.Truncate(header, contentCols, "…") + "\n")
	if !selected {
		preview := strings.SplitN(strings.TrimSpace(first.Body), "\n", 2)[0]
		line := "    " + styles.DimStyle.Render(fmt.Sprintf("@%s: %s", first.Author, preview))
		b.WriteString(ansi.Truncate(line, contentCols, "…") + "\n")
		return b.String()
	}
	for _, c := range t.Comments {
		b.WriteString("    " + styles.BoldStyle.Render("@"+c.Author) + "\n")
		body := util.WrapForViewport(strings.TrimSpace(c.Body), max(8, contentCols-4))
		for _, ln := range strings.Split(body, "\n") {
			b.WriteString("      " + ln + "\n")
		}
	}
	return b.String()
}

// stateChip renders a thread's resolved/outdated state as a short chip.
func stateChip(t gh.ReviewThread) string {
	switch {
	case t.IsResolved:
		return styles.OkStyle.Render("resolved")
	case t.IsOutdated:
		return styles.DimStyle.Render("outdated")
	default:
		return styles.WarnStyle.Render("open")
	}
}
