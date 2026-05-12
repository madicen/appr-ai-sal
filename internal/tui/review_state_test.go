package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

func mkPR(num int, updated time.Time, rs gh.ReviewState) gh.PR {
	return gh.PR{
		Number:      num,
		Title:       "t",
		Repository:  "o/r",
		Owner:       "o",
		Repo:        "r",
		Author:      "a",
		UpdatedAt:   updated,
		ReviewState: rs,
	}
}

func TestSortPRsByActionability_Ordering(t *testing.T) {
	now := time.Now()
	// One PR per tier. Within tier ordering covered below.
	needsYouDirect := mkPR(1, now, gh.ReviewState{
		Decision:             gh.ReviewDecisionReviewRequired,
		ViewerStillRequested: true,
	})
	needsYouTeam := mkPR(2, now, gh.ReviewState{
		Decision: gh.ReviewDecisionReviewRequired,
	})
	youReviewed := mkPR(3, now, gh.ReviewState{
		Decision:          gh.ReviewDecisionReviewRequired,
		ViewerHasReviewed: true,
	})
	changesRequested := mkPR(4, now, gh.ReviewState{
		Decision:         gh.ReviewDecisionChangesRequested,
		ChangesRequested: 1,
	})
	youApproved := mkPR(5, now, gh.ReviewState{
		Decision:          gh.ReviewDecisionReviewRequired,
		ViewerHasReviewed: true,
		ViewerHasApproved: true,
		Approvals:         1,
	})
	fullyApproved := mkPR(6, now, gh.ReviewState{
		Decision:  gh.ReviewDecisionApproved,
		Approvals: 2,
	})
	// Scramble input order so we know the sort moved them.
	input := []gh.PR{fullyApproved, youApproved, changesRequested, youReviewed, needsYouTeam, needsYouDirect}
	got := sortPRsByActionability(input)
	wantOrder := []int{1, 2, 3, 4, 5, 6}
	for i, want := range wantOrder {
		if got[i].Number != want {
			t.Fatalf("position %d = #%d, want #%d (full order: %v)", i, got[i].Number, want, prNums(got))
		}
	}
}

func TestSortPRsByActionability_WithinTierUpdatedAtDesc(t *testing.T) {
	now := time.Now()
	older := mkPR(10, now.Add(-2*time.Hour), gh.ReviewState{
		Decision:             gh.ReviewDecisionReviewRequired,
		ViewerStillRequested: true,
	})
	newer := mkPR(11, now, gh.ReviewState{
		Decision:             gh.ReviewDecisionReviewRequired,
		ViewerStillRequested: true,
	})
	got := sortPRsByActionability([]gh.PR{older, newer})
	if got[0].Number != 11 || got[1].Number != 10 {
		t.Fatalf("within-tier order wrong: %v", prNums(got))
	}
}

func TestSortPRsByActionability_DoesNotMutateInput(t *testing.T) {
	now := time.Now()
	in := []gh.PR{
		mkPR(1, now, gh.ReviewState{Decision: gh.ReviewDecisionApproved}),
		mkPR(2, now, gh.ReviewState{Decision: gh.ReviewDecisionReviewRequired, ViewerStillRequested: true}),
	}
	original := []gh.PR{in[0], in[1]}
	_ = sortPRsByActionability(in)
	for i := range in {
		if in[i].Number != original[i].Number {
			t.Fatalf("input mutated at %d: %d -> %d", i, original[i].Number, in[i].Number)
		}
	}
}

func TestReviewStateBadge(t *testing.T) {
	cases := []struct {
		name     string
		rs       gh.ReviewState
		contains string
	}{
		{"approved", gh.ReviewState{Decision: gh.ReviewDecisionApproved, Approvals: 2}, "approved"},
		{"changes requested decision", gh.ReviewState{Decision: gh.ReviewDecisionChangesRequested}, "changes requested"},
		{"changes requested count only", gh.ReviewState{ChangesRequested: 1}, "changes requested"},
		{"approval + more needed", gh.ReviewState{Decision: gh.ReviewDecisionReviewRequired, Approvals: 1}, "more needed"},
		{"no review", gh.ReviewState{Decision: gh.ReviewDecisionReviewRequired}, "no review"},
		{"empty", gh.ReviewState{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reviewStateBadge(tc.rs)
			if tc.contains == "" {
				if got != "" {
					t.Fatalf("expected empty badge, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.contains) {
				t.Fatalf("badge %q does not contain %q", got, tc.contains)
			}
		})
	}
}

func TestViewerActionBadge(t *testing.T) {
	cases := []struct {
		name     string
		rs       gh.ReviewState
		contains string
	}{
		{"you approved", gh.ReviewState{ViewerHasReviewed: true, ViewerHasApproved: true}, "you approved"},
		{"you reviewed", gh.ReviewState{ViewerHasReviewed: true}, "you reviewed"},
		{"needs you direct", gh.ReviewState{Decision: gh.ReviewDecisionReviewRequired, ViewerStillRequested: true}, "needs you"},
		{"needs you team", gh.ReviewState{Decision: gh.ReviewDecisionReviewRequired}, "needs you (team)"},
		{"pr approved, viewer hasnt reviewed", gh.ReviewState{Decision: gh.ReviewDecisionApproved}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := viewerActionBadge(tc.rs)
			if tc.contains == "" {
				if got != "" {
					t.Fatalf("expected empty badge, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.contains) {
				t.Fatalf("badge %q does not contain %q", got, tc.contains)
			}
		})
	}
}

func TestPRItemDescriptionIncludesBadges(t *testing.T) {
	pr := mkPR(42, time.Now(), gh.ReviewState{
		Decision:             gh.ReviewDecisionReviewRequired,
		ViewerStillRequested: true,
	})
	desc := prItem{pr: pr}.Description()
	if !strings.Contains(desc, "no review") {
		t.Fatalf("description missing approval-state badge: %q", desc)
	}
	if !strings.Contains(desc, "needs you") {
		t.Fatalf("description missing viewer-action badge: %q", desc)
	}
}

func prNums(prs []gh.PR) []int {
	out := make([]int, 0, len(prs))
	for _, p := range prs {
		out = append(out, p.Number)
	}
	return out
}
