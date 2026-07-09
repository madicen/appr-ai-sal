// Package theme owns the user-configurable colour palette used by the TUI.
//
// The palette is split into two groups:
//
//   - Tag colours (one per row label rendered in the running view: the five
//     code-review specialists, the vibe-coach, the four context-injection
//     rows, the repo arbiter, and the four PR agents).
//   - Severity colours (one per finding severity in the comment list).
//
// Defaults match the historical hardcoded palette in internal/tui/styles.go
// so a fresh install renders identically. Users can override any subset via
// the Theme settings subtab; overrides are persisted as JSON under the same
// config dir as aiconfig and repoconfig (~/.config/appr-ai-sal/theme.json by
// default, overridable with APPR_AI_SAL_CONFIG_DIR).
package theme

import "sync"

// Key identifies a single configurable colour slot. Stable string values are
// used as JSON keys so the on-disk file is human-readable.
type Key string

const (
	// Specialist tag colours (rendered as a coloured pill next to comments).
	KeyTagFormatting Key = "tag_formatting"
	KeyTagDesign     Key = "tag_design"
	KeyTagTesting    Key = "tag_testing"
	KeyTagDocs       Key = "tag_docs"
	KeyTagSecurity   Key = "tag_security"
	KeyTagTech       Key = "tag_tech"
	KeyTagVibeCoach  Key = "tag_vibe_coach"

	// Context-injection row tags (shown above specialists in the running view).
	KeyTagLangBriefs  Key = "tag_lang_briefs"
	KeyTagTechExperts Key = "tag_tech_experts"
	KeyTagRepoExperts Key = "tag_repo_experts"
	KeyTagRepoArbiter Key = "tag_repo_arbiter"

	// PR-agent tags (whole-PR review group shown after the specialists).
	KeyTagDescription Key = "tag_description"
	KeyTagChecks      Key = "tag_checks"
	KeyTagDiscussion  Key = "tag_discussion"
	KeyTagScope       Key = "tag_scope"

	// Severity colours (foreground for finding lines).
	KeySevInfo     Key = "sev_info"
	KeySevWarning  Key = "sev_warning"
	KeySevError    Key = "sev_error"
	KeySevCritical Key = "sev_critical"
)

// Slot describes a configurable colour, including the user-facing label and
// a short hint shown next to the swatch.
type Slot struct {
	Key   Key
	Label string
	Hint  string
}

// Slots returns every configurable colour, in the order they should appear
// in the Theme settings panel. Group ordering (specialists, then context
// injection, then severities) matches the running view's vertical stacking.
func Slots() []Slot {
	return []Slot{
		// Specialists — saturated palette consistent with the running view.
		{KeyTagFormatting, "formatting", "code-review formatting specialist"},
		{KeyTagDesign, "design", "code-review design specialist"},
		{KeyTagTesting, "testing", "code-review testing specialist"},
		{KeyTagDocs, "docs", "code-review documentation specialist"},
		{KeyTagSecurity, "security", "code-review security specialist"},
		{KeyTagTech, "tech", "code-review technology-conventions specialist"},
		{KeyTagVibeCoach, "vibe-coach", "post-arbiter vibe-coach pass"},

		// Context injection — supporting rows shown above specialists.
		{KeyTagLangBriefs, "language briefs", "language convention digest row"},
		{KeyTagTechExperts, "tech experts", "technology brief row"},
		{KeyTagRepoExperts, "repo experts", "repo-agent brief row"},
		{KeyTagRepoArbiter, "repo arbiter", "post-specialist arbiter row"},

		// PR agents — whole-PR review rows shown after the specialists.
		{KeyTagDescription, "description", "PR description agent"},
		{KeyTagChecks, "checks", "PR CI-checks agent"},
		{KeyTagDiscussion, "discussion", "PR discussion agent"},
		{KeyTagScope, "scope", "PR scope agent"},

		// Severities — foreground colours for the inline finding list.
		{KeySevInfo, "info", "lowest-severity findings"},
		{KeySevWarning, "warning", "medium-severity findings"},
		{KeySevError, "error", "high-severity findings"},
		{KeySevCritical, "critical", "merge-blocking findings"},
	}
}

// Theme is a snapshot of every configurable colour. Values are hex strings
// (e.g. "#7AA2F7"); empty strings fall back to the default for that key when
// resolved via Color().
//
// Mode selects the appearance preset (dark|light|auto) applied at startup; it
// is persisted alongside the colour overrides so a user who prefers the light
// preset keeps it across sessions. Empty means "unset" — the default (dark)
// applies unless the APPR_AI_SAL_THEME env var overrides it.
type Theme struct {
	Colors map[Key]string `json:"colors,omitempty"`
	Mode   string         `json:"mode,omitempty"`
}

// Default returns the historical hardcoded palette. Mutating the returned
// map does not affect future calls.
func Default() *Theme {
	return &Theme{Colors: defaultColors()}
}

func defaultColors() map[Key]string {
	return map[Key]string{
		// Specialists.
		KeyTagFormatting: "#7AA2F7", // blue
		KeyTagDesign:     "#BB9AF7", // purple
		KeyTagTesting:    "#9ECE6A", // green
		KeyTagDocs:       "#E0AF68", // yellow
		KeyTagSecurity:   "#F7768E", // red
		KeyTagTech:       "#2AC3DE", // sky / electric teal
		KeyTagVibeCoach:  "#7DCFFF", // cyan

		// Context injection.
		KeyTagLangBriefs:  "#7BC5CC", // pastel teal
		KeyTagTechExperts: "#ECB088", // pastel peach
		KeyTagRepoExperts: "#E5A1B5", // pastel rose
		KeyTagRepoArbiter: "#A8B5DC", // pastel lavender

		// PR agents — a distinct saturated quartet (teal / orange / violet /
		// gold) so the whole-PR group reads apart from the specialists above.
		KeyTagDescription: "#73DACA", // teal
		KeyTagChecks:      "#FF9E64", // orange
		KeyTagDiscussion:  "#C792EA", // violet
		KeyTagScope:       "#FFC777", // gold

		// Severities — derived from the semantic palette's status roles so
		// the severity list and the rest of the chrome share one source. The
		// dark-preset values equal the historical severity hexes, so a fresh
		// install renders identically.
		KeySevInfo:     darkPalette.Hex(RoleInfo),
		KeySevWarning:  darkPalette.Hex(RoleWarning),
		KeySevError:    darkPalette.Hex(RoleError),
		KeySevCritical: darkPalette.Hex(RoleCritical),
	}
}

// DefaultColor returns the built-in colour for k regardless of any active
// override. Useful for "reset" buttons and for tests that need to assert the
// shipping palette is unchanged.
func DefaultColor(k Key) string {
	return defaultColors()[k]
}

// Clone returns a deep copy so the caller can mutate freely without
// affecting the source.
func (t *Theme) Clone() *Theme {
	if t == nil {
		return Default()
	}
	out := &Theme{Colors: make(map[Key]string, len(t.Colors)), Mode: t.Mode}
	for k, v := range t.Colors {
		out.Colors[k] = v
	}
	return out
}

// AppearanceMode returns the parsed appearance mode for this theme, defaulting
// to dark when unset.
func (t *Theme) AppearanceMode() Mode {
	if t == nil {
		return ModeDark
	}
	return ParseMode(t.Mode)
}

// Color resolves k against the theme, falling back to the built-in default
// when the override is empty or missing. Unknown keys return an empty string
// so callers can detect bugs.
func (t *Theme) Color(k Key) string {
	if t != nil {
		if v, ok := t.Colors[k]; ok && v != "" {
			return v
		}
	}
	if v, ok := defaultColors()[k]; ok {
		return v
	}
	return ""
}

// Set stores hex against k after lightweight validation; invalid hex values
// (anything that does not look like #rgb / #rrggbb) are silently ignored so
// callers do not need to guard against bubble-color-picker round-trips.
func (t *Theme) Set(k Key, hex string) {
	if !validHex(hex) {
		return
	}
	if t.Colors == nil {
		t.Colors = map[Key]string{}
	}
	t.Colors[k] = normalizeHex(hex)
}

// Reset removes any override for k so future reads fall back to the default.
func (t *Theme) Reset(k Key) {
	if t == nil || t.Colors == nil {
		return
	}
	delete(t.Colors, k)
}

// Equal reports whether two themes resolve to the same colour for every
// known slot. Empty / missing entries are treated as their default value.
func (t *Theme) Equal(other *Theme) bool {
	for _, s := range Slots() {
		if t.Color(s.Key) != other.Color(s.Key) {
			return false
		}
	}
	return true
}

var (
	currentMu sync.RWMutex
	current   = Default()
)

// Current returns the active theme. The returned pointer is a defensive copy
// — callers may mutate it without holding the package lock.
func Current() *Theme {
	currentMu.RLock()
	defer currentMu.RUnlock()
	return current.Clone()
}

// Color is a convenience for theme.Current().Color(k); call it from hot
// paths (renderTag, renderSeverity) to avoid a per-call clone.
func Color(k Key) string {
	currentMu.RLock()
	defer currentMu.RUnlock()
	return current.Color(k)
}

// Apply replaces the active theme with t. Pass nil to reset to defaults.
func Apply(t *Theme) {
	currentMu.Lock()
	defer currentMu.Unlock()
	if t == nil {
		current = Default()
		return
	}
	current = t.Clone()
}

func validHex(s string) bool {
	if len(s) != 4 && len(s) != 7 {
		return false
	}
	if s[0] != '#' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func normalizeHex(s string) string {
	if len(s) == 4 {
		s = "#" + string([]byte{s[1], s[1], s[2], s[2], s[3], s[3]})
	}
	out := []byte(s)
	for i := 1; i < len(out); i++ {
		c := out[i]
		if c >= 'A' && c <= 'F' {
			out[i] = c + ('a' - 'A')
		}
	}
	return string(out)
}
