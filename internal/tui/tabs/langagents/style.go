package langagents

import "github.com/charmbracelet/lipgloss"

// Styles intentionally re-use the same palette ideas as the repoagents
// tab so the two tabs feel like siblings, but with a slightly narrower
// table since language briefs have less per-row content.
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("213"))

	hintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Italic(true)

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("250"))

	rowStyle = lipgloss.NewStyle().
			Padding(0, 1)

	rowSelectedStyle = rowStyle.
				Background(lipgloss.Color("236")).
				Foreground(lipgloss.Color("231"))

	chipBundled = lipgloss.NewStyle().
			Foreground(lipgloss.Color("114")).
			Bold(true)

	chipCached = lipgloss.NewStyle().
			Foreground(lipgloss.Color("117"))

	chipMissing = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203")).
			Bold(true)

	chipStale = lipgloss.NewStyle().
			Foreground(lipgloss.Color("215"))

	chipBusy = lipgloss.NewStyle().
			Foreground(lipgloss.Color("228")).
			Italic(true)

	errStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203")).
			Bold(true)

	// btnStyle / btnDangerStyle render the per-row mouse action buttons
	// (Generate/Refresh and Delete) and the footer Close button so the
	// tab can be driven without the keyboard.
	btnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("238")).
			Padding(0, 1)

	btnDangerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("131")).
			Padding(0, 1)
)
