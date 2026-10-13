package review

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// summaryCollapsedPreviewLines is how many non-empty markdown lines a
// collapsed section card previews.
const summaryCollapsedPreviewLines = 2

// syncSummarySections rebuilds the summary body sections from the draft and
// drops reviewer overrides whose section no longer exists. Overrides for
// sections that survive (same ID) are kept so edits/exclusions persist across
// re-entering the summary.
func (m *Model) syncSummarySections() {
	if m.draft == nil {
		m.summarySections = nil
		return
	}
	next := m.draft.BodySections()
	alive := make(map[string]string, len(next))
	for _, s := range next {
		alive[s.ID] = s.Markdown
	}
	if m.summaryOverrides != nil {
		for id, ov := range m.summaryOverrides {
			md, ok := alive[id]
			// Drop overrides whose section vanished or whose generated
			// content changed since the override was made (positional IDs can
			// be reused for different content after a re-sync).
			if !ok || (ov.BaseMarkdown != "" && ov.BaseMarkdown != md) {
				delete(m.summaryOverrides, id)
			}
		}
	} else {
		m.summaryOverrides = map[string]review.BodySectionOverride{}
	}
	if m.summaryExpanded == nil {
		m.summaryExpanded = map[string]bool{}
	}
	m.summarySections = next
	if m.summaryFocus >= len(m.summarySections) {
		m.summaryFocus = max(0, len(m.summarySections)-1)
	}
}

// resetSummaryEdits drops every reviewer summary-section edit (session UI
// state) when leaving the overlay or adopting a new draft.
func (m *Model) resetSummaryEdits() {
	m.summarySections = nil
	m.summaryOverrides = nil
	m.summaryExpanded = nil
	m.summaryFocus = 0
	m.summaryEditing = false
}

// effectiveSectionMarkdown returns the reviewer's edit when set (never for
// Fixed sections), else the generated markdown.
func (m *Model) effectiveSectionMarkdown(s review.BodySection) string {
	if s.Fixed {
		return s.Markdown
	}
	if ov, ok := m.summaryOverrides[s.ID]; ok && strings.TrimSpace(ov.Edited) != "" {
		return ov.Edited
	}
	return s.Markdown
}

// sectionIncluded reports whether s will be part of the posted body. A missing
// override means included; Fixed sections are always included.
func (m *Model) sectionIncluded(s review.BodySection) bool {
	if s.Fixed {
		return true
	}
	if ov, ok := m.summaryOverrides[s.ID]; ok {
		return ov.Included
	}
	return true
}

// composedSummaryBody is the GitHub summary body after applying overrides.
func (m *Model) composedSummaryBody() string {
	return review.ComposeBody(m.summarySections, m.summaryOverrides)
}

// collapsedSectionPreview returns the first few non-empty lines of md, each
// truncated to width cells, for a collapsed card.
func collapsedSectionPreview(md string, width int) string {
	var out []string
	for _, ln := range strings.Split(md, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		out = append(out, ansi.Truncate(ln, max(1, width), "…"))
		if len(out) == summaryCollapsedPreviewLines {
			break
		}
	}
	return strings.Join(out, "\n")
}

// expandedSectionBody renders effective markdown as raw text: each source line
// is prefixed with indentCols spaces and hard-wrapped to rowW (no glamour).
func expandedSectionBody(md string, rowW, indentCols int) string {
	prefix := strings.Repeat(" ", indentCols)
	var b strings.Builder
	for i, ln := range strings.Split(md, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		for j, wl := range strings.Split(util.WrapForViewport(prefix+ln, rowW), "\n") {
			if j > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(wl)
		}
	}
	return b.String()
}

// renderSummarySectionCard renders one section card. idx is its position in
// m.summarySections (used for the focus marker).
func (m *Model) renderSummarySectionCard(idx int, s review.BodySection, rowW int) string {
	included := m.sectionIncluded(s)
	expanded := m.summaryExpanded[s.ID]
	focused := idx == m.summaryFocus

	focusMark := "  "
	if focused {
		focusMark = styles.BoldStyle.Render("▌ ")
	}
	var check string
	if s.Fixed {
		check = styles.DimStyle.Render("🔒")
	} else {
		box := "[ ]"
		if included {
			box = "[x]"
		}
		check = zone.Mark(zones.SummarySectionCheck(s.ID), styles.OkStyle.Render(box))
		if !included {
			check = zone.Mark(zones.SummarySectionCheck(s.ID), styles.DimStyle.Render(box))
		}
	}
	caret := "▸"
	if expanded {
		caret = "▾"
	}
	title := s.Title
	switch {
	case s.Fixed:
		title += " (always included)"
	case !included:
		title += " (excluded)"
	}
	titleStyle := styles.BoldStyle
	if !included {
		titleStyle = styles.DimStyle
	}
	header := zone.Mark(zones.SummarySectionHeader(s.ID), titleStyle.Render(caret+" "+title))

	row := focusMark + check + " " + header
	if !s.Fixed {
		row += "  " + zone.Mark(zones.SummarySectionEdit(s.ID), styles.DimStyle.Render("Edit"))
	}

	var b strings.Builder
	b.WriteString(row + "\n")
	if m.summaryEditing && s.ID == m.summaryEditID {
		b.WriteString(styles.DimStyle.Render("    Section markdown:") + "\n")
		b.WriteString(m.summaryInput.View() + "\n")
		return b.String()
	}
	md := m.effectiveSectionMarkdown(s)
	if expanded {
		body := expandedSectionBody(md, rowW, 4)
		if !included {
			body = styles.DimStyle.Render(body)
		}
		b.WriteString(body + "\n")
	} else if prev := collapsedSectionPreview(md, rowW-6); prev != "" {
		for _, ln := range strings.Split(prev, "\n") {
			b.WriteString("      " + styles.DimStyle.Render(ln) + "\n")
		}
	}
	return b.String()
}

// renderSummarySectionCards renders every section card, blank-line separated.
func (m *Model) renderSummarySectionCards(rowW int) string {
	var b strings.Builder
	b.WriteString(styles.DimStyle.Render("Summary sections — choose what GitHub publishes:") + "\n\n")
	for i, s := range m.summarySections {
		b.WriteString(m.renderSummarySectionCard(i, s, rowW))
		b.WriteString("\n")
	}
	return b.String()
}

// summarySectionKeyActive reports whether a key should be routed to
// handleSummarySectionKey: always while the editor is open (it owns all
// input), otherwise only for the section navigation/edit keys.
func (m *Model) summarySectionKeyActive(msg tea.KeyMsg) bool {
	if m.summaryEditing {
		return true
	}
	switch msg.String() {
	case "up", "k", "down", "j", " ", "enter", "e":
		return true
	}
	return false
}

// handleSummarySectionKey handles the section-card keys in phaseSummary:
// up/down/j/k focus, space toggles inclusion, enter expands/collapses and e
// opens the editor. While the editor is open every key except esc goes to the
// textarea. Callers should gate on summarySectionKeyActive so y/a/n/q/R and
// scrolling keep their existing meaning.
func (m *Model) handleSummarySectionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.summaryEditing {
		switch msg.String() {
		case "esc", "ctrl+c":
			return m.closeSummaryEditor()
		}
		var cmd tea.Cmd
		m.summaryInput, cmd = m.summaryInput.Update(msg)
		m.rebuildBody()
		return m, cmd
	}
	switch msg.String() {
	case "up", "k":
		if m.summaryFocus > 0 {
			m.summaryFocus--
			m.rebuildBody()
		}
		return m, nil
	case "down", "j":
		if m.summaryFocus < len(m.summarySections)-1 {
			m.summaryFocus++
			m.rebuildBody()
		}
		return m, nil
	case " ":
		m.toggleSummaryInclude(m.summaryFocus)
		m.rebuildBody()
		return m, nil
	case "enter":
		m.toggleSummaryExpand(m.summaryFocus)
		m.rebuildBody()
		return m, nil
	case "e":
		return m.openSummaryEditor(m.summaryFocus)
	}
	return m, nil
}

// toggleSummaryInclude flips whether section idx is published. Fixed sections
// and out-of-range indexes are no-ops.
func (m *Model) toggleSummaryInclude(idx int) {
	if idx < 0 || idx >= len(m.summarySections) {
		return
	}
	s := m.summarySections[idx]
	if s.Fixed {
		return
	}
	ov := m.summaryOverrides[s.ID]
	ov.Included = !m.sectionIncluded(s)
	ov.BaseMarkdown = s.Markdown
	if m.summaryOverrides == nil {
		m.summaryOverrides = map[string]review.BodySectionOverride{}
	}
	m.summaryOverrides[s.ID] = ov
}

// toggleSummaryExpand flips the expanded state of section idx.
func (m *Model) toggleSummaryExpand(idx int) {
	if idx < 0 || idx >= len(m.summarySections) {
		return
	}
	if m.summaryExpanded == nil {
		m.summaryExpanded = map[string]bool{}
	}
	id := m.summarySections[idx].ID
	m.summaryExpanded[id] = !m.summaryExpanded[id]
}

// newSummaryInput builds the section editor textarea, sized like the
// challenge input to the current viewport.
func (m *Model) newSummaryInput() textarea.Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 20000
	ta.Placeholder = "Edit section markdown…"
	ta.SetWidth(max(20, m.vp.Width-2))
	ta.SetHeight(8)
	return ta
}

// openSummaryEditor opens the textarea for section idx, prefilled with its
// effective markdown. A no-op for Fixed or out-of-range sections.
func (m *Model) openSummaryEditor(idx int) (tea.Model, tea.Cmd) {
	if idx < 0 || idx >= len(m.summarySections) {
		return m, nil
	}
	s := m.summarySections[idx]
	if s.Fixed {
		return m, nil
	}
	m.summaryFocus = idx
	m.summaryEditID = s.ID
	m.summaryInput = m.newSummaryInput()
	m.summaryInput.SetValue(m.effectiveSectionMarkdown(s))
	m.summaryEditing = true
	m.rebuildBody()
	return m, m.summaryInput.Focus()
}

// closeSummaryEditor commits the textarea contents as the section's Edited
// markdown (keeping its include state) and closes the editor.
func (m *Model) closeSummaryEditor() (tea.Model, tea.Cmd) {
	if m.summaryEditing {
		for _, s := range m.summarySections {
			if s.ID != m.summaryEditID {
				continue
			}
			if m.summaryOverrides == nil {
				m.summaryOverrides = map[string]review.BodySectionOverride{}
			}
			ov := review.BodySectionOverride{
				Included:     m.sectionIncluded(s),
				Edited:       m.summaryInput.Value(),
				BaseMarkdown: s.Markdown,
			}
			// Saving an empty textarea means "publish nothing for this
			// section": exclude it rather than reverting to the original.
			if strings.TrimSpace(ov.Edited) == "" {
				ov.Included = false
				ov.Edited = ""
			}
			m.summaryOverrides[s.ID] = ov
			break
		}
	}
	m.summaryEditing = false
	m.summaryEditID = ""
	m.summaryInput.Blur()
	m.rebuildBody()
	return m, nil
}
