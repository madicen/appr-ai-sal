// Package styles owns every lipgloss style used by the root TUI, the
// tabs, and the overlay stack. All chrome colours are sourced from the
// semantic palette in internal/theme (theme.Adaptive(role)) so a single
// source drives the whole app: user customisation of the tag/severity
// slots takes effect live, and the light/dark/NO_COLOR appearance chosen
// at startup is honoured because lipgloss resolves the AdaptiveColors these
// styles carry at render time.
package styles

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/madicen/appr-ai-sal/internal/theme"
)

var (
	// AppPadding is the standard 1-cell horizontal padding applied to most
	// body content so the active text never butts against the terminal
	// edge.
	AppPadding = lipgloss.NewStyle().Padding(0, 1)

	// HeaderBar is the full-width accent bar at the top of every screen.
	HeaderBar = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.Adaptive(theme.RoleOnAccent)).
			Background(theme.Adaptive(theme.RoleAccent)).
			Padding(0, 1)

	// StatusBar is the dim help/footer line at the bottom of the screen.
	StatusBar = lipgloss.NewStyle().
			Foreground(theme.Adaptive(theme.RoleMuted)).
			Padding(0, 1)

	// PanelBorder is the colour for two-pane layouts; high enough
	// contrast on both light and dark backgrounds to read as a real box.
	PanelBorder = theme.Adaptive(theme.RoleBorder)

	// PanelBorderAccent highlights the panes flanking an in-flight pane
	// seam drag. Reads as "active" against PanelBorder while keeping the
	// pane chrome consistent with the rest of the app.
	PanelBorderAccent = theme.Adaptive(theme.RoleBorderAccent)

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
				Foreground(theme.Adaptive(theme.RoleTitleFg)).
				Background(theme.Adaptive(theme.RoleTitleBg)).
				Padding(0, 0)

	// ModalButtonStyle styles secondary modal actions (Copy, OK, etc.) in
	// a jj-tui-style filled chip.
	ModalButtonStyle = lipgloss.NewStyle().
				Foreground(theme.Adaptive(theme.RoleOnSurface)).
				Background(theme.Adaptive(theme.RoleSurface)).
				Padding(0, 1).
				Bold(true)

	// Chip styles are the filled action pills shared by the agent-management
	// tabs (repo-agents, and — via aliases — settings). Centralising them
	// here keeps the palette in one place instead of copied hex per tab.
	//
	// ChipStyle is the neutral chip (same surface as ModalButtonStyle).
	ChipStyle = ModalButtonStyle
	// ChipBusyStyle marks an in-flight action (info/blue surface).
	ChipBusyStyle = lipgloss.NewStyle().
			Foreground(theme.Adaptive(theme.RoleOnAccent)).
			Background(theme.Adaptive(theme.RoleInfo)).
			Padding(0, 1).
			Bold(true)
	// ChipPrimaryStyle marks the primary action (accent surface, matching
	// the header bar).
	ChipPrimaryStyle = lipgloss.NewStyle().
				Foreground(theme.Adaptive(theme.RoleOnAccent)).
				Background(theme.Adaptive(theme.RoleAccent)).
				Padding(0, 1).
				Bold(true)
	// ChipDangerStyle marks a destructive action (danger surface).
	ChipDangerStyle = lipgloss.NewStyle().
			Foreground(theme.Adaptive(theme.RoleOnAccent)).
			Background(theme.Adaptive(theme.RoleDanger)).
			Padding(0, 1).
			Bold(true)

	// SectionRule draws the thin horizontal separator between agent rows.
	// Its foreground matches PanelBorder so the chrome reads consistently.
	SectionRule = lipgloss.NewStyle().Foreground(PanelBorder)
)
