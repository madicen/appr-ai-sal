package overlays

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// ErrorOverlay shows a long error message in a scrollable viewport with a
// "Copy to clipboard" affordance. Long stack traces and API responses can
// blow past the terminal height; the overlay lets the user actually read
// (and copy) them.
type ErrorOverlay struct {
	rawText string
	vp      viewport.Model
	copied  bool
	copyErr string
}

// NewErrorOverlay constructs the error modal sized for the given window
// dimensions. The viewport is clamped so the modal frame never overshoots
// the terminal in either direction.
func NewErrorOverlay(text string, w, h int) ErrorOverlay {
	vw := util.Clamp(w-8, 10, 120)
	vh := util.Clamp(h-14, 5, 40)
	vp := viewport.New(vw, vh)
	vp.MouseWheelEnabled = true
	m := ErrorOverlay{rawText: text, vp: vp}
	m.vp.SetContent(util.WrapForViewport(m.rawText, m.vp.Width))
	return m
}

func (m ErrorOverlay) Init() tea.Cmd { return nil }

func (m ErrorOverlay) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.vp.Width = util.Clamp(msg.Width-8, 10, 120)
		m.vp.Height = util.Clamp(msg.Height-14, 5, 40)
		m.vp.SetContent(util.WrapForViewport(m.rawText, m.vp.Width))
		var c tea.Cmd
		m.vp, c = m.vp.Update(msg)
		return m, c
	case util.ClipboardCopiedMsg:
		if msg.Success {
			m.copied = true
			m.copyErr = ""
			return m, nil
		}
		m.copied = false
		if msg.Err != nil {
			m.copyErr = msg.Err.Error()
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "enter", "q", " ":
			return m, dismiss(nil)
		case "c", "C":
			m.copyErr = ""
			m.copied = false
			return m, util.CopyPlainTextCmd(m.rawText)
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if z := zone.Get(zones.ErrorDismiss); z != nil && z.InBounds(msg) {
				return m, dismiss(nil)
			}
			if z := zone.Get(zones.ErrorCopy); z != nil && z.InBounds(msg) {
				m.copyErr = ""
				m.copied = false
				return m, util.CopyPlainTextCmd(m.rawText)
			}
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m ErrorOverlay) View() string {
	hint := styles.DimStyle.Render("Scroll with mouse wheel · press c for full message on clipboard")
	var footer strings.Builder
	footer.WriteString(zone.Mark(zones.ErrorDismiss, styles.DimStyle.Render(" Dismiss (Esc) ")))
	footer.WriteString("  ")
	if m.copied {
		footer.WriteString(styles.OkStyle.Render(" ✓ Copied! "))
	} else {
		footer.WriteString(zone.Mark(zones.ErrorCopy, styles.ModalButtonStyle.Render(" Copy (c) ")))
	}
	var out strings.Builder
	out.WriteString(styles.ErrStyle.Render("Error"))
	out.WriteString("\n\n")
	out.WriteString(m.vp.View())
	out.WriteString("\n\n")
	out.WriteString(hint)
	if m.copyErr != "" {
		out.WriteString("\n")
		out.WriteString(styles.ErrStyle.Render(m.copyErr))
	}
	out.WriteString("\n\n")
	out.WriteString(footer.String())
	return ModalFrameSized(util.Clamp(m.vp.Width+8, 56, 120)).Render(out.String())
}
