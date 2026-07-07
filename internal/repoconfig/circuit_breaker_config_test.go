package repoconfig

import (
	"testing"
	"time"
)

func TestMaxConsecutiveStageFailuresOrDefault(t *testing.T) {
	t.Parallel()
	if got := (&Config{}).MaxConsecutiveStageFailuresOrDefault(); got != defaultMaxConsecutiveStageFailures {
		t.Fatalf("unset = %d, want default %d", got, defaultMaxConsecutiveStageFailures)
	}
	if got := (&Config{MaxConsecutiveStageFailures: 7}).MaxConsecutiveStageFailuresOrDefault(); got != 7 {
		t.Fatalf("explicit = %d, want 7", got)
	}
	// Negative disables the arm → 0 ("no limit").
	if got := (&Config{MaxConsecutiveStageFailures: -1}).MaxConsecutiveStageFailuresOrDefault(); got != 0 {
		t.Fatalf("negative (disabled) = %d, want 0", got)
	}
	if got := (*Config)(nil).MaxConsecutiveStageFailuresOrDefault(); got != defaultMaxConsecutiveStageFailures {
		t.Fatalf("nil = %d, want default", got)
	}
}

func TestRunWallClockCap(t *testing.T) {
	t.Parallel()
	if got := (&Config{}).RunWallClockCap(); got != defaultRunWallClockCap {
		t.Fatalf("unset = %v, want default %v", got, defaultRunWallClockCap)
	}
	if got := (&Config{RunWallClockCapSeconds: 120}).RunWallClockCap(); got != 2*time.Minute {
		t.Fatalf("explicit = %v, want 2m", got)
	}
	// Negative disables the cap → 0 duration ("no cap").
	if got := (&Config{RunWallClockCapSeconds: -1}).RunWallClockCap(); got != 0 {
		t.Fatalf("negative (disabled) = %v, want 0", got)
	}
	if got := (*Config)(nil).RunWallClockCap(); got != defaultRunWallClockCap {
		t.Fatalf("nil = %v, want default", got)
	}
}

// A negative "disable" value must survive Merge (which otherwise only copies
// non-zero values) so a user can turn an arm off from repo-context.json.
func TestMergeCarriesDisableSentinels(t *testing.T) {
	t.Parallel()
	c := Default()
	c.Merge(&Config{MaxConsecutiveStageFailures: -1, RunWallClockCapSeconds: -1})
	if c.MaxConsecutiveStageFailuresOrDefault() != 0 {
		t.Fatal("merged -1 should disable the consecutive-failure arm")
	}
	if c.RunWallClockCap() != 0 {
		t.Fatal("merged -1 should disable the wall-clock cap")
	}
}
