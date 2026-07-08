package evals

import (
	"context"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/review"
)

// withReplay installs the deterministic ReplayProvider for one case and
// returns a restore func. The provider is global and not parallel-safe, so
// these tests never call t.Parallel().
func withReplay(c Case) func() {
	return ai.SetBaseProviderForTest(func(*aiconfig.Config) (ai.Provider, error) {
		return NewReplayProvider(c), nil
	})
}

func runCaseReplay(t *testing.T, c Case) CaseScore {
	t.Helper()
	restore := withReplay(c)
	defer restore()
	return RunCase(context.Background(), aiconfig.DefaultConfig(), c)
}

func findSpec(cs CaseScore, name string) (SpecialistScore, bool) {
	for _, s := range cs.Specialists {
		if strings.EqualFold(s.Specialist, name) {
			return s, true
		}
	}
	return SpecialistScore{}, false
}

// TestCorpusLoads asserts the corpus loads, has the promised breadth (>=10
// cases covering every specialist and PR agent), and every case is
// well-formed (a diff, at least one canned response, and at least one
// expectation).
func TestCorpusLoads(t *testing.T) {
	cases, err := LoadCorpus()
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	if len(cases) < 10 {
		t.Fatalf("corpus has %d cases, want >= 10", len(cases))
	}

	wantTargets := []string{
		review.SpecSecurity, review.SpecTech, review.SpecFormatting,
		review.SpecDesign, review.SpecTesting, review.SpecDocs,
		review.SpecDescription, review.SpecChecks, review.SpecDiscussion, review.SpecScope,
	}
	seen := map[string]bool{}
	for _, c := range cases {
		seen[strings.ToLower(strings.TrimSpace(c.Meta.Target))] = true
		if strings.TrimSpace(c.Diff) == "" {
			t.Errorf("case %q: empty diff", c.Meta.ID)
		}
		if len(c.Responses) == 0 {
			t.Errorf("case %q: no canned responses", c.Meta.ID)
		}
		if len(c.Expectations.MustAppear)+len(c.Expectations.MustNotAppear) == 0 {
			t.Errorf("case %q: no expectations", c.Meta.ID)
		}
	}
	for _, tg := range wantTargets {
		if !seen[tg] {
			t.Errorf("no corpus case targets specialist %q", tg)
		}
	}
}

// TestCorpusReplayScores runs the WHOLE corpus through the real review
// pipeline against the deterministic ReplayProvider and asserts, per case:
// every must-appear finding is recalled, no must-not-appear scar survives the
// gates (precision), the expected verdict is reached, and the JSON-first-try
// expectations hold. This proves the scoring math end to end with zero
// network.
func TestCorpusReplayScores(t *testing.T) {
	cases, err := LoadCorpus()
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	for _, c := range cases {
		t.Run(c.Meta.ID, func(t *testing.T) {
			cs := runCaseReplay(t, c)

			// Recall: every labelled must-appear finding was matched.
			var wantAppear, gotAppear int
			for _, s := range cs.Specialists {
				wantAppear += s.ExpectedTotal
				gotAppear += s.ExpectedMatched
				// Precision: no forbidden scar survived the gates.
				if s.ForbiddenHits > 0 {
					t.Errorf("specialist %q: %d forbidden (must-not-appear) finding(s) survived the gates", s.Specialist, s.ForbiddenHits)
				}
			}
			if wantAppear != len(c.Expectations.MustAppear) {
				t.Fatalf("expected-total mismatch: scored %d must-appear rows, corpus has %d", wantAppear, len(c.Expectations.MustAppear))
			}
			if gotAppear != wantAppear {
				t.Errorf("recall incomplete: %d/%d must-appear findings matched", gotAppear, wantAppear)
			}

			// Verdict.
			if cs.VerdictScored && !cs.VerdictOK() {
				t.Errorf("verdict = %q, want %q", cs.VerdictActual, cs.VerdictExpected)
			}

			// JSON parse first-try expectations.
			if fails := cs.JSONFirstTryFailures(); len(fails) > 0 {
				t.Errorf("JSON-first-try expectation failed for: %v", fails)
			}
		})
	}
}

// TestSuggestionSurvivalAndAnchor pins the suggestion-survival and anchor-hit
// arithmetic on the two cases engineered to exercise the gates: the formatting
// case emits two suggestions of which one (the snake_case rename) is stripped,
// and the IaC case emits one suggestion that the schema gate strips entirely.
func TestSuggestionSurvivalAndAnchor(t *testing.T) {
	cases, err := LoadCorpus()
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	byID := map[string]Case{}
	for _, c := range cases {
		byID[c.Meta.ID] = c
	}

	t.Run("formatting-spacing", func(t *testing.T) {
		c, ok := byID["formatting-spacing"]
		if !ok {
			t.Skip("case not present")
		}
		cs := runCaseReplay(t, c)
		s, ok := findSpec(cs, review.SpecFormatting)
		if !ok {
			t.Fatal("no formatting score")
		}
		if s.RawSuggestionAttempts != 2 {
			t.Errorf("raw suggestion attempts = %d, want 2", s.RawSuggestionAttempts)
		}
		if s.SurvivingSuggestions != 1 {
			t.Errorf("surviving suggestions = %d, want 1 (snake_case rename stripped)", s.SurvivingSuggestions)
		}
		if s.AnchorHits != 1 {
			t.Errorf("anchor hits = %d, want 1", s.AnchorHits)
		}
		// Only the legitimate spacing finding should still be emitted.
		if s.Emitted != 1 {
			t.Errorf("emitted findings = %d, want 1", s.Emitted)
		}
	})

	t.Run("tech-iac-s3-tags", func(t *testing.T) {
		c, ok := byID["tech-iac-s3-tags"]
		if !ok {
			t.Skip("case not present")
		}
		cs := runCaseReplay(t, c)
		s, ok := findSpec(cs, review.SpecTech)
		if !ok {
			t.Fatal("no tech score")
		}
		if s.RawSuggestionAttempts != 1 {
			t.Errorf("raw suggestion attempts = %d, want 1", s.RawSuggestionAttempts)
		}
		if s.SurvivingSuggestions != 0 {
			t.Errorf("surviving suggestions = %d, want 0 (schema gate strips tags-on-policy)", s.SurvivingSuggestions)
		}
	})

	t.Run("security-weak-hash", func(t *testing.T) {
		c, ok := byID["security-weak-hash"]
		if !ok {
			t.Skip("case not present")
		}
		cs := runCaseReplay(t, c)
		s, ok := findSpec(cs, review.SpecSecurity)
		if !ok {
			t.Fatal("no security score")
		}
		if s.SurvivingSuggestions != 1 || s.AnchorHits != 1 {
			t.Errorf("security survival/anchor = %d/%d, want 1/1", s.SurvivingSuggestions, s.AnchorHits)
		}
	})
}

// TestScoreCasePure proves the scoring arithmetic in isolation from the
// pipeline: it feeds ScoreCase a hand-built EvalObservation and checks recall,
// precision, suggestion-survival, anchor-hit, and JSON-first-try directly.
func TestScoreCasePure(t *testing.T) {
	c := Case{
		Meta: CaseMeta{ID: "pure", Title: "pure"},
		Expectations: Expectations{
			ExpectedVerdict: "request_changes",
			MustAppear: []ExpectFinding{
				{Specialist: "security", Path: "a.go", Line: 10, Pattern: "(?i)injection"},
			},
			MustNotAppear: []ExpectFinding{
				{Specialist: "security", Path: "a.go", Pattern: "(?i)snake_case"},
			},
			ExpectJSONFirstTry: map[string]bool{"security": true, "docs": true},
		},
	}
	obs := review.EvalObservation{
		Agents: []review.EvalAgentObservation{
			{
				Agent:    "security",
				Kind:     review.KindCode,
				ParsedOK: true,
				// 3 raw suggestions: 1 clean survivor+anchor-hit, 1 relocated
				// (survives but anchor miss), 1 unlabelled scar we forbid.
				RawSuggestionAttempts: 3,
				Findings: []review.Finding{
					{Path: "a.go", Line: 10, Severity: review.SeverityError, Comment: "SQL injection here", Suggestion: "fixed line one"},
					{Path: "a.go", Line: 20, Severity: review.SeverityWarning, Comment: "relocated finding", Suggestion: "fixed line two", AnchorRelocatedFrom: 19},
				},
			},
			{
				Agent:    "docs",
				Kind:     review.KindCode,
				ParsedOK: false, // pinned true -> should count as a failure
			},
		},
		Vibe: &review.VibeCoachResult{Verdict: "request_changes"},
	}

	cs := ScoreCase(c, obs)

	sec, ok := findSpec(cs, "security")
	if !ok {
		t.Fatal("no security score")
	}
	if got := sec.Recall(); got.Num != 1 || got.Den != 1 {
		t.Errorf("recall = %s, want 1/1", got)
	}
	if got := sec.Precision(); got.Num != 1 || got.Den != 1 {
		t.Errorf("precision = %s, want 1/1 (1 TP, 0 forbidden)", got)
	}
	if got := sec.SuggestionSurvival(); got.Num != 2 || got.Den != 3 {
		t.Errorf("survival = %s, want 2/3", got)
	}
	if got := sec.AnchorHitRate(); got.Num != 1 || got.Den != 3 {
		t.Errorf("anchor-hit = %s, want 1/3 (one relocated)", got)
	}
	if !cs.VerdictOK() {
		t.Errorf("verdict actual = %q, want request_changes", cs.VerdictActual)
	}
	fails := cs.JSONFirstTryFailures()
	if len(fails) != 1 || fails[0] != "docs" {
		t.Errorf("json-first-try failures = %v, want [docs]", fails)
	}
}

// TestRenderReport exercises report generation against a small scored corpus.
func TestRenderReport(t *testing.T) {
	csr := CorpusScore{
		Provider: "ollama",
		Model:    "llama3.1",
		Cases: []CaseScore{{
			ID: "c1", Title: "t1",
			Specialists: []SpecialistScore{{
				Specialist: "security", Kind: review.KindCode, ParsedOK: true,
				ExpectedTotal: 1, ExpectedMatched: 1, TruePositives: 1,
				RawSuggestionAttempts: 1, SurvivingSuggestions: 1, AnchorHits: 1,
				JSONFirstTry: true,
			}},
			VerdictScored: true, VerdictExpected: "request_changes", VerdictActual: "request_changes",
			Calls: 3, InputTokens: 1200, OutputTokens: 300,
		}},
	}
	out := RenderReport(csr)
	for _, want := range []string{
		"# appr-ai-sal eval report", "Scores by specialist", "security",
		"## Cases", "## Totals", "ollama", "Verdicts matched",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q\n---\n%s", want, out)
		}
	}
}

// TestRenderABReport exercises the A/B delta report.
func TestRenderABReport(t *testing.T) {
	mk := func(recall int) CorpusScore {
		return CorpusScore{Provider: "ollama", Model: "m", Cases: []CaseScore{{
			ID: "c1", Specialists: []SpecialistScore{{
				Specialist: "docs", ExpectedTotal: 2, ExpectedMatched: recall, JSONFirstTry: true,
			}},
		}}}
	}
	out := RenderABReport(mk(1), mk(2))
	for _, want := range []string{"(A/B)", "Recall", "| Specialist | A | B | Δ |", "docs", "pp"} {
		if !strings.Contains(out, want) {
			t.Errorf("A/B report missing %q\n---\n%s", want, out)
		}
	}
}
