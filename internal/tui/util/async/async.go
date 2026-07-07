// Package async provides the shared vocabulary for the TUI's keyed
// asynchronous flows: a generic Started/Result message pair and a per-key
// lifecycle Tracker.
//
// Before F6 each tab hand-rolled a parallel set of *StartedMsg / *DoneMsg
// structs (repoagents alone had two agent families, each with
// regenerate/delete/save/suggest variants) plus an ad-hoc `busy map[K]bool`
// gate. They are now expressed as instantiations of Started[K] / Result[K, T]
// and a Tracker[K], so the shape lives in one place.
//
// Distinct instantiations stay distinct Go types, so a type switch still
// dispatches each operation. Operations that share a key type and carry no
// payload (e.g. delete vs. save) are disambiguated by a small marker value
// type in the owning package.
package async

// Started signals that a keyed async operation has begun. Tabs handle it to
// move the row into its running state immediately (before the result lands).
type Started[K any] struct {
	Key K
}

// Result carries the outcome of a keyed async operation: Val is the produced
// value (its zero value when the operation has no payload) and Err the
// failure, if any.
type Result[K any, T any] struct {
	Key K
	Val T
	Err error
}

// Phase is the lifecycle state of a keyed async row.
type Phase uint8

const (
	// Idle is the zero value: no operation tracked for the key.
	Idle Phase = iota
	// Running: an operation is in flight.
	Running
	// Done: the last operation succeeded.
	Done
	// Failed: the last operation errored.
	Failed
)

// Tracker records the Phase of each keyed operation, replacing the ad-hoc
// `busy map[K]bool` gates the tabs used. The zero value is ready to use.
type Tracker[K comparable] struct {
	phase map[K]Phase
}

func (t *Tracker[K]) set(k K, p Phase) {
	if t.phase == nil {
		t.phase = make(map[K]Phase)
	}
	t.phase[k] = p
}

// Start marks the key as Running.
func (t *Tracker[K]) Start(k K) { t.set(k, Running) }

// Finish marks the key Done (or Failed when err != nil).
func (t *Tracker[K]) Finish(k K, err error) {
	if err != nil {
		t.set(k, Failed)
		return
	}
	t.set(k, Done)
}

// Clear forgets the key entirely (returning it to Idle).
func (t *Tracker[K]) Clear(k K) {
	delete(t.phase, k)
}

// Phase returns the current phase for the key (Idle when untracked).
func (t *Tracker[K]) Phase(k K) Phase { return t.phase[k] }

// Running reports whether an operation is in flight for the key.
func (t *Tracker[K]) Running(k K) bool { return t.phase[k] == Running }
