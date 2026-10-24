package model

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
)

func listWithPRs(t *testing.T, n int) *Model {
	t.Helper()
	zone.NewGlobal()
	m := New(Options{})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 42})
	prs := make([]gh.PR, n)
	for i := range prs {
		prs[i] = gh.PR{Number: i + 1, Title: "t", Repository: "o/r", Owner: "o", Repo: "r", Author: "a"}
	}
	m.Update(data.PRListMsg{PRs: prs})
	_ = m.View()
	return m
}

func TestListMouseWheelMovesCursor(t *testing.T) {
	m := listWithPRs(t, 20)
	listTop := m.listBodyOriginY()
	y := listTop + m.list.Height()/2
	before := m.list.Index()

	out, _ := m.handleMouse(tea.MouseMsg{
		X: 40, Y: y,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelDown,
	})
	m2 := out.(*Model)
	if m2.list.Index() != before+1 {
		t.Fatalf("wheel down over list: index got %d want %d", m2.list.Index(), before+1)
	}
}

// Wheel must move the list even when Y is outside the list widget (filter
// strip, status bar, or a terminal that reports Y=0 for wheel). The old
// Y-bounds check dropped those events, so the cursor never moved and the
// terminal's own scrollback took over.
func TestListMouseWheelMovesCursorOutsideListBounds(t *testing.T) {
	m := listWithPRs(t, 20)
	before := m.list.Index()

	out, _ := m.handleMouse(tea.MouseMsg{
		X: 40, Y: 0, // above the list body
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelDown,
	})
	m2 := out.(*Model)
	if m2.list.Index() != before+1 {
		t.Fatalf("wheel down at Y=0: index got %d want %d (Y-bounds drop?)", m2.list.Index(), before+1)
	}

	out, _ = m2.handleMouse(tea.MouseMsg{
		X: 40, Y: m2.height - 1, // status row
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelUp,
	})
	m3 := out.(*Model)
	if m3.list.Index() != before {
		t.Fatalf("wheel up at status row: index got %d want %d", m3.list.Index(), before)
	}
}

func TestListMouseWheelIgnoredWhenInputFocused(t *testing.T) {
	m := listWithPRs(t, 20)
	m.focusSearchInput()
	before := m.list.Index()

	out, _ := m.handleMouse(tea.MouseMsg{
		X: 40, Y: 0,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelDown,
	})
	m2 := out.(*Model)
	if m2.list.Index() != before {
		t.Fatalf("wheel with search focused must not move list; got %d want %d", m2.list.Index(), before)
	}
}

func TestListMouseWheelViaRootUpdate(t *testing.T) {
	m := listWithPRs(t, 20)
	before := m.list.Index()
	out, _ := m.Update(tea.MouseMsg{
		X: 40, Y: 0,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelDown,
	})
	m2 := out.(*Model)
	if m2.list.Index() != before+1 {
		t.Fatalf("Update wheel down: index got %d want %d", m2.list.Index(), before+1)
	}
}
