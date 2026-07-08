package review

import (
	"testing"

	"github.com/madicen/appr-ai-sal/internal/review"
)

func card(spec string, sev review.Severity, path string, line int, conf *float64) approvalCard {
	return approvalCard{finding: review.FlatFinding{
		Specialist: spec,
		Finding: review.Finding{
			Path:       path,
			Line:       line,
			Severity:   sev,
			Comment:    "c",
			Confidence: conf,
		},
	}}
}

func fp(f float64) *float64 { return &f }

func TestTriageSortSeverityDesc(t *testing.T) {
	cards := []approvalCard{
		card("a", review.SeverityWarning, "x.go", 1, nil),
		card("a", review.SeverityCritical, "y.go", 2, nil),
		card("a", review.SeverityInfo, "z.go", 3, nil),
		card("a", review.SeverityError, "w.go", 4, nil),
	}
	base := []int{0, 1, 2, 3}
	got := triageOrder(cards, base, sortSeverityDesc, "", -1)
	wantOrder := []review.Severity{
		review.SeverityCritical, review.SeverityError, review.SeverityWarning, review.SeverityInfo,
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 cards, got %d", len(got))
	}
	for i, gi := range got {
		if cards[gi].finding.Finding.Severity != wantOrder[i] {
			t.Errorf("pos %d = %s, want %s", i, cards[gi].finding.Finding.Severity, wantOrder[i])
		}
	}
}

func TestTriageSortConfidenceDesc(t *testing.T) {
	cards := []approvalCard{
		card("a", review.SeverityWarning, "x.go", 1, fp(0.2)),
		card("a", review.SeverityWarning, "y.go", 2, fp(0.9)),
		card("a", review.SeverityWarning, "z.go", 3, nil), // no confidence → last
		card("a", review.SeverityWarning, "w.go", 4, fp(0.5)),
	}
	got := triageOrder(cards, []int{0, 1, 2, 3}, sortConfidenceDesc, "", -1)
	wantLines := []int{2, 4, 1, 3} // 0.9, 0.5, 0.2, nil
	for i, gi := range got {
		if cards[gi].finding.Finding.Line != wantLines[i] {
			t.Errorf("pos %d = line %d, want %d", i, cards[gi].finding.Finding.Line, wantLines[i])
		}
	}
}

func TestTriageSortFile(t *testing.T) {
	cards := []approvalCard{
		card("a", review.SeverityWarning, "b.go", 10, nil),
		card("a", review.SeverityWarning, "a.go", 30, nil),
		card("a", review.SeverityWarning, "a.go", 5, nil),
	}
	got := triageOrder(cards, []int{0, 1, 2}, sortFile, "", -1)
	// a.go:5, a.go:30, b.go:10
	want := []struct {
		p string
		l int
	}{{"a.go", 5}, {"a.go", 30}, {"b.go", 10}}
	for i, gi := range got {
		f := cards[gi].finding.Finding
		if f.Path != want[i].p || f.Line != want[i].l {
			t.Errorf("pos %d = %s:%d, want %s:%d", i, f.Path, f.Line, want[i].p, want[i].l)
		}
	}
}

func TestTriageSeverityFloorFilters(t *testing.T) {
	cards := []approvalCard{
		card("a", review.SeverityInfo, "x.go", 1, nil),
		card("a", review.SeverityWarning, "y.go", 2, nil),
		card("a", review.SeverityError, "z.go", 3, nil),
	}
	got := triageOrder(cards, []int{0, 1, 2}, sortFinding, review.SeverityWarning, -1)
	if len(got) != 2 {
		t.Fatalf("warning floor should keep 2 cards, got %d (%v)", len(got), got)
	}
	// The info card (index 0) must be gone.
	for _, gi := range got {
		if cards[gi].finding.Finding.Severity == review.SeverityInfo {
			t.Error("info card should have been filtered out")
		}
	}
	// keep=0 keeps the focused card even below the floor.
	got = triageOrder(cards, []int{0, 1, 2}, sortFinding, review.SeverityWarning, 0)
	if len(got) != 3 {
		t.Fatalf("keep exception should retain the focused card, got %d", len(got))
	}
}

func TestSeverityTallyAndFormat(t *testing.T) {
	cards := []approvalCard{
		card("a", review.SeverityCritical, "x.go", 1, nil),
		card("a", review.SeverityWarning, "y.go", 2, nil),
		card("a", review.SeverityWarning, "z.go", 3, nil),
		{finding: review.FlatFinding{Finding: review.Finding{Severity: review.SeverityError}}, demoted: true}, // excluded
	}
	counts := severityTally(cards)
	if counts[review.SeverityCritical] != 1 {
		t.Errorf("critical count = %d, want 1", counts[review.SeverityCritical])
	}
	if counts[review.SeverityWarning] != 2 {
		t.Errorf("warning count = %d, want 2", counts[review.SeverityWarning])
	}
	if counts[review.SeverityError] != 0 {
		t.Errorf("demoted card should be excluded; error count = %d, want 0", counts[review.SeverityError])
	}
	out := formatSeverityCounts(counts)
	if out == "" {
		t.Fatal("expected a non-empty severity summary")
	}
	// Critical should appear before warning (most-severe first). Strip ANSI by
	// checking the raw counts substrings exist and order.
	if !contains(out, "1 critical") || !contains(out, "2 warning") {
		t.Errorf("summary missing expected chips: %q", out)
	}
}

func TestFormatSeverityCountsEmpty(t *testing.T) {
	if out := formatSeverityCounts(map[review.Severity]int{}); out != "" {
		t.Errorf("empty counts should render empty, got %q", out)
	}
}

func TestTriageCycleHelpers(t *testing.T) {
	// Sort cycle wraps through all four modes.
	m := sortFinding
	seen := map[triageSortMode]bool{}
	for i := 0; i < 4; i++ {
		seen[m] = true
		m = nextTriageSort(m)
	}
	if m != sortFinding {
		t.Errorf("sort cycle should return to start, got %v", m)
	}
	if len(seen) != 4 {
		t.Errorf("sort cycle should visit 4 modes, visited %d", len(seen))
	}
	// Severity floor cycle: all → warning → error → critical → all.
	var s review.Severity
	s = nextTriageMinSev(s)
	if s != review.SeverityWarning {
		t.Fatalf("first cycle should be warning, got %q", s)
	}
	s = nextTriageMinSev(s) // → error
	s = nextTriageMinSev(s) // → critical
	if s != review.SeverityCritical {
		t.Fatalf("third cycle should be critical, got %q", s)
	}
	s = nextTriageMinSev(s) // → all
	if s != "" {
		t.Errorf("cycle from critical should return to all, got %q", s)
	}
}

func TestActJumpToDiffEmitsMessage(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.AdoptDraft(tabsTestDraft())
	focusAgentTabForTest(t, ro, review.SpecDocs)
	if ro.idx < 0 || ro.idx >= len(ro.cards) {
		t.Fatalf("expected a focused card, idx=%d", ro.idx)
	}
	want := ro.cards[ro.idx].finding.Finding
	_, cmd := ro.actJumpToDiff()
	if cmd == nil {
		t.Fatal("actJumpToDiff should return a command for an anchored finding")
	}
	msg, ok := cmd().(JumpToDiffMsg)
	if !ok {
		t.Fatalf("actJumpToDiff produced %T, want JumpToDiffMsg", cmd())
	}
	if msg.Path != want.Path || msg.Line != want.Line {
		t.Errorf("jump msg = %s:%d, want %s:%d", msg.Path, msg.Line, want.Path, want.Line)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
