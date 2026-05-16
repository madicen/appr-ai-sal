package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
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

// GetChecks fetches the PR head commit's status-check rollup and the per-run
// detail (status / conclusion / output / annotations) in one GraphQL round
// trip. Returns a zero-value report (RollupState="") when there are no
// checks at all — the renderer treats that as "no checks configured" rather
// than failing.
func GetChecks(ctx context.Context, ref Ref) (*ChecksReport, error) {
	out, err := runGraphQL(ctx, graphqlChecksQuery, map[string]string{
		"owner":  ref.Owner,
		"name":   ref.Repo,
		"number": fmt.Sprintf("%d", ref.Number),
	})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					Commits struct {
						Nodes []struct {
							Commit struct {
								Oid               string `json:"oid"`
								StatusCheckRollup *struct {
									State    string `json:"state"`
									Contexts struct {
										Nodes []struct {
											Typename string `json:"__typename"`
											// CheckRun fields
											Name        string `json:"name"`
											Status      string `json:"status"`
											Conclusion  string `json:"conclusion"`
											StartedAt   string `json:"startedAt"`
											CompletedAt string `json:"completedAt"`
											DetailsURL  string `json:"detailsUrl"`
											Title       string `json:"title"`
											Summary     string `json:"summary"`
											CheckSuite  struct {
												App struct {
													Name string `json:"name"`
												} `json:"app"`
											} `json:"checkSuite"`
											Annotations struct {
												Nodes []struct {
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
										} `json:"nodes"`
									} `json:"contexts"`
								} `json:"statusCheckRollup"`
							} `json:"commit"`
						} `json:"nodes"`
					} `json:"commits"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parse checks response: %w", err)
	}
	if len(resp.Errors) > 0 {
		msgs := make([]string, 0, len(resp.Errors))
		for _, e := range resp.Errors {
			msgs = append(msgs, e.Message)
		}
		return nil, fmt.Errorf("graphql checks: %s", strings.Join(msgs, "; "))
	}
	report := &ChecksReport{}
	if len(resp.Data.Repository.PullRequest.Commits.Nodes) == 0 {
		return report, nil
	}
	commit := resp.Data.Repository.PullRequest.Commits.Nodes[len(resp.Data.Repository.PullRequest.Commits.Nodes)-1].Commit
	report.HeadSHA = commit.Oid
	if commit.StatusCheckRollup == nil {
		return report, nil
	}
	report.RollupState = strings.ToUpper(strings.TrimSpace(commit.StatusCheckRollup.State))
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
			if t, err := time.Parse(time.RFC3339, n.StartedAt); err == nil {
				run.StartedAt = t
			}
			if t, err := time.Parse(time.RFC3339, n.CompletedAt); err == nil {
				run.CompletedAt = t
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
	return report, nil
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
                nodes {
                  __typename
                  ... on CheckRun {
                    name
                    status
                    conclusion
                    startedAt
                    completedAt
                    detailsUrl
                    title
                    summary
                    checkSuite { app { name } }
                    annotations(first: 10) {
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
