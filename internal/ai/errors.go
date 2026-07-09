package ai

import (
	"fmt"
	"time"
)

// APIHTTPError is a non-2xx response from an HTTP-based AI backend.
type APIHTTPError struct {
	Provider string
	Status   int
	Body     string
	// RetryAfter is the greater of HTTP Retry-After (when present) and any
	// embedded hint in the body (e.g. Gemini 429: "Please retry in 16.497428316s").
	RetryAfter time.Duration
}

func (e *APIHTTPError) Error() string {
	if e == nil {
		return "<nil>"
	}
	body := e.Body
	if len(body) > 800 {
		body = body[:800] + "…"
	}
	s := fmt.Sprintf("%s: HTTP %03d: %s", e.Provider, e.Status, body)
	if e.RetryAfter > 0 {
		s += fmt.Sprintf(" (retry-after %s)", e.RetryAfter.Round(time.Millisecond))
	}
	return s
}

// Retryable reports whether the HTTP status often resolves with a backoff retry.
func (e *APIHTTPError) Retryable() bool {
	if e == nil {
		return false
	}
	switch e.Status {
	case 408, 425, 429, 500, 502, 503, 504, 529:
		return true
	default:
		return false
	}
}
