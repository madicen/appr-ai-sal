package ai

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// scriptedReader is a fake stream whose Read blocks until the test feeds a
// chunk (or the associated context is cancelled, mirroring how net/http aborts
// an in-flight body read when the request context is cancelled). Closing chunks
// signals EOF.
type scriptedReader struct {
	ctx    context.Context
	chunks chan []byte
}

func (s *scriptedReader) Read(p []byte) (int, error) {
	select {
	case <-s.ctx.Done():
		return 0, s.ctx.Err()
	case b, ok := <-s.chunks:
		if !ok {
			return 0, io.EOF
		}
		return copy(p, b), nil
	}
}

// TestStreamFirstByteTimeoutFires proves a stream that produces NO bytes at all
// is aborted at the first-byte budget with the typed first-byte error.
func TestStreamFirstByteTimeoutFires(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	r := &scriptedReader{ctx: ctx, chunks: make(chan []byte)}
	tr := newStreamTimeoutReader(r, cancel, 40*time.Millisecond, time.Second)
	defer tr.stop()

	buf := make([]byte, 32)
	if _, err := tr.Read(buf); err == nil {
		t.Fatal("expected Read to fail once first-byte timeout fires")
	}
	if cause := context.Cause(ctx); !errors.Is(cause, ErrStreamFirstByteTimeout) {
		t.Fatalf("cause = %v, want ErrStreamFirstByteTimeout", cause)
	}
	if !IsRetryableCompleteError(ErrStreamFirstByteTimeout) {
		t.Fatal("first-byte timeout should be retryable")
	}
}

// TestStreamIdleTimeoutFires proves a stream that starts and then goes silent
// is aborted at the idle budget with the typed idle error (NOT the first-byte
// error, since it already produced output).
func TestStreamIdleTimeoutFires(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	r := &scriptedReader{ctx: ctx, chunks: make(chan []byte)}
	tr := newStreamTimeoutReader(r, cancel, time.Second /*generous first-byte*/, 40*time.Millisecond /*tight idle*/)
	defer tr.stop()

	// Feed one chunk so the reader transitions past first-byte into idle mode.
	go func() { r.chunks <- []byte("hello") }()
	buf := make([]byte, 32)
	if n, err := tr.Read(buf); err != nil || n == 0 {
		t.Fatalf("first Read: n=%d err=%v, want a byte and no error", n, err)
	}
	// Now go silent — the idle timer should fire.
	if _, err := tr.Read(buf); err == nil {
		t.Fatal("expected Read to fail once idle timeout fires")
	}
	if cause := context.Cause(ctx); !errors.Is(cause, ErrStreamIdleTimeout) {
		t.Fatalf("cause = %v, want ErrStreamIdleTimeout", cause)
	}
	if !IsRetryableCompleteError(ErrStreamIdleTimeout) {
		t.Fatal("idle timeout should be retryable")
	}
}

// TestStreamTrickleSurvives proves a slow-but-alive stream that keeps producing
// a byte within each idle window is NEVER killed — the whole point of the
// idle timeout replacing the whole-response timeout. It reads to a clean EOF.
func TestStreamTrickleSurvives(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	r := &scriptedReader{ctx: ctx, chunks: make(chan []byte)}
	// Idle budget 120ms; trickle a chunk every 40ms — comfortably inside it.
	tr := newStreamTimeoutReader(r, cancel, 120*time.Millisecond, 120*time.Millisecond)
	defer tr.stop()

	const chunks = 6
	go func() {
		for i := 0; i < chunks; i++ {
			time.Sleep(40 * time.Millisecond)
			r.chunks <- []byte("x")
		}
		close(r.chunks)
	}()

	buf := make([]byte, 32)
	total := 0
	for {
		n, err := tr.Read(buf)
		total += n
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("trickle stream should not error, got %v (cause %v)", err, context.Cause(ctx))
		}
	}
	if total != chunks {
		t.Fatalf("read %d bytes, want %d", total, chunks)
	}
	if cause := context.Cause(ctx); cause != nil {
		t.Fatalf("no timeout expected on a trickle stream, cause = %v", cause)
	}
}
