package detail

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
)

func TestBeginAndCommitDiffSearch(t *testing.T) {
	m := detailModelWithDiff(t)
	m.beginDiffSearch()
	if !m.diffSearching {
		t.Fatal("beginDiffSearch should enter search mode")
	}
	m.diffSearchInput.SetValue("neu")
	m.commitDiffSearch()
	if m.diffSearching {
		t.Error("commit should exit search-input mode")
	}
	if m.diffSearchQuery != "neu" {
		t.Errorf("query = %q, want neu", m.diffSearchQuery)
	}
	if m.diffSearch.Count() == 0 {
		t.Error("expected at least one search match for 'neu'")
	}
}

func TestClearDiffSearch(t *testing.T) {
	m := detailModelWithDiff(t)
	m.diffSearchQuery = "func"
	m.refreshDetailViews()
	m.clearDiffSearch()
	if m.diffSearchQuery != "" || m.diffSearch.Count() != 0 {
		t.Error("clearDiffSearch should drop the query and matches")
	}
}

func TestJumpToFindingSelectsFileAndScrolls(t *testing.T) {
	m := detailModelWithDiff(t)
	if ok := m.JumpToFinding("main.go", 2); !ok {
		t.Fatal("JumpToFinding should succeed for an existing file")
	}
	if m.selectedFilePath != "main.go" {
		t.Errorf("selected file = %q, want main.go", m.selectedFilePath)
	}
	if m.centerView != centerDiff {
		t.Error("JumpToFinding should switch to the diff view")
	}
	if ok := m.JumpToFinding("missing.go", 1); ok {
		t.Error("JumpToFinding should fail for an unknown file")
	}
}

func TestToggleThreadsFetchesAndRenders(t *testing.T) {
	m := detailModelWithDiff(t)
	cmd := m.toggleThreads()
	if !m.showThreads {
		t.Fatal("toggleThreads should enable inline comments")
	}
	if cmd == nil {
		t.Fatal("first toggle should fire a fetch command")
	}
	// Simulate the fetch completing.
	m.ApplyThreadsLoaded(
		[]gh.PullReviewComment{{Path: "main.go", Line: 3, Body: "old comment", AuthorLogin: "octocat"}},
		[]gh.ReviewThread{{
			ID:       "T1",
			Comments: []gh.ReviewThreadComment{{Author: "octocat", Body: "old comment", Path: "main.go", Line: 3}},
		}},
	)
	if !m.threadsLoaded {
		t.Error("threads should be marked loaded")
	}
	// A second toggle should not fire another fetch.
	if cmd := m.toggleThreads(); cmd != nil {
		t.Error("second toggle should not re-fetch")
	}
}

func TestReviewHistoryReplyFlowCallsBackend(t *testing.T) {
	m := detailModelWithDiff(t)
	m.ApplyThreadsLoaded(nil, []gh.ReviewThread{{
		ID:       "T1",
		Comments: []gh.ReviewThreadComment{{Author: "octocat", Body: "please fix", Path: "main.go", Line: 3}},
	}})
	m.openReviewHistory()
	if m.centerView != centerHistory {
		t.Fatal("openReviewHistory should switch to the history pane")
	}
	// Start a reply to the selected thread.
	if cmd := m.beginReply("T1"); cmd == nil {
		t.Fatal("beginReply should focus the reply input")
	}
	if m.replyingTo != "T1" {
		t.Fatalf("replyingTo = %q, want T1", m.replyingTo)
	}
	m.replyInput.SetValue("done")
	_, cmd := m.handleReplyKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("submitting a non-empty reply should return a post command")
	}
	if m.replyingTo != "" {
		t.Error("submitting should close the reply prompt")
	}
	// The command should resolve to a ThreadReplyPostedMsg (demo backend no-op).
	msg := cmd()
	if _, ok := msg.(data.ThreadReplyPostedMsg); !ok {
		t.Fatalf("reply cmd produced %T, want ThreadReplyPostedMsg", msg)
	}
}

func TestBeginReplyRejectsEmptyThreadID(t *testing.T) {
	m := detailModelWithDiff(t)
	if cmd := m.beginReply(""); cmd != nil {
		t.Error("beginReply with no thread id should be a no-op")
	}
	if m.replyingTo != "" {
		t.Error("no reply should be armed for an empty thread id")
	}
}
