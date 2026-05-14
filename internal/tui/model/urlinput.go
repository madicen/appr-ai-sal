package model

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
)

func (m *Model) handleURLInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.urlInput.Blur()
		return m, nil
	case "enter":
		v := strings.TrimSpace(m.urlInput.Value())
		if v == "" {
			return m, nil
		}
		ref, err := gh.ParsePRURL(v)
		if err != nil {
			return m, func() tea.Msg { return data.ErrMsg{Err: err} }
		}
		m.urlInput.Blur()
		return m, data.LoadPRDetailCmd(ref)
	}
	var cmd tea.Cmd
	m.urlInput, cmd = m.urlInput.Update(msg)
	return m, cmd
}
