package theme

import "github.com/charmbracelet/lipgloss"

// palette.go adds the semantic colour model that drives all TUI chrome
// (Phase 5 item 7). Where theme.Theme (theme.go) owns the user-customisable
// per-row tag and per-severity slots, the Palette owns the structural roles
// every screen shares: background, foreground, surfaces, the brand accent,
// borders, selection, and the semantic status colours.
//
// A Palette is a data table of Role → light/dark hex pair. Two presets ship
// (Dark and Light); adding another appearance is a data change, never a code
// change. internal/tui/styles builds its lipgloss.Style values from
// theme.Adaptive(role) so the whole app draws from one source instead of the
// dozens of hardcoded hexes that used to be scattered across the TUI.
//
// Light/dark selection and the NO_COLOR degraded mode are handled in
// appearance.go by driving the global lipgloss renderer (SetHasDarkBackground
// / SetColorProfile(Ascii)); the palette itself just supplies the two-sided
// colour data, so a package-level style built once at init still adapts at
// render time.

// Role identifies a single semantic colour slot shared across the TUI chrome.
type Role int

const (
	// RoleBg / RoleFg are the base background and foreground. They are defined
	// for completeness (and used for contrast in a few surfaces); the root view
	// deliberately does not paint a full background so the terminal's own
	// backdrop shows through, matching the historical look.
	RoleBg Role = iota
	RoleFg

	// RoleSurface is a raised neutral surface (chips, buttons); RoleOnSurface is
	// the text drawn on it.
	RoleSurface
	RoleOnSurface

	// RoleAccent is the brand accent (the purple header/primary bar);
	// RoleOnAccent is the text drawn on any saturated accent surface.
	RoleAccent
	RoleOnAccent

	// RoleMuted is dim ancillary text (hints, timestamps, the status bar).
	// RoleSubtle is the dimmest tier (idle input gutters).
	RoleMuted
	RoleSubtle

	// RoleBorder is the standard panel border; RoleBorderAccent marks a focused
	// / active border (and doubles as the focus accent for labels and prompts).
	RoleBorder
	RoleBorderAccent

	// RoleSelectionBg / RoleSelectionFg style the highlighted row in list-like
	// panes (the file tree, the overview list).
	RoleSelectionBg
	RoleSelectionFg

	// RoleTitleFg / RoleTitleBg style the one-line pane-title strips (and the
	// secondary-text chips that share their tone).
	RoleTitleFg
	RoleTitleBg

	// Semantic status colours. RoleInfo doubles as the "in flight" / "needs you"
	// blue. These are the source of the severity slot defaults in theme.go.
	RoleInfo
	RoleSuccess
	RoleWarning
	RoleError
	RoleCritical

	// RoleDanger is a destructive-action surface (a filled red chip), distinct
	// from RoleError which is a foreground severity colour.
	RoleDanger

	// Diff row background tints for added / removed lines.
	RoleDiffAddedBg
	RoleDiffRemovedBg

	numRoles
)

// roleNames maps each role to a stable, human-readable name for diagnostics
// and the no-stray-hex audit test. Kept in sync with the constants above.
var roleNames = [numRoles]string{
	RoleBg:            "bg",
	RoleFg:            "fg",
	RoleSurface:       "surface",
	RoleOnSurface:     "on_surface",
	RoleAccent:        "accent",
	RoleOnAccent:      "on_accent",
	RoleMuted:         "muted",
	RoleSubtle:        "subtle",
	RoleBorder:        "border",
	RoleBorderAccent:  "border_accent",
	RoleSelectionBg:   "selection_bg",
	RoleSelectionFg:   "selection_fg",
	RoleTitleFg:       "title_fg",
	RoleTitleBg:       "title_bg",
	RoleInfo:          "info",
	RoleSuccess:       "success",
	RoleWarning:       "warning",
	RoleError:         "error",
	RoleCritical:      "critical",
	RoleDanger:        "danger",
	RoleDiffAddedBg:   "diff_added_bg",
	RoleDiffRemovedBg: "diff_removed_bg",
}

// String returns the stable name of a role ("" for out-of-range values).
func (r Role) String() string {
	if r < 0 || int(r) >= len(roleNames) {
		return ""
	}
	return roleNames[r]
}

// Roles returns every semantic role in declaration order. Handy for tests and
// for any UI that wants to enumerate the palette.
func Roles() []Role {
	out := make([]Role, 0, numRoles)
	for r := Role(0); r < numRoles; r++ {
		out = append(out, r)
	}
	return out
}

// Palette is a data table mapping every Role to a concrete hex colour. A
// palette carries a name and one hex per role; the light/dark split lives
// across two palettes (Dark and Light) which are zipped into
// lipgloss.AdaptiveColor by Adaptive.
type Palette struct {
	name string
	hex  [numRoles]string
}

// Name returns the palette's identifier ("dark" / "light").
func (p *Palette) Name() string {
	if p == nil {
		return ""
	}
	return p.name
}

// Hex returns the raw hex string for role r (empty for an out-of-range role).
func (p *Palette) Hex(r Role) string {
	if p == nil || r < 0 || int(r) >= len(p.hex) {
		return ""
	}
	return p.hex[r]
}

// darkPalette is the shipping default. Its values are exactly the historical
// hardcoded hexes that used to live in internal/tui/styles and the model /
// tabs packages, so a dark terminal renders byte-for-byte identically to the
// pre-refactor UI. Do not "improve" these casually: the dark-equivalence test
// (palette_test.go) asserts every entry against a frozen table.
var darkPalette = &Palette{
	name: "dark",
	hex: [numRoles]string{
		RoleBg:            "#1A1B26",
		RoleFg:            "#C0CAF5",
		RoleSurface:       "#30363d",
		RoleOnSurface:     "#FFFFFF",
		RoleAccent:        "#5D2D91",
		RoleOnAccent:      "#FFFFFF",
		RoleMuted:         "#888888",
		RoleSubtle:        "#6E6E6E",
		RoleBorder:        "#9A9A9A",
		RoleBorderAccent:  "#58A6FF",
		RoleSelectionBg:   "#3D4F5F",
		RoleSelectionFg:   "#FFFFFF",
		RoleTitleFg:       "#BBBBBB",
		RoleTitleBg:       "#2C2C2C",
		RoleInfo:          "#7AA2F7",
		RoleSuccess:       "#9ECE6A",
		RoleWarning:       "#E0AF68",
		RoleError:         "#F7768E",
		RoleCritical:      "#FF5555",
		RoleDanger:        "#7A1F1F",
		RoleDiffAddedBg:   "#173027",
		RoleDiffRemovedBg: "#2C1A1F",
	},
}

// lightPalette is the light-terminal preset. Structural roles flip to
// light-appropriate tones (light surfaces, dark text, deeper status colours
// for contrast on white); the brand accent stays purple so the app keeps its
// identity. These values are what lipgloss renders when the terminal reports a
// light background (auto mode) or when the user forces light mode.
var lightPalette = &Palette{
	name: "light",
	hex: [numRoles]string{
		RoleBg:            "#FFFFFF",
		RoleFg:            "#24292F",
		RoleSurface:       "#D0D7DE",
		RoleOnSurface:     "#24292F",
		RoleAccent:        "#5D2D91",
		RoleOnAccent:      "#FFFFFF",
		RoleMuted:         "#666666",
		RoleSubtle:        "#9A9A9A",
		RoleBorder:        "#666666",
		RoleBorderAccent:  "#1F6FEB",
		RoleSelectionBg:   "#CCE0FF",
		RoleSelectionFg:   "#24292F",
		RoleTitleFg:       "#555555",
		RoleTitleBg:       "#E8E8E8",
		RoleInfo:          "#1F6FEB",
		RoleSuccess:       "#2A7A2A",
		RoleWarning:       "#B7791F",
		RoleError:         "#C4314B",
		RoleCritical:      "#D63031",
		RoleDanger:        "#B01515",
		RoleDiffAddedBg:   "#E6FFED",
		RoleDiffRemovedBg: "#FFEEF0",
	},
}

// Dark returns the dark preset. The returned pointer is shared and must not be
// mutated.
func Dark() *Palette { return darkPalette }

// Light returns the light preset. The returned pointer is shared and must not
// be mutated.
func Light() *Palette { return lightPalette }

// Presets returns every shipped preset, newest-neutral order first. Adding a
// preset here (plus its data table) is all it takes to expose a new
// appearance.
func Presets() []*Palette { return []*Palette{darkPalette, lightPalette} }

// PaletteByName returns the preset with the given name, or nil when unknown.
func PaletteByName(name string) *Palette {
	for _, p := range Presets() {
		if p.name == name {
			return p
		}
	}
	return nil
}

// Adaptive returns role r as a lipgloss.AdaptiveColor zipping the light and
// dark presets. Because lipgloss resolves an AdaptiveColor at render time
// (consulting the renderer's dark-background flag), a style built once at
// package-init from Adaptive still honours a mode chosen later by
// ApplyAppearance — and collapses to no colour when the renderer's profile is
// forced to Ascii for NO_COLOR.
func Adaptive(r Role) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{
		Light: lightPalette.Hex(r),
		Dark:  darkPalette.Hex(r),
	}
}
