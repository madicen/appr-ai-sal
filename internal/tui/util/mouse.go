package util

import (
	"io"
	"os"
	"runtime"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

var mouseTrackingOffSeq = ansi.ResetModeMouseX10 +
	ansi.ResetModeMouseNormal +
	ansi.ResetModeMouseHighlight +
	ansi.ResetModeMouseButtonEvent +
	ansi.ResetModeMouseAnyEvent +
	ansi.ResetModeMouseExtUtf8 +
	ansi.ResetModeMouseExtSgr +
	ansi.ResetModeMouseExtUrxvt +
	ansi.ResetModeMouseExtSgrPixel +
	"\x1b[0m"

func writeMouseOffAndSync(w io.Writer) {
	if w == nil {
		return
	}
	_, _ = io.WriteString(w, mouseTrackingOffSeq)
	if f, ok := w.(*os.File); ok {
		_ = f.Sync()
	}
}

// FlushMouse disables xterm mouse reporting on stdout, stderr, and
// /dev/tty (Unix). Called on quit so the user's terminal isn't left in a
// state where every motion produces escape sequences after the TUI exits.
func FlushMouse() {
	writeMouseOffAndSync(os.Stdout)
	writeMouseOffAndSync(os.Stderr)
	if runtime.GOOS == "windows" {
		return
	}
	f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return
	}
	_, _ = io.WriteString(f, mouseTrackingOffSeq)
	_ = f.Sync()
	_ = f.Close()
}

// WheelScrollViewport applies a wheel event to a single viewport. The
// parent decides which pane receives the event so two side-by-side panes
// never share one wheel tick. Shift+wheel scrolls horizontally.
func WheelScrollViewport(vp *viewport.Model, msg tea.MouseMsg) {
	delta := vp.MouseWheelDelta
	if delta < 1 {
		delta = 3
	}
	const hStep = 4
	switch msg.Button { //nolint:exhaustive
	case tea.MouseButtonWheelUp:
		if msg.Shift {
			vp.ScrollLeft(hStep)
		} else {
			vp.ScrollUp(delta)
		}
	case tea.MouseButtonWheelDown:
		if msg.Shift {
			vp.ScrollRight(hStep)
		} else {
			vp.ScrollDown(delta)
		}
	case tea.MouseButtonWheelLeft:
		vp.ScrollLeft(hStep)
	case tea.MouseButtonWheelRight:
		vp.ScrollRight(hStep)
	}
}
