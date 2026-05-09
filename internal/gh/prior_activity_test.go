package gh

import (
	"testing"
	"time"
)

func TestDetectPriorAprrAISalActivityFromMatchesInlineMarker(t *testing.T) {
	now := time.Now().UTC()
	comments := []PullReviewComment{
		{
			Body:        "**AI-generated review comment** — tool: **appr-ai-sal**, agent: **docs**\n\nSomething.",
			Path:        "main.go",
			Line:        10,
			AuthorLogin: "alice",
			CreatedAt:   now.Add(-2 * time.Hour),
		},
		{
			Body:        "Just a normal human comment.",
			Path:        "main.go",
			Line:        12,
			AuthorLogin: "alice",
			CreatedAt:   now,
		},
	}
	got := DetectPriorAprrAISalActivityFrom(comments, nil, "alice")
	if !got.Found() {
		t.Fatal("Found() should be true when an inline appr-ai-sal comment is present")
	}
	if got.InlineCount != 1 {
		t.Fatalf("InlineCount: got %d want 1", got.InlineCount)
	}
	if got.LastAt.IsZero() {
		t.Fatal("LastAt should be set to the matching comment's CreatedAt")
	}
}

func TestDetectPriorAprrAISalActivityFromMatchesReviewMarker(t *testing.T) {
	now := time.Now().UTC()
	reviews := []PullReviewRow{
		{
			PRNumber:    42,
			Author:      "alice",
			State:       "COMMENTED",
			Body:        "> **AI disclosure:** This review was produced by **appr-ai-sal** (automated AI tools).\n\nFindings below.",
			SubmittedAt: now.Add(-30 * time.Minute),
		},
	}
	got := DetectPriorAprrAISalActivityFrom(nil, reviews, "alice")
	if !got.Found() {
		t.Fatal("Found() should be true when a review-body marker matches")
	}
	if got.ReviewCount != 1 {
		t.Fatalf("ReviewCount: got %d want 1", got.ReviewCount)
	}
	if got.LastSummarySnippet == "" {
		t.Fatal("LastSummarySnippet should be populated from the matching review body")
	}
}

func TestDetectPriorAprrAISalActivityFromIgnoresOtherAuthors(t *testing.T) {
	comments := []PullReviewComment{
		{
			Body:        "**AI-generated review comment** — tool: **appr-ai-sal**, agent: **docs**\n\nFrom Bob's local run.",
			Path:        "main.go",
			Line:        10,
			AuthorLogin: "bob",
		},
	}
	got := DetectPriorAprrAISalActivityFrom(comments, nil, "alice")
	if got.Found() {
		t.Fatalf("comments authored by another viewer should not match for viewer=alice; got %+v", got)
	}
}

func TestDetectPriorAprrAISalActivityFromEmptyViewerMatchesAny(t *testing.T) {
	comments := []PullReviewComment{
		{
			Body:        "**AI-generated review comment** — tool: **appr-ai-sal**, agent: **docs**",
			Path:        "main.go",
			Line:        10,
			AuthorLogin: "carol",
		},
	}
	got := DetectPriorAprrAISalActivityFrom(comments, nil, "")
	if !got.Found() {
		t.Fatal("empty viewer should match any author so we still surface 'tool ran here before'")
	}
	if got.InlineCount != 1 {
		t.Fatalf("InlineCount: got %d want 1", got.InlineCount)
	}
}

func TestDetectPriorAprrAISalActivityFromIgnoresUnrelatedBodies(t *testing.T) {
	comments := []PullReviewComment{
		{Body: "human comment", AuthorLogin: "alice"},
		{Body: "another tool with its own marker", AuthorLogin: "alice"},
	}
	reviews := []PullReviewRow{
		{Author: "alice", State: "APPROVED", Body: "lgtm"},
	}
	got := DetectPriorAprrAISalActivityFrom(comments, reviews, "alice")
	if got.Found() {
		t.Fatalf("nothing should match without the disclosure markers; got %+v", got)
	}
}

func TestDetectPriorAprrAISalActivityFromTracksMostRecent(t *testing.T) {
	now := time.Now().UTC()
	comments := []PullReviewComment{
		{
			Body:        "tool: **appr-ai-sal** old",
			AuthorLogin: "alice",
			CreatedAt:   now.Add(-72 * time.Hour),
		},
	}
	reviews := []PullReviewRow{
		{
			Author:      "alice",
			Body:        "produced by **appr-ai-sal** newer",
			SubmittedAt: now.Add(-1 * time.Hour),
		},
	}
	got := DetectPriorAprrAISalActivityFrom(comments, reviews, "alice")
	if got.LastAt != now.Add(-1*time.Hour) {
		t.Fatalf("LastAt should track the most recent matching entry; got %v want %v", got.LastAt, now.Add(-1*time.Hour))
	}
}

func TestSnippetForBannerStripsDisclosureLines(t *testing.T) {
	body := "> **AI disclosure:** This review was produced by **appr-ai-sal** (automated AI tools). Verify everything before merging.\n\nThe actual summary line goes here.\n\nMore details."
	got := snippetForBanner(body)
	want := "The actual summary line goes here."
	if got != want {
		t.Fatalf("snippetForBanner: got %q want %q", got, want)
	}
}
