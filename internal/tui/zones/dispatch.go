package zones

import (
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
)

// ClickHandler pairs a bubblezone marker ID with the action to run when a
// left-button press lands inside that zone. Do mutates the owning model as
// needed and returns any resulting tea.Cmd (or nil).
type ClickHandler struct {
	Zone string
	Do   func() tea.Cmd
}

// IsLeftPress reports whether msg is a left-button press (and not a wheel
// event). Tabs use it to decide whether a mouse event is a click they own
// versus a scroll they should pass through to their viewport.
func IsLeftPress(msg tea.MouseMsg) bool {
	if tea.MouseEvent(msg).IsWheel() {
		return false
	}
	return msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft
}

// DispatchClick walks handlers in order and runs the first whose zone is
// currently registered and contains msg, returning its command. It returns
// nil when msg is not a left-button press (wheel / release / right-click)
// or when no handler's zone is hit — matching the pre-refactor mouse
// handlers, which returned nil for both cases so the caller can fall
// through to its own wheel/scroll handling.
//
// This replaces the long `if z := zone.Get(ID); z != nil && z.InBounds(msg)`
// if-chains each tab hand-rolled in its mouse.go.
func DispatchClick(msg tea.MouseMsg, handlers []ClickHandler) tea.Cmd {
	if !IsLeftPress(msg) {
		return nil
	}
	for _, h := range handlers {
		if h.Do == nil {
			continue
		}
		if z := zone.Get(h.Zone); z != nil && z.InBounds(msg) {
			return h.Do()
		}
	}
	return nil
}

// ForEachRowZone flattens per-row handlers into a single slice: for each
// item it appends the handlers rows(item) produces. It's a convenience for
// building the repetitive per-row chip tables (Regenerate/Edit/Delete, …)
// that used to be nested `for … { if z := … }` loops.
func ForEachRowZone[T any](items []T, rows func(T) []ClickHandler) []ClickHandler {
	out := make([]ClickHandler, 0, len(items)*3)
	for _, it := range items {
		out = append(out, rows(it)...)
	}
	return out
}
