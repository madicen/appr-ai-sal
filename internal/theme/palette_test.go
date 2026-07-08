package theme

import (
	"regexp"
	"testing"
)

var hexRE = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// priorDarkHexes freezes the exact colours the pre-refactor TUI hardcoded, so
// the dark preset can be proven visually equivalent to the old chrome. Every
// entry here was a literal hex (or the Dark side of an AdaptiveColor) somewhere
// in internal/tui before Phase 5 item 7 routed them through the palette. If a
// value changes, the default-dark appearance changed — that must be
// deliberate, so update this table consciously.
var priorDarkHexes = map[Role]string{
	RoleSurface:       "#30363d", // ModalButton / neutral chip background
	RoleOnSurface:     "#FFFFFF", // white text on chips
	RoleAccent:        "#5D2D91", // header bar / primary chip / modal border
	RoleOnAccent:      "#FFFFFF", // white text on the accent bar
	RoleMuted:         "#888888", // DimStyle / StatusBar / DimColor
	RoleSubtle:        "#6E6E6E", // idle input prompt gutter (dark side)
	RoleBorder:        "#9A9A9A", // PanelBorder / section labels (dark side)
	RoleBorderAccent:  "#58A6FF", // PanelBorderAccent / focus (dark side)
	RoleSelectionBg:   "#3D4F5F", // file-tree selection background
	RoleSelectionFg:   "#FFFFFF", // file-tree selection foreground
	RoleTitleFg:       "#BBBBBB", // detail pane title / inactive chip (dark side)
	RoleTitleBg:       "#2C2C2C", // detail pane title background (dark side)
	RoleInfo:          "#7AA2F7", // sev info / needs-you / busy chip
	RoleSuccess:       "#9ECE6A", // OkStyle / OkColor
	RoleWarning:       "#E0AF68", // WarnStyle / WarnColor / sev warning
	RoleError:         "#F7768E", // ErrStyle / sev error
	RoleCritical:      "#FF5555", // sev critical
	RoleDanger:        "#7A1F1F", // ChipDanger surface
	RoleDiffAddedBg:   "#173027", // diff added-row tint
	RoleDiffRemovedBg: "#2C1A1F", // diff removed-row tint
}

// priorAdaptivePairs freezes the light sides of the two AdaptiveColors that
// existed before this change (plus the pane-title / input-gutter pairs added by
// earlier Phase-5 groups) so the light preset preserves the colours the app
// already adapted to on light terminals.
var priorLightSides = map[Role]string{
	RoleBorder:       "#666666", // PanelBorder light side
	RoleBorderAccent: "#1F6FEB", // PanelBorderAccent light side
	RoleTitleFg:      "#555555", // detail pane title light side
	RoleTitleBg:      "#E8E8E8", // detail pane title background light side
	RoleSubtle:       "#9A9A9A", // idle input prompt light side
}

func TestDarkPresetEqualsPriorHexes(t *testing.T) {
	d := Dark()
	for role, want := range priorDarkHexes {
		if got := d.Hex(role); !equalHex(got, want) {
			t.Errorf("dark preset for %s = %q, want %q (default-dark appearance changed!)", role, got, want)
		}
	}
}

func TestLightPresetPreservesPriorAdaptiveLightSides(t *testing.T) {
	l := Light()
	for role, want := range priorLightSides {
		if got := l.Hex(role); !equalHex(got, want) {
			t.Errorf("light preset for %s = %q, want %q (previously-adaptive light colour changed)", role, got, want)
		}
	}
}

func TestEveryRoleHasValidHexInBothPresets(t *testing.T) {
	for _, r := range Roles() {
		for _, p := range Presets() {
			got := p.Hex(r)
			if !hexRE.MatchString(got) {
				t.Errorf("%s preset role %s = %q, not a valid #rrggbb hex", p.Name(), r, got)
			}
		}
	}
}

func TestLightPresetDiffersFromDark(t *testing.T) {
	d, l := Dark(), Light()
	// Structural roles must adapt for a light preset to be meaningful.
	mustDiffer := []Role{
		RoleSurface, RoleOnSurface, RoleMuted, RoleBorder, RoleBorderAccent,
		RoleSelectionBg, RoleTitleFg, RoleTitleBg,
		RoleSuccess, RoleWarning, RoleError, RoleCritical,
		RoleDiffAddedBg, RoleDiffRemovedBg,
	}
	for _, r := range mustDiffer {
		if equalHex(d.Hex(r), l.Hex(r)) {
			t.Errorf("light and dark presets share %s = %q; light preset should differ here", r, d.Hex(r))
		}
	}
	// Count overall differences to guard against an accidental clone.
	diff := 0
	for _, r := range Roles() {
		if !equalHex(d.Hex(r), l.Hex(r)) {
			diff++
		}
	}
	if diff < len(mustDiffer) {
		t.Errorf("only %d/%d roles differ between light and dark; expected the light preset to be a substantial data change", diff, int(numRoles))
	}
}

func TestAdaptiveZipsLightAndDark(t *testing.T) {
	for _, r := range Roles() {
		a := Adaptive(r)
		if a.Light != Light().Hex(r) {
			t.Errorf("Adaptive(%s).Light = %q, want %q", r, a.Light, Light().Hex(r))
		}
		if a.Dark != Dark().Hex(r) {
			t.Errorf("Adaptive(%s).Dark = %q, want %q", r, a.Dark, Dark().Hex(r))
		}
	}
}

func TestSeverityDefaultsDeriveFromPalette(t *testing.T) {
	cases := []struct {
		key  Key
		role Role
	}{
		{KeySevInfo, RoleInfo},
		{KeySevWarning, RoleWarning},
		{KeySevError, RoleError},
		{KeySevCritical, RoleCritical},
	}
	for _, c := range cases {
		if got, want := DefaultColor(c.key), Dark().Hex(c.role); !equalHex(got, want) {
			t.Errorf("severity default %s = %q, want palette dark role %s = %q", c.key, got, c.role, want)
		}
	}
}

func TestRoleStringIsStableAndUnique(t *testing.T) {
	seen := map[string]Role{}
	for _, r := range Roles() {
		name := r.String()
		if name == "" {
			t.Errorf("role %d has empty String()", int(r))
			continue
		}
		if prev, ok := seen[name]; ok {
			t.Errorf("role name %q used by both %d and %d", name, int(prev), int(r))
		}
		seen[name] = r
	}
	if Role(-1).String() != "" || Role(numRoles).String() != "" {
		t.Errorf("out-of-range roles should stringify to empty")
	}
}

func equalHex(a, b string) bool {
	return normalizeHex(a) == normalizeHex(b)
}
