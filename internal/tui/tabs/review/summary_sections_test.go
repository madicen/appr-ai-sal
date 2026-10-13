package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/review"
)

func hasSection(secs []review.BodySection, id string) bool {
	for _, s := range secs {
		if s.ID == id {
			return true
		}
	}
	return false
}

func TestSyncSummarySectionsPreservesOverridesForAliveIDs(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	d := newDeferTestDraft(t)
	d.VibeCoach.Prompts = []review.AuthorPrompt{
		{Title: "One", AgentPrompt: "do one"},
		{Title: "Two", AgentPrompt: "do two"},
	}
	ro.AdoptDraft(d)

	ro.syncSummarySections()
	if !hasSection(ro.summarySections, "prompt:0") || !hasSection(ro.summarySections, "prompt:1") {
		t.Fatalf("expected prompt:0 and prompt:1, got %+v", ro.summarySections)
	}

	ro.summaryOverrides["prompt:0"] = review.BodySectionOverride{Included: false}
	ro.summaryOverrides["prompt:1"] = review.BodySectionOverride{Included: true, Edited: "edited two"}

	// Draft changes: only one prompt survives (prompt:0).
	d.VibeCoach.Prompts = d.VibeCoach.Prompts[:1]
	ro.syncSummarySections()

	if hasSection(ro.summarySections, "prompt:1") {
		t.Fatalf("prompt:1 should have vanished")
	}
	if ov, ok := ro.summaryOverrides["prompt:0"]; !ok || ov.Included {
		t.Errorf("override for surviving prompt:0 lost: %+v ok=%v", ov, ok)
	}
	if _, ok := ro.summaryOverrides["prompt:1"]; ok {
		t.Errorf("override for vanished prompt:1 should be dropped")
	}
	if strings.Contains(ro.composedSummaryBody(), "do one") {
		t.Errorf("excluded prompt:0 leaked into composed body")
	}
}

func TestSyncSummarySectionsClampsFocusAndNilDraft(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.AdoptDraft(newDeferTestDraft(t))
	ro.syncSummarySections()
	ro.summaryFocus = 999
	ro.syncSummarySections()
	if ro.summaryFocus != len(ro.summarySections)-1 {
		t.Errorf("focus %d, want %d", ro.summaryFocus, len(ro.summarySections)-1)
	}

	ro.draft = nil
	ro.syncSummarySections()
	if ro.summarySections != nil {
		t.Errorf("sections should be nil with nil draft")
	}
}

func TestEffectiveSectionMarkdownAndIncluded(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.AdoptDraft(newDeferTestDraft(t))
	ro.syncSummarySections()
	var caution, stats review.BodySection
	for _, s := range ro.summarySections {
		switch s.ID {
		case "caution":
			caution = s
		case "stats":
			stats = s
		}
	}
	ro.summaryOverrides["caution"] = review.BodySectionOverride{Included: false, Edited: "x"}
	if !ro.sectionIncluded(caution) || ro.effectiveSectionMarkdown(caution) != caution.Markdown {
		t.Errorf("fixed section must ignore overrides")
	}
	ro.summaryOverrides["stats"] = review.BodySectionOverride{Included: true, Edited: "custom"}
	if got := ro.effectiveSectionMarkdown(stats); got != "custom" {
		t.Errorf("edited markdown = %q", got)
	}
	ro.summaryOverrides["stats"] = review.BodySectionOverride{Included: false}
	if ro.sectionIncluded(stats) {
		t.Errorf("stats should be excluded")
	}
}

func TestEnterSummarySyncsSections(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.AdoptDraft(newDeferTestDraft(t))
	ro.summarySections = nil
	ro.enterSummary()
	if len(ro.summarySections) == 0 {
		t.Fatalf("enterSummary should populate summarySections")
	}
}

func TestRenderSummaryBodyShowsSectionCards(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	d := newDeferTestDraft(t)
	d.VibeCoach.Prompts = []review.AuthorPrompt{
		{Title: "One", AgentPrompt: "do one"},
		{Title: "Two", AgentPrompt: "do two"},
	}
	ro.AdoptDraft(d)
	ro.phase = phaseSummary
	ro.rebuildBody()

	out := ro.renderSummaryBody()
	for _, want := range []string{"Headline & summary", "[x]", "AI disclosure", "Edit", "▸"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in summary cards:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Markdown body GitHub will publish") {
		t.Errorf("old glamour blob label should be gone:\n%s", out)
	}
	if !strings.Contains(out, "Post summary (y)") {
		t.Errorf("footer buttons missing:\n%s", out)
	}

	// Exclude a section: its checkbox flips and the card is still listed.
	ro.summaryOverrides["prompt:0"] = review.BodySectionOverride{Included: false}
	out = ro.renderSummaryBody()
	if !strings.Contains(out, "[ ]") {
		t.Errorf("excluded section should show an empty checkbox:\n%s", out)
	}

	// Expanding shows full effective (edited) markdown.
	ro.summaryOverrides["prompt:1"] = review.BodySectionOverride{Included: true, Edited: "EDITED-TWO-BODY\nline2\nline3\nline4"}
	if strings.Contains(ro.renderSummaryBody(), "line4") {
		t.Errorf("collapsed card should truncate body")
	}
	ro.summaryExpanded["prompt:1"] = true
	out = ro.renderSummaryBody()
	if !strings.Contains(out, "line4") || !strings.Contains(out, "▾") {
		t.Errorf("expanded card should show full edited body:\n%s", out)
	}
}

func TestHelpForPhaseSummaryListsSectionKeys(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.AdoptDraft(newDeferTestDraft(t))
	ro.phase = phaseSummary
	h := ro.helpForPhase()
	if !strings.Contains(h, "space include") || !strings.Contains(h, "enter expand") || !strings.Contains(h, "e edit") {
		t.Errorf("help line = %q", h)
	}
}
