package demo

import (
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

// DemoChecks returns the canned status-check rollup for ref. Each fixture
// PR has a different mix (one mostly-passing, one with a dramatic CI
// failure) so the Checks tab in the demo GIF reads as a real review
// session rather than a single uniform "all green" scene.
func DemoChecks(ref gh.Ref) *gh.ChecksReport {
	pr := LookupPR(ref)
	if pr == nil {
		return &gh.ChecksReport{}
	}
	now := time.Date(2026, time.May, 14, 15, 30, 0, 0, time.UTC)
	switch {
	case strings.EqualFold(pr.Owner, "madicen") && strings.EqualFold(pr.Repo, "appr-ai-sal") && pr.Number == 318:
		return &gh.ChecksReport{
			HeadSHA:     pr.HeadSHA,
			RollupState: "FAILURE",
			Runs: []gh.CheckRun{
				{
					Name:        "go test ./...",
					App:         "GitHub Actions",
					Status:      "COMPLETED",
					Conclusion:  "FAILURE",
					State:       "FAILURE",
					Title:       "TestRenderTreeViewRowFolderTruncation failed",
					Summary:     "Folder names that exceed the pane width were wrapping onto a second blank row instead of being truncated. The new tree-view test reproduces the regression.",
					DetailsURL:  "https://github.com/madicen/appr-ai-sal/actions/runs/9001",
					StartedAt:   now.Add(-12 * time.Minute),
					CompletedAt: now.Add(-9 * time.Minute),
					Annotations: []gh.CheckRunAnnotation{
						{
							Path:    "internal/tui/model/pr_detail.go",
							Line:    274,
							Level:   "FAILURE",
							Message: "lipgloss.NewStyle().Width(contentCols) wrapped overflow onto a second padded line; expected a single ansi.Truncate'd row.",
						},
						{
							Path:    "internal/tui/model/tree_view_test.go",
							Line:    198,
							Level:   "FAILURE",
							Message: "want one rendered line for the folder row, got 2 (extra trailing line was all spaces).",
						},
					},
				},
				{
					Name:        "lint",
					App:         "GitHub Actions",
					Status:      "COMPLETED",
					Conclusion:  "SUCCESS",
					State:       "SUCCESS",
					DetailsURL:  "https://github.com/madicen/appr-ai-sal/actions/runs/9000",
					StartedAt:   now.Add(-13 * time.Minute),
					CompletedAt: now.Add(-12 * time.Minute),
				},
				{
					Name:        "build",
					App:         "GitHub Actions",
					Status:      "COMPLETED",
					Conclusion:  "SUCCESS",
					State:       "SUCCESS",
					DetailsURL:  "https://github.com/madicen/appr-ai-sal/actions/runs/8999",
					StartedAt:   now.Add(-14 * time.Minute),
					CompletedAt: now.Add(-12 * time.Minute),
				},
			},
		}
	case strings.EqualFold(pr.Owner, "madicen") && strings.EqualFold(pr.Repo, "appr-ai-sal") && pr.Number == 742:
		return &gh.ChecksReport{
			HeadSHA:     pr.HeadSHA,
			RollupState: "PENDING",
			Runs: []gh.CheckRun{
				{
					Name:       "integration",
					App:        "GitHub Actions",
					Status:     "IN_PROGRESS",
					DetailsURL: "https://github.com/madicen/appr-ai-sal/actions/runs/9100",
					StartedAt:  now.Add(-3 * time.Minute),
				},
				{
					Name:        "go test ./...",
					App:         "GitHub Actions",
					Status:      "COMPLETED",
					Conclusion:  "SUCCESS",
					State:       "SUCCESS",
					DetailsURL:  "https://github.com/madicen/appr-ai-sal/actions/runs/9099",
					StartedAt:   now.Add(-7 * time.Minute),
					CompletedAt: now.Add(-2 * time.Minute),
				},
				{
					Name:        "lint",
					App:         "GitHub Actions",
					Status:      "COMPLETED",
					Conclusion:  "SUCCESS",
					State:       "SUCCESS",
					DetailsURL:  "https://github.com/madicen/appr-ai-sal/actions/runs/9098",
					StartedAt:   now.Add(-8 * time.Minute),
					CompletedAt: now.Add(-7 * time.Minute),
				},
			},
		}
	default:
		return &gh.ChecksReport{
			HeadSHA:     pr.HeadSHA,
			RollupState: pr.ChecksState,
			Runs: []gh.CheckRun{
				{
					Name:        "go test ./...",
					App:         "GitHub Actions",
					Status:      "COMPLETED",
					Conclusion:  "SUCCESS",
					State:       "SUCCESS",
					DetailsURL:  "https://github.com/" + pr.Owner + "/" + pr.Repo + "/actions/runs/8001",
					StartedAt:   now.Add(-22 * time.Minute),
					CompletedAt: now.Add(-19 * time.Minute),
				},
				{
					Name:        "lint",
					App:         "GitHub Actions",
					Status:      "COMPLETED",
					Conclusion:  "SUCCESS",
					State:       "SUCCESS",
					DetailsURL:  "https://github.com/" + pr.Owner + "/" + pr.Repo + "/actions/runs/8000",
					StartedAt:   now.Add(-22 * time.Minute),
					CompletedAt: now.Add(-21 * time.Minute),
				},
			},
		}
	}
}
