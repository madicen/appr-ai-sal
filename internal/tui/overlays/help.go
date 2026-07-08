package overlays

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/keys"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// HelpOverlay is the full-screen `?` reference: every binding the keymap
// defines, grouped by context (global / list / detail / review overlay).
// It is derived entirely from keys.Map.Sections() so it can never drift
// from the handlers or the status bar. Toggled by `?`, dismissed by
// `?`/esc/q/enter.
type HelpOverlay struct {
	sections []keys.Section
	vp       viewport.Model
}

// NewHelpOverlay builds the help modal for the given keymap sections and
// window size.
func NewHelpOverlay(sections []keys.Section, w, h int) HelpOverlay {
	vw := util.Clamp(w-8, 20, 100)
	vh := util.Clamp(h-10, 6, 40)
	vp := viewport.New(vw, vh)
	vp.MouseWheelEnabled = true
	m := HelpOverlay{sections: sections, vp: vp}
	m.vp.SetContent(m.render(vw))
	return m
}

func (m HelpOverlay) Init() tea.Cmd { return nil }

func (m HelpOverlay) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.vp.Width = util.Clamp(msg.Width-8, 20, 100)
		m.vp.Height = util.Clamp(msg.Height-10, 6, 40)
		m.vp.SetContent(m.render(m.vp.Width))
		var c tea.Cmd
		m.vp, c = m.vp.Update(msg)
		return m, c
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, helpDismissKeys):
			return m, dismiss(nil)
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if z := zone.Get(zones.HelpDismiss); z != nil && z.InBounds(msg) {
				return m, dismiss(nil)
			}
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// render lays out every section as "  key   description" rows under a bold
// title, with the key column padded to a common width so the descriptions
// line up.
func (m HelpOverlay) render(width int) string {
	var b strings.Builder
	// Compute the widest key label so descriptions align across sections.
	keyW := 0
	for _, s := range m.sections {
		for _, bnd := range s.Bindings {
			if k := bnd.Help().Key; len(k) > keyW {
				keyW = len(k)
			}
		}
	}
	for i, s := range m.sections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(styles.BoldStyle.Render(s.Title))
		b.WriteString("\n")
		for _, bnd := range s.Bindings {
			h := bnd.Help()
			if h.Key == "" {
				continue
			}
			pad := strings.Repeat(" ", max0(keyW-len(h.Key)))
			b.WriteString("  " + styles.BoldStyle.Render(h.Key) + pad + "  " +
				styles.DimStyle.Render(h.Desc) + "\n")
		}
	}
	return b.String()
}

func (m HelpOverlay) View() string {
	var out strings.Builder
	out.WriteString(styles.BoldStyle.Render("Keyboard shortcuts"))
	out.WriteString("\n\n")
	out.WriteString(m.vp.View())
	out.WriteString("\n\n")
	out.WriteString(styles.DimStyle.Render("scroll with the mouse wheel · "))
	out.WriteString(zone.Mark(zones.HelpDismiss, styles.DimStyle.Render(" close (? / esc) ")))
	return ModalFrameSized(util.Clamp(m.vp.Width+8, 56, 108)).Render(out.String())
}

// max0 clamps n to a minimum of 0 (padding width guard).
func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

var helpDismissKeys = key.NewBinding(key.WithKeys("?", "esc", "q", "enter", " "))
