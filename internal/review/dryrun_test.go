package review

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

func testRef() gh.Ref { return gh.Ref{Owner: "o", Repo: "r", Number: 7} }

// TestInlineReviewComment covers the single source of truth for turning a
// finding into an inline comment payload: body disclosure + RIGHT-side default.
func TestInlineReviewComment(t *testing.T) {
	c := InlineReviewComment(SpecDocs, Finding{Path: "a.go", Line: 12, Comment: "fix it"})
	if c.Path != "a.go" || c.Line != 12 {
		t.Fatalf("path/line: got %s:%d want a.go:12", c.Path, c.Line)
	}
	if c.Side != "RIGHT" {
		t.Fatalf("side default: got %q want RIGHT", c.Side)
	}
	if !strings.Contains(c.Body, "appr-ai-sal") || !strings.Contains(c.Body, "fix it") {
		t.Fatalf("body should disclose tool + carry comment: %q", c.Body)
	}

	left := InlineReviewComment(SpecDocs, Finding{Path: "a.go", Line: 1, Side: "LEFT", Comment: "x"})
	if left.Side != "LEFT" {
		t.Fatalf("explicit side should be preserved, got %q", left.Side)
	}
}

// TestDryRunFullReview verifies the preview is the indented JSON of the review
// payload with the fixed title.
func TestDryRunFullReview(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{{
			Specialist: SpecDocs,
			Findings:   []Finding{{Path: "a.go", Line: 10, Comment: "inline", Severity: SeverityWarning}},
		}},
	}
	p := DryRunFullReview(d)
	if p.Title != "Dry-run: full review payload (not posted)" {
		t.Fatalf("title: %q", p.Title)
	}
	// Payload must be valid JSON decoding back to the same review event.
	var rev gh.Review
	if err := json.Unmarshal([]byte(p.Payload), &rev); err != nil {
		t.Fatalf("payload not valid JSON: %v\n%s", err, p.Payload)
	}
	if rev.CommitID != "abc" {
		t.Fatalf("payload commit id: got %q want abc", rev.CommitID)
	}
}

func TestDryRunSingleFinding(t *testing.T) {
	pr := &gh.PR{HeadSHA: "headsha"}
	p := DryRunSingleFinding(testRef(), pr, SpecDocs, Finding{Path: "a.go", Line: 9, Comment: "c"})
	if p.Title != "Dry-run: single comment (not posted)" {
		t.Fatalf("title: %q", p.Title)
	}
	for _, want := range []string{"POST o/r/pulls/7/comments", `"commit_id": "headsha"`, `"path": "a.go"`, `"line": 9`, `"side": "RIGHT"`} {
		if !strings.Contains(p.Payload, want) {
			t.Fatalf("payload missing %q:\n%s", want, p.Payload)
		}
	}
}

func TestDryRunFileLevelFinding(t *testing.T) {
	pr := &gh.PR{HeadSHA: "headsha"}
	p := DryRunFileLevelFinding(testRef(), pr, SpecDocs, Finding{Path: "a.go", Line: 42, Comment: "c"})
	if p.Title != "Dry-run: single file-level comment (not posted)" {
		t.Fatalf("title: %q", p.Title)
	}
	for _, want := range []string{"POST o/r/pulls/7/comments (file-level)", `"subject_type": "file"`, `"path": "a.go"`} {
		if !strings.Contains(p.Payload, want) {
			t.Fatalf("payload missing %q:\n%s", want, p.Payload)
		}
	}
	if strings.Contains(p.Payload, `"line"`) {
		t.Fatalf("file-level payload must not carry a line field:\n%s", p.Payload)
	}
}

// TestDryRunVerdictReview covers both the plain case and the self-author
// downgrade case (intendedEvent != event surfaces the coercion note + title).
func TestDryRunVerdictReview(t *testing.T) {
	plain := DryRunVerdictReview(testRef(), "abc", "REQUEST_CHANGES", "REQUEST_CHANGES", "body text")
	if plain.Title != "Dry-run: REQUEST_CHANGES review (not posted)" {
		t.Fatalf("plain title: %q", plain.Title)
	}
	if strings.Contains(plain.Payload, "NOTE: You are the PR author") {
		t.Fatalf("plain payload must not include the downgrade note:\n%s", plain.Payload)
	}
	if !strings.Contains(plain.Payload, "verdict event=REQUEST_CHANGES") {
		t.Fatalf("plain payload should name the event:\n%s", plain.Payload)
	}

	down := DryRunVerdictReview(testRef(), "abc", "COMMENT", "REQUEST_CHANGES", "body text")
	if !strings.Contains(down.Title, "own PR: cannot submit REQUEST_CHANGES") {
		t.Fatalf("downgrade title should explain coercion: %q", down.Title)
	}
	if !strings.Contains(down.Payload, "NOTE: You are the PR author — GitHub rejects event=REQUEST_CHANGES; posting as COMMENT.") {
		t.Fatalf("downgrade payload should include the note:\n%s", down.Payload)
	}
}

func TestDryRunApproveBare(t *testing.T) {
	plain := DryRunApproveBare(testRef(), "abc", "APPROVE", "APPROVE", "")
	if plain.Title != "Dry-run: APPROVE review · approve only (not posted)" {
		t.Fatalf("plain title: %q", plain.Title)
	}
	if !strings.Contains(plain.Payload, "approve-only") {
		t.Fatalf("plain payload should mark approve-only:\n%s", plain.Payload)
	}

	down := DryRunApproveBare(testRef(), "abc", "COMMENT", "APPROVE", "note only")
	if !strings.Contains(down.Title, "note-only comment") {
		t.Fatalf("downgrade title should mention note-only comment: %q", down.Title)
	}
	if !strings.Contains(down.Payload, "posting as COMMENT") {
		t.Fatalf("downgrade payload should include coercion note:\n%s", down.Payload)
	}
}
