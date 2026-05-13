package settings

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	bubblepicker "github.com/madicen/bubble-color-picker"

	"github.com/madicen/appr-ai-sal/internal/theme"
)

// themePanel owns the state for the Theme settings subtab. Swatches mirror
// theme.Slots() in display order; row indices below are populated as the
// panel string is assembled so the bubble-color-picker overlay can be
// centred on the correct line.
type themePanel struct {
	draft    *theme.Theme
	swatches []*themeSwatch
	focus    int
}

// themeSwatch couples a SwatchPicker with the theme.Key it edits and the
// row offset (within the rendered panel) used for overlay positioning.
type themeSwatch struct {
	key     theme.Key
	label   string
	hint    string
	swatch  *bubblepicker.SwatchPicker
	row     int
	col     int
	zoneID  string
}

// themeGroupHeader returns a printable group label for slot k. Three groups
// (specialists, context-injection rows, severities) match the running view.
func themeGroupHeader(k theme.Key) string {
	switch k {
	case theme.KeyTagFormatting:
		return "Specialists"
	case theme.KeyTagLangBriefs:
		return "Context injection"
	case theme.KeySevInfo:
		return "Severities"
	default:
		return ""
	}
}

func newThemePanel() *themePanel {
	draft := theme.Current()
	slots := theme.Slots()
	p := &themePanel{
		draft:    draft,
		swatches: make([]*themeSwatch, 0, len(slots)),
		focus:    0,
	}
	for _, s := range slots {
		sw := bubblepicker.NewSwatchPicker(draft.Color(s.Key), s.Label)
		sw.SetZoneManager(zone.DefaultManager)
		// Auto-dismiss so picking a colour closes the modal immediately;
		// the host still receives ColorChangedMsg to persist the value.
		sw.SetPickerOptions(bubblepicker.WithAutoDismiss(true))
		p.swatches = append(p.swatches, &themeSwatch{
			key:    s.Key,
			label:  s.Label,
			hint:   s.Hint,
			swatch: sw,
			zoneID: themeSwatchZoneID(s.Key),
		})
	}
	if len(p.swatches) > 0 {
		p.swatches[0].swatch.SetFocused(true)
	}
	return p
}

// openSwatchIndex returns the index of the open swatch, or -1 if none are
// open. At most one swatch can be open at a time.
func (p *themePanel) openSwatchIndex() int {
	for i, sw := range p.swatches {
		if sw.swatch.Open() {
			return i
		}
	}
	return -1
}

// indexForKey returns the swatch index for k, or -1 if k is unknown.
func (p *themePanel) indexForKey(k theme.Key) int {
	for i, sw := range p.swatches {
		if sw.key == k {
			return i
		}
	}
	return -1
}

// advanceFocus moves the focused swatch by delta (wrapping). The focus
// indicator is the SwatchPicker's bright arrow when SetFocused(true).
func (p *themePanel) advanceFocus(delta int) {
	if len(p.swatches) == 0 {
		return
	}
	p.swatches[p.focus].swatch.SetFocused(false)
	p.focus = (p.focus + delta + len(p.swatches)) % len(p.swatches)
	p.swatches[p.focus].swatch.SetFocused(true)
}

// openFocused triggers the focused swatch as if the user clicked it. The
// SwatchPicker keys off a left-button press, so we synthesise one. The
// caller must then forward subsequent input to the open swatch.
func (p *themePanel) openFocused() tea.Cmd {
	if len(p.swatches) == 0 {
		return nil
	}
	sw := p.swatches[p.focus]
	openPress := tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	updated, cmd := sw.swatch.Update(openPress)
	sw.swatch = updated
	return cmd
}

// applyChosenColor persists colour against the focused / open slot, updates
// the swatch UI, and resets focus indicator. Returns true when the change
// is for a known slot.
func (p *themePanel) applyChosenColor(hex string) bool {
	idx := p.openSwatchIndex()
	if idx < 0 {
		idx = p.focus
	}
	if idx < 0 || idx >= len(p.swatches) {
		return false
	}
	sw := p.swatches[idx]
	p.draft.Set(sw.key, hex)
	sw.swatch.SetColor(p.draft.Color(sw.key))
	return true
}

// resetAll restores every slot to the built-in default and refreshes
// swatch colours so the preview matches.
func (p *themePanel) resetAll() {
	p.draft = theme.Default()
	for _, sw := range p.swatches {
		sw.swatch.SetColor(p.draft.Color(sw.key))
	}
}

// renderPanel builds the panel string and records each swatch's row/col so
// SetBounds can be called before any open swatch overlays the modal.
//
// Layout per swatch row:
//
//	  formatting           ■▼  #7AA2F7   formatting (preview pill)
//
// labelW + 2 leading spaces fixes the swatch column so SetBounds matches
// the actual terminal cell of the colour cell.
func (p *themePanel) renderPanel(width int) string {
	const (
		leftPad = 2
		labelW  = 20
		hexW    = 9
	)
	swatchCol := leftPad + labelW

	var b strings.Builder
	header := boldStyle.Render("Theme")
	b.WriteString(header + "\n\n")
	b.WriteString(dimStyle.Render("Saved to "+theme.DefaultPath()) + "\n")
	b.WriteString(dimStyle.Render("tab / shift+tab swatch · enter open · ctrl+s save · esc cancel · [ ] tabs · r reset all") + "\n\n")
	row := lipgloss.Height(b.String())

	currentGroup := ""
	for i, sw := range p.swatches {
		if g := themeGroupHeader(sw.key); g != "" && g != currentGroup {
			if currentGroup != "" {
				b.WriteString("\n")
				row++
			}
			b.WriteString(boldStyle.Render(g) + "\n")
			row++
			currentGroup = g
		}
		hex := p.draft.Color(sw.key)
		sw.swatch.SetColor(hex)
		sw.row = row
		sw.col = swatchCol
		sw.swatch.SetBounds(sw.row, sw.col, 0, 0)

		focusMark := "  "
		if i == p.focus {
			focusMark = okStyle.Render("▶ ")
		}
		labelCell := padOrTruncate(sw.label, labelW-2)
		swatchCell := sw.swatch.SwatchView()
		hexCell := padOrTruncate(hex, hexW)
		preview := previewForKey(sw.key, sw.label)
		hint := dimStyle.Render(sw.hint)

		line := fmt.Sprintf("%s%s  %s  %s  %s",
			focusMark, labelCell, swatchCell, hexCell, preview)
		// Mark the row so handleMouse can route presses to the swatch even
		// when the panel is wrapped by parent containers that rebase coords.
		marked := zone.Mark(sw.zoneID, line)
		// Right-aligned hint (best effort; may wrap on narrow terminals).
		fullW := width
		if fullW > 0 {
			lineWidth := lipgloss.Width(marked)
			pad := fullW - lineWidth - lipgloss.Width(hint) - leftPad
			if pad > 1 {
				marked = marked + strings.Repeat(" ", pad) + hint
			}
		}
		b.WriteString(marked + "\n")
		row++
	}

	b.WriteString("\n")
	b.WriteString(zone.Mark(ZoneSettingsSave, okStyle.Render(" Save ")) + "  ")
	b.WriteString(zone.Mark(ZoneSettingsCancel, errStyle.Render(" Cancel ")) + "  ")
	b.WriteString(zone.Mark(ZoneThemeReset, boldStyle.Render(" Reset to defaults ")))
	return b.String()
}

// applyOverlays composes every open swatch's modal overlay onto panel. At
// most one swatch is open in practice; the loop guards against future
// changes.
func (p *themePanel) applyOverlays(panel string, width, height int) string {
	for _, sw := range p.swatches {
		if sw.swatch.Open() {
			panel = sw.swatch.ViewWithOverlay(panel, width, height)
		}
	}
	return panel
}

// previewForKey renders a short example of the colour as it appears in the
// running view (a coloured pill for tag slots, a coloured word for
// severities) so users can judge readability before saving.
func previewForKey(k theme.Key, label string) string {
	switch k {
	case theme.KeyTagFormatting, theme.KeyTagDesign, theme.KeyTagTesting,
		theme.KeyTagDocs, theme.KeyTagSecurity, theme.KeyTagVibeCoach,
		theme.KeyTagLangBriefs, theme.KeyTagTechExperts,
		theme.KeyTagRepoExperts, theme.KeyTagRepoArbiter:
		return tagPreview(theme.Current().Color(k), label)
	case theme.KeySevInfo:
		return sevPreview(k, "info")
	case theme.KeySevWarning:
		return sevPreview(k, "warn")
	case theme.KeySevError:
		return sevPreview(k, "ERROR")
	case theme.KeySevCritical:
		return sevPreview(k, "CRITICAL")
	}
	return label
}

// tagPreview reproduces internal/tui.tagStyle locally to avoid an import
// cycle. Visual parity with the running view matters more than DRY here.
func tagPreview(hex, text string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color(hex)).
		Padding(0, 1).
		Bold(true).
		Render(text)
}

// sevPreview reproduces internal/tui.severityStyle for the same reason as
// tagPreview. The active-theme colour is read at preview time, so updates
// reflect immediately while the user is still picking.
func sevPreview(k theme.Key, text string) string {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Current().Color(k)))
	if k == theme.KeySevError || k == theme.KeySevCritical {
		style = style.Bold(true)
	}
	return style.Render(text)
}

func padOrTruncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) >= w {
		// Truncation is rare for our short labels; use a runes-aware truncate
		// only if we cross the boundary.
		runes := []rune(s)
		if len(runes) > w {
			return string(runes[:w])
		}
		return s
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

func themeSwatchZoneID(k theme.Key) string {
	return "zone:settings:theme:swatch:" + string(k)
}

// ZoneThemeReset is the bubblezone ID for the "Reset to defaults" button on
// the Theme tab. Defined here (rather than in zones.go) so the theme panel
// is self-contained.
const ZoneThemeReset = "zone:settings:theme:reset"

// ZoneSettingsTabTheme is the bubblezone ID for the Theme tab strip entry.
const ZoneSettingsTabTheme = "zone:settings:tab:theme"
