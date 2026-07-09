package review

import (
	"testing"
	"time"
)

func TestRunBreakerConsecutiveFailures(t *testing.T) {
	t.Parallel()
	start := time.Unix(0, 0)
	b := newRunBreaker(start, 0 /* no wall-clock cap */, 3)

	// Two failures: not yet tripped.
	if tripped, _, _ := b.recordStage(true, start); tripped {
		t.Fatal("1 failure should not trip a 3-consecutive breaker")
	}
	if tripped, _, _ := b.recordStage(true, start); tripped {
		t.Fatal("2 failures should not trip a 3-consecutive breaker")
	}
	// A success resets the counter.
	if tripped, _, _ := b.recordStage(false, start); tripped {
		t.Fatal("a success must not trip the breaker")
	}
	// Now three more failures in a row trip it on the third.
	b.recordStage(true, start)
	b.recordStage(true, start)
	tripped, justTripped, reason := b.recordStage(true, start)
	if !tripped || !justTripped {
		t.Fatalf("3 consecutive failures should trip (tripped=%v just=%v)", tripped, justTripped)
	}
	if reason == "" {
		t.Fatal("tripped breaker should carry a reason")
	}
	// justTripped must be true exactly once.
	if _, again, _ := b.recordStage(true, start); again {
		t.Fatal("justTripped should only be reported on the transition")
	}
}

func TestRunBreakerWallClockCap(t *testing.T) {
	t.Parallel()
	start := time.Unix(0, 0)
	b := newRunBreaker(start, 10*time.Second, 0 /* consecutive disabled */)

	if tripped, _, _ := b.check(start.Add(9 * time.Second)); tripped {
		t.Fatal("before the cap the breaker must not trip")
	}
	tripped, justTripped, reason := b.check(start.Add(11 * time.Second))
	if !tripped || !justTripped {
		t.Fatalf("past the wall-clock cap the breaker should trip (tripped=%v just=%v)", tripped, justTripped)
	}
	if reason == "" {
		t.Fatal("wall-clock trip should carry a reason")
	}
}

func TestRunBreakerDisabledArms(t *testing.T) {
	t.Parallel()
	start := time.Unix(0, 0)
	// Both arms disabled: never trips, no matter how many failures or how long.
	b := newRunBreaker(start, 0, 0)
	for i := 0; i < 50; i++ {
		if tripped, _, _ := b.recordStage(true, start.Add(time.Hour)); tripped {
			t.Fatalf("breaker with both arms disabled must never trip (i=%d)", i)
		}
	}
	if tripped, _, _ := b.check(start.Add(1000 * time.Hour)); tripped {
		t.Fatal("disabled wall-clock arm must never trip")
	}
}
