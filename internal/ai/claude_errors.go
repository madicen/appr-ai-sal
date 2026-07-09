package ai

import (
	"fmt"
	"strings"
)

// ClaudeErrorClass is the parsed classification of a `claude` subprocess
// failure. It replaces the previous fragile approach of substring-matching the
// whole error string (which, e.g., treated any message containing "eof" or
// "429" anywhere — even inside an unrelated identifier — as retryable). The
// class is derived once from the process exit + stderr and carried on
// ClaudeExecError so IsRetryableCompleteError can classify via error types
// instead of raw text scans.
type ClaudeErrorClass int

const (
	// ClaudeClassOther is a non-transient failure (bad flags, logic error, an
	// unrecognised stderr). Not retryable.
	ClaudeClassOther ClaudeErrorClass = iota
	// ClaudeClassRateLimited is an explicit rate-limit / quota / overloaded
	// signal from the CLI or the upstream Anthropic API. Retryable (a backoff
	// often clears it).
	ClaudeClassRateLimited
	// ClaudeClassTransientNetwork is a transient connectivity failure
	// (connection reset/refused, timeout, unexpected EOF talking to the API).
	// Retryable.
	ClaudeClassTransientNetwork
	// ClaudeClassAuth is an authentication / authorization failure (missing or
	// invalid credentials). NOT retryable — retrying with the same bad
	// credentials just burns attempts.
	ClaudeClassAuth
)

func (c ClaudeErrorClass) String() string {
	switch c {
	case ClaudeClassRateLimited:
		return "rate-limited"
	case ClaudeClassTransientNetwork:
		return "transient-network"
	case ClaudeClassAuth:
		return "auth"
	default:
		return "other"
	}
}

// ClaudeExecError is the typed error returned by runClaude when the `claude`
// subprocess fails (non-zero exit, an unparseable envelope, or an
// envelope-level error). It carries the process exit code, a parsed stderr
// classification, and the trimmed stderr so callers can classify the failure
// by type (errors.As) rather than by scanning the whole message for magic
// substrings.
type ClaudeExecError struct {
	// ExitCode is the subprocess exit code, or -1 when the process could not be
	// started / did not exit with a code (e.g. killed by signal, or an
	// envelope-level error where the process exited 0).
	ExitCode int
	// Class is the parsed failure classification driving retryability.
	Class ClaudeErrorClass
	// Stderr is the trimmed subprocess stderr (or the envelope error text for
	// envelope-level failures).
	Stderr string
	// Err is the underlying error (exec error, JSON error, …) for %w chains.
	Err error
}

func (e *ClaudeExecError) Error() string {
	if e == nil {
		return "<nil>"
	}
	var b strings.Builder
	b.WriteString("claude subprocess failed")
	if e.ExitCode >= 0 {
		fmt.Fprintf(&b, " (exit %d", e.ExitCode)
		b.WriteString(", ")
	} else {
		b.WriteString(" (")
	}
	b.WriteString(e.Class.String())
	b.WriteString(")")
	if s := strings.TrimSpace(e.Stderr); s != "" {
		b.WriteString(": ")
		b.WriteString(truncate(s, 500))
	}
	return b.String()
}

// Unwrap exposes the underlying error for errors.Is / errors.As chains.
func (e *ClaudeExecError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Retryable reports whether re-running the subprocess after a backoff is worth
// it. Only transient classes (rate-limit, transient network) retry; auth and
// other failures are deterministic and would just burn the attempt budget.
func (e *ClaudeExecError) Retryable() bool {
	if e == nil {
		return false
	}
	switch e.Class {
	case ClaudeClassRateLimited, ClaudeClassTransientNetwork:
		return true
	default:
		return false
	}
}

// classifyClaudeStderr maps subprocess stderr text to a ClaudeErrorClass. It
// inspects the stderr of THIS process's own failure (a bounded, structured
// signal) rather than an arbitrary wrapped error string, so it does not suffer
// the "substring anywhere" false positives the old whole-message scan did.
//
// Order matters: auth is checked before rate-limit before network so the most
// specific/actionable class wins when several keywords co-occur.
func classifyClaudeStderr(stderr string) ClaudeErrorClass {
	s := strings.ToLower(stderr)
	// Deliberately phrase-based, never bare short tokens like "eof" or "429":
	// those are exactly the substrings that produced false positives when the
	// old code scanned the whole (wrapped) error message. Matching multi-word
	// phrases against the subprocess's own stderr is a bounded, intentional
	// signal rather than an accidental collision with an unrelated identifier.
	switch {
	case containsAny(s,
		"unauthorized", "authentication error", "authentication_error",
		"invalid api key", "invalid x-api-key", "invalid_api_key",
		"permission denied", "forbidden", "not logged in", "please run /login",
		"oauth error", "invalid credentials"):
		return ClaudeClassAuth
	case containsAny(s,
		"rate limit", "rate_limit", "rate-limit",
		"too many requests", "quota exceeded", "insufficient_quota",
		"overloaded", "overloaded_error", "resource exhausted",
		"resource_exhausted", "requests per minute"):
		return ClaudeClassRateLimited
	case containsAny(s,
		"connection reset", "connection refused", "broken pipe",
		"unexpected eof", "timed out", "deadline exceeded",
		"no such host", "tls handshake", "temporary failure",
		"service unavailable", "bad gateway", "gateway timeout",
		"network is unreachable", "connection timed out"):
		return ClaudeClassTransientNetwork
	default:
		return ClaudeClassOther
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
