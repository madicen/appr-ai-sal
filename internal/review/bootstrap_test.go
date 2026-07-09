package review

import (
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

func TestBootstrapPRMatches(t *testing.T) {
	ref := gh.Ref{Owner: "o", Repo: "r", Number: 7}
	if bootstrapPRMatches(ref, nil) {
		t.Fatal("nil PR should not match")
	}
	if bootstrapPRMatches(ref, &gh.PR{Owner: "x", Repo: "r", Number: 7}) {
		t.Fatal("owner mismatch")
	}
	if !bootstrapPRMatches(ref, &gh.PR{Owner: "o", Repo: "r", Number: 7}) {
		t.Fatal("expected match")
	}
}
