package model

import (
	"runtime"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	detailtab "github.com/madicen/appr-ai-sal/internal/tui/tabs/detail"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

const threeFileDiff = `diff --git a/aa.go b/aa.go
--- /dev/null
+++ b/aa.go
@@ -0,0 +1 @@
+a
diff --git a/bb.go b/bb.go
--- /dev/null
+++ b/bb.go
@@ -0,0 +1 @@
+b
diff --git a/cc.go b/cc.go
--- /dev/null
+++ b/cc.go
@@ -0,0 +1 @@
+c
`

func waitBubbleZone(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for zone.Get(id) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for zone %q", id)
		}
		runtime.Gosched()
	}
}

func clickCenterOfZone(t *testing.T, id string) tea.MouseMsg {
	t.Helper()
	waitBubbleZone(t, id)
	z := zone.Get(id)
	if z == nil {
		t.Fatalf("zone %q not registered", id)
	}
	return tea.MouseMsg{
		X:      (z.StartX + z.EndX) / 2,
		Y:      (z.StartY + z.EndY) / 2,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
}

func detailFixtureModel(t *testing.T) *Model {
	t.Helper()
	zone.NewGlobal()
	m := New(Options{Demo: true})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 42})
	m.mode = modeDetail
	m.currentPR = &gh.PR{
		Owner: "o", Repo: "r", Repository: "o/r", Number: 1,
		Title: "title", Author: "a", BaseRef: "main", HeadRef: "feat",
		URL: "https://example.com", HeadSHA: "abc",
	}
	m.diff = threeFileDiff
	m.parsedDiff = review.ParseDiff(m.diff)
	m.ensureDetailTab()
	if dt := m.detailTab(); dt != nil {
		dt.OnPRLoaded(m.parsedDiff, m.draft)
		dt.RefreshViews()
	}
	_ = m.View()
	waitBubbleZone(t, zones.PaneTreeBody)
	return m
}

func detailState(t *testing.T, m *Model) *detailtab.Model {
	t.Helper()
	dt := m.detailTab()
	if dt == nil {
		t.Fatal("detail tab missing")
	}
	return dt
}
