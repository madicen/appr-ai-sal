package evals

import (
	"os"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/review"
)

// stripUnsupportedClaims drops findings that have ≥1 worktree-unresolvable
// claim ref (same resolver as the unsupported-claim metric). Findings with no
// extractable refs are kept. Empty worktree is a no-op.
//
// Eval-only treatment: APPR_AI_SAL_EVAL_TREATMENT=claim-strip-v1.
func stripUnsupportedClaims(worktree string, findings []review.Finding) []review.Finding {
	worktree = strings.TrimSpace(worktree)
	if worktree == "" || len(findings) == 0 {
		return findings
	}
	out := make([]review.Finding, 0, len(findings))
	for _, f := range findings {
		if findingHasUnsupportedClaim(worktree, f) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func findingHasUnsupportedClaim(worktree string, f review.Finding) bool {
	refs := extractClaimRefs(f.Comment)
	if len(refs) == 0 {
		return false
	}
	for _, r := range refs {
		if !claimResolves(worktree, r) {
			return true
		}
	}
	return false
}

// applyPostEvalTreatment mutates the observation after EvalRun for the
// selected eval treatment. claim-strip-v1 drops unsupported-claim findings.
func applyPostEvalTreatment(worktree string, obs review.EvalObservation) review.EvalObservation {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(evalTreatmentEnv))) {
	case "claim-strip-v1":
		for i := range obs.Agents {
			obs.Agents[i].Findings = stripUnsupportedClaims(worktree, obs.Agents[i].Findings)
		}
		return obs
	default:
		return obs
	}
}
