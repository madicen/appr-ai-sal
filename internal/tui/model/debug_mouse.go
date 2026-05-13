package model

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// debugLogDetailMouse writes one stderr line on left-button press in PR detail:
// terminal cell, which pane zones contain the point, and key zone Y ranges.
func (m *Model) debugLogDetailMouse(msg tea.MouseMsg) {
	if !m.opts.DebugMouse || m.mode != modeDetail {
		return
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[appr-ai-sal debug-mouse] cell=(%d,%d)", msg.X, msg.Y)
	for _, z := range []struct {
		id   string
		name string
	}{
		{zones.PaneTree, "pane-tree"},
		{zones.PaneDiff, "pane-diff"},
	} {
		if zz := zone.Get(z.id); zz != nil && zz.InBounds(msg) {
			fmt.Fprintf(&b, " %s", z.name)
		}
	}

	appendZoneY := func(label, id string) {
		zz := zone.Get(id)
		if zz == nil {
			fmt.Fprintf(&b, " %s=?", label)
			return
		}
		fmt.Fprintf(&b, " %s=y[%d..%d]", label, zz.StartY, zz.EndY)
	}

	appendZoneY("treeBody", zones.PaneTreeBody)
	appendZoneY("diffBody", zones.PaneDiffBody)

	if len(m.treeRows) > 0 {
		appendZoneY("treeRow0", zones.TreeFile(0))
	}

	if ti, ok := m.treeRowFromMouse(msg); ok {
		fmt.Fprintf(&b, " ->treeIdx=%d", ti)
	}

	fmt.Fprintln(os.Stderr, b.String())
}
