package util

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// BubbleZoneCSI matches lrstanley/bubblezone private CSI markers (\x1b[Nz).
// Lines containing markers must be skipped by the wrap helpers below; the
// markers are width-zero but ansi.Wrap and ansi.Truncate would still split
// them and corrupt zone hit-testing.
var BubbleZoneCSI = regexp.MustCompile("\x1b\\[\\d+z")

// EnforceMaxLineWidth trims any output line that still exceeds the
// viewport after ansi.Wrap so bubbles/viewport's lipgloss pass does not
// insert extra wraps (which desynchronises bubblezone hit boxes). Lines
// containing bubblezone markers are skipped — those rows must be pre-sized
// before zone.Mark.
func EnforceMaxLineWidth(s string, maxCols int) string {
	if maxCols < 8 {
		maxCols = 8
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if BubbleZoneCSI.MatchString(line) {
			continue
		}
		if ansi.StringWidth(line) > maxCols {
			lines[i] = ansi.Truncate(line, maxCols, "")
		}
	}
	return strings.Join(lines, "\n")
}

// ViewportLineCount returns how many newline-separated rows s occupies
// when rendered in a viewport (empty string → 0).
func ViewportLineCount(s string) int {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// HardWrapOverflowLines splits lines that are still wider than maxCols
// after ansi.Wrap (e.g. long URLs or unbroken tokens). Skips lines
// containing bubblezone markers — those rows must be pre-sized before
// zone.Mark.
func HardWrapOverflowLines(s string, maxCols int) string {
	if maxCols < 8 {
		maxCols = 8
	}
	lines := strings.Split(s, "\n")
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if BubbleZoneCSI.MatchString(line) {
			b.WriteString(line)
			continue
		}
		if ansi.StringWidth(line) <= maxCols {
			b.WriteString(line)
			continue
		}
		b.WriteString(ansi.Hardwrap(line, maxCols, false))
	}
	return b.String()
}

// Clamp constrains v to [lo, hi]. Used pervasively by the overlay
// constructors to size viewports without overshooting the terminal in
// either direction.
func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// WrapForViewport hard-wraps styled text to the viewport content width so
// the terminal does not soft-wrap (which breaks line-based scrolling and
// clipping).
func WrapForViewport(s string, contentCols int) string {
	if contentCols < 8 {
		contentCols = 8
	}
	if s == "" {
		return s
	}
	wrapped := ansi.Wrap(s, contentCols, " /.,;:[]{}()=_`\"'+|&")
	wrapped = HardWrapOverflowLines(wrapped, contentCols)
	return EnforceMaxLineWidth(wrapped, contentCols)
}
