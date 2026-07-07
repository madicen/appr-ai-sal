// Package dropdown wraps the madicen/bubble-dropdown component in a Host that
// owns the single concern every tab used to hand-roll: creating the dropdown
// bound to the global bubblezone manager, recreating it when the option set
// changes (the component has no runtime SetOptions), tracking focus,
// translating mouse presses into body-local space, forwarding messages and
// writing the returned pointer back, applying the selection via an OnSelect
// callback, and compositing the open panel over a view.
//
// Before F6 this logic was copy-pasted three times: settings had three
// dropdowns (strictness / profile / provider), repoagents had the repo picker,
// and the root controls pane had the AI-profile picker. They now all share
// this one integration.
package dropdown

import (
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
	bubbledropdown "github.com/madicen/bubble-dropdown"
)

// Host owns one bubble-dropdown pointer plus the glue around it. The zero
// value is usable after New; all methods are nil-safe so a Host whose dropdown
// has not been built yet is inert.
type Host struct {
	dd          *bubbledropdown.Dropdown
	placeholder string

	// ContentTop is the absolute terminal row where the owning body begins.
	// An open panel hit-tests item rows geometrically, so mouse presses are
	// shifted up by ContentTop before reaching the component. Leave it 0 for
	// panels composited at the root (screen-absolute coordinates).
	ContentTop int

	// OnSelect, when set, runs after Forward observes the selection index
	// change (keyboard or mouse) and may return a command that Forward
	// batches with the dropdown's own.
	OnSelect func(index int) tea.Cmd
}

// New returns a Host that will build dropdowns with the given placeholder.
func New(placeholder string) *Host { return &Host{placeholder: placeholder} }

// Rebuild recreates the dropdown from labels with selected pre-selected,
// unless the panel is currently open (an open panel implies no structural
// change is in flight). The new dropdown is bound to the global zone manager
// so its trigger is hit-tested by the root zone.Scan.
func (h *Host) Rebuild(labels []string, selected int) {
	if h == nil || (h.dd != nil && h.dd.Open()) {
		return
	}
	if selected < 0 || selected >= len(labels) {
		selected = 0
	}
	d := bubbledropdown.New(
		bubbledropdown.WithOptions(labels),
		bubbledropdown.WithInitialIndex(selected),
		bubbledropdown.WithPlaceholder(h.placeholder),
	)
	d.SetZoneManager(zone.DefaultManager)
	h.dd = d
}

// Clear drops the dropdown (e.g. when there are no options to show).
func (h *Host) Clear() {
	if h != nil {
		h.dd = nil
	}
}

// Built reports whether a dropdown has been created.
func (h *Host) Built() bool { return h != nil && h.dd != nil }

// Open reports whether the panel is currently displayed.
func (h *Host) Open() bool { return h != nil && h.dd != nil && h.dd.Open() }

// SelectedIndex returns the selected option index (0 when not built).
func (h *Host) SelectedIndex() int {
	if h == nil || h.dd == nil {
		return 0
	}
	return h.dd.SelectedIndex()
}

// SetSelectedIndex updates the displayed selection (clamped by the component).
func (h *Host) SetSelectedIndex(i int) {
	if h != nil && h.dd != nil {
		h.dd.SetSelectedIndex(i)
	}
}

// SetFocused toggles the trigger's focused (emphasised arrow) indicator.
func (h *Host) SetFocused(f bool) {
	if h != nil && h.dd != nil {
		h.dd.SetFocused(f)
	}
}

// TriggerView renders the closed-state trigger; "" when not built.
func (h *Host) TriggerView() string {
	if h == nil || h.dd == nil {
		return ""
	}
	return h.dd.TriggerView()
}

// Forward routes msg to the dropdown, translating a mouse press into
// body-local space (see ContentTop), writes back the returned pointer, and
// invokes OnSelect when the selection index changed. It returns the batched
// command (nil when not built).
func (h *Host) Forward(msg tea.Msg) tea.Cmd {
	if h == nil || h.dd == nil {
		return nil
	}
	if mm, ok := msg.(tea.MouseMsg); ok && h.ContentTop != 0 {
		mm.Y -= h.ContentTop
		msg = mm
	}
	prev := h.dd.SelectedIndex()
	updated, cmd := h.dd.Update(msg)
	h.dd = updated
	if h.OnSelect != nil {
		if sel := h.dd.SelectedIndex(); sel != prev {
			if c := h.OnSelect(sel); c != nil {
				cmd = tea.Batch(cmd, c)
			}
		}
	}
	return cmd
}

// Composite draws the open panel over main, anchoring the trigger at
// (row, col) and sizing the overlay to viewW×viewH. When closed (or not
// built) it returns main unchanged.
func (h *Host) Composite(main string, row, col, viewW, viewH int) string {
	if h == nil || h.dd == nil || !h.dd.Open() {
		return main
	}
	tw, th := h.dd.TriggerSize()
	h.dd.SetBounds(row, col, tw, th)
	return h.dd.ViewWithOverlay(main, viewW, viewH)
}
