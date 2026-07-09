package memory

import (
	"testing"
)

func TestPathGlobGeneralizes(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"internal/review/agents.go", "internal/review/*.go"},
		{"internal/review/runner.go", "internal/review/*.go"},
		{"README.md", "*.md"},
		{"Makefile", "*"},
		{"", "*"},
		{"cmd/app/main.go", "cmd/app/*.go"},
		{"a/b/c/x.tf", "a/b/c/*.tf"},
		{"windows\\path\\file.py", "windows/path/*.py"},
	}
	for _, tc := range tests {
		if got := PathGlob(tc.in); got != tc.want {
			t.Errorf("PathGlob(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Two sibling files in the same dir with the same extension must share a
	// fingerprint (that is the whole point of the generalization).
	if PathGlob("internal/review/agents.go") != PathGlob("internal/review/runner.go") {
		t.Fatal("sibling .go files must generalize to the same glob")
	}
}

func TestHashCommentNearIdentical(t *testing.T) {
	// Case / whitespace / punctuation / word-order differences must collapse
	// to the same hash — this is the near-identical matching.
	base := HashComment("This allocates inside the loop; hoist it out.")
	variants := []string{
		"this allocates inside the loop hoist it out",
		"  This   allocates inside the loop, hoist it out!!! ",
		"out it hoist loop the inside allocates this", // reordered words
		"THIS ALLOCATES INSIDE THE LOOP - HOIST IT OUT",
	}
	for _, v := range variants {
		if got := HashComment(v); got != base {
			t.Errorf("HashComment(%q)=%q, want %q (near-identical must collapse)", v, got, base)
		}
	}
	// A materially different comment must NOT collide.
	if HashComment("Missing test coverage for the error branch.") == base {
		t.Fatal("different comments must hash differently")
	}
	// Empty / whitespace-only comment hashes to "".
	if HashComment("   ") != "" {
		t.Fatal("whitespace-only comment must hash to empty")
	}
}

func TestStoreRecordRoundTripAndAtomicity(t *testing.T) {
	t.Setenv("APPR_AI_SAL_CACHE_DIR", t.TempDir())
	s := NewStore()

	fp := NewFingerprint("formatting", "internal/review/agents.go", "fix the indentation here", "warning")

	// Fold three skips across "runs".
	for i := 0; i < 3; i++ {
		if err := s.Record("acme", "widget", Entry{Fingerprint: fp, Decision: DecisionSkipped}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	// Reload from disk (a fresh store instance proves persistence).
	s2 := NewStore()
	mem, err := s2.Load("acme", "widget")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := mem.SkipCount(fp); got != 3 {
		t.Fatalf("SkipCount=%d, want 3 (skips must persist across runs)", got)
	}
	if len(mem.Records) != 1 {
		t.Fatalf("want a single folded record, got %d", len(mem.Records))
	}
	if mem.Records[0].Last.IsZero() {
		t.Fatal("Last timestamp must be set")
	}
}

func TestShouldSuppressConservative(t *testing.T) {
	fp := NewFingerprint("formatting", "a/b.go", "nit", "info")
	m := &Memory{}

	// Under threshold: no suppression.
	for i := 0; i < 2; i++ {
		foldEntryHelper(m, fp, DecisionSkipped)
	}
	if m.ShouldSuppress(fp, DefaultSuppressThreshold) {
		t.Fatal("2 skips < threshold 3 must not suppress")
	}
	// At threshold: suppress.
	foldEntryHelper(m, fp, DecisionSkipped)
	if !m.ShouldSuppress(fp, DefaultSuppressThreshold) {
		t.Fatal("3 skips must suppress")
	}
	// But once the reviewer posts it as often as they skipped it, back off.
	for i := 0; i < 3; i++ {
		foldEntryHelper(m, fp, DecisionPosted)
	}
	if m.ShouldSuppress(fp, DefaultSuppressThreshold) {
		t.Fatal("skips must strictly exceed positives to suppress (conservative)")
	}
}

func TestRejectedPatternsAndExport(t *testing.T) {
	m := &Memory{}
	strong := NewFingerprint("design", "pkg/x.go", "over-engineered abstraction", "warning")
	weak := NewFingerprint("docs", "pkg/y.go", "add a doc comment", "info")
	for i := 0; i < 4; i++ {
		foldEntryHelper(m, strong, DecisionSkipped)
	}
	foldEntryHelper(m, weak, DecisionSkipped) // only 1 skip → below arbiter min

	rp := m.RejectedPatterns()
	if len(rp) != 1 {
		t.Fatalf("want 1 rejected pattern (count>=2), got %d", len(rp))
	}
	if rp[0].Fingerprint.Specialist != "design" || rp[0].Count != 4 {
		t.Fatalf("unexpected rejected pattern: %+v", rp[0])
	}
	neg := m.ExportNegatives()
	if len(neg) != 1 || neg[0].Specialist != "design" || neg[0].Pattern != "" {
		t.Fatalf("export must scaffold one privacy-safe negative, got %+v", neg)
	}
}

func TestClearAndClearFingerprint(t *testing.T) {
	t.Setenv("APPR_AI_SAL_CACHE_DIR", t.TempDir())
	s := NewStore()
	fpA := NewFingerprint("formatting", "a.go", "aaa", "info")
	fpB := NewFingerprint("design", "b.go", "bbb", "warning")
	if err := s.Record("o", "r", Entry{Fingerprint: fpA, Decision: DecisionSkipped}, Entry{Fingerprint: fpB, Decision: DecisionSkipped}); err != nil {
		t.Fatal(err)
	}
	removed, err := s.ClearFingerprint("o", "r", fpA)
	if err != nil || removed != 1 {
		t.Fatalf("ClearFingerprint removed=%d err=%v", removed, err)
	}
	mem, _ := s.Load("o", "r")
	if len(mem.Records) != 1 || mem.Records[0].Fingerprint != fpB {
		t.Fatalf("only fpB should remain, got %+v", mem.Records)
	}
	if err := s.Clear("o", "r"); err != nil {
		t.Fatal(err)
	}
	mem, _ = s.Load("o", "r")
	if len(mem.Records) != 0 {
		t.Fatalf("clear must empty the store, got %+v", mem.Records)
	}
}

// foldEntryHelper folds a single decision into m for tests without touching
// disk (exercises the same fold logic Record uses).
func foldEntryHelper(m *Memory, fp Fingerprint, d Decision) {
	for i := range m.Records {
		if m.Records[i].Fingerprint == fp && m.Records[i].Decision == d {
			m.Records[i].Count++
			return
		}
	}
	m.Records = append(m.Records, Record{Fingerprint: fp, Decision: d, Count: 1})
}
