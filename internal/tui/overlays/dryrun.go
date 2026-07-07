package overlays

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// DryRunOverlay shows the JSON payload that would have been posted to
// GitHub in --dry-run mode. Scrollable because review payloads routinely
// run several screens.
type DryRunOverlay struct {
	title   string
	rawBody string
	vp      viewport.Model
}

// NewDryRunOverlay constructs the dry-run preview modal sized for the
// given window dimensions.
func NewDryRunOverlay(title, body string, w, h int) DryRunOverlay {
	vw := util.Clamp(w-8, 10, 120)
	vh := util.Clamp(h-12, 8, 45)
	vp := viewport.New(vw, vh)
	vp.MouseWheelEnabled = true
	m := DryRunOverlay{title: title, rawBody: body, vp: vp}
	m.vp.SetContent(util.WrapForViewport(m.rawBody, m.vp.Width))
	return m
}

func (m DryRunOverlay) Init() tea.Cmd { return nil }

func (m DryRunOverlay) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.vp.Width = util.Clamp(msg.Width-8, 10, 120)
		m.vp.Height = util.Clamp(msg.Height-12, 8, 45)
		m.vp.SetContent(util.WrapForViewport(m.rawBody, m.vp.Width))
		var c tea.Cmd
		m.vp, c = m.vp.Update(msg)
		return m, c
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "enter", "q", " ":
			return m, dismiss(nil)
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if z := zone.Get(zones.DryDismiss); z != nil && z.InBounds(msg) {
				return m, dismiss(nil)
			}
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m DryRunOverlay) View() string {
	content := styles.BoldStyle.Render(m.title) + "\n\n" + m.vp.View() + "\n\n" +
		zone.Mark(zones.DryDismiss, styles.DimStyle.Render(" OK "))
	return ModalFrameSized(util.Clamp(m.vp.Width+8, 56, 120)).Render(content)
}
