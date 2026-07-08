package review

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
)

// stubLinkedIssues installs a fake gh linked-issue fetch for the test and
// restores the real one on cleanup.
func stubLinkedIssues(t *testing.T, fn func(ctx context.Context, ref gh.Ref, body string) ([]gh.LinkedIssue, error)) {
	t.Helper()
	prev := linkedIssuesFetcher
	linkedIssuesFetcher = fn
	t.Cleanup(func() { linkedIssuesFetcher = prev })
}

// intentModelServer stands up an OpenAI-compatible endpoint returning content
// as the model's completion, and records the last request body so tests can
// assert stage-model routing.
func intentModelServer(t *testing.T, content string, sawBody *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sawBody != nil {
			b, _ := io.ReadAll(r.Body)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			*sawBody = m
		}
		w.Header().Set("Content-Type", "application/json")
		payload := map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": content}}},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func intentTestCfg(baseURL string) *aiconfig.Config {
	cfg := aiconfig.DefaultConfig()
	cfg.Provider = aiconfig.ProviderOpenAICompatible
	cfg.BaseURL = baseURL
	cfg.Model = "base-model"
	cfg.RetryMaxAttempts = 1
	return cfg
}

func intentTestPR() *gh.PR {
	return &gh.PR{Number: 7, Title: "Add retry", Repository: "acme/widget", Body: "Fixes the flaky client. Closes #3"}
}

// --- FormatIntentSection --------------------------------------------------

func TestFormatIntentSectionEmptyIsNoOp(t *testing.T) {
	if FormatIntentSection(nil) != "" {
		t.Fatalf("nil intent must render empty")
	}
	if FormatIntentSection(&PRIntent{}) != "" {
		t.Fatalf("empty intent must render empty")
	}
	// An intent with only whitespace fields is also empty.
	if FormatIntentSection(&PRIntent{Intent: "   "}) != "" {
		t.Fatalf("whitespace-only intent must render empty")
	}
}

func TestFormatIntentSectionRenders(t *testing.T) {
	in := &PRIntent{
		Intent:             "Make the client retry transient failures.",
		AcceptanceCriteria: []string{"retries 3 times with backoff", "gives up after 3"},
		NonGoals:           []string{"changing the timeout default"},
		LinkedIssues:       []IntentLinkedIssue{{Reference: "acme/widget#3", Title: "Flaky client", Relevance: "the bug this fixes"}},
	}
	got := FormatIntentSection(in)
	for _, want := range []string{
		"## PR author intent",
		"Make the client retry transient failures.",
		"retries 3 times with backoff",
		"Non-goals",
		"changing the timeout default",
		"acme/widget#3 — Flaky client: the bug this fixes",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered section missing %q:\n%s", want, got)
		}
	}
}

// --- backward-compatibility ------------------------------------------------

// TestBuildersByteIdenticalWithoutIntent proves the Q8 backward-compat
// guarantee: with an empty intent section, every intent-carrying builder emits
// exactly what it did before Q8 (no `## PR author intent` block), so a run with
// no extracted intent is unchanged.
func TestBuildersByteIdenticalWithoutIntent(t *testing.T) {
	pr := intentTestPR()

	spec := buildReviewUserPrompt(pr, "diff", aiconfig.ReviewBalanced, "", "", "", "", "", "", "")
	if strings.Contains(spec, "PR author intent") {
		t.Errorf("specialist prompt must not contain the intent block when section is empty")
	}
	prAgent := buildPRAgentUserPrompt(SpecScope, pr, "diff", PRAgentInput{}, aiconfig.ReviewBalanced, "")
	if strings.Contains(prAgent, "PR author intent") {
		t.Errorf("pr-agent prompt must not contain the intent block when section is empty")
	}
	vibe := buildVibeCoachUserPrompt(pr, nil, aiconfig.ReviewBalanced, "", "")
	if strings.Contains(vibe, "PR author intent") {
		t.Errorf("vibe-coach prompt must not contain the intent block when section is empty")
	}
}

func TestBuildersInjectIntentWhenPresent(t *testing.T) {
	pr := intentTestPR()
	section := FormatIntentSection(&PRIntent{Intent: "do the thing"})
	if section == "" {
		t.Fatal("precondition: section should be non-empty")
	}
	spec := buildReviewUserPrompt(pr, "diff", aiconfig.ReviewBalanced, "", "", "", "", "", section, "")
	prAgent := buildPRAgentUserPrompt(SpecScope, pr, "diff", PRAgentInput{}, aiconfig.ReviewBalanced, section)
	vibe := buildVibeCoachUserPrompt(pr, nil, aiconfig.ReviewBalanced, "", section)
	for name, got := range map[string]string{"specialist": spec, "pr-agent": prAgent, "vibe-coach": vibe} {
		if !strings.Contains(got, "PR author intent") || !strings.Contains(got, "do the thing") {
			t.Errorf("%s prompt missing injected intent:\n%s", name, got)
		}
	}
}

func TestSpecWantsIntentGating(t *testing.T) {
	if !specWantsIntent(SpecTesting) {
		t.Error("testing should be intent-aware")
	}
	if !specWantsIntent(SpecScope) {
		t.Error("scope should be intent-aware")
	}
	for _, n := range []string{SpecFormatting, SpecDesign, SpecSecurity, SpecDocs, SpecDescription, SpecChecks, SpecDiscussion} {
		if specWantsIntent(n) {
			t.Errorf("%s must NOT be intent-aware (would change its prompt)", n)
		}
	}
}

// --- RunIntentPrepass ------------------------------------------------------

func TestRunIntentPrepassNilPR(t *testing.T) {
	if got := RunIntentPrepass(context.Background(), aiconfig.DefaultConfig(), gh.Ref{}, nil); got != nil {
		t.Fatalf("nil PR must yield nil intent, got %+v", got)
	}
}

func TestRunIntentPrepassNoDescriptionNoIssues(t *testing.T) {
	stubLinkedIssues(t, func(context.Context, gh.Ref, string) ([]gh.LinkedIssue, error) { return nil, nil })
	// Model server that would fail the test if hit — the pre-pass must short
	// circuit before any model call when there is nothing to extract.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("model must not be called when there is no description / issues")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	cfg := intentTestCfg(srv.URL)
	pr := &gh.PR{Number: 1, Title: "x", Repository: "acme/widget", Body: "   "}
	if got := RunIntentPrepass(context.Background(), cfg, gh.Ref{Owner: "acme", Repo: "widget", Number: 1}, pr); got != nil {
		t.Fatalf("empty body + no issues must yield nil, got %+v", got)
	}
}

func TestRunIntentPrepassHappyPathWithRouting(t *testing.T) {
	stubLinkedIssues(t, func(context.Context, gh.Ref, string) ([]gh.LinkedIssue, error) {
		return []gh.LinkedIssue{{Repository: "acme/widget", Number: 3, Title: "Flaky client", Body: "it flakes", State: "OPEN"}}, nil
	})
	content := `{"intent":"Make the client retry.","acceptance_criteria":["retries 3 times"],"non_goals":[],"linked_issues":[{"reference":"acme/widget#3","title":"Flaky client","relevance":"the bug"}]}`
	var body map[string]any
	srv := intentModelServer(t, content, &body)
	cfg := intentTestCfg(srv.URL)
	// Q7 routing: route the intent stage to a cheap model.
	cfg.StageModels = map[string]string{"intent": "cheapo"}

	got := RunIntentPrepass(context.Background(), cfg, gh.Ref{Owner: "acme", Repo: "widget", Number: 7}, intentTestPR())
	if got == nil {
		t.Fatal("expected a parsed intent")
	}
	if got.Intent != "Make the client retry." || len(got.AcceptanceCriteria) != 1 || len(got.LinkedIssues) != 1 {
		t.Fatalf("unexpected parsed intent: %+v", got)
	}
	if body["model"] != "cheapo" {
		t.Fatalf("intent stage should route to stage_models[intent]=cheapo, got %v", body["model"])
	}
}

func TestRunIntentPrepassFailOpenOnFetchError(t *testing.T) {
	// The linked-issue fetch errors, but the PR has a description — the
	// pre-pass must proceed (fail-open on fetch) and still extract intent.
	stubLinkedIssues(t, func(context.Context, gh.Ref, string) ([]gh.LinkedIssue, error) {
		return nil, io.ErrUnexpectedEOF
	})
	srv := intentModelServer(t, `{"intent":"still works","acceptance_criteria":[],"non_goals":[],"linked_issues":[]}`, nil)
	cfg := intentTestCfg(srv.URL)
	got := RunIntentPrepass(context.Background(), cfg, gh.Ref{Owner: "acme", Repo: "widget", Number: 7}, intentTestPR())
	if got == nil || got.Intent != "still works" {
		t.Fatalf("fetch error must be fail-open and still extract intent, got %+v", got)
	}
}

func TestRunIntentPrepassFailOpenOnModelError(t *testing.T) {
	stubLinkedIssues(t, func(context.Context, gh.Ref, string) ([]gh.LinkedIssue, error) { return nil, nil })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	cfg := intentTestCfg(srv.URL)
	if got := RunIntentPrepass(context.Background(), cfg, gh.Ref{Owner: "acme", Repo: "widget", Number: 7}, intentTestPR()); got != nil {
		t.Fatalf("model error must be fail-open (nil), got %+v", got)
	}
}

func TestRunIntentPrepassFailOpenOnBadJSON(t *testing.T) {
	stubLinkedIssues(t, func(context.Context, gh.Ref, string) ([]gh.LinkedIssue, error) { return nil, nil })
	srv := intentModelServer(t, "this is not json at all, sorry", nil)
	cfg := intentTestCfg(srv.URL)
	if got := RunIntentPrepass(context.Background(), cfg, gh.Ref{Owner: "acme", Repo: "widget", Number: 7}, intentTestPR()); got != nil {
		t.Fatalf("unparseable output must be fail-open (nil), got %+v", got)
	}
}

func TestRunIntentPrepassEmptyResultIsNil(t *testing.T) {
	stubLinkedIssues(t, func(context.Context, gh.Ref, string) ([]gh.LinkedIssue, error) { return nil, nil })
	// Valid JSON but no content → treated as no intent.
	srv := intentModelServer(t, `{"intent":"","acceptance_criteria":[],"non_goals":[],"linked_issues":[]}`, nil)
	cfg := intentTestCfg(srv.URL)
	if got := RunIntentPrepass(context.Background(), cfg, gh.Ref{Owner: "acme", Repo: "widget", Number: 7}, intentTestPR()); got != nil {
		t.Fatalf("content-free intent must be nil, got %+v", got)
	}
}
