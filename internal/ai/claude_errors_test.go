package ai

import (
	"errors"
	"fmt"
	"testing"
)

func TestClassifyClaudeStderr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		stderr string
		want   ClaudeErrorClass
	}{
		{"rate limit", "Error: 429 rate limit exceeded, too many requests", ClaudeClassRateLimited},
		{"overloaded", "the model is overloaded, please try again", ClaudeClassRateLimited},
		{"quota", "quota exceeded for this month", ClaudeClassRateLimited},
		{"conn reset", "read tcp: connection reset by peer", ClaudeClassTransientNetwork},
		{"unexpected eof", "post https://api: unexpected EOF", ClaudeClassTransientNetwork},
		{"service unavailable", "503 service unavailable", ClaudeClassTransientNetwork},
		{"auth", "authentication error: invalid api key", ClaudeClassAuth},
		{"not logged in", "you are not logged in; please run /login", ClaudeClassAuth},
		{"logic error", "TypeError: cannot read properties of undefined", ClaudeClassOther},
		// The scar-tissue false positive: a message merely CONTAINING "eof"
		// as a fragment (or a JS test hook name) must NOT be classified as a
		// transient/retryable failure now that we key off phrases, not the
		// bare "eof" substring.
		{"beforeEach false positive", "ReferenceError: beforeEach is not defined", ClaudeClassOther},
		{"eof fragment false positive", "cannot resolve symbol 'eof_marker' in config.go", ClaudeClassOther},
		{"429 fragment false positive", "value 4291 is out of range for field size", ClaudeClassOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyClaudeStderr(tc.stderr); got != tc.want {
				t.Fatalf("classifyClaudeStderr(%q) = %v, want %v", tc.stderr, got, tc.want)
			}
		})
	}
}

func TestClaudeExecErrorRetryable(t *testing.T) {
	t.Parallel()
	if !(&ClaudeExecError{Class: ClaudeClassRateLimited}).Retryable() {
		t.Fatal("rate-limited claude error should retry")
	}
	if !(&ClaudeExecError{Class: ClaudeClassTransientNetwork}).Retryable() {
		t.Fatal("transient-network claude error should retry")
	}
	if (&ClaudeExecError{Class: ClaudeClassAuth}).Retryable() {
		t.Fatal("auth claude error should NOT retry")
	}
	if (&ClaudeExecError{Class: ClaudeClassOther}).Retryable() {
		t.Fatal("other claude error should NOT retry")
	}
}

// TestIsRetryableCompleteErrorTypeBased proves classification is by error type,
// not by scanning the whole message for "eof"/"429" substrings.
func TestIsRetryableCompleteErrorTypeBased(t *testing.T) {
	t.Parallel()

	// A non-retryable Claude class stays non-retryable even when the wrapped
	// message contains "eof"/"429" fragments (the old bug retried these).
	other := &ClaudeExecError{ExitCode: 1, Class: ClaudeClassOther, Stderr: "beforeEach failed; eof-like token; code 4291"}
	if IsRetryableCompleteError(other) {
		t.Fatal("Class=other must not be retryable despite eof/429 fragments in the text")
	}
	if IsRetryableCompleteError(fmt.Errorf("wrapped: %w", other)) {
		t.Fatal("wrapping a non-retryable claude error must not make it retryable")
	}

	// Retryable classes are retried via errors.As.
	rl := &ClaudeExecError{Class: ClaudeClassRateLimited}
	if !IsRetryableCompleteError(fmt.Errorf("claude: %w", rl)) {
		t.Fatal("rate-limited claude error should be retryable through a wrap")
	}
	net := &ClaudeExecError{Class: ClaudeClassTransientNetwork}
	if !IsRetryableCompleteError(net) {
		t.Fatal("transient-network claude error should be retryable")
	}

	// A plain error whose text merely contains "eof" or "429" is no longer
	// retryable (this is the removed substring-anywhere behaviour).
	if IsRetryableCompleteError(errors.New("panic in beforeEach hook (eof)")) {
		t.Fatal("plain error containing 'eof' must not be retryable")
	}
	if IsRetryableCompleteError(errors.New("http status 4291 unexpected")) {
		t.Fatal("plain error containing '429' fragment must not be retryable")
	}
}
