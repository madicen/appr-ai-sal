package review

import (
	"strings"
	"testing"
	"time"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review/memory"
)

// memWith builds an in-memory *memory.Memory with the given records so tests
// don't touch disk.
func memWith(recs ...memory.Record) *memory.Memory {
	return &memory.Memory{Owner: "acme", Repo: "widget", Records: recs}
}

func skipRecord(specialist, path, comment, severity string, count int) memory.Record {
	return memory.Record{
		Fingerprint: memory.NewFingerprint(specialist, path, comment, severity),
		Decision:    memory.DecisionSkipped,
		Count:       count,
		Last:        time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	}
}

// The arbiter prompt must be byte-identical to a no-memory run when there is
// no reviewer memory: the new "previously rejected patterns" section must be
// entirely absent so existing arbiter behaviour is unchanged.
func TestArbiterPromptByteIdenticalWhenMemoryEmpty(t *testing.T) {
	pr := &gh.PR{Number: 7, Title: "x", Repository: "acme/widget"}
	base := buildRepoArbiterUserPrompt(pr, "digest", nil, "", nil, "")
	if strings.Contains(base, "Previously rejected patterns") {
		t.Fatalf("empty memory must not add the rejected-patterns section:\n%s", base)
	}
	// An empty RejectedPatternsSection produces exactly the same prompt.
	again := buildRepoArbiterUserPrompt(pr, "digest", nil, "", nil, RejectedPatternsSection(memWith()))
	if base != again {
		t.Fatalf("empty-memory prompt must be byte-identical\n--- base ---\n%s\n--- again ---\n%s", base, again)
	}
}

// When memory exists the section appears and is inserted without disturbing
// the rest of the prompt (the base prompt is a substring of the augmented one
// once the injected block is removed).
func TestArbiterPromptIncludesRejectedPatternsWhenMemoryPresent(t *testing.T) {
	pr := &gh.PR{Number: 7, Title: "x", Repository: "acme/widget"}
	mem := memWith(
		skipRecord("formatting", "internal/review/agents.go", "fix the indentation here", "warning", 4),
		skipRecord("design", "pkg/x.go", "over-engineered abstraction", "info", 1), // below arbiter min → excluded
	)
	section := RejectedPatternsSection(mem)
	if section == "" {
		t.Fatal("expected a non-empty rejected-patterns section for a repo with skips")
	}
	if !strings.Contains(section, "formatting") || !strings.Contains(section, "internal/review/*.go") {
		t.Fatalf("section should describe the repeatedly-skipped pattern:\n%s", section)
	}
	if strings.Contains(section, "over-engineered") || strings.Contains(section, "pkg/*.go") {
		t.Fatalf("a single skip is below the arbiter minimum and must be omitted:\n%s", section)
	}
	got := buildRepoArbiterUserPrompt(pr, "digest", nil, "", nil, section)
	if !strings.Contains(got, "## Previously rejected patterns (reviewer memory)") {
		t.Fatalf("augmented prompt missing the section header:\n%s", got)
	}
	// Removing the injected block recovers the base prompt exactly.
	base := buildRepoArbiterUserPrompt(pr, "digest", nil, "", nil, "")
	block := "## Previously rejected patterns (reviewer memory)\n\n" + strings.TrimSpace(section) + "\n"
	if recovered := strings.Replace(got, block, "", 1); recovered != base {
		t.Fatalf("section must be inserted cleanly; recovered != base\n--- recovered ---\n%s\n--- base ---\n%s", recovered, base)
	}
}

// N≥3 near-identical skips → the deterministic pre-arbiter suppressor pulls the
// matching finding out of the specialist set and into MemorySuppressed; fewer
// than 3 leaves it untouched.
func TestApplyMemorySuppressionThreshold(t *testing.T) {
	specs := []SpecialistResult{
		{Specialist: SpecFormatting, Findings: []Finding{
			{Path: "internal/review/runner.go", Line: 12, Side: "RIGHT", Severity: SeverityWarning, Comment: "fix the indentation here"},
			{Path: "internal/review/other.go", Line: 3, Side: "RIGHT", Severity: SeverityInfo, Comment: "a genuinely novel finding"},
		}},
	}

	// Below threshold: nothing suppressed.
	memLow := memWith(skipRecord("formatting", "internal/review/agents.go", "fix the indentation here", "warning", 2))
	kept, sup := ApplyMemorySuppression(memLow, specs)
	if len(sup) != 0 || len(kept[0].Findings) != 2 {
		t.Fatalf("2 skips < threshold must not suppress; sup=%d kept=%d", len(sup), len(kept[0].Findings))
	}

	// At threshold: the matching finding (sibling .go file, near-identical
	// comment) is suppressed; the novel one survives.
	memHi := memWith(skipRecord("formatting", "internal/review/agents.go", "Fix the indentation HERE!", "warning", 3))
	kept, sup = ApplyMemorySuppression(memHi, specs)
	if len(sup) != 1 {
		t.Fatalf("3 near-identical skips must suppress exactly one finding, got %d", len(sup))
	}
	if sup[0].Finding.Line != 12 || sup[0].SkipCount != 3 {
		t.Fatalf("wrong finding suppressed / skip count: %+v", sup[0])
	}
	if len(kept[0].Findings) != 1 || kept[0].Findings[0].Line != 3 {
		t.Fatalf("the novel finding must survive suppression, got %+v", kept[0].Findings)
	}
}

func TestCollectMemoryEntriesDecisions(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{Owner: "acme", Repo: "widget", Repository: "acme/widget"},
		Specialists: []SpecialistResult{
			{Specialist: SpecFormatting, Findings: []Finding{
				{Path: "a.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "post me"},
				{Path: "b.go", Line: 2, Side: "RIGHT", Severity: SeverityWarning, Comment: "skip me"},
			}},
		},
		UserSkipPostKeys: map[string]struct{}{
			FindingSuppressionKey(SpecFormatting, Finding{Path: "b.go", Line: 2, Side: "RIGHT"}): {},
		},
		DemotedHidden: []FlatFinding{
			{Specialist: SpecDocs, Finding: Finding{Path: "", Line: 0, Side: "RIGHT", Severity: SeverityInfo, Comment: "demoted note"}},
		},
		MemorySuppressed: []MemorySuppressedFinding{
			{Specialist: SpecDesign, Finding: Finding{Path: "c.go", Line: 3, Side: "RIGHT", Severity: SeverityInfo, Comment: "was suppressed"}, Resurfaced: true},
			{Specialist: SpecDesign, Finding: Finding{Path: "d.go", Line: 4, Side: "RIGHT", Severity: SeverityInfo, Comment: "still suppressed"}, Resurfaced: false},
		},
	}
	// Opt the demoted PR-wide note back in.
	d.ToggleDemotedPosting(SpecDocs, d.DemotedHidden[0].Finding)

	entries := collectMemoryEntries(d)
	counts := map[memory.Decision]int{}
	for _, e := range entries {
		counts[e.Decision]++
	}
	if counts[memory.DecisionPosted] != 1 {
		t.Errorf("posted=%d want 1", counts[memory.DecisionPosted])
	}
	if counts[memory.DecisionSkipped] != 2 { // UserSkipPostKeys b.go + non-resurfaced d.go
		t.Errorf("skipped=%d want 2", counts[memory.DecisionSkipped])
	}
	if counts[memory.DecisionDemoteReversed] != 2 { // demoted opt-in + resurfaced c.go
		t.Errorf("demote_reversed=%d want 2", counts[memory.DecisionDemoteReversed])
	}
}

// End-to-end round trip through the public seam: RecordReviewerMemory persists,
// then a fresh load shows the skip count so ApplyMemorySuppression fires on a
// later run — the "learns across runs" acceptance criterion.
func TestRecordReviewerMemoryRoundTripDrivesSuppression(t *testing.T) {
	t.Setenv("APPR_AI_SAL_CACHE_DIR", t.TempDir())
	skipComment := "prefer a table-driven test here"
	for run := 0; run < 3; run++ {
		d := &Draft{
			PR: &gh.PR{Owner: "acme", Repo: "widget", Repository: "acme/widget"},
			Specialists: []SpecialistResult{
				{Specialist: SpecTesting, Findings: []Finding{
					{Path: "svc/handler.go", Line: 10, Side: "RIGHT", Severity: SeverityWarning, Comment: skipComment},
				}},
			},
			UserSkipPostKeys: map[string]struct{}{
				FindingSuppressionKey(SpecTesting, Finding{Path: "svc/handler.go", Line: 10, Side: "RIGHT"}): {},
			},
		}
		RecordReviewerMemory(d)
	}
	mem := LoadRepoMemory(&gh.PR{Owner: "acme", Repo: "widget"})
	// A sibling file with a reworded comment must now be suppressed.
	specs := []SpecialistResult{
		{Specialist: SpecTesting, Findings: []Finding{
			{Path: "svc/other.go", Line: 44, Side: "RIGHT", Severity: SeverityWarning, Comment: "Prefer a TABLE-DRIVEN test, here."},
		}},
	}
	_, sup := ApplyMemorySuppression(mem, specs)
	if len(sup) != 1 {
		t.Fatalf("3 persisted skips must drive suppression on a later run, got %d (records=%+v)", len(sup), mem.Records)
	}
}
