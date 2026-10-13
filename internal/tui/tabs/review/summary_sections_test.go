package review

import (
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
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

	// Expanded headline shows raw markdown markers, not glamour-rendered text.
	ro.summaryExpanded["headline"] = true
	out = ro.renderSummaryBody()
	if !strings.Contains(out, "## ") {
		t.Errorf("expanded section should show raw markdown (## headline):\n%s", out)
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
	ro.summaryEditing = true
	h = ro.helpForPhase()
	if !strings.Contains(h, "esc save") {
		t.Errorf("editing help line = %q", h)
	}
}

func summaryKey(t *testing.T, ro *Model, msg tea.KeyMsg) (*Model, tea.Cmd) {
	t.Helper()
	out, cmd := ro.handleKey(msg)
	return out.(*Model), cmd
}

func runesKey(r string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)} }

func summaryKeysModel(t *testing.T) *Model {
	t.Helper()
	ro := New(120, 44, false, false, false, nil, false)
	d := newDeferTestDraft(t)
	d.VibeCoach.Prompts = []review.AuthorPrompt{
		{Title: "One", AgentPrompt: "do one"},
		{Title: "Two", AgentPrompt: "do two"},
	}
	ro.AdoptDraft(d)
	ro.syncSummarySections()
	ro.phase = phaseSummary
	ro.rebuildBody()
	if len(ro.summarySections) < 2 {
		t.Fatalf("need >=2 sections, got %d", len(ro.summarySections))
	}
	return ro
}

func TestSummarySectionKeysToggleExpandEdit(t *testing.T) {
	ro := summaryKeysModel(t)

	// Focus moves with down/j and up/k, clamped.
	ro, _ = summaryKey(t, ro, tea.KeyMsg{Type: tea.KeyDown})
	if ro.summaryFocus != 1 {
		t.Fatalf("down: focus=%d want 1", ro.summaryFocus)
	}
	ro, _ = summaryKey(t, ro, runesKey("k"))
	if ro.summaryFocus != 0 {
		t.Fatalf("k: focus=%d want 0", ro.summaryFocus)
	}
	ro, _ = summaryKey(t, ro, tea.KeyMsg{Type: tea.KeyUp})
	if ro.summaryFocus != 0 {
		t.Fatalf("up at top: focus=%d want 0", ro.summaryFocus)
	}
	ro, _ = summaryKey(t, ro, runesKey("j"))
	if ro.summaryFocus != 1 {
		t.Fatalf("j: focus=%d want 1", ro.summaryFocus)
	}

	// Move focus to the first editable (non-fixed) section.
	idx := -1
	for i, s := range ro.summarySections {
		if !s.Fixed {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("no editable section")
	}
	for ro.summaryFocus < idx {
		ro, _ = summaryKey(t, ro, runesKey("j"))
	}
	for ro.summaryFocus > idx {
		ro, _ = summaryKey(t, ro, runesKey("k"))
	}

	// Space toggles include on the focused (non-fixed) section.
	sec := ro.summarySections[idx]
	ro, _ = summaryKey(t, ro, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	if ov, ok := ro.summaryOverrides[sec.ID]; !ok || ov.Included {
		t.Fatalf("space: override=%+v ok=%v, want Included=false", ov, ok)
	}
	ro, _ = summaryKey(t, ro, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	if !ro.sectionIncluded(sec) {
		t.Fatalf("second space should re-include")
	}

	// Enter expands and must NOT post.
	phaseBefore := ro.phase
	ro, cmd := summaryKey(t, ro, tea.KeyMsg{Type: tea.KeyEnter})
	if !ro.summaryExpanded[sec.ID] {
		t.Fatalf("enter should expand %q", sec.ID)
	}
	if cmd != nil {
		t.Errorf("enter must not issue a post command")
	}
	if ro.phase != phaseBefore || ro.summaryDone {
		t.Errorf("enter must not post: phase=%v done=%v", ro.phase, ro.summaryDone)
	}
	ro, _ = summaryKey(t, ro, tea.KeyMsg{Type: tea.KeyEnter})
	if ro.summaryExpanded[sec.ID] {
		t.Fatalf("second enter should collapse")
	}

	// e opens the editor prefilled with effective markdown.
	ro, _ = summaryKey(t, ro, runesKey("e"))
	if !ro.summaryEditing || ro.summaryEditID != sec.ID {
		t.Fatalf("e: editing=%v id=%q", ro.summaryEditing, ro.summaryEditID)
	}
	if got := ro.summaryInput.Value(); got != sec.Markdown {
		t.Fatalf("prefill = %q, want %q", got, sec.Markdown)
	}
	body := ro.renderSummaryBody()
	if !strings.Contains(body, "Section markdown:") {
		t.Fatalf("editing view should show the textarea:\n%s", body)
	}
	if val := strings.TrimSpace(ro.summaryInput.Value()); val != "" {
		first := val
		if i := strings.Index(first, "\n"); i >= 0 {
			first = first[:i]
		}
		if !strings.Contains(body, strings.TrimSpace(first)) {
			t.Fatalf("editing view should show textarea content %q:\n%s", first, body)
		}
	} else if !strings.Contains(body, "Edit section markdown") {
		t.Fatalf("empty editor should show placeholder:\n%s", body)
	}

	// While editing, keys go to the textarea (j must not move focus, y must not post).
	ro.summaryInput.SetValue("")
	ro, _ = summaryKey(t, ro, runesKey("y"))
	ro, _ = summaryKey(t, ro, runesKey("j"))
	if ro.summaryFocus != idx {
		t.Errorf("focus moved while editing: %d", ro.summaryFocus)
	}
	if !ro.summaryEditing || ro.summaryInput.Value() != "yj" {
		t.Fatalf("typing: editing=%v value=%q", ro.summaryEditing, ro.summaryInput.Value())
	}
	if !strings.Contains(ro.renderSummaryBody(), "yj") {
		t.Fatalf("editing view should reflect typed text")
	}

	// esc commits Edited and closes.
	ro, _ = summaryKey(t, ro, tea.KeyMsg{Type: tea.KeyEsc})
	if ro.summaryEditing {
		t.Fatalf("esc should close the editor")
	}
	if ov := ro.summaryOverrides[sec.ID]; ov.Edited != "yj" || !ov.Included {
		t.Fatalf("override after esc = %+v", ov)
	}
	if got := ro.effectiveSectionMarkdown(sec); got != "yj" {
		t.Fatalf("effective = %q", got)
	}
}

func TestSummarySectionKeysFixedSectionNoEdit(t *testing.T) {
	ro := summaryKeysModel(t)
	fixed := -1
	for i, s := range ro.summarySections {
		if s.Fixed {
			fixed = i
			break
		}
	}
	if fixed < 0 {
		t.Skip("no fixed section")
	}
	ro.summaryFocus = fixed
	id := ro.summarySections[fixed].ID
	ro, _ = summaryKey(t, ro, runesKey("e"))
	if ro.summaryEditing {
		t.Errorf("fixed section must not open editor")
	}
	ro, _ = summaryKey(t, ro, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	if _, ok := ro.summaryOverrides[id]; ok {
		t.Errorf("space on fixed section must not create an override")
	}
}

func TestSummaryYStillPosts(t *testing.T) {
	ro := summaryKeysModel(t)
	_, cmd := summaryKey(t, ro, runesKey("y"))
	if cmd == nil {
		t.Fatalf("y should post the summary")
	}
}

// summaryZone renders + scans the overlay and polls (bubblezone registers
// bounds asynchronously after Scan) until the zone id is available.
func summaryZone(t *testing.T, ro *Model, id string) *zone.ZoneInfo {
	t.Helper()
	zone.Scan(ro.View())
	deadline := time.Now().Add(time.Second)
	for zone.Get(id) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("zone %q not registered after render", id)
		}
		runtime.Gosched()
	}
	return zone.Get(id)
}

func summaryClick(t *testing.T, ro *Model, z *zone.ZoneInfo) *Model {
	t.Helper()
	out, _ := ro.handleMouse(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		X: z.StartX, Y: z.StartY,
	})
	return out.(*Model)
}

func TestSummarySectionMouseToggleAndExpand(t *testing.T) {
	ro := summaryKeysModel(t)
	idx := -1
	for i, s := range ro.summarySections {
		if !s.Fixed {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("no editable section")
	}
	sec := ro.summarySections[idx]

	// Checkbox click flips Included.
	ro = summaryClick(t, ro, summaryZone(t, ro, zones.SummarySectionCheck(sec.ID)))
	if ro.sectionIncluded(sec) {
		t.Fatalf("check click should exclude %q", sec.ID)
	}
	ro = summaryClick(t, ro, summaryZone(t, ro, zones.SummarySectionCheck(sec.ID)))
	if !ro.sectionIncluded(sec) {
		t.Fatalf("second check click should re-include %q", sec.ID)
	}

	// Header click focuses + toggles expand.
	ro = summaryClick(t, ro, summaryZone(t, ro, zones.SummarySectionHeader(sec.ID)))
	if !ro.summaryExpanded[sec.ID] || ro.summaryFocus != idx {
		t.Fatalf("header click: expanded=%v focus=%d want true/%d", ro.summaryExpanded[sec.ID], ro.summaryFocus, idx)
	}
	ro = summaryClick(t, ro, summaryZone(t, ro, zones.SummarySectionHeader(sec.ID)))
	if ro.summaryExpanded[sec.ID] {
		t.Fatalf("second header click should collapse")
	}

	// Edit click opens the editor.
	ro = summaryClick(t, ro, summaryZone(t, ro, zones.SummarySectionEdit(sec.ID)))
	if !ro.summaryEditing || ro.summaryEditID != sec.ID {
		t.Fatalf("edit click: editing=%v id=%q", ro.summaryEditing, ro.summaryEditID)
	}

	// While editing, card clicks are swallowed.
	other := -1
	for i, s := range ro.summarySections {
		if i != idx && !s.Fixed {
			other = i
			break
		}
	}
	if other >= 0 {
		os := ro.summarySections[other]
		before := ro.sectionIncluded(os)
		ro = summaryClick(t, ro, summaryZone(t, ro, zones.SummarySectionCheck(os.ID)))
		if ro.sectionIncluded(os) != before || !ro.summaryEditing {
			t.Fatalf("clicks while editing must be swallowed")
		}
	}
}
