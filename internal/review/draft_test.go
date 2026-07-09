package review

import (
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

func TestFindingOriginalSeverityReturnsDemotedSeverity(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{{
			Specialist: SpecTesting,
			Findings: []Finding{
				{Path: "a.go", Line: 10, Side: "RIGHT", Severity: SeverityWarning, Comment: "missing test"},
			},
		}},
		RepoArbiter: &RepoArbiterResult{
			demoteKeySet: map[string]Severity{
				suppressionKey(SpecTesting, "a.go", 10, "RIGHT"): SeverityError,
			},
		},
	}
	if !d.HasRepoExpertDemotions() {
		t.Fatalf("HasRepoExpertDemotions should be true when demoteKeySet is non-empty")
	}
	got, ok := d.FindingOriginalSeverity(SpecTesting, d.Specialists[0].Findings[0])
	if !ok {
		t.Fatalf("FindingOriginalSeverity should report a hit for a demoted finding")
	}
	if got != SeverityError {
		t.Fatalf("FindingOriginalSeverity got %q want %q", got, SeverityError)
	}
	miss := Finding{Path: "z.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning}
	if _, ok := d.FindingOriginalSeverity(SpecTesting, miss); ok {
		t.Fatalf("FindingOriginalSeverity should miss for non-demoted finding")
	}
}

func TestHasRepoExpertDemotionsHandlesNilArbiter(t *testing.T) {
	if (&Draft{}).HasRepoExpertDemotions() {
		t.Fatalf("HasRepoExpertDemotions should be false without an arbiter")
	}
}

func TestSpecialistsForVibeCoachRemovesSuppressedInlines(t *testing.T) {
	specs := []SpecialistResult{
		{Specialist: SpecDocs, Findings: []Finding{
			{Path: "e.go", Line: 1, Comment: "drop", Severity: SeverityInfo},
			{Path: "e.go", Line: 2, Comment: "keep inline", Severity: SeverityInfo},
			{Path: "", Line: 0, Comment: "general", Severity: SeverityInfo},
		}},
	}
	d := &Draft{
		RepoArbiter: &RepoArbiterResult{
			suppressKeySet: map[string]struct{}{
				suppressionKey(SpecDocs, "e.go", 1, "RIGHT"): {},
			},
		},
	}
	out := SpecialistsForVibeCoach(d, specs)
	if len(out[0].Findings) != 2 {
		t.Fatalf("want 2 findings after strip, got %d: %#v", len(out[0].Findings), out[0].Findings)
	}
	if out[0].Findings[0].Comment != "keep inline" || out[0].Findings[1].Comment != "general" {
		t.Fatalf("unexpected findings: %#v", out[0].Findings)
	}
	if len(specs[0].Findings) != 3 {
		t.Fatal("SpecialistsForVibeCoach must not mutate input slice")
	}
}

func TestSpecialistsForVibeCoachNilDraftUnchanged(t *testing.T) {
	specs := []SpecialistResult{{Specialist: SpecDocs, Findings: []Finding{{Path: "a.go", Line: 1}}}}
	out := SpecialistsForVibeCoach(nil, specs)
	if len(out) != 1 || len(out[0].Findings) != 1 {
		t.Fatalf("got %#v", out)
	}
}

func TestFlatPostableFindingsForPostUserSkipKeys(t *testing.T) {
	fSkip := Finding{Path: "e.go", Line: 1, Severity: SeverityInfo, Comment: "skip"}
	fPost := Finding{Path: "e.go", Line: 2, Severity: SeverityInfo, Comment: "post"}
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{fSkip, fPost}},
		},
		UserSkipPostKeys: map[string]struct{}{
			FindingSuppressionKey(SpecDocs, fSkip): {},
		},
	}
	post := d.FlatPostableFindingsForPost()
	if len(post) != 1 || post[0].Finding.Line != 2 {
		t.Fatalf("got %#v", post)
	}
}

func TestFlatPostableFindingsForPostArbiterAndUserSkip(t *testing.T) {
	a := Finding{Path: "a.go", Line: 1, Severity: SeverityInfo, Comment: "arb"}
	b := Finding{Path: "b.go", Line: 1, Severity: SeverityInfo, Comment: "user"}
	c := Finding{Path: "c.go", Line: 1, Severity: SeverityInfo, Comment: "stay"}
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{a, b, c}},
		},
		RepoArbiter: &RepoArbiterResult{
			suppressKeySet: map[string]struct{}{
				suppressionKey(SpecDocs, "a.go", 1, "RIGHT"): {},
			},
		},
		UserSkipPostKeys: map[string]struct{}{
			FindingSuppressionKey(SpecDocs, b): {},
		},
	}
	post := d.FlatPostableFindingsForPost()
	if len(post) != 1 || post[0].Finding.Path != "c.go" {
		t.Fatalf("got %#v", post)
	}
}

func TestHasNoFindings(t *testing.T) {
	cases := []struct {
		name string
		d    *Draft
		want bool
	}{
		{
			name: "nil draft",
			d:    nil,
			want: false,
		},
		{
			name: "empty draft",
			d:    &Draft{PR: &gh.PR{HeadSHA: "abc"}},
			want: true,
		},
		{
			name: "vibe-coach verdict only, no prompts",
			d: &Draft{
				PR:        &gh.PR{HeadSHA: "abc"},
				VibeCoach: &VibeCoachResult{Verdict: VibeVerdictComment},
			},
			want: true,
		},
		{
			name: "inline finding present",
			d: &Draft{
				PR: &gh.PR{HeadSHA: "abc"},
				Specialists: []SpecialistResult{
					{Specialist: SpecDocs, Findings: []Finding{
						{Path: "a.go", Line: 1, Comment: "x", Severity: SeverityInfo},
					}},
				},
			},
			want: false,
		},
		{
			name: "general finding only",
			d: &Draft{
				PR: &gh.PR{HeadSHA: "abc"},
				Specialists: []SpecialistResult{
					{Specialist: SpecDocs, Findings: []Finding{
						{Path: "", Line: 0, Comment: "pr-wide", Severity: SeverityInfo},
					}},
				},
			},
			want: false,
		},
		{
			name: "agent failure",
			d: &Draft{
				PR: &gh.PR{HeadSHA: "abc"},
				Specialists: []SpecialistResult{
					{Specialist: SpecDocs, Err: fmtErrorf("boom")},
				},
			},
			want: false,
		},
		{
			name: "vibe-coach prompt present",
			d: &Draft{
				PR: &gh.PR{HeadSHA: "abc"},
				VibeCoach: &VibeCoachResult{
					Verdict: VibeVerdictComment,
					Prompts: []AuthorPrompt{{Title: "T", AgentPrompt: "do x"}},
				},
			},
			want: false,
		},
		{
			name: "repo arbiter suppression present",
			d: &Draft{
				PR: &gh.PR{HeadSHA: "abc"},
				RepoArbiter: &RepoArbiterResult{
					Suppressed: []SuppressedFindingRef{{Specialist: SpecDocs, Path: "a.go", Line: 1}},
				},
			},
			want: false,
		},
		{
			name: "vibe-coach summary is content",
			d: &Draft{
				PR: &gh.PR{HeadSHA: "abc"},
				VibeCoach: &VibeCoachResult{
					Verdict: VibeVerdictApprove,
					Summary: "Looks good, ship it.",
				},
			},
			want: false,
		},
		{
			name: "vibe-coach request_changes verdict alone",
			d: &Draft{
				PR:        &gh.PR{HeadSHA: "abc"},
				VibeCoach: &VibeCoachResult{Verdict: VibeVerdictRequestChanges},
			},
			want: false,
		},
		{
			name: "repo arbiter user summary",
			d: &Draft{
				PR:          &gh.PR{HeadSHA: "abc"},
				RepoArbiter: &RepoArbiterResult{UserSummary: "Note from experts."},
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.d.HasNoFindings(); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// fmtErrorf is a tiny helper kept local to the review tests so the no-findings
// test can construct an error without dragging in the fmt import everywhere.
func fmtErrorf(s string) error {
	return errSimple(s)
}

type errSimple string

func (e errSimple) Error() string { return string(e) }
