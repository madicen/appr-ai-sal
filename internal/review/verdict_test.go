package review

import "testing"

// TestReduceMergeVerdictExhaustive enumerates EVERY state of the reducer's
// enumerable input space (4 Vibe × 2 ArbiterActive × 4 Arbiter × 2 Blocking =
// 64 states) and asserts reduceMergeVerdict against the independent oracle
// (refEffective / refReconciled, transcribed from the documented rules in
// verdict_characterization_test.go). This is the exhaustive table test the
// plan requires: it proves the single reducer reproduces the documented
// state machine over the whole domain, not just the sampled draft scenarios.
func TestReduceMergeVerdictExhaustive(t *testing.T) {
	verdicts := []string{"", VibeVerdictApprove, VibeVerdictComment, VibeVerdictRequestChanges}
	count := 0
	for _, vibe := range verdicts {
		for _, arbiterActive := range []bool{false, true} {
			for _, arb := range verdicts {
				for _, blocking := range []bool{false, true} {
					in := verdictInputs{
						Vibe:          vibe,
						ArbiterActive: arbiterActive,
						Arbiter:       arb,
						Blocking:      blocking,
					}
					got := reduceMergeVerdict(in)

					wantEff := refEffective(vibe, arbiterActive, arb, blocking)
					wantRec := refReconciled(vibe, arbiterActive, arb, blocking)

					if got.Effective != wantEff {
						t.Errorf("%+v: Effective=%q want %q", in, got.Effective, wantEff)
					}
					if got.Reconciled != wantRec {
						t.Errorf("%+v: Reconciled=%q want %q", in, got.Reconciled, wantRec)
					}
					count++
				}
			}
		}
	}
	if count != 64 {
		t.Fatalf("expected 64 enumerated states, got %d", count)
	}
}

// TestReduceMergeVerdictInactiveArbiterIgnored pins the invariant that an
// inactive arbiter never influences the verdict regardless of what its
// (ignored) Arbiter field holds — the vibe-coach verdict stands.
func TestReduceMergeVerdictInactiveArbiterIgnored(t *testing.T) {
	for _, arb := range []string{"", VibeVerdictApprove, VibeVerdictComment, VibeVerdictRequestChanges} {
		in := verdictInputs{Vibe: VibeVerdictApprove, ArbiterActive: false, Arbiter: arb, Blocking: false}
		if got := reduceMergeVerdict(in).Effective; got != VibeVerdictApprove {
			t.Fatalf("inactive arbiter (Arbiter=%q) should not change verdict; got %q", arb, got)
		}
	}
}

// TestReduceMergeVerdictRelaxingOverrideGuard pins the two headline cases of
// the arbiter override guard: a relaxing override is clamped while blocking
// content survives, but stands once the blockers are cleared.
func TestReduceMergeVerdictRelaxingOverrideGuard(t *testing.T) {
	clamped := reduceMergeVerdict(verdictInputs{
		Vibe: VibeVerdictRequestChanges, ArbiterActive: true, Arbiter: VibeVerdictApprove, Blocking: true,
	})
	if clamped.Effective != VibeVerdictRequestChanges {
		t.Fatalf("relaxing override with live blockers should clamp to request_changes, got %q", clamped.Effective)
	}
	stands := reduceMergeVerdict(verdictInputs{
		Vibe: VibeVerdictRequestChanges, ArbiterActive: true, Arbiter: VibeVerdictApprove, Blocking: false,
	})
	if stands.Effective != VibeVerdictApprove {
		t.Fatalf("relaxing override with no blockers should stand at approve, got %q", stands.Effective)
	}
	// A stricter override is always honoured, blockers or not.
	stricter := reduceMergeVerdict(verdictInputs{
		Vibe: VibeVerdictApprove, ArbiterActive: true, Arbiter: VibeVerdictRequestChanges, Blocking: false,
	})
	if stricter.Effective != VibeVerdictRequestChanges {
		t.Fatalf("stricter override should always apply, got %q", stricter.Effective)
	}
}
