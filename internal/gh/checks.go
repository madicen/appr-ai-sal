package gh

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/applog"
)

// CheckRunAnnotation is one inline annotation attached to a CheckRun output.
// Mirrors the GitHub Checks API "annotation" object — `path` is repo-relative,
// `line` is the new-side line, and `level` is one of NOTICE/WARNING/FAILURE.
type CheckRunAnnotation struct {
	Path    string
	Line    int
	Level   string
	Message string
}

// CheckRun is a single check run (GitHub Actions / external CI / etc).
// Each run carries enough context to render a status glyph, the check name,
// a "click to open in browser" link, and (for failing runs) an excerpt of
// its output so the reviewer can grok the failure without leaving the TUI.
type CheckRun struct {
	Name        string
	App         string // e.g. "GitHub Actions" — empty for legacy commit statuses
	Status      string // "QUEUED", "IN_PROGRESS", "COMPLETED" (check-runs) or "" for status contexts
	Conclusion  string // "SUCCESS", "FAILURE", "NEUTRAL", … (check-runs) or "" for status contexts
	State       string // "SUCCESS"/"FAILURE"/… for legacy status contexts; mirrors Conclusion otherwise
	Title       string // CheckRun.output.title — usually the failing-step summary
	Summary     string // CheckRun.output.summary — markdown body GitHub renders inline
	DetailsURL  string // best-effort permalink to the run on GitHub
	Required    bool   // true when the check is required for merge (branch protection)
	StartedAt   time.Time
	CompletedAt time.Time
	Annotations []CheckRunAnnotation
}

// ChecksReport is the rolled-up status for a PR's head commit. RollupState is
// the same enum CollapseChecksRollup emits ("SUCCESS" / "FAILURE" / etc.).
// HeadSHA is the commit those checks ran against — useful for the "stale
// since force-push" hint we may add later.
type ChecksReport struct {
	HeadSHA     string
	RollupState string
	Runs        []CheckRun
}

// checksData mirrors the `data` object of graphqlChecksQuery. It is also the
// checks slice of the fused PR-agent prefetch query, so checksReportFromData
// converts a single commit node reused by both callers.
type checksData struct {
	Repository struct {
		PullRequest struct {
			Commits struct {
				Nodes []checksCommitNode `json:"nodes"`
			} `json:"commits"`
		} `json:"pullRequest"`
	} `json:"repository"`
}

type checksCommitNode struct {
	Commit struct {
		Oid               string `json:"oid"`
		StatusCheckRollup *struct {
			State    string `json:"state"`
			Contexts struct {
				PageInfo   pageInfo           `json:"pageInfo"`
				TotalCount int                `json:"totalCount"`
				Nodes      []checkContextNode `json:"nodes"`
			} `json:"contexts"`
		} `json:"statusCheckRollup"`
	} `json:"commit"`
}

type checkContextNode struct {
	Typename string `json:"__typename"`
	// CheckRun fields
	Name        string `json:"name"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt"`
	DetailsURL  string `json:"detailsUrl"`
	IsRequired  bool   `json:"isRequired"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	CheckSuite  struct {
		App struct {
			Name string `json:"name"`
		} `json:"app"`
	} `json:"checkSuite"`
	Annotations struct {
		PageInfo pageInfo `json:"pageInfo"`
		Nodes    []struct {
			Path     string `json:"path"`
			Location struct {
				Start struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"location"`
			Message         string `json:"message"`
			AnnotationLevel string `json:"annotationLevel"`
		} `json:"nodes"`
	} `json:"annotations"`
	// StatusContext fields
	Context     string `json:"context"`
	State       string `json:"state"`
	Description string `json:"description"`
	TargetURL   string `json:"targetUrl"`
	CreatedAt   string `json:"createdAt"`
}

// GetChecks fetches the PR head commit's status-check rollup and the per-run
// detail (status / conclusion / output / annotations) in one GraphQL round
// trip. Returns a zero-value report (RollupState="") when there are no
// checks at all — the renderer treats that as "no checks configured" rather
// than failing.
func GetChecks(ctx context.Context, ref Ref) (*ChecksReport, error) {
	data, err := graphQLQuery[checksData](ctx, graphqlChecksQuery, map[string]any{
		"owner":  ref.Owner,
		"name":   ref.Repo,
		"number": ref.Number,
	})
	if err != nil {
		return nil, err
	}
	return checksReportFromData(ref, data.Repository.PullRequest.Commits.Nodes), nil
}

// checksReportFromData collapses the (last) commit node into a ChecksReport.
// A rollup with more than the fetched page of contexts (first: 50) or
// annotations (first: 10) is rare; paginating the nested connections is
// overkill, so we log an explicit overflow warning (R6.3) and render what we
// have — the failing runs the reviewer needs are already surfaced.
func checksReportFromData(ref Ref, commitNodes []checksCommitNode) *ChecksReport {
	report := &ChecksReport{}
	if len(commitNodes) == 0 {
		return report
	}
	commit := commitNodes[len(commitNodes)-1].Commit
	report.HeadSHA = commit.Oid
	if commit.StatusCheckRollup == nil {
		return report
	}
	report.RollupState = strings.ToUpper(strings.TrimSpace(commit.StatusCheckRollup.State))
	if commit.StatusCheckRollup.Contexts.PageInfo.HasNextPage {
		applog.Warn("check contexts truncated",
			"ref", ref.String(),
			"fetched", len(commit.StatusCheckRollup.Contexts.Nodes),
			"total", commit.StatusCheckRollup.Contexts.TotalCount)
	}
	for _, n := range commit.StatusCheckRollup.Contexts.Nodes {
		run := CheckRun{}
		switch n.Typename {
		case "CheckRun":
			run.Name = n.Name
			run.Status = n.Status
			run.Conclusion = n.Conclusion
			run.State = n.Conclusion
			run.Title = n.Title
			run.Summary = n.Summary
			run.DetailsURL = n.DetailsURL
			run.App = n.CheckSuite.App.Name
			run.Required = n.IsRequired
			if t, err := time.Parse(time.RFC3339, n.StartedAt); err == nil {
				run.StartedAt = t
			}
			if t, err := time.Parse(time.RFC3339, n.CompletedAt); err == nil {
				run.CompletedAt = t
			}
			if n.Annotations.PageInfo.HasNextPage {
				applog.Warn("check annotations truncated", "ref", ref.String(), "check", n.Name)
			}
			for _, a := range n.Annotations.Nodes {
				run.Annotations = append(run.Annotations, CheckRunAnnotation{
					Path:    a.Path,
					Line:    a.Location.Start.Line,
					Level:   a.AnnotationLevel,
					Message: a.Message,
				})
			}
		case "StatusContext":
			run.Name = n.Context
			run.State = strings.ToUpper(strings.TrimSpace(n.State))
			run.Conclusion = run.State
			run.Required = n.IsRequired
			run.Title = n.Description
			run.DetailsURL = n.TargetURL
			if t, err := time.Parse(time.RFC3339, n.CreatedAt); err == nil {
				run.StartedAt = t
				run.CompletedAt = t
			}
		default:
			continue
		}
		report.Runs = append(report.Runs, run)
	}
	// Failing / pending runs first so the failure summary is the first thing
	// the reviewer reads when they land on the Checks tab. Within a tier we
	// keep API order so deterministic re-renders are easy to test.
	sort.SliceStable(report.Runs, func(i, j int) bool {
		return checkSortRank(report.Runs[i]) < checkSortRank(report.Runs[j])
	})
	return report
}

// checkSortRank gives failing/error runs the lowest score so they bubble to
// the top of the Checks pane. Pending sits in the middle (still actionable),
// and successful / neutral runs settle at the bottom.
func checkSortRank(r CheckRun) int {
	switch strings.ToUpper(strings.TrimSpace(coalesce(r.Conclusion, r.State))) {
	case "FAILURE":
		return 0
	case "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STALE", "ERROR":
		return 1
	case "":
		// Empty conclusion + non-COMPLETED status = in-flight.
		if !strings.EqualFold(r.Status, "COMPLETED") && r.Status != "" {
			return 2
		}
		return 4
	case "PENDING", "QUEUED", "IN_PROGRESS", "EXPECTED":
		return 2
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return 3
	}
	return 4
}

func coalesce(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// graphqlChecksQuery walks the PR's most recent commit and pulls every check
// run / commit status under the rollup, plus the per-CheckRun output and the
// first ~10 annotations. We ask for the last commit (the head) explicitly
// rather than referencing it by SHA so we don't need a separate fetch to
// learn the SHA first.
const graphqlChecksQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      commits(last: 1) {
        nodes {
          commit {
            oid
            statusCheckRollup {
              state
              contexts(first: 50) {
                pageInfo { hasNextPage }
                totalCount
                nodes {
                  __typename
                  ... on CheckRun {
                    name
                    status
                    conclusion
                    startedAt
                    completedAt
                    detailsUrl
                    isRequired
                    title
                    summary
                    checkSuite { app { name } }
                    annotations(first: 10) {
                      pageInfo { hasNextPage }
                      nodes {
                        path
                        location { start { line } }
                        message
                        annotationLevel
                      }
                    }
                  }
                  ... on StatusContext {
                    context
                    state
                    isRequired
                    description
                    targetUrl
                    createdAt
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}`
