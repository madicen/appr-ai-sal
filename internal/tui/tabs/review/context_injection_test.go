package review

import (
	"errors"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/review"
)

// The Context-injection group is a synthetic group at the top of the
// running view. Three rows live there (language briefs, tech experts,
// repo experts) and they resolve from the lang-agents / tech-agents /
// repo-agents Progress events the runner emits before any specialist
// starts.

func TestNewReviewOverlayHasContextInjectionRowsAtTopOfPipeline(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	wantOrder := []string{
		overlayAgentLangBriefs,
		overlayAgentTechExperts,
		overlayAgentRepoExperts,
		// then the five specialists, then arbiter, then vibe — those
		// are pinned by AllSpecialists order; we only check the head.
	}
	if len(ro.agents) < len(wantOrder) {
		t.Fatalf("overlay should expose at least %d agent rows, got %d", len(wantOrder), len(ro.agents))
	}
	for i, want := range wantOrder {
		got := ro.agents[i]
		if got.name != want {
			t.Fatalf("row %d: got %q want %q", i, got.name, want)
		}
		if got.stage != stageGroupContextInjection {
			t.Fatalf("row %q: stage %v want stageGroupContextInjection", got.name, got.stage)
		}
		if got.phase != oaPending {
			t.Fatalf("row %q: phase %v want oaPending", got.name, got.phase)
		}
	}
}

func TestStageGroupOrderListsContextInjectionFirst(t *testing.T) {
	if len(stageGroupOrder) == 0 || stageGroupOrder[0] != stageGroupContextInjection {
		t.Fatalf("stageGroupOrder must start with stageGroupContextInjection, got %v", stageGroupOrder)
	}
}

func TestMergeProgressLangAgentsInjectedMarksRowDone(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.mergeProgress(review.Progress{Stage: "lang-agents", Detail: "injected go+python"})

	i := ro.agentIndex(overlayAgentLangBriefs)
	if i < 0 {
		t.Fatal("language-briefs row missing")
	}
	row := ro.agents[i]
	if row.phase != oaDone {
		t.Fatalf("phase: got %v want oaDone", row.phase)
	}
	if row.findingsN != 2 {
		t.Fatalf("findingsN: got %d want 2 (go+python)", row.findingsN)
	}
	if !strings.Contains(row.summary, "injected go+python") {
		t.Fatalf("summary should preserve the detail, got %q", row.summary)
	}
}

func TestMergeProgressTechAgentsInjectedMarksRowDone(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.mergeProgress(review.Progress{Stage: "tech-agents", Detail: "injected Kestra+Terraform"})

	row := ro.agents[ro.agentIndex(overlayAgentTechExperts)]
	if row.phase != oaDone {
		t.Fatalf("phase: got %v want oaDone", row.phase)
	}
	if row.findingsN != 2 {
		t.Fatalf("findingsN: got %d want 2 (kestra+terraform)", row.findingsN)
	}
}

func TestMergeProgressRepoAgentsLoadedMarksRowDone(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.mergeProgress(review.Progress{Stage: "repo-agents", Detail: "loaded 3 brief(s)"})

	row := ro.agents[ro.agentIndex(overlayAgentRepoExperts)]
	if row.phase != oaDone {
		t.Fatalf("phase: got %v want oaDone", row.phase)
	}
	if row.findingsN != 3 {
		t.Fatalf("findingsN: got %d want 3", row.findingsN)
	}
}

func TestMergeProgressNoneMarksRowSkipped(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.mergeProgress(review.Progress{Stage: "tech-agents", Detail: "none"})
	row := ro.agents[ro.agentIndex(overlayAgentTechExperts)]
	if row.phase != oaSkipped {
		t.Fatalf("phase: got %v want oaSkipped", row.phase)
	}
}

func TestMergeProgressDisabledMarksRowSkipped(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.mergeProgress(review.Progress{Stage: "tech-agents", Detail: "disabled"})
	row := ro.agents[ro.agentIndex(overlayAgentTechExperts)]
	if row.phase != oaSkipped {
		t.Fatalf("phase: got %v want oaSkipped", row.phase)
	}
}

func TestMergeProgressWarningMarksRowError(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.mergeProgress(review.Progress{Stage: "repo-agents", Detail: "warning: file unreadable"})
	row := ro.agents[ro.agentIndex(overlayAgentRepoExperts)]
	if row.phase != oaErr {
		t.Fatalf("phase: got %v want oaErr", row.phase)
	}
	if row.err == nil || !strings.Contains(row.err.Error(), "file unreadable") {
		t.Fatalf("expected wrapped warning text, got %v", row.err)
	}
}

func TestMergeProgressErrFieldOnContextInjectionMarksRowError(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.mergeProgress(review.Progress{Stage: "tech-agents", Err: errors.New("disk full")})
	row := ro.agents[ro.agentIndex(overlayAgentTechExperts)]
	if row.phase != oaErr {
		t.Fatalf("phase: got %v want oaErr", row.phase)
	}
}

func TestRunningBodyRendersContextInjectionGroupHeader(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	body := ro.renderRunningBody()
	if !strings.Contains(body, "Context injection") {
		t.Fatalf("running body should render the Context injection group header:\n%s", body)
	}
	// Tags follow the lowercase convention used by the specialist tags
	// (formatting, design, ...): see styles.go styles.RenderTag().
	for _, label := range []string{"language briefs", "tech experts", "repo experts"} {
		if !strings.Contains(body, label) {
			t.Fatalf("running body should render row label %q:\n%s", label, body)
		}
	}
}

func TestParseLoadedBriefCount(t *testing.T) {
	cases := map[string]int{
		"loaded 0 brief(s)":  0,
		"loaded 1 brief(s)":  1,
		"loaded 7 brief(s)":  7,
		"loaded 12 brief(s)": 12,
		"":                   0,
		"none":               0,
	}
	for in, want := range cases {
		if got := parseLoadedBriefCount(in); got != want {
			t.Errorf("parseLoadedBriefCount(%q): got %d want %d", in, got, want)
		}
	}
}

func TestCountInjectedItems(t *testing.T) {
	cases := map[string]int{
		"injected go":                                 1,
		"injected go+python":                          2,
		"injected go+python+rust":                     3,
		"injected go, python":                         2,
		"injected Kestra+Terraform; missing: airflow": 2,
		"":          0,
		"injected ": 0,
	}
	for in, want := range cases {
		if got := countInjectedItems(in); got != want {
			t.Errorf("countInjectedItems(%q): got %d want %d", in, got, want)
		}
	}
}

func TestMergeProgressFetchPRErrSurfacesRunFailure(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	err := errors.New(`GraphQL: Fragment on User can't be spread inside ReviewRequest`)
	ro.mergeProgress(review.Progress{Stage: "fetch-pr", Err: err})
	if ro.runErr != err {
		t.Fatalf("runErr = %v, want %v", ro.runErr, err)
	}
	body := ro.renderRunningBody()
	if !strings.Contains(body, "Run failed:") {
		t.Fatalf("running body should surface run failure:\n%s", body)
	}
	ro.OnRunClosed()
	if ro.runErr == nil {
		t.Fatal("OnRunClosed should not clear an existing runErr")
	}
}
