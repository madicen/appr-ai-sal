package ai

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// ---------------------------------------------------------------------------
// Streaming toggle (context-based, defaults OFF)
// ---------------------------------------------------------------------------

// streamingKey is the context key marking inference made under it as wanting
// streaming (SSE for HTTP providers, --output-format stream-json for the
// claude CLI). Like WithUsageObserver / WithConcurrencyLimit it rides the run
// context so the fixed ai.CompleteFunc signature never has to grow a flag: the
// review.Complete shim reads it and sets Request.Stream, and every stage
// goroutine that derives from the run context inherits it. It defaults OFF so
// direct ad-hoc callers (and tests) keep the whole-response path unless they
// opt in — the runner installs it once per run.
type streamingKey struct{}

// WithStreaming returns a context whose inference calls stream their responses.
// The review runner installs it once per run so every stage (specialists, PR
// agents, arbiter, witness, vibe-coach, the repair pass) streams — surfacing
// token-liveness and replacing the whole-response HTTP timeout with the
// idle/first-byte timeouts. A nil ctx is treated as context.Background().
func WithStreaming(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, streamingKey{}, true)
}

// StreamingFromContext reports whether WithStreaming was applied to ctx.
func StreamingFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, ok := ctx.Value(streamingKey{}).(bool)
	return ok && v
}

// ---------------------------------------------------------------------------
// Typed idle / first-byte timeout errors
// ---------------------------------------------------------------------------

// ErrStreamFirstByteTimeout is returned when a streaming call produced no bytes
// at all within the first-byte timeout (the connection stalled before
// response-start). Classified as retryable (like other transient transport
// failures) via IsRetryableCompleteError.
var ErrStreamFirstByteTimeout = errors.New("streaming: no response within first-byte timeout")

// ErrStreamIdleTimeout is returned when a streaming call went silent — no bytes
// for the idle timeout — after having started. A slow-but-alive stream keeps
// resetting the idle timer and never hits this. Classified as retryable.
var ErrStreamIdleTimeout = errors.New("streaming: stream went idle (no bytes within idle timeout)")

// ---------------------------------------------------------------------------
// Idle / first-byte timeout reader
// ---------------------------------------------------------------------------

// streamTimeoutReader wraps a streaming body (an HTTP response body or a
// subprocess stdout pipe) and cancels the associated context via a typed cause
// when the stream stalls. It enforces two independent budgets:
//
//   - a first-byte budget from stream start until the first byte arrives, and
//   - an idle budget that fires only when NO bytes arrive for that long AFTER
//     the stream has started producing output.
//
// Every successful Read (n>0) resets the idle timer, so an actively trickling
// stream is never killed — this is what replaces the old whole-response HTTP
// timeout that cut generations off at exactly TimeoutSec. Cancellation is
// delivered by cancelling the request/subprocess context with a typed cause,
// which unblocks the in-flight Read.
type streamTimeoutReader struct {
	r        io.Reader
	reset    chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func newStreamTimeoutReader(r io.Reader, cancel context.CancelCauseFunc, firstByte, idle time.Duration) *streamTimeoutReader {
	tr := &streamTimeoutReader{
		r:     r,
		reset: make(chan struct{}, 1),
		done:  make(chan struct{}),
	}
	go tr.watch(cancel, firstByte, idle)
	return tr
}

// watch is the single-goroutine timeout state machine. It owns the timer so no
// lock is needed: Read only ever signals activity on the buffered reset
// channel (non-blocking), and this loop consumes it.
func (tr *streamTimeoutReader) watch(cancel context.CancelCauseFunc, firstByte, idle time.Duration) {
	first := firstByte
	if first <= 0 {
		first = idle
	}
	if first <= 0 {
		// No timeout configured at all — nothing to guard.
		return
	}
	timer := time.NewTimer(first)
	defer timer.Stop()
	gotFirst := false
	for {
		select {
		case <-tr.done:
			return
		case <-tr.reset:
			// Activity: drain a possibly-already-fired timer, then re-arm on
			// the idle budget. Reaching here means bytes are flowing, so from
			// now on only a true idle gap should trip the timeout.
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			gotFirst = true
			if idle <= 0 {
				return
			}
			timer.Reset(idle)
		case <-timer.C:
			if gotFirst {
				cancel(ErrStreamIdleTimeout)
			} else {
				cancel(ErrStreamFirstByteTimeout)
			}
			return
		}
	}
}

func (tr *streamTimeoutReader) Read(p []byte) (int, error) {
	n, err := tr.r.Read(p)
	if n > 0 {
		// Non-blocking activity signal; a full buffer already means "recent
		// activity is pending", so dropping the extra signal is harmless.
		select {
		case tr.reset <- struct{}{}:
		default:
		}
	}
	return n, err
}

func (tr *streamTimeoutReader) stop() {
	tr.stopOnce.Do(func() { close(tr.done) })
}

// ---------------------------------------------------------------------------
// SSE scanner
// ---------------------------------------------------------------------------

// scanSSE reads a Server-Sent-Events stream from r, invoking onEvent for each
// dispatched event with its `event:` field (may be empty) and the joined
// `data:` payload. It stops early when onEvent returns stop=true (e.g. on the
// OpenAI `data: [DONE]` sentinel) or when the stream ends. Comment lines
// (starting with ':', used as heartbeats) are ignored for parsing but still
// count as activity upstream because they arrive as bytes through the
// timeout reader.
func scanSSE(r io.Reader, onEvent func(event, data string) (stop bool, err error)) error {
	sc := bufio.NewScanner(r)
	// Allow large SSE frames: individual JSON chunks (especially the final
	// usage frame or a big tool-input delta) can exceed the default 64KiB
	// token limit.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var event string
	var data strings.Builder
	dispatch := func() (bool, error) {
		if data.Len() == 0 && event == "" {
			return false, nil
		}
		payload := strings.TrimSuffix(data.String(), "\n")
		stop, err := onEvent(event, payload)
		event = ""
		data.Reset()
		return stop, err
	}

	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			stop, err := dispatch()
			if err != nil {
				return err
			}
			if stop {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // comment / heartbeat
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			data.WriteString(value)
			data.WriteString("\n")
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	// Flush a trailing event that had no closing blank line.
	if _, err := dispatch(); err != nil {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Shared HTTP SSE driver
// ---------------------------------------------------------------------------

// streamState accumulates a streamed response as its deltas arrive. The final
// Result/Usage produced from it is byte-for-byte what the non-streaming path
// would have returned for the same logical response.
type streamState struct {
	text  strings.Builder
	usage Usage
	model string
}

// sseHandler maps one provider-specific SSE event onto streamState, returning
// the human-visible delta text (for liveness token counting) and whether the
// stream is complete. A non-nil error aborts the stream.
type sseHandler func(event, data string, st *streamState) (delta string, done bool, err error)

// streamingHTTPClient is the client for streaming calls. It has NO overall
// Timeout on purpose: a long-but-active generation must not be cut off. The
// streamTimeoutReader (idle + first-byte) and the caller's per-stage context
// deadline are the only time bounds.
func streamingHTTPClient(_ *aiconfig.Config) *http.Client {
	return &http.Client{}
}

// runSSEStream performs one streaming HTTP request and accumulates the SSE
// delta stream into a Result. req must be built WITHOUT a context (the driver
// installs a cancellable one so the idle/first-byte timeouts can abort the
// in-flight read). provider is the telemetry/error label; defaultModel is used
// when the stream does not report its own model (keeping Result.Model
// identical to the non-streaming path). handle maps each event's deltas.
func runSSEStream(ctx context.Context, cfg *aiconfig.Config, provider string, req *http.Request, defaultModel string, handle sseHandler) (Result, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	req = req.WithContext(ctx)

	resp, err := streamingHTTPClient(cfg).Do(req)
	if err != nil {
		return Result{}, translateStreamErr(ctx, fmt.Errorf("%s stream: %w", provider, err))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return Result{}, &APIHTTPError{
			Provider:   provider,
			Status:     resp.StatusCode,
			Body:       string(body),
			RetryAfter: httpRetryAfter(resp, body),
		}
	}

	tr := newStreamTimeoutReader(resp.Body, cancel, cfg.StreamFirstByteTimeout(), cfg.StreamIdleTimeout())
	defer tr.stop()

	st := &streamState{model: defaultModel}
	emit := newActivityEmitter(ctx)
	scanErr := scanSSE(tr, func(event, data string) (bool, error) {
		delta, done, herr := handle(event, data, st)
		if herr != nil {
			return false, herr
		}
		emit.tick(delta)
		return done, nil
	})
	if scanErr != nil {
		return Result{}, translateStreamErr(ctx, scanErr)
	}

	out := strings.TrimSpace(st.text.String())
	if out == "" {
		return Result{}, fmt.Errorf("%s stream: empty response", provider)
	}
	emit.flush()

	model := st.model
	if model == "" {
		model = defaultModel
	}
	return Result{Text: out, Model: model, Usage: st.usage}, nil
}

// translateStreamErr converts a low-level stream failure into the typed
// idle/first-byte timeout error when that is why the context was cancelled, so
// IsRetryableCompleteError can classify it. Otherwise it returns err unchanged.
func translateStreamErr(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); cause != nil {
		if errors.Is(cause, ErrStreamIdleTimeout) || errors.Is(cause, ErrStreamFirstByteTimeout) {
			return cause
		}
	}
	return err
}
