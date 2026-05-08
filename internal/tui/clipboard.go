package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/atotto/clipboard"
)

// clipboardCopiedMsg is delivered after a copy-to-clipboard attempt (mirrors jj-tui).
type clipboardCopiedMsg struct {
	Success bool
	Err     error
}

func copyPlainTextCmd(text string) tea.Cmd {
	return func() tea.Msg {
		if err := clipboard.WriteAll(text); err != nil {
			return clipboardCopiedMsg{Success: false, Err: err}
		}
		return clipboardCopiedMsg{Success: true}
	}
}
