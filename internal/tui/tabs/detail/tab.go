// Package detail implements the PR detail view as a first-class Tab (F5 item 4).
package detail

import tea "github.com/charmbracelet/bubbletea"

// Host is implemented by the root model for cross-cutting services the detail
// tab delegates to (overlays, settings, review launch, shared PR state).
type Host interface {
	DetailHandleKey(msg tea.KeyMsg) tea.Cmd
	DetailHandleMouse(msg tea.MouseMsg, wheel bool) (tea.Cmd, bool)
	DetailViewBody() string
	DetailRelayout()
	DetailRefreshViews()
	DetailResize(w, bodyH int)
	DetailSetContentOrigin(top int)
}

// View adapts the root's detail handlers to the uniform Tab interface via
// model.tabAdapter. Handlers and state still live under model/ for now; this
// package owns the Tab seam (F5 item 4).
type View struct {
	host Host
}

// New constructs the detail tab view wired to root host.
func New(host Host) *View { return &View{host: host} }

func (v *View) Init() tea.Cmd { return nil }

func (v *View) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return v, v.host.DetailHandleKey(msg)
	case tea.MouseMsg:
		cmd, _ := v.host.DetailHandleMouse(msg, false)
		return v, cmd
	case tea.WindowSizeMsg:
		v.host.DetailResize(msg.Width, 0)
		return v, nil
	default:
		return v, nil
	}
}

func (v *View) View() string { return v.host.DetailViewBody() }

func (v *View) Resize(w, h int) { v.host.DetailResize(w, h) }

func (v *View) SetContentOrigin(top int) { v.host.DetailSetContentOrigin(top) }
