// Package util holds cross-cutting helpers shared by the root TUI, every
// tab package, and overlays. Nothing in here imports back into tui or its
// tabs — keeping it a leaf prevents import cycles when sub-packages need
// the same primitives.
package util

import (
	"io"
	"os"

	"github.com/atotto/clipboard"
	osc52 "github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"
)

// ClipboardCopiedMsg is delivered after a copy-to-clipboard attempt.
// Mirrors jj-tui: hosts can show a "Copied!" footer or surface the error
// without coupling the copy command to a particular UI surface.
type ClipboardCopiedMsg struct {
	Success bool
	// ViaOSC52 is true when the native clipboard was unavailable and the
	// copy fell back to an OSC52 terminal escape (the SSH-safe path). Hosts
	// can nuance the footer ("Copied (via terminal)") but should still treat
	// it as success — fail-open.
	ViaOSC52 bool
	Err      error
}

// nativeClipboardWrite is the system-clipboard writer (pbcopy / xclip /
// wl-copy / the Windows clipboard, via atotto/clipboard). It is a package
// var so tests can force the native path to fail and exercise the OSC52
// fallback deterministically without depending on the host environment.
var nativeClipboardWrite = clipboard.WriteAll

// osc52Writer is where the OSC52 clipboard escape is emitted when the native
// clipboard is unavailable. It defaults to os.Stdout (the terminal bubbletea
// draws to), so the sequence reaches the outer terminal — including over SSH,
// where pbcopy/xclip aren't reachable but the terminal still honours OSC52.
// Tests point it at a buffer to assert the emitted payload.
var osc52Writer io.Writer = os.Stdout

// copyToClipboard tries the native system clipboard first and, if that fails
// (typically the "no clipboard utilities available" case over SSH / headless),
// falls back to emitting an OSC52 escape to the terminal. It reports whether
// the OSC52 fallback was used and any final error. Fail-open: the OSC52 write
// is best-effort — a terminal that ignores the sequence still leaves the copy
// looking successful to the caller, which is the correct UX (we cannot read
// the clipboard back to confirm anyway).
func copyToClipboard(text string) (viaOSC52 bool, err error) {
	if nerr := nativeClipboardWrite(text); nerr == nil {
		return false, nil
	}
	// Native clipboard unavailable — fall back to OSC52. Works over SSH and
	// inside most modern terminals where the native helpers don't exist.
	if _, werr := osc52.New(text).WriteTo(osc52Writer); werr != nil {
		return true, werr
	}
	return true, nil
}

// CopyPlainTextCmd writes text to the system clipboard (native, then OSC52
// fallback) and returns a ClipboardCopiedMsg with the outcome. It never
// panics or blocks the UI — a copy that fails every path returns
// Success:false with the error so the host can flash a brief status.
func CopyPlainTextCmd(text string) tea.Cmd {
	return func() tea.Msg {
		via, err := copyToClipboard(text)
		if err != nil {
			return ClipboardCopiedMsg{Success: false, ViaOSC52: via, Err: err}
		}
		return ClipboardCopiedMsg{Success: true, ViaOSC52: via}
	}
}
