package review

import (
	"strings"

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
	alive := make(map[string]struct{}, len(next))
	for _, s := range next {
		alive[s.ID] = struct{}{}
	}
	if m.summaryOverrides != nil {
		for id := range m.summaryOverrides {
			if _, ok := alive[id]; !ok {
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
	md := m.effectiveSectionMarkdown(s)
	if expanded {
		body := util.RenderMarkdownIndented(md, rowW, 4)
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
