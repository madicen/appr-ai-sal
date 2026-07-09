package review

import (
	"fmt"
	"sync"
	"time"
)

// runBreaker is the aggregate run circuit breaker (R4). It trips when EITHER of
// two guards fires:
//
//   - N consecutive AI stages fail after their retries (maxConsecutive), or
//   - the whole run exceeds a wall-clock cap (wallClockCap).
//
// Once tripped, the runner stops STARTING new stages — it never interrupts an
// in-flight call — and marks the remaining stages skipped, surfacing a Progress
// event that explains why. Either guard is disabled by passing a non-positive
// bound (0 for maxConsecutive, 0 duration for wallClockCap).
//
// It is safe for concurrent use: parallel specialist goroutines record their
// results through it. Consecutive counting is deterministic in result order
// when the runner feeds results sequentially (it does), which keeps the
// behaviour reproducible even when the stages themselves ran in parallel.
type runBreaker struct {
	mu             sync.Mutex
	start          time.Time
	wallClockCap   time.Duration // 0 = disabled
	maxConsecutive int           // 0 = disabled
	consecutive    int
	tripped        bool
	reason         string
}

func newRunBreaker(start time.Time, wallClockCap time.Duration, maxConsecutive int) *runBreaker {
	if maxConsecutive < 0 {
		maxConsecutive = 0
	}
	if wallClockCap < 0 {
		wallClockCap = 0
	}
	return &runBreaker{start: start, wallClockCap: wallClockCap, maxConsecutive: maxConsecutive}
}

// recordStage updates the breaker after a stage finished, given whether it
// failed after its retries. It returns whether the breaker is now tripped,
// whether THIS call caused the trip (so the caller emits the Progress event
// exactly once), and the human-readable reason.
func (b *runBreaker) recordStage(failed bool, now time.Time) (tripped bool, justTripped bool, reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	was := b.tripped
	if failed {
		b.consecutive++
	} else {
		b.consecutive = 0
	}
	b.evaluateLocked(now)
	return b.tripped, b.tripped && !was, b.reason
}

// check evaluates the breaker (mostly the wall-clock cap) without recording a
// stage result. Call it before starting each stage. Returns the tripped state,
// whether this call caused the trip, and the reason.
func (b *runBreaker) check(now time.Time) (tripped bool, justTripped bool, reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	was := b.tripped
	b.evaluateLocked(now)
	return b.tripped, b.tripped && !was, b.reason
}

func (b *runBreaker) evaluateLocked(now time.Time) {
	if b.tripped {
		return
	}
	if b.maxConsecutive > 0 && b.consecutive >= b.maxConsecutive {
		b.tripped = true
		b.reason = fmt.Sprintf("%d consecutive stage failures", b.consecutive)
		return
	}
	if b.wallClockCap > 0 && now.Sub(b.start) >= b.wallClockCap {
		b.tripped = true
		b.reason = fmt.Sprintf("run exceeded wall-clock cap of %s", b.wallClockCap)
		return
	}
}
