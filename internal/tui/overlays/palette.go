package overlays

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/commands"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// PaletteChoice is the DismissMsg.Result payload emitted by PaletteOverlay
// when the user runs a command. The root pops the palette and invokes
// Cmd.Run — which returns the tea.Cmd the model already understands, so a
// palette invocation performs the exact same action as the command's key.
type PaletteChoice struct {
	Cmd commands.Command
}

// paletteMaxRows caps how many command rows are shown at once so a long
// registry never blows past the modal / terminal height.
const paletteMaxRows = 12

// PaletteOverlay is the ctrl+k fuzzy command palette. It is modal: while
// open it owns key input (the overlay stack routes every key/mouse event
// here). Typing filters the enabled command snapshot with sahilm/fuzzy,
// up/down move the selection, enter runs it, esc/ctrl+k closes.
//
// It is a pointer type because it holds a live textinput.Model whose state
// mutates as the user types.
type PaletteOverlay struct {
	input    textinput.Model
	all      []commands.Command // enabled snapshot, in registration order
	filtered []commands.Command
	cursor   int
	width    int
}

// NewPaletteOverlay builds the palette over the given enabled command
// snapshot, sized to the window.
func NewPaletteOverlay(cmds []commands.Command, w, h int) *PaletteOverlay {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.Placeholder = "type to filter commands…"
	ti.CharLimit = 120
	ti.Width = util.Clamp(w-14, 20, 80)
	ti.Focus()
	m := &PaletteOverlay{
		input: ti,
		all:   cmds,
		width: util.Clamp(w-8, 40, 96),
	}
	m.refilter()
	return m
}

func (m *PaletteOverlay) Init() tea.Cmd { return textinput.Blink }

// refilter recomputes the visible command list from the current query and
// clamps the cursor into range.
func (m *PaletteOverlay) refilter() {
	m.filtered = commands.Filter(m.all, m.input.Value())
	if m.cursor >= len(m.filtered) {
		m.cursor = max0(len(m.filtered) - 1)
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *PaletteOverlay) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.input.Width = util.Clamp(msg.Width-14, 20, 80)
		m.width = util.Clamp(msg.Width-8, 40, 96)
		return m, nil
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, paletteCloseKeys):
			return m, dismiss(nil)
		case key.Matches(msg, paletteDownKeys):
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
			}
			return m, nil
		case key.Matches(msg, paletteUpKeys):
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case key.Matches(msg, paletteRunKeys):
			return m, m.runSelected()
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.refilter()
		return m, cmd
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			for i := range m.visible() {
				if z := zone.Get(zones.PaletteRow(i)); z != nil && z.InBounds(msg) {
					m.cursor = m.windowStart() + i
					return m, m.runSelected()
				}
			}
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.refilter()
	return m, cmd
}

// runSelected dismisses the palette carrying the chosen command so the root
// can run it. A no-op (plain dismiss) when there is no selection.
func (m *PaletteOverlay) runSelected() tea.Cmd {
	if m.cursor < 0 || m.cursor >= len(m.filtered) {
		return dismiss(nil)
	}
	return dismiss(PaletteChoice{Cmd: m.filtered[m.cursor]})
}

// windowStart is the index of the first visible row given the cursor and
// the paletteMaxRows window (keeps the cursor on screen while scrolling).
func (m *PaletteOverlay) windowStart() int {
	if len(m.filtered) <= paletteMaxRows {
		return 0
	}
	start := m.cursor - paletteMaxRows/2
	if start < 0 {
		start = 0
	}
	if start > len(m.filtered)-paletteMaxRows {
		start = len(m.filtered) - paletteMaxRows
	}
	return start
}

// visible returns the slice of commands currently rendered (the window).
func (m *PaletteOverlay) visible() []commands.Command {
	start := m.windowStart()
	end := start + paletteMaxRows
	if end > len(m.filtered) {
		end = len(m.filtered)
	}
	return m.filtered[start:end]
}

func (m *PaletteOverlay) View() string {
	var b strings.Builder
	b.WriteString(styles.BoldStyle.Render("Command palette"))
	b.WriteString("\n\n")
	b.WriteString(m.input.View())
	b.WriteString("\n\n")

	if len(m.filtered) == 0 {
		b.WriteString(styles.DimStyle.Render("no matching commands"))
	} else {
		start := m.windowStart()
		for i, c := range m.visible() {
			absolute := start + i
			row := m.renderRow(c, absolute == m.cursor)
			b.WriteString(zone.Mark(zones.PaletteRow(i), row))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(styles.DimStyle.Render("↑/↓ move · enter run · esc close"))
	return ModalFrameSized(m.width).Render(b.String())
}

// renderRow renders one command line: a selection caret, the title, and a
// right-aligned shortcut label when the command has a key binding.
func (m *PaletteOverlay) renderRow(c commands.Command, selected bool) string {
	caret := "  "
	title := c.Title
	if selected {
		caret = styles.OkStyle.Render("› ")
		title = styles.BoldStyle.Render(title)
	}
	line := caret + title
	if sc := c.ShortcutLabel(); sc != "" {
		line += "  " + styles.DimStyle.Render("["+sc+"]")
	}
	if c.Category != "" {
		line += "  " + styles.DimStyle.Render(c.Category)
	}
	return line
}

var (
	paletteCloseKeys = key.NewBinding(key.WithKeys("esc", "ctrl+k"))
	paletteDownKeys  = key.NewBinding(key.WithKeys("down", "ctrl+n"))
	paletteUpKeys    = key.NewBinding(key.WithKeys("up", "ctrl+p"))
	paletteRunKeys   = key.NewBinding(key.WithKeys("enter"))
)
