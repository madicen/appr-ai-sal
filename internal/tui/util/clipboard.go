// Package util holds cross-cutting helpers shared by the root TUI, every
// tab package, and overlays. Nothing in here imports back into tui or its
// tabs — keeping it a leaf prevents import cycles when sub-packages need
// the same primitives.
package util

import (
	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
)

// ClipboardCopiedMsg is delivered after a copy-to-clipboard attempt.
// Mirrors jj-tui: hosts can show a "Copied!" footer or surface the error
// without coupling the copy command to a particular UI surface.
type ClipboardCopiedMsg struct {
	Success bool
	Err     error
}

// CopyPlainTextCmd writes text to the system clipboard and returns a
// ClipboardCopiedMsg with the outcome.
func CopyPlainTextCmd(text string) tea.Cmd {
	return func() tea.Msg {
		if err := clipboard.WriteAll(text); err != nil {
			return ClipboardCopiedMsg{Success: false, Err: err}
		}
		return ClipboardCopiedMsg{Success: true}
	}
}
