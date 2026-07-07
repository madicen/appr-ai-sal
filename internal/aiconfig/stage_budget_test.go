package aiconfig

import "testing"

func TestStageAttemptBudget(t *testing.T) {
	t.Parallel()
	if got := (&Config{}).StageAttemptBudget(); got != 5 {
		t.Fatalf("unset = %d, want default 5", got)
	}
	if got := (&Config{RetryStageAttemptBudget: 8}).StageAttemptBudget(); got != 8 {
		t.Fatalf("explicit = %d, want 8", got)
	}
	if got := (*Config)(nil).StageAttemptBudget(); got != 5 {
		t.Fatalf("nil = %d, want default 5", got)
	}
	// Capped at 30.
	if got := (&Config{RetryStageAttemptBudget: 1000}).StageAttemptBudget(); got != 30 {
		t.Fatalf("huge = %d, want cap 30", got)
	}
}

// The budget must survive the profile mirror (snapshot on save, apply on load /
// SetActive) like the other retry knobs.
func TestStageAttemptBudgetRoundTripsThroughProfile(t *testing.T) {
	t.Parallel()
	c := DefaultConfig()
	c.RetryStageAttemptBudget = 6
	// snapshotProfile copies top-level → profile; applyActiveProfile copies back.
	c.Profiles[0] = c.snapshotProfile(c.Profiles[0].Name)
	c.RetryStageAttemptBudget = 0 // scribble the flat field
	c.applyActiveProfile()
	if c.RetryStageAttemptBudget != 6 {
		t.Fatalf("RetryStageAttemptBudget after profile round-trip = %d, want 6", c.RetryStageAttemptBudget)
	}
}
