package ai

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// countingProvider records the peak number of concurrent Complete calls it
// observes, so a test can assert the semaphore never lets more than N run at
// once. It also counts total invocations so cancellation tests can prove the
// inner call was never reached.
type countingProvider struct {
	mu       sync.Mutex
	current  int
	maxSeen  int
	invoked  int64
	hold     time.Duration   // how long each call occupies its slot
	blockOn  <-chan struct{} // when non-nil, the FIRST call blocks until closed
	firstIn  chan struct{}   // closed once the first call is in-flight
	oncePost sync.Once
}

func (c *countingProvider) Name() string               { return "counting" }
func (c *countingProvider) Capabilities() Capabilities { return Capabilities{} }

func (c *countingProvider) Complete(ctx context.Context, _ Request) (Result, error) {
	n := atomic.AddInt64(&c.invoked, 1)
	c.mu.Lock()
	c.current++
	if c.current > c.maxSeen {
		c.maxSeen = c.current
	}
	c.mu.Unlock()

	if n == 1 {
		if c.firstIn != nil {
			c.oncePost.Do(func() { close(c.firstIn) })
		}
		if c.blockOn != nil {
			<-c.blockOn
		}
	}
	if c.hold > 0 {
		time.Sleep(c.hold)
	}

	c.mu.Lock()
	c.current--
	c.mu.Unlock()
	return Result{Text: "ok"}, nil
}

func (c *countingProvider) peakConcurrency() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxSeen
}

// wrap builds the real retry+seam provider around the fake so tests exercise
// the actual acquisition point in retryProvider.Complete, not a shortcut.
func wrapCounting(fake Provider) *retryProvider {
	return &retryProvider{cfg: &aiconfig.Config{Provider: aiconfig.ProviderOllama}, inner: fake}
}

// TestConcurrencyLimitNeverExceedsN drives many goroutines through the shared
// per-run semaphore and asserts the observed peak concurrency never exceeds N.
func TestConcurrencyLimitNeverExceedsN(t *testing.T) {
	t.Parallel()
	const (
		limit   = 3
		callers = 30
	)
	fake := &countingProvider{hold: 2 * time.Millisecond}
	prov := wrapCounting(fake)

	ctx := WithConcurrencyLimit(context.Background(), limit)

	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := prov.Complete(ctx, Request{}); err != nil {
				t.Errorf("Complete returned error: %v", err)
			}
		}()
	}
	wg.Wait()

	if peak := fake.peakConcurrency(); peak > limit {
		t.Fatalf("peak concurrency %d exceeded limit %d", peak, limit)
	}
	if got := atomic.LoadInt64(&fake.invoked); got != callers {
		t.Fatalf("expected %d invocations, got %d", callers, got)
	}
}

// TestConcurrencyLimitSerializesAtOne confirms N=1 fully serializes calls.
func TestConcurrencyLimitSerializesAtOne(t *testing.T) {
	t.Parallel()
	fake := &countingProvider{hold: time.Millisecond}
	prov := wrapCounting(fake)
	ctx := WithConcurrencyLimit(context.Background(), 1)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = prov.Complete(ctx, Request{})
		}()
	}
	wg.Wait()
	if peak := fake.peakConcurrency(); peak != 1 {
		t.Fatalf("with limit 1, peak concurrency = %d, want 1", peak)
	}
}

// TestConcurrencyLimitResolvesNonPositive proves a <= 0 requested limit never
// deadlocks (it resolves to the default) — a semaphore sized at 0 would hang.
func TestConcurrencyLimitResolvesNonPositive(t *testing.T) {
	t.Parallel()
	if got := ResolveMaxConcurrentInference(0); got != DefaultMaxConcurrentInference {
		t.Fatalf("ResolveMaxConcurrentInference(0) = %d, want %d", got, DefaultMaxConcurrentInference)
	}
	if got := ResolveMaxConcurrentInference(-5); got != DefaultMaxConcurrentInference {
		t.Fatalf("ResolveMaxConcurrentInference(-5) = %d, want %d", got, DefaultMaxConcurrentInference)
	}
	if got := ResolveMaxConcurrentInference(4); got != 4 {
		t.Fatalf("ResolveMaxConcurrentInference(4) = %d, want 4", got)
	}

	// A zero/negative limit must still let calls proceed (bounded by 3), not
	// hang forever.
	fake := &countingProvider{hold: time.Millisecond}
	prov := wrapCounting(fake)
	ctx := WithConcurrencyLimit(context.Background(), 0)
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, _ = prov.Complete(ctx, Request{}) }()
		}
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("calls under a resolved (non-zero) limit deadlocked")
	}
	if peak := fake.peakConcurrency(); peak > DefaultMaxConcurrentInference {
		t.Fatalf("peak concurrency %d exceeded resolved default %d", peak, DefaultMaxConcurrentInference)
	}
}

// TestConcurrencyLimitCtxCancelWhileBlocked confirms a caller blocked waiting
// for a slot returns promptly with the ctx error and never runs the inner
// Complete.
func TestConcurrencyLimitCtxCancelWhileBlocked(t *testing.T) {
	t.Parallel()
	unblock := make(chan struct{})
	firstIn := make(chan struct{})
	fake := &countingProvider{blockOn: unblock, firstIn: firstIn}
	prov := wrapCounting(fake)

	// Limit of 1: the first call takes the only slot and holds it.
	limitedCtx := WithConcurrencyLimit(context.Background(), 1)

	go func() { _, _ = prov.Complete(limitedCtx, Request{}) }()

	// Wait until the first call is genuinely in-flight (holding the slot).
	select {
	case <-firstIn:
	case <-time.After(2 * time.Second):
		t.Fatal("first call never started")
	}

	// A second call must now block on the semaphore. Cancel its ctx and expect
	// a prompt ctx error, with the inner Complete never invoked (invoked stays 1).
	cancelCtx, cancel := context.WithCancel(limitedCtx)
	errCh := make(chan error, 1)
	go func() {
		_, err := prov.Complete(cancelCtx, Request{})
		errCh <- err
	}()

	// Give the second call a moment to reach the blocked Acquire, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked call returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled call did not return promptly")
	}

	if got := atomic.LoadInt64(&fake.invoked); got != 1 {
		t.Fatalf("inner Complete should not run for the cancelled call; invoked=%d, want 1", got)
	}

	close(unblock) // let the first call finish
}
