package review

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/madicen/appr-ai-sal/internal/applog"
	"github.com/madicen/appr-ai-sal/internal/gh"
)

// Bootstrap carries optional PR metadata and diff bytes the caller already
// fetched (e.g. the TUI detail view). When populated and still fresh for ref,
// runBootstrap skips the matching network calls so review start is not blocked
// on redundant gh round-trips.
type Bootstrap struct {
	PR   *gh.PR
	Diff string
}

func bootstrapPRMatches(ref gh.Ref, pr *gh.PR) bool {
	if pr == nil {
		return false
	}
	return pr.Owner == ref.Owner && pr.Repo == ref.Repo && pr.Number == ref.Number
}

type bootstrapResult struct {
	pr       *gh.PR
	worktree string
	diff     string
}

// runBootstrap fetches (or reuses) the PR view, worktree, and unified diff.
// The three network/git legs run concurrently whenever a leg is not satisfied
// by bootstrap, so wall-clock prep is roughly max(fetch-pr, checkout, diff)
// instead of their sum.
func runBootstrap(ctx context.Context, ref gh.Ref, boot Bootstrap, strictness string, out chan<- Progress) (bootstrapResult, error) {
	out <- Progress{Stage: "fetch-pr", Detail: "start"}
	out <- Progress{Stage: "checkout", Detail: "start"}
	out <- Progress{Stage: "diff", Detail: "start"}

	reusePR := bootstrapPRMatches(ref, boot.PR)
	reuseDiff := reusePR && strings.TrimSpace(boot.Diff) != ""

	var (
		pr     *gh.PR
		prErr  error
		wt     string
		wtErr  error
		diff   string
		diffErr error
		wg     sync.WaitGroup
	)

	if reusePR {
		pr = boot.PR
		out <- Progress{Stage: "fetch-pr", Detail: "reused cached view"}
	} else {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pr, prErr = gh.GetPR(ctx, ref)
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		wt, wtErr = prepareWorktree(ctx, ref)
	}()

	if reuseDiff {
		diff = boot.Diff
		out <- Progress{Stage: "diff", Detail: fmt.Sprintf("reused cached view · %d bytes", len(diff))}
	} else {
		wg.Add(1)
		go func() {
			defer wg.Done()
			diff, diffErr = gh.GetDiff(ctx, ref)
		}()
	}

	wg.Wait()

	if !reusePR {
		if prErr != nil {
			applog.Error("review stage failed", "stage", "fetch-pr", "ref", ref.String(), "err", prErr.Error())
			out <- Progress{Stage: "fetch-pr", Err: fmt.Errorf("fetch PR: %w", prErr)}
			return bootstrapResult{}, prErr
		}
		out <- Progress{Stage: "fetch-pr", Detail: "done"}
	}

	if wtErr != nil {
		applog.Error("review stage failed", "stage", "checkout", "ref", ref.String(), "err", wtErr.Error())
		out <- Progress{Stage: "checkout", Err: fmt.Errorf("prepare worktree: %w", wtErr)}
		return bootstrapResult{}, wtErr
	}
	out <- Progress{Stage: "checkout", Detail: fmt.Sprintf("%s · review %s", wt, strictness)}

	if !reuseDiff {
		if diffErr != nil {
			out <- Progress{Stage: "diff", Err: fmt.Errorf("fetch diff: %w", diffErr)}
			return bootstrapResult{}, diffErr
		}
		out <- Progress{Stage: "diff", Detail: fmt.Sprintf("%d bytes", len(diff))}
	}

	return bootstrapResult{pr: pr, worktree: wt, diff: diff}, nil
}
