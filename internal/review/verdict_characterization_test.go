package review

import (
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

// This file is a CHARACTERIZATION suite for the merge-verdict logic. It drives
// the public verdict methods (EffectiveMergeVerdict, ReconciledMergeVerdict,
// PostEvent) over a broad matrix of real Drafts and asserts their output
// against an INDEPENDENT oracle that encodes the documented rules (not the
// implementation). It was written and run GREEN against the pre-F4 five-
// function implementation, then kept green across the refactor into the single
// reduceMergeVerdict reducer — that green→green transition is the behavior-
// preservation proof for the state-machine rename.

// refRank mirrors the documented verdict ordering (approve most permissive,
// request_changes strictest); unknown/empty rank lowest.
func refRank(v string) int {
	switch v {
	case VibeVerdictRequestChanges:
		return 2
	case VibeVerdictComment:
		return 1
	default:
		return 0
	}
}

// refEffective / refReconciled are the oracle: a direct transcription of the
// documented merge-verdict rules, independent of the production code path.
func refEffective(vibe string, arbiterActive bool, arbiter string, blocking bool) string {
	eff := vibe
	if arbiterActive {
		if refRank(arbiter) < refRank(vibe) && blocking {
			eff = vibe
		} else {
			eff = arbiter
		}
	}
	return eff
}

func refReconciled(vibe string, arbiterActive bool, arbiter string, blocking bool) string {
	eff := refEffective(vibe, arbiterActive, arbiter, blocking)
	if eff == VibeVerdictRequestChanges && !blocking {
		return VibeVerdictComment
	}
	return eff
}

func refPostEvent(reconciled string) string {
	switch reconciled {
	case VibeVerdictApprove:
		return "APPROVE"
	case VibeVerdictRequestChanges:
		return "REQUEST_CHANGES"
	default:
		return "COMMENT"
	}
}

func TestVerdictCharacterizationMatrix(t *testing.T) {
	verdicts := []string{"", VibeVerdictApprove, VibeVerdictComment, VibeVerdictRequestChanges}
	for _, vibe := range verdicts {
		for _, arbPresent := range []bool{false, true} {
			for _, arb := range verdicts {
				for _, prWideErr := range []bool{false, true} {
					// arbiterActive mirrors the production guard:
					// RepoArbiter present, no error, EffectiveVerdict != "".
					arbiterActive := arbPresent && arb != ""
					// hasBlockingContent, for these drafts, is driven by a
					// PR-wide error finding OR an arbiter request_changes
					// override (no inline findings / prompts / suppressions
					// are set here).
					blocking := prWideErr || (arbPresent && arb == VibeVerdictRequestChanges)

					d := buildVerdictDraft(vibe, arbPresent, arb, prWideErr)

					wantEff := refEffective(vibe, arbiterActive, arb, blocking)
					wantRec := refReconciled(vibe, arbiterActive, arb, blocking)
					wantEvent := refPostEvent(wantRec)

					if got := d.EffectiveMergeVerdict(); got != wantEff {
						t.Errorf("vibe=%q arbPresent=%v arb=%q prWideErr=%v: EffectiveMergeVerdict=%q want %q",
							vibe, arbPresent, arb, prWideErr, got, wantEff)
					}
					if got := d.ReconciledMergeVerdict(); got != wantRec {
						t.Errorf("vibe=%q arbPresent=%v arb=%q prWideErr=%v: ReconciledMergeVerdict=%q want %q",
							vibe, arbPresent, arb, prWideErr, got, wantRec)
					}
					if got := d.PostEvent(); got != wantEvent {
						t.Errorf("vibe=%q arbPresent=%v arb=%q prWideErr=%v: PostEvent=%q want %q",
							vibe, arbPresent, arb, prWideErr, got, wantEvent)
					}
				}
			}
		}
	}
}

// buildVerdictDraft constructs a Draft whose derived verdict inputs match the
// requested (vibe, arbiter, blocking) state. blocking is induced with a
// PR-wide error finding so it is independent of any inline/prompt machinery.
func buildVerdictDraft(vibe string, arbPresent bool, arb string, prWideErr bool) *Draft {
	d := &Draft{PR: &gh.PR{HeadSHA: "abc"}}
	if vibe != "" {
		d.VibeCoach = &VibeCoachResult{Verdict: vibe}
	}
	if prWideErr {
		d.Specialists = []SpecialistResult{{
			Specialist: SpecTesting,
			Findings:   []Finding{{Path: "", Line: 0, Severity: SeverityError, Comment: "no tests added"}},
		}}
	}
	if arbPresent {
		d.RepoArbiter = &RepoArbiterResult{
			VerdictOverride:  arb,
			EffectiveVerdict: arb,
		}
	}
	return d
}
