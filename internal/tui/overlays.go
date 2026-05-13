package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
)

// --- Messages to root after overlay interaction ---

type bulkPostAnswerMsg struct {
	Confirm bool
}

type errorOverlayDismissMsg struct{}

type dryRunDismissMsg struct{}

type postedOverlayDismissMsg struct{}

// --- Bulk post confirm ---

type bulkConfirmOverlay struct {
	ref string
}

func newBulkConfirmOverlay(ref string) bulkConfirmOverlay {
	return bulkConfirmOverlay{ref: ref}
}

func (m bulkConfirmOverlay) Init() tea.Cmd { return nil }

func (m bulkConfirmOverlay) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, bulkYesKeys):
			return m, func() tea.Msg { return bulkPostAnswerMsg{Confirm: true} }
		case key.Matches(msg, bulkNoKeys):
			return m, func() tea.Msg { return bulkPostAnswerMsg{Confirm: false} }
		case msg.String() == "ctrl+c":
			return m, func() tea.Msg { return bulkPostAnswerMsg{Confirm: false} }
		}
	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		if z := zone.Get(ZoneConfirmYes); z != nil && z.InBounds(msg) {
			return m, func() tea.Msg { return bulkPostAnswerMsg{Confirm: true} }
		}
		if z := zone.Get(ZoneConfirmNo); z != nil && z.InBounds(msg) {
			return m, func() tea.Msg { return bulkPostAnswerMsg{Confirm: false} }
		}
	}
	return m, nil
}

func (m bulkConfirmOverlay) View() string {
	body := "Post entire review (all comments + summary)\nto " + m.ref + "?\n\n" +
		zone.Mark(ZoneConfirmYes, okStyle.Render(" Yes ")) + "  " +
		zone.Mark(ZoneConfirmNo, errStyle.Render(" No "))
	return modalFrameSized(56).Render(body)
}

// --- Error overlay ---

type errorOverlay struct {
	rawText string
	vp      viewport.Model
	copied  bool
	copyErr string
}

func newErrorOverlay(text string, w, h int) errorOverlay {
	vw := clamp(w-8, 10, 120)
	vh := clamp(h-14, 5, 40)
	vp := viewport.New(vw, vh)
	vp.MouseWheelEnabled = true
	m := errorOverlay{rawText: text, vp: vp}
	m.vp.SetContent(wrapForViewport(m.rawText, m.vp.Width))
	return m
}

func (m errorOverlay) Init() tea.Cmd { return nil }

func (m errorOverlay) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.vp.Width = clamp(msg.Width-8, 10, 120)
		m.vp.Height = clamp(msg.Height-14, 5, 40)
		m.vp.SetContent(wrapForViewport(m.rawText, m.vp.Width))
		var c tea.Cmd
		m.vp, c = m.vp.Update(msg)
		return m, c
	case clipboardCopiedMsg:
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
		case "esc", "enter", "q":
			return m, func() tea.Msg { return errorOverlayDismissMsg{} }
		case " ":
			return m, func() tea.Msg { return errorOverlayDismissMsg{} }
		case "c", "C":
			m.copyErr = ""
			m.copied = false
			return m, copyPlainTextCmd(m.rawText)
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if z := zone.Get(ZoneErrorDismiss); z != nil && z.InBounds(msg) {
				return m, func() tea.Msg { return errorOverlayDismissMsg{} }
			}
			if z := zone.Get(ZoneErrorCopy); z != nil && z.InBounds(msg) {
				m.copyErr = ""
				m.copied = false
				return m, copyPlainTextCmd(m.rawText)
			}
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m errorOverlay) View() string {
	hint := dimStyle.Render("Scroll with mouse wheel · press c for full message on clipboard")
	var footer strings.Builder
	footer.WriteString(zone.Mark(ZoneErrorDismiss, dimStyle.Render(" Dismiss (Esc) ")))
	footer.WriteString("  ")
	if m.copied {
		footer.WriteString(okStyle.Render(" ✓ Copied! "))
	} else {
		footer.WriteString(zone.Mark(ZoneErrorCopy, modalButtonStyle.Render(" Copy (c) ")))
	}
	var out strings.Builder
	out.WriteString(errStyle.Render("Error"))
	out.WriteString("\n\n")
	out.WriteString(m.vp.View())
	out.WriteString("\n\n")
	out.WriteString(hint)
	if m.copyErr != "" {
		out.WriteString("\n")
		out.WriteString(errStyle.Render(m.copyErr))
	}
	out.WriteString("\n\n")
	out.WriteString(footer.String())
	return modalFrameSized(clamp(m.vp.Width+8, 56, 120)).Render(out.String())
}

// --- Dry-run payload preview ---

type dryRunOverlay struct {
	title   string
	rawBody string
	vp      viewport.Model
}

func newDryRunOverlay(title, body string, w, h int) dryRunOverlay {
	vw := clamp(w-8, 10, 120)
	vh := clamp(h-12, 8, 45)
	vp := viewport.New(vw, vh)
	vp.MouseWheelEnabled = true
	m := dryRunOverlay{title: title, rawBody: body, vp: vp}
	m.vp.SetContent(wrapForViewport(m.rawBody, m.vp.Width))
	return m
}

func (m dryRunOverlay) Init() tea.Cmd { return nil }

func (m dryRunOverlay) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.vp.Width = clamp(msg.Width-8, 10, 120)
		m.vp.Height = clamp(msg.Height-12, 8, 45)
		m.vp.SetContent(wrapForViewport(m.rawBody, m.vp.Width))
		var c tea.Cmd
		m.vp, c = m.vp.Update(msg)
		return m, c
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "enter", "q", " ":
			return m, func() tea.Msg { return dryRunDismissMsg{} }
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if z := zone.Get(ZoneDryDismiss); z != nil && z.InBounds(msg) {
				return m, func() tea.Msg { return dryRunDismissMsg{} }
			}
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m dryRunOverlay) View() string {
	content := boldStyle.Render(m.title) + "\n\n" + m.vp.View() + "\n\n" +
		zone.Mark(ZoneDryDismiss, dimStyle.Render(" OK "))
	return modalFrameSized(clamp(m.vp.Width+8, 56, 120)).Render(content)
}

// --- Posted success ---

type postedOverlay struct{}

func (m postedOverlay) Init() tea.Cmd { return nil }

func (m postedOverlay) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "enter", "q", " ":
			return m, func() tea.Msg { return postedOverlayDismissMsg{} }
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if z := zone.Get(ZonePostedOK); z != nil && z.InBounds(msg) {
				return m, func() tea.Msg { return postedOverlayDismissMsg{} }
			}
		}
	}
	return m, nil
}

func (m postedOverlay) View() string {
	return modalFrameSized(56).Render(okStyle.Render("Posted.") + "\n\n" +
		zone.Mark(ZonePostedOK, dimStyle.Render(" OK ")))
}

// modalChrome is the shared border/padding for modal dialogs. Outer width is set
// via modalFrameSized so lipgloss layout matches bubblezone.Scan (same string).
var modalChrome = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("#5D2D91")).
	Padding(1, 2)

func modalFrameSized(outerWidth int) lipgloss.Style {
	return modalChrome.Copy().Width(clamp(outerWidth, 40, 120))
}

var bulkYesKeys = key.NewBinding(key.WithKeys("y", "Y", "enter"))
var bulkNoKeys = key.NewBinding(key.WithKeys("n", "N", "esc", "q"))

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

