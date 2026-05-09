package gh

import "testing"

func TestViewerHasMatchingComment(t *testing.T) {
	t.Parallel()
	v := "alice"
	body := "**AI-generated review comment** — tool: **appr-ai-sal**, agent: **formatting**\n\nhello"
	existing := []PullReviewComment{
		{AuthorLogin: "alice", Path: "pkg/foo.go", Line: 10, Side: "RIGHT", Body: body},
	}
	if !ViewerHasMatchingComment(v, "pkg/foo.go", 10, "RIGHT", body, existing) {
		t.Fatal("expected match")
	}
	if ViewerHasMatchingComment(v, "pkg/foo.go", 11, "RIGHT", body, existing) {
		t.Fatal("line mismatch should not match")
	}
	if ViewerHasMatchingComment("bob", "pkg/foo.go", 10, "RIGHT", body, existing) {
		t.Fatal("author mismatch should not match")
	}
	if ViewerHasMatchingComment(v, "pkg/foo.go", 10, "RIGHT", body+"x", existing) {
		t.Fatal("body mismatch should not match")
	}
}
