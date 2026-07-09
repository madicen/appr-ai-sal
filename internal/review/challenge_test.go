package review

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review/memory"
)

// withStubbedChallengeComplete swaps challengeComplete for a deterministic
// completer for the duration of the test and returns a restore func. It also
// captures the last (system, user) prompt pair the caller built so tests can
// assert the persona/finding/hunk/transcript made it into the request.
func withStubbedChallengeComplete(t *testing.T, reply func(system, user string) (string, error)) (sys, user *string) {
	t.Helper()
	sys = new(string)
	user = new(string)
	prev := challengeComplete
	challengeComplete = func(_ context.Context, _ *aiconfig.Config, system, u string) (string, error) {
		*sys = system
		*user = u
		return reply(system, u)
	}
	t.Cleanup(func() { challengeComplete = prev })
	return sys, user
}

func testFinding() Finding {
	return Finding{
		Path:     "internal/api/handler.go",
		Line:     42,
		Side:     "RIGHT",
		Severity: SeverityWarning,
		Comment:  "This handler ignores the context cancellation error.",
	}
}

func TestChallengeFindingWithdrawParse(t *testing.T) {
	sys, user := withStubbedChallengeComplete(t, func(system, u string) (string, error) {
		return `{"decision":"withdraw","justification":"You're right, the error is handled upstream."}`, nil
	})
	resp, err := ChallengeFinding(context.Background(), aiconfig.DefaultConfig(), SpecSecurity, testFinding(), "@@ -1,2 +1,3 @@\n+ 42| do()", nil, "This is handled by the caller.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Withdrawn() {
		t.Fatalf("expected withdraw, got %q", resp.Decision)
	}
	if !strings.Contains(*sys, "CHALLENGING") {
		t.Fatalf("system prompt should splice the challenge addendum onto the persona:\n%s", *sys)
	}
	if !strings.Contains(*user, "handler.go") || !strings.Contains(*user, "42| do()") {
		t.Fatalf("user prompt should carry the finding + hunk:\n%s", *user)
	}
}

func TestChallengeFindingUpholdParseWithRevisions(t *testing.T) {
	withStubbedChallengeComplete(t, func(system, u string) (string, error) {
		return `{"decision":"uphold","justification":"The cancellation path still drops the error at line 42.","revised_comment":"Return ctx.Err() instead of nil here.","revised_severity":"error"}`, nil
	})
	resp, err := ChallengeFinding(context.Background(), aiconfig.DefaultConfig(), SpecDesign, testFinding(), "", nil, "Why does this matter?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Withdrawn() {
		t.Fatal("expected uphold, got withdraw")
	}
	if resp.RevisedComment != "Return ctx.Err() instead of nil here." {
		t.Fatalf("revised comment not parsed: %q", resp.RevisedComment)
	}
	if resp.RevisedSeverity != SeverityError {
		t.Fatalf("revised severity not parsed/normalized: %q", resp.RevisedSeverity)
	}
}

// A multi-turn challenge must carry the prior transcript into the next scoped
// call so the specialist sees the running conversation.
func TestChallengeFindingCarriesTranscript(t *testing.T) {
	_, user := withStubbedChallengeComplete(t, func(system, u string) (string, error) {
		return `{"decision":"withdraw","justification":"ok"}`, nil
	})
	transcript := []ChallengeTurn{
		{
			Question: "First question about the finding",
			Response: ChallengeResponse{Decision: ChallengeUphold, Justification: "Standing by it for now"},
		},
	}
	_, err := ChallengeFinding(context.Background(), aiconfig.DefaultConfig(), SpecTesting, testFinding(), "", transcript, "Second follow-up question")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(*user, "First question about the finding") {
		t.Fatalf("prior transcript question must be in the follow-up prompt:\n%s", *user)
	}
	if !strings.Contains(*user, "Standing by it for now") {
		t.Fatalf("prior transcript response must be in the follow-up prompt:\n%s", *user)
	}
	if !strings.Contains(*user, "Second follow-up question") {
		t.Fatalf("the new question must be in the prompt:\n%s", *user)
	}
}

// A model/transport error is returned verbatim so the caller can disclose it
// and leave the finding unchanged (fail-open).
func TestChallengeFindingFailOpenOnError(t *testing.T) {
	withStubbedChallengeComplete(t, func(system, u string) (string, error) {
		return "", errors.New("boom")
	})
	_, err := ChallengeFinding(context.Background(), aiconfig.DefaultConfig(), SpecFormatting, testFinding(), "", nil, "q")
	if err == nil {
		t.Fatal("expected an error to propagate")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("underlying error should be wrapped: %v", err)
	}
}

func TestChallengeFindingEmptyQuestionRejected(t *testing.T) {
	if _, err := ChallengeFinding(context.Background(), aiconfig.DefaultConfig(), SpecSecurity, testFinding(), "", nil, "   "); err == nil {
		t.Fatal("expected an error for an empty question")
	}
}

// parseChallengeResponse folds an unknown/blank decision to uphold (never
// silently dropping a finding) and normalizes a revised severity synonym.
func TestParseChallengeResponseNormalization(t *testing.T) {
	resp, err := parseChallengeResponse(`{"decision":"maybe","justification":"unsure","revised_severity":"blocker"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Decision != ChallengeUphold {
		t.Fatalf("unknown decision must default to uphold, got %q", resp.Decision)
	}
	if resp.RevisedSeverity != SeverityCritical {
		t.Fatalf("blocker should normalize to critical, got %q", resp.RevisedSeverity)
	}
	// A blank revised severity stays empty (distinguishable from a deliberate
	// re-grade, so an uphold with no severity change leaves the card alone).
	resp2, _ := parseChallengeResponse(`{"decision":"uphold","justification":"x"}`)
	if resp2.RevisedSeverity != "" {
		t.Fatalf("omitted revised severity must stay empty, got %q", resp2.RevisedSeverity)
	}
}

// The generated challenge contract must splice the registry-sourced severity
// ladder (Q2 drift-proofing) and register a non-empty schema.
func TestChallengeContractAndSchema(t *testing.T) {
	if !strings.Contains(challengeOutputContract, severityLadderEnum()) {
		t.Fatalf("challenge contract must splice the severity ladder enum:\n%s", challengeOutputContract)
	}
	if len(challengeSchema()) == 0 {
		t.Fatal("challenge schema must be non-empty")
	}
	if !strings.Contains(string(challengeSchema()), string(ChallengeWithdraw)) {
		t.Fatalf("challenge schema decision enum must list withdraw:\n%s", challengeSchema())
	}
}

// A challenge withdrawal folds a negative (skip) signal into reviewer memory,
// exactly like a manual skip, so a repeatedly-withdrawn pattern drives
// suppression on later runs.
func TestRecordChallengeWithdrawalFeedsMemory(t *testing.T) {
	t.Setenv("APPR_AI_SAL_CACHE_DIR", t.TempDir())
	pr := &gh.PR{Owner: "acme", Repo: "widget", Repository: "acme/widget"}
	f := Finding{Path: "svc/x.go", Line: 3, Side: "RIGHT", Severity: SeverityWarning, Comment: "this is speculative"}
	for i := 0; i < memory.DefaultSuppressThreshold; i++ {
		RecordChallengeWithdrawal(pr, SpecDesign, f)
	}
	mem := LoadRepoMemory(pr)
	fp := memory.NewFingerprint(SpecDesign, f.Path, f.Comment, string(f.Severity))
	if got := mem.SkipCount(fp); got != memory.DefaultSuppressThreshold {
		t.Fatalf("expected %d skip signals from withdrawals, got %d", memory.DefaultSuppressThreshold, got)
	}
	if !mem.ShouldSuppress(fp, memory.DefaultSuppressThreshold) {
		t.Fatal("repeated challenge-withdrawals should drive suppression on later runs")
	}
}

// A blank repo key is a no-op (fail-open) rather than a panic.
func TestRecordChallengeWithdrawalNoRepoKeyIsNoop(t *testing.T) {
	RecordChallengeWithdrawal(nil, SpecDesign, testFinding())
	RecordChallengeWithdrawal(&gh.PR{}, SpecDesign, testFinding())
}
