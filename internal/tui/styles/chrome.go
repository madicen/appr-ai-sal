// Package styles owns every lipgloss style used by the root TUI, the
// tabs, and the overlay stack. Tag and severity colours are sourced from
// internal/theme on every render so user customisation in the Theme
// settings tab takes effect without a TUI restart.
package styles

import "github.com/charmbracelet/lipgloss"

var (
	// AppPadding is the standard 1-cell horizontal padding applied to most
	// body content so the active text never butts against the terminal
	// edge.
	AppPadding = lipgloss.NewStyle().Padding(0, 1)

	// HeaderBar is the full-width purple bar at the top of every screen.
	HeaderBar = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#5D2D91")).
			Padding(0, 1)

	// StatusBar is the dim help/footer line at the bottom of the screen.
	StatusBar = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			Padding(0, 1)

	// PanelBorder is the colour for two-pane layouts; high enough
	// contrast on both light and dark backgrounds to read as a real box.
	PanelBorder = lipgloss.AdaptiveColor{Light: "#666666", Dark: "#9A9A9A"}

	// PanelBorderAccent highlights the panes flanking an in-flight pane
	// seam drag. Picked to read as "active" against PanelBorder without
	// turning purple — keeps the pane chrome visually consistent with
	// the rest of the app while signalling which seam is being grabbed.
	PanelBorderAccent = lipgloss.AdaptiveColor{Light: "#1F6FEB", Dark: "#58A6FF"}

	// LeftPanel frames a column with a rounded border tinted by
	// PanelBorder.
	LeftPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(PanelBorder).
			Padding(0, 1)

	// LeftPanelAccent is LeftPanel with the accent border colour applied
	// — used by the drag-resize handler to highlight the panes flanking
	// the seam currently being dragged.
	LeftPanelAccent = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(PanelBorderAccent).
			Padding(0, 1)

	// DetailPaneTitleStyle is the one-line strip drawn above each scroll
	// viewport in the PR detail and staged-comments views.
	DetailPaneTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#555555", Dark: "#BBBBBB"}).
				Background(lipgloss.AdaptiveColor{Light: "#E8E8E8", Dark: "#2C2C2C"}).
				Padding(0, 0)

	// ModalButtonStyle styles secondary modal actions (Copy, OK, etc.) in
	// a jj-tui-style filled chip.
	ModalButtonStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(lipgloss.Color("#30363d")).
				Padding(0, 1).
				Bold(true)

	// Chip styles are the filled action pills shared by the agent-management
	// tabs (repo-agents, and — via aliases — settings). Centralising them
	// here keeps the palette in one place instead of copied hex per tab.
	//
	// ChipStyle is the neutral chip (same surface as ModalButtonStyle).
	ChipStyle = ModalButtonStyle
	// ChipBusyStyle marks an in-flight action (blue surface).
	ChipBusyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#7AA2F7")).
			Padding(0, 1).
			Bold(true)
	// ChipPrimaryStyle marks the primary action (purple surface, matching
	// the header bar).
	ChipPrimaryStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(lipgloss.Color("#5D2D91")).
				Padding(0, 1).
				Bold(true)
	// ChipDangerStyle marks a destructive action (red surface).
	ChipDangerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#7A1F1F")).
			Padding(0, 1).
			Bold(true)

	// SectionRule draws the thin horizontal separator between agent rows.
	// Its foreground matches PanelBorder so the chrome reads consistently.
	SectionRule = lipgloss.NewStyle().Foreground(PanelBorder)
)
