package model

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
	overlay "github.com/madicen/bubble-overlay"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/overlays"
	reviewtab "github.com/madicen/appr-ai-sal/internal/tui/tabs/review"
)

// Regression: a review overlay left on the modal stack traps every key/mouse
// event (InteractiveToBase=false) even after the user navigates back to the PR
// list, making the list appear frozen.
func TestListInputWorksAfterReviewClosedAndBackToList(t *testing.T) {
	zone.NewGlobal()
	m := New(Options{AIConfig: aiconfig.DefaultConfig()})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(data.PRListMsg{PRs: []gh.PR{
		{Number: 1, Title: "One", Repository: "o/r", Owner: "o", Repo: "r", Author: "alice"},
		{Number: 2, Title: "Two", Repository: "o/r", Owner: "o", Repo: "r", Author: "bob"},
	}})

	pr := &gh.PR{Number: 1, Title: "One", Repository: "o/r", Owner: "o", Repo: "r", Author: "alice"}
	m.Update(data.PRDetailMsg{PR: pr, Diff: "diff --git a/a.go b/a.go\n"})

	ro := reviewtab.New(m.width, m.height, false, false, false, m.opts.AIConfig, false)
	m.overlayStack.Push(ro, reviewWindowConfig())
	m.currentReviewOverlay = ro

	out, _ := m.Update(reviewtab.CloseMsg{})
	m = out.(*Model)
	if m.overlayStack.Depth() != 0 {
		t.Fatalf("CloseMsg should drain the review overlay; stack depth=%d", m.overlayStack.Depth())
	}

	m.BackToList()
	if m.mode != modeList {
		t.Fatalf("mode=%v want modeList", m.mode)
	}
	if m.overlayStack.Depth() != 0 {
		t.Fatalf("BackToList must not leave overlays on the stack; depth=%d", m.overlayStack.Depth())
	}

	before := m.list.Index()
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = out.(*Model)
	if m.list.Index() == before {
		t.Fatalf("list cursor did not advance after ↓ (still at %d)", before)
	}
}

func TestListInputWorksViaEscAfterReviewClosed(t *testing.T) {
	zone.NewGlobal()
	m := New(Options{AIConfig: aiconfig.DefaultConfig()})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(data.PRListMsg{PRs: []gh.PR{
		{Number: 1, Title: "One", Repository: "o/r", Owner: "o", Repo: "r", Author: "alice"},
		{Number: 2, Title: "Two", Repository: "o/r", Owner: "o", Repo: "r", Author: "bob"},
	}})

	pr := &gh.PR{Number: 1, Title: "One", Repository: "o/r", Owner: "o", Repo: "r", Author: "alice"}
	m.Update(data.PRDetailMsg{PR: pr, Diff: "diff --git a/a.go b/a.go\n"})

	ro := reviewtab.New(m.width, m.height, false, false, false, m.opts.AIConfig, false)
	m.overlayStack.Push(ro, reviewWindowConfig())
	m.currentReviewOverlay = ro

	out, _ := m.Update(reviewtab.CloseMsg{})
	m = out.(*Model)

	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = out.(*Model)
	if m.mode != modeList {
		t.Fatalf("esc on detail after review close should return to list; mode=%v", m.mode)
	}
	if m.overlayStack.Depth() != 0 {
		t.Fatalf("esc back to list must drain overlays; depth=%d", m.overlayStack.Depth())
	}
	if m.tabs[modeList] != nil {
		t.Fatal("detail tab must not be registered under modeList after esc → BackToList")
	}

	before := m.list.Index()
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = out.(*Model)
	if m.list.Index() == before {
		t.Fatalf("list cursor did not advance after ↓ via Update (still at %d)", before)
	}
}

func TestBackToListClearsNonReviewOverlayAboveReview(t *testing.T) {
	zone.NewGlobal()
	m := New(Options{AIConfig: aiconfig.DefaultConfig()})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(data.PRListMsg{PRs: []gh.PR{
		{Number: 1, Title: "One", Repository: "o/r", Owner: "o", Repo: "r", Author: "alice"},
		{Number: 2, Title: "Two", Repository: "o/r", Owner: "o", Repo: "r", Author: "bob"},
	}})

	ro := reviewtab.New(m.width, m.height, false, false, false, m.opts.AIConfig, false)
	m.overlayStack.Push(ro, reviewWindowConfig())
	m.overlayStack.Push(overlays.NewErrorOverlay("boom", 80, 24), overlay.DefaultOverlayConfig())
	m.currentReviewOverlay = ro
	m.mode = modeDetail

	m.BackToList()
	if m.mode != modeList {
		t.Fatalf("mode=%v want modeList", m.mode)
	}
	if m.overlayStack.Depth() != 0 {
		t.Fatalf("BackToList must clear error + review overlays; depth=%d", m.overlayStack.Depth())
	}

	before := m.list.Index()
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = out.(*Model)
	if m.list.Index() == before {
		t.Fatalf("list cursor did not advance; non-review overlay still trapping keys?")
	}
}

func TestListModeClearsPostedOverlayBeforeRoutingInput(t *testing.T) {
	zone.NewGlobal()
	m := New(Options{AIConfig: aiconfig.DefaultConfig()})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(data.PRListMsg{PRs: []gh.PR{
		{Number: 1, Title: "One", Repository: "o/r", Owner: "o", Repo: "r"},
		{Number: 2, Title: "Two", Repository: "o/r", Owner: "o", Repo: "r"},
	}})
	m.mode = modeList

	m.overlayStack.Push(overlays.PostedOverlay{}, overlay.DefaultOverlayConfig())

	before := m.list.Index()
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = out.(*Model)
	if m.overlayStack.Depth() != 0 {
		t.Fatalf("list-mode input should clear stray posted overlay; depth=%d", m.overlayStack.Depth())
	}
	if m.list.Index() == before {
		t.Fatalf("list cursor did not advance; posted overlay still trapping keys?")
	}
}

func TestBackToListClearsErrorOverlay(t *testing.T) {
	zone.NewGlobal()
	m := New(Options{AIConfig: aiconfig.DefaultConfig()})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.err = errors.New("boom")
	m.overlayStack.Push(overlays.NewErrorOverlay("boom", 80, 24), overlay.DefaultOverlayConfig())
	m.mode = modeDetail

	m.BackToList()
	if m.overlayStack.Depth() != 0 {
		t.Fatalf("BackToList must clear error overlay; depth=%d", m.overlayStack.Depth())
	}
	if m.err != nil {
		t.Fatal("BackToList should clear m.err when dismissing error overlay")
	}
}

func TestCloseMsgDrainsStackedReviewOverlays(t *testing.T) {
	zone.NewGlobal()
	m := New(Options{AIConfig: aiconfig.DefaultConfig()})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	ro1 := reviewtab.New(m.width, m.height, false, false, false, m.opts.AIConfig, false)
	ro2 := reviewtab.New(m.width, m.height, false, false, false, m.opts.AIConfig, false)
	m.overlayStack.Push(ro1, reviewWindowConfig())
	m.overlayStack.Push(ro2, reviewWindowConfig())
	m.currentReviewOverlay = ro2

	out, _ := m.Update(reviewtab.CloseMsg{})
	m = out.(*Model)
	if m.overlayStack.Depth() != 0 {
		t.Fatalf("CloseMsg should pop every stacked review overlay; depth=%d", m.overlayStack.Depth())
	}
}

func TestListModeClearsStrayReviewOverlayBeforeRoutingInput(t *testing.T) {
	zone.NewGlobal()
	m := New(Options{AIConfig: aiconfig.DefaultConfig()})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(data.PRListMsg{PRs: []gh.PR{
		{Number: 1, Title: "One", Repository: "o/r", Owner: "o", Repo: "r"},
		{Number: 2, Title: "Two", Repository: "o/r", Owner: "o", Repo: "r"},
	}})
	m.mode = modeList

	ro := reviewtab.New(m.width, m.height, false, false, false, m.opts.AIConfig, false)
	m.overlayStack.Push(ro, reviewWindowConfig())
	m.currentReviewOverlay = ro

	before := m.list.Index()
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = out.(*Model)
	if m.overlayStack.Depth() != 0 {
		t.Fatalf("list-mode input should clear stray review overlays; depth=%d", m.overlayStack.Depth())
	}
	if m.list.Index() == before {
		t.Fatalf("list cursor did not advance; overlay still trapping keys?")
	}
}
