package gh

import (
	"errors"
	"strings"
	"testing"
)

// TestParseGHError_LineCouldNotBeResolved confirms we extract status, message,
// per-error fields, and human-readable hint from gh's combined output for the
// real-world 422 the user reported. This is the failure mode that motivates
// the whole errors.go file, so it gets a wide range of assertions.
func TestParseGHError_LineCouldNotBeResolved(t *testing.T) {
	raw := []byte(`gh api repos/StackAdapt/product-crawler/pulls/23/comments: {"message":"Validation Failed","errors":[{"resource":"PullRequestReviewComment","code":"custom","field":"pull_request_review_thread.line","message":"could not be resolved"}],"documentation_url":"https://docs.github.com/rest/pulls/comments#create-a-review-comment-for-a-pull-request","status":"422"}
gh: Validation Failed (HTTP 422)`)

	ae := parseGHError(raw, "repos/StackAdapt/product-crawler/pulls/23/comments")
	if ae == nil {
		t.Fatal("parseGHError returned nil")
	}
	if ae.Status != 422 {
		t.Errorf("want status 422, got %d", ae.Status)
	}
	if ae.Message != "Validation Failed" {
		t.Errorf("want top-level message %q, got %q", "Validation Failed", ae.Message)
	}
	if len(ae.Errors) != 1 {
		t.Fatalf("want 1 error item, got %d", len(ae.Errors))
	}
	if ae.Errors[0].Field != "pull_request_review_thread.line" {
		t.Errorf("want field pull_request_review_thread.line, got %q", ae.Errors[0].Field)
	}
	if ae.Errors[0].Message != "could not be resolved" {
		t.Errorf("want item message 'could not be resolved', got %q", ae.Errors[0].Message)
	}
	if ae.HumanReason == "" {
		t.Error("want a human-readable hint for the line-resolved failure")
	}
	if !strings.Contains(strings.ToLower(ae.HumanReason), "refresh") {
		t.Errorf("hint should mention refresh, got %q", ae.HumanReason)
	}
	if !IsLineUnresolvable(ae) {
		t.Error("IsLineUnresolvable should match this error")
	}
	// Wrapping with errors.As must continue to work.
	wrapped := error(ae)
	if !IsLineUnresolvable(wrapped) {
		t.Error("IsLineUnresolvable should match wrapped error")
	}
}

// TestParseGHError_NoJSON covers the path where gh prints a non-JSON error
// (e.g. network failure, "could not connect"). We should still produce a
// non-nil APIError with the raw message and status 0.
func TestParseGHError_NoJSON(t *testing.T) {
	raw := []byte("gh: connection refused\n")
	ae := parseGHError(raw, "repos/x/y/pulls/1/comments")
	if ae == nil {
		t.Fatal("parseGHError returned nil")
	}
	if ae.Status != 0 {
		t.Errorf("want status 0 when no JSON present, got %d", ae.Status)
	}
	if !strings.Contains(ae.Message, "connection refused") {
		t.Errorf("want raw message preserved, got %q", ae.Message)
	}
	if IsLineUnresolvable(ae) {
		t.Error("non-422 error should not be IsLineUnresolvable")
	}
}

// TestAPIError_ErrorIncludesCommentContext verifies that when CreatePullReviewComment
// attaches a Comment + CommitID, the formatted error names the file/line/side and
// commit so the user can see exactly which finding hit the failure.
func TestAPIError_ErrorIncludesCommentContext(t *testing.T) {
	ae := &APIError{
		Status:  422,
		Message: "Validation Failed",
		Errors: []APIErrorItem{{
			Field: "pull_request_review_thread.line", Message: "could not be resolved",
		}},
		Comment: &ReviewComment{
			Path: "pkg/foo/bar.go", Line: 42, Side: "RIGHT", Body: "x",
		},
		CommitID:    "abcdef0123456789",
		HumanReason: "PR was force-pushed",
	}
	got := ae.Error()
	for _, want := range []string{
		"GitHub 422", "Validation Failed",
		"pkg/foo/bar.go:42 (RIGHT)",
		"commit abcdef0",
		"could not be resolved",
		"PR was force-pushed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("APIError.Error() missing %q\nfull message:\n%s", want, got)
		}
	}
}

// TestHeadDriftError verifies the typed error message and IsHeadDrift helper.
func TestHeadDriftError(t *testing.T) {
	d := &HeadDriftError{Was: "1234567890abcdef", Now: "fedcba0987654321"}
	if !strings.Contains(d.Error(), "1234567") || !strings.Contains(d.Error(), "fedcba0") {
		t.Errorf("HeadDriftError.Error() should include short SHAs, got %q", d.Error())
	}
	if !strings.Contains(d.Error(), "Refresh the PR") {
		t.Errorf("HeadDriftError.Error() should mention refresh, got %q", d.Error())
	}
	if got, ok := IsHeadDrift(error(d)); !ok || got == nil {
		t.Error("IsHeadDrift should match a *HeadDriftError")
	}
	if _, ok := IsHeadDrift(errors.New("other error")); ok {
		t.Error("IsHeadDrift should not match unrelated errors")
	}
}

// TestParseGHError_NumericStatus exercises the alternate status field where
// GitHub returns the status as a JSON number rather than a string.
func TestParseGHError_NumericStatus(t *testing.T) {
	raw := []byte(`gh api repos/x/y/pulls/1/reviews: {"message":"Boom","status":500}
gh: server error (HTTP 500)`)
	ae := parseGHError(raw, "repos/x/y/pulls/1/reviews")
	if ae.Status != 500 {
		t.Errorf("want status 500, got %d", ae.Status)
	}
	if ae.Message != "Boom" {
		t.Errorf("want message Boom, got %q", ae.Message)
	}
}
