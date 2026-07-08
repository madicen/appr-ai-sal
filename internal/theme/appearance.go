package theme

import (
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// appearance.go owns light/dark selection and the NO_COLOR degraded mode
// (Phase 5 item 7). It is the one place that touches the global lipgloss
// renderer so the palette data (palette.go) stays pure.
//
// Selection precedence (highest wins):
//
//  1. NO_COLOR env (https://no-color.org/) → monochrome, overrides everything.
//  2. APPR_AI_SAL_THEME env (dark|light|auto|none).
//  3. The persisted theme mode (theme.json "mode").
//  4. The built-in default: dark.

// Mode selects how the palette's light/dark pairs resolve to concrete colours.
type Mode int

const (
	// ModeDark forces the dark preset regardless of the terminal background.
	// This is the default so the app looks the same as it always has (the
	// pre-refactor UI was overwhelmingly fixed dark).
	ModeDark Mode = iota
	// ModeLight forces the light preset.
	ModeLight
	// ModeAuto defers to lipgloss's own terminal-background detection so
	// AdaptiveColors pick the side matching the user's terminal.
	ModeAuto
)

// String returns the stable token for a mode ("dark"/"light"/"auto"), matching
// the values accepted by ParseMode and persisted in theme.json.
func (m Mode) String() string {
	switch m {
	case ModeLight:
		return "light"
	case ModeAuto:
		return "auto"
	default:
		return "dark"
	}
}

// ParseMode maps a persisted / env string to a Mode, defaulting to dark for
// empty or unknown values (the historical appearance).
func ParseMode(s string) Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "light":
		return ModeLight
	case "auto", "adaptive", "system":
		return ModeAuto
	default:
		return ModeDark
	}
}

// Appearance is the resolved rendering decision: which preset to use and
// whether colour is disabled entirely.
type Appearance struct {
	Mode    Mode
	NoColor bool
}

// Palette returns the concrete preset this appearance renders with. NoColor is
// orthogonal — it strips colour at the renderer level — so a NoColor
// appearance still reports its underlying (dark/light) preset here.
func (a Appearance) Palette() *Palette {
	if a.Mode == ModeLight {
		return lightPalette
	}
	return darkPalette
}

var (
	appearanceMu     sync.RWMutex
	activeAppearance = Appearance{Mode: ModeDark}
)

// noColorEnv reports whether NO_COLOR is present in the environment. Per the
// spec the variable disables colour when set to any value (including empty),
// so a mere presence check is used — matching the diffview highlighter's gate
// so chrome and syntax highlighting agree.
func noColorEnv() bool {
	_, ok := os.LookupEnv("NO_COLOR")
	return ok
}

// DetectAppearance resolves the effective appearance from the environment,
// layered on top of a persisted mode (pass ModeDark when there is no saved
// preference). NO_COLOR wins over any mode selection.
func DetectAppearance(saved Mode) Appearance {
	a := Appearance{Mode: saved}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APPR_AI_SAL_THEME"))) {
	case "dark":
		a.Mode = ModeDark
	case "light":
		a.Mode = ModeLight
	case "auto", "adaptive", "system":
		a.Mode = ModeAuto
	case "none", "mono", "no-color", "nocolor":
		a.NoColor = true
	}
	if noColorEnv() {
		a.NoColor = true
	}
	return a
}

// ApplyAppearance enforces a on the global lipgloss renderer and records it as
// the active appearance. It is the single mutation point for the renderer's
// colour behaviour:
//
//   - NoColor → force the Ascii profile so every lipgloss.Style renders with no
//     ANSI colour (the chroma diff highlighter is disabled separately, via its
//     own NO_COLOR gate, so syntax colour vanishes too).
//   - ModeDark / ModeLight → pin the dark-background flag so AdaptiveColors pick
//     the forced side.
//   - ModeAuto → leave lipgloss's terminal detection untouched.
func ApplyAppearance(a Appearance) {
	appearanceMu.Lock()
	activeAppearance = a
	appearanceMu.Unlock()

	if a.NoColor {
		lipgloss.SetColorProfile(termenv.Ascii)
		return
	}
	switch a.Mode {
	case ModeDark:
		lipgloss.SetHasDarkBackground(true)
	case ModeLight:
		lipgloss.SetHasDarkBackground(false)
	case ModeAuto:
		// Defer to lipgloss's own background detection.
	}
}

// SetupRendering is the convenience entry point for main / demo: detect the
// appearance from env layered over the persisted theme mode and apply it. It
// returns the resolved appearance for logging.
func SetupRendering(savedMode Mode) Appearance {
	a := DetectAppearance(savedMode)
	ApplyAppearance(a)
	return a
}

// ActiveAppearance returns the appearance most recently applied.
func ActiveAppearance() Appearance {
	appearanceMu.RLock()
	defer appearanceMu.RUnlock()
	return activeAppearance
}

// NoColor reports whether colour output is currently disabled.
func NoColor() bool {
	return ActiveAppearance().NoColor
}
