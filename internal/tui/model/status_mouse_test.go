package model

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// statusListFixture parks a Model in modeList with a very wide window so the
// long list status hint stays on one line (keeps each zone's bounding box
// contiguous for the center-click helper).
func statusListFixture(t *testing.T) *Model {
	t.Helper()
	zone.NewGlobal()
	m := New(Options{})
	m.Update(tea.WindowSizeMsg{Width: 400, Height: 40})
	m.prsLoaded = true
	return m
}

// TestStatusBarQuitClickReturnsQuit: the always-present "quit" status hint
// is clickable and dispatches tea.Quit.
func TestStatusBarQuitClickReturnsQuit(t *testing.T) {
	m := statusListFixture(t)
	_ = m.View()
	waitBubbleZone(t, zones.StatusQuit)

	_, cmd, handled := m.handleStatusBarMouse(clickCenterOfZone(t, zones.StatusQuit))
	if !handled {
		t.Fatal("quit status click should be handled")
	}
	if cmd == nil {
		t.Fatal("quit status click should return a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("quit status click should yield tea.QuitMsg, got %T", cmd())
	}
}

// TestStatusBarSettingsClickOpensSettings: clicking the "settings" hint
// from the list view opens the settings tab (the o/, key equivalent).
func TestStatusBarSettingsClickOpensSettings(t *testing.T) {
	m := statusListFixture(t)
	_ = m.View()
	waitBubbleZone(t, zones.StatusSettingsAI)

	out, _, handled := m.handleStatusBarMouse(clickCenterOfZone(t, zones.StatusSettingsAI))
	if !handled {
		t.Fatal("settings status click should be handled")
	}
	if out.(*Model).mode != modeSettings {
		t.Fatalf("settings status click should switch to modeSettings, got %v", out.(*Model).mode)
	}
}

// TestStatusBarRepoAgentsClickOpensTab: the "repo agents" hint opens the
// repo-agents tab (the ctrl+r equivalent).
func TestStatusBarRepoAgentsClickOpensTab(t *testing.T) {
	m := statusListFixture(t)
	_ = m.View()
	waitBubbleZone(t, zones.StatusRepoAgents)

	out, _, handled := m.handleStatusBarMouse(clickCenterOfZone(t, zones.StatusRepoAgents))
	if !handled {
		t.Fatal("repo-agents status click should be handled")
	}
	if out.(*Model).mode != modeRepoAgents {
		t.Fatalf("repo-agents status click should switch to modeRepoAgents, got %v", out.(*Model).mode)
	}
}

// TestStatusBarDetailDiffOnlyToggles: in detail mode the "diff-only" hint
// toggles the full-width diff layout (the d key equivalent).
func TestStatusBarDetailDiffOnlyToggles(t *testing.T) {
	m := detailFixtureModel(t)
	_ = m.View()
	waitBubbleZone(t, zones.StatusDiffOnly)

	before := detailState(t, m).DiffOnly()
	out, _, handled := m.handleStatusBarMouse(clickCenterOfZone(t, zones.StatusDiffOnly))
	if !handled {
		t.Fatal("diff-only status click should be handled")
	}
	if detailState(t, out.(*Model)).DiffOnly() == before {
		t.Fatalf("diff-only status click should flip diffOnly (was %v)", before)
	}
}

// TestStatusBarDetailBackReturnsToList: the "back" hint returns to the
// list view (the esc key equivalent).
func TestStatusBarDetailBackReturnsToList(t *testing.T) {
	m := detailFixtureModel(t)
	_ = m.View()
	waitBubbleZone(t, zones.StatusBack)

	out, _, handled := m.handleStatusBarMouse(clickCenterOfZone(t, zones.StatusBack))
	if !handled {
		t.Fatal("back status click should be handled")
	}
	if out.(*Model).mode != modeList {
		t.Fatalf("back status click should return to modeList, got %v", out.(*Model).mode)
	}
}
