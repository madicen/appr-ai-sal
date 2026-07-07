package review

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// stageWithRetry retries any stage that returns a transiently-failing error
// (timeouts, parse failures, transient HTTP). Stages that succeed or return a
// non-retryable error exit immediately. This is broader than the per-call
// retry inside internal/ai (which only retries inside one Complete call) and
// complements it: if a specialist's JSON response is unparseable on the first
// attempt, the whole stage runs again rather than failing the review.
//
// notify, if non-nil, is called on each retry with the attempt number (1-based)
// and the error that triggered it, so the UI can surface progress.
func stageWithRetry(ctx context.Context, cfg *aiconfig.Config, name string, notify func(attempt int, err error), fn func(context.Context) error) error {
	max := cfg.InferenceRetryMaxAttempts()
	if max < 1 {
		max = 1
	}
	base := cfg.InferenceRetryBase()
	maxWait := cfg.InferenceRetryMaxBackoff()

	var lastErr error
	for attempt := 0; attempt < max; attempt++ {
		if attempt > 0 {
			if notify != nil {
				notify(attempt, lastErr)
			}
			if err := ai.SleepBeforeRetry(ctx, attempt-1, base, maxWait, lastErr); err != nil {
				return err
			}
		}
		err := fn(ctx)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryableStageError(err) {
			return err
		}
		if attempt == max-1 {
			break
		}
	}
	return fmt.Errorf("%s failed after %d attempts: %w", name, max, lastErr)
}

// isRetryableStageError accepts the ai.IsRetryableCompleteError set plus errors
// that bubble up through specialist parsing (e.g. "parse specialist output:
// invalid character"), since a transient model glitch can produce malformed
// JSON that retrying often clears.
func isRetryableStageError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if ai.IsRetryableCompleteError(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "parse specialist output"),
		strings.Contains(msg, "parse vibe-coach output"),
		strings.Contains(msg, "parse repo arbiter"),
		strings.Contains(msg, "parse repo expert narrative"),
		strings.Contains(msg, "no json object found"),
		strings.Contains(msg, "unexpected end of json"),
		strings.Contains(msg, "invalid character"):
		return true
	}
	return false
}
