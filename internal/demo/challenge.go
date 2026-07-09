package demo

import (
	"strings"

	"github.com/madicen/appr-ai-sal/internal/review"
)

// CannedChallengeResponse returns a deterministic, offline ChallengeResponse
// for the B4 "challenge this finding" exchange so the feature is fully
// demoable (and VHS-recordable) without a live model.
//
// It scripts a believable multi-turn arc off the transcript length:
//   - the first turn UPHOLDS the finding with a strengthened, in-lane
//     justification (showing the "specialist doubles down" path), and
//   - any follow-up turn WITHDRAWS it (showing the withdraw → auto-skip path,
//     including the reviewer-memory negative signal it feeds).
//
// specialist and f let the canned prose name the lane and location so the demo
// reads like a real exchange; question is echoed lightly so the reply feels
// responsive.
func CannedChallengeResponse(specialist string, f review.Finding, transcript []review.ChallengeTurn, question string) review.ChallengeResponse {
	loc := strings.TrimSpace(f.Path)
	if loc == "" {
		loc = "this change"
	}
	if len(transcript) == 0 {
		return review.ChallengeResponse{
			Decision: review.ChallengeUphold,
			Justification: "I'm keeping this " + specialist + " finding. Looking again at " + loc +
				", the concern in my original comment still holds against the hunk shown — the consequence I described would ship as-is. Happy to narrow it if you can point to something in the diff that already addresses it.",
			RevisedComment: strings.TrimSpace(f.Comment),
		}
	}
	return review.ChallengeResponse{
		Decision: review.ChallengeWithdraw,
		Justification: "You're right — with the context you've added I can no longer justify this " + specialist +
			" finding on " + loc + ". Withdrawing it so it won't be posted.",
	}
}
