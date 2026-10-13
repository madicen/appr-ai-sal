package review

import (
	"strings"

	"github.com/madicen/appr-ai-sal/internal/review"
)

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
