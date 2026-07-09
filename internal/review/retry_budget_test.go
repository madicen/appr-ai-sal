package review

import (
	"context"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// countingProvider is a fake ai.Provider that always fails with a retryable
// error and counts how many times it was invoked, so a test can prove the
// total provider calls per stage are bounded by the shared attempt budget.
type countingProvider struct{ calls *int }

func (p countingProvider) Name() string                  { return "counting" }
func (p countingProvider) Capabilities() ai.Capabilities { return ai.Capabilities{} }
func (p countingProvider) Complete(context.Context, ai.Request) (ai.Result, error) {
	*p.calls++
	// Retryable (both IsRetryableCompleteError and isRetryableStageError say
	// yes) and non-quota, so no artificial backoff floor slows the test.
	return ai.Result{}, &ai.APIHTTPError{Status: 503}
}

// TestStageAndInnerRetryShareAttemptBudget is the R4 acceptance test: a stage
// that keeps failing must not make stage-retries × inner-retries (~25) provider
// calls. With a shared budget the total is bounded by StageAttemptBudget.
func TestStageAndInnerRetryShareAttemptBudget(t *testing.T) {
	for _, budget := range []int{3, 5} {
		calls := 0
		restore := ai.SetBaseProviderForTest(func(*aiconfig.Config) (ai.Provider, error) {
			return countingProvider{calls: &calls}, nil
		})

		cfg := &aiconfig.Config{
			Provider:                aiconfig.ProviderClaude,
			RetryMaxAttempts:        5, // inner loop: up to 5
			RetryBaseMS:             1,
			RetryMaxMS:              1,
			RetryStageAttemptBudget: budget,
		}

		err := stageWithRetry(context.Background(), cfg, "specialist test", nil, func(sctx context.Context) error {
			_, e := Complete(sctx, cfg, "system", "user", "/tmp/worktree")
			return e
		})
		restore()

		if err == nil {
			t.Fatalf("budget=%d: expected the stage to fail after exhausting retries", budget)
		}
		if calls != budget {
			t.Fatalf("budget=%d: provider invoked %d times; want exactly %d (shared budget must bound stage×inner, not reach %d)",
				budget, calls, budget, cfg.RetryMaxAttempts*cfg.RetryMaxAttempts)
		}
	}
}
