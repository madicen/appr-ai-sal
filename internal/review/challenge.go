package review

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/applog"
	"github.com/madicen/appr-ai-sal/internal/llmjson"
)

// challenge.go implements B4 "chat-with-specialist (challenge this finding)".
//
// From a finding card the reviewer opens a scoped exchange: the specialist
// that filed the finding is handed its OWN original finding + the surrounding
// diff hunk + the reviewer's question, and must either WITHDRAW the finding
// (the reviewer was right; the card is auto-skipped) or UPHOLD it with a
// strengthened justification (optionally revising the comment / severity).
//
// It mirrors the suggestion-repair pass (suggestion_repair.go): ONE cheap,
// scoped model call — the specialist's persona as the system prompt, the
// finding + hunk + question as the user prompt, strict JSON out parsed via the
// shared llmjson ladder. The call is routed as its own Q7 stage ("challenge")
// so a profile can send it to a cheap model, and it respects the R1 usage
// metering + R2 concurrency semaphore that live in the internal/ai layer every
// Complete call passes through.
//
// Multi-turn: the reviewer can ask follow-ups. Each turn carries the full
// prior transcript so the specialist sees the running conversation, and the
// transcript is what feeds reviewer memory (B1) — a withdrawal is a strong
// negative signal for that finding's pattern (see RecordChallengeWithdrawal).
//
// The whole feature is fail-open: any error (load, model, parse) is returned
// to the caller, which shows it in the overlay and leaves the card unchanged.

// ChallengeStage is the Q7 stage-routing key for the challenge call. A profile
// can route challenges to a cheaper/faster model via stage_models["challenge"]
// independently of the review model; ForStage(ChallengeStage) is a no-op clone
// when unrouted.
const ChallengeStage = "challenge"

// ChallengeDecision is the specialist's verdict on a challenged finding.
type ChallengeDecision string

const (
	// ChallengeWithdraw: the specialist concedes the finding and withdraws it.
	// The TUI auto-skips the card (marking it withdrawn-via-challenge) and
	// records a negative signal into reviewer memory.
	ChallengeWithdraw ChallengeDecision = "withdraw"
	// ChallengeUphold: the specialist stands by the finding and returns a
	// strengthened justification (and optionally a revised comment/severity).
	ChallengeUphold ChallengeDecision = "uphold"
)

// ChallengeResponse is the specialist's structured reply to one challenge turn.
type ChallengeResponse struct {
	// Decision is "withdraw" or "uphold". An unrecognised / missing decision
	// is normalized to uphold — the safe default keeps the finding rather than
	// silently dropping it on a malformed response.
	Decision ChallengeDecision `json:"decision"`
	// Justification is the specialist's reasoning: why it withdrew, or the
	// strengthened case for why the finding stands. Always human-facing prose.
	Justification string `json:"justification"`
	// RevisedComment, when non-empty on an uphold, is a rewritten finding
	// comment the specialist offers in light of the reviewer's question. The
	// TUI may apply it to the card (the posted comment) or leave it to the
	// reviewer. Empty means "no change to the comment".
	RevisedComment string `json:"revised_comment,omitempty"`
	// RevisedSeverity, when non-empty on an uphold, is a re-graded severity
	// (normalized to the canonical ladder). Empty means "no change".
	RevisedSeverity Severity `json:"revised_severity,omitempty"`
}

// Withdrawn reports whether the response withdrew the finding.
func (r ChallengeResponse) Withdrawn() bool { return r.Decision == ChallengeWithdraw }

// ChallengeTurn is one round of a challenge exchange: the reviewer's question
// and the specialist's response. A slice of these is the in-memory transcript
// the TUI keeps on the card; ChallengeFinding takes the prior transcript so
// each follow-up call carries the full conversation.
type ChallengeTurn struct {
	Question string            `json:"question"`
	Response ChallengeResponse `json:"response"`
}

// challengeComplete is indirected through a package var purely so tests can
// inject a deterministic completer, exactly like repairComplete. Production
// always uses completeJSONWithSchema via challengeCompleteDefault.
var challengeComplete = challengeCompleteDefault

func challengeCompleteDefault(ctx context.Context, cfg *aiconfig.Config, system, user string) (string, error) {
	return completeJSONWithSchema(ctx, cfg, system, user, "", challengeSchema())
}

// ChallengeFinding runs ONE scoped challenge call for a single finding and
// returns the specialist's structured decision.
//
// specialist is the lane that filed f (reused as the persona via
// SpecialistPrompt); hunk is the surrounding diff text the finding anchors to
// (the caller extracts it the same way the repair pass / excerpt gate do, so
// the call stays cheap — never the whole PR); transcript is the prior
// conversation for a multi-turn exchange (nil on the first turn); question is
// the reviewer's new message.
//
// The call is routed as the "challenge" stage (Q7) and produces strict JSON
// parsed via the shared llmjson ladder. Errors are returned verbatim so the
// caller can disclose them and leave the finding unchanged (fail-open).
func ChallengeFinding(ctx context.Context, cfg *aiconfig.Config, specialist string, f Finding, hunk string, transcript []ChallengeTurn, question string) (ChallengeResponse, error) {
	if strings.TrimSpace(question) == "" {
		return ChallengeResponse{}, fmt.Errorf("challenge: empty question")
	}
	if cfg == nil {
		cfg = aiconfig.DefaultConfig()
	}

	persona, err := SpecialistPrompt(specialist)
	if err != nil {
		return ChallengeResponse{}, fmt.Errorf("challenge: load %s persona: %w", specialist, err)
	}
	system := persona + "\n\n" + challengeSystemAddendum

	user := buildChallengeUserPrompt(specialist, f, hunk, transcript, question)

	// Q7: route the challenge to its configured model (stage_models["challenge"]
	// / "default"); a no-op clone when unrouted.
	cfg = cfg.ForStage(ChallengeStage)
	cctx := applog.WithStage(ctx, ChallengeStage)

	raw, err := challengeComplete(cctx, cfg, system, user)
	if err != nil {
		return ChallengeResponse{}, fmt.Errorf("challenge: model call: %w", err)
	}
	return parseChallengeResponse(raw)
}

// parseChallengeResponse parses the model's JSON (via the shared salvage
// ladder) into a normalized ChallengeResponse: the decision is folded to
// withdraw/uphold (unknown → uphold, the finding-preserving default) and any
// revised severity is normalized to the canonical ladder. A missing/blank
// revised severity stays empty so "no change" is distinguishable from a
// deliberate re-grade.
func parseChallengeResponse(raw string) (ChallengeResponse, error) {
	type challengeJSON struct {
		Decision        string `json:"decision"`
		Justification   string `json:"justification"`
		RevisedComment  string `json:"revised_comment"`
		RevisedSeverity string `json:"revised_severity"`
	}
	parsed, err := llmjson.Parse[challengeJSON](raw)
	if err != nil {
		return ChallengeResponse{}, fmt.Errorf("challenge: parse response: %w", err)
	}
	out := ChallengeResponse{
		Decision:       normalizeChallengeDecision(parsed.Decision),
		Justification:  strings.TrimSpace(parsed.Justification),
		RevisedComment: strings.TrimSpace(parsed.RevisedComment),
	}
	if s := strings.TrimSpace(parsed.RevisedSeverity); s != "" {
		out.RevisedSeverity = normalizeSeverity(Severity(s))
	}
	return out, nil
}

// normalizeChallengeDecision folds a model-supplied decision string to a
// canonical ChallengeDecision. Only an explicit "withdraw" (and common
// synonyms) withdraws the finding; everything else — including an empty or
// unrecognised value — is uphold, so a malformed response never drops a
// finding out from under the reviewer.
func normalizeChallengeDecision(s string) ChallengeDecision {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "withdraw", "withdrawn", "retract", "retracted", "concede", "conceded", "drop":
		return ChallengeWithdraw
	default:
		return ChallengeUphold
	}
}

// buildChallengeUserPrompt assembles the scoped user message: the original
// finding, the diff hunk it anchors to, the running transcript (for follow-up
// turns), the reviewer's new question, and the output contract.
func buildChallengeUserPrompt(specialist string, f Finding, hunk string, transcript []ChallengeTurn, question string) string {
	var b strings.Builder
	b.WriteString("A human reviewer is CHALLENGING one of your findings. Reconsider it honestly against the evidence below and decide whether to WITHDRAW it or UPHOLD it.\n\n")

	b.WriteString("--- your original finding ---\n")
	b.WriteString("specialist: " + specialist + "\n")
	if strings.TrimSpace(f.Path) != "" {
		b.WriteString("location: " + f.Path)
		if f.Line > 0 {
			b.WriteString(":" + strconv.Itoa(f.Line))
		}
		b.WriteString("\n")
	}
	if sev := strings.TrimSpace(string(f.Severity)); sev != "" {
		b.WriteString("severity: " + sev + "\n")
	}
	b.WriteString("comment: " + strings.TrimSpace(f.Comment) + "\n")
	if s := strings.TrimSpace(f.Suggestion); s != "" {
		b.WriteString("suggested fix:\n" + s + "\n")
	}
	b.WriteString("\n")

	if h := strings.TrimSpace(hunk); h != "" {
		b.WriteString("--- the diff hunk this finding anchors to ---\n")
		b.WriteString(h)
		b.WriteString("\n\n")
	} else {
		b.WriteString("--- diff hunk: not available (reason from the diff/context you were given) ---\n\n")
	}

	if len(transcript) > 0 {
		b.WriteString("--- the exchange so far ---\n")
		for i, t := range transcript {
			fmt.Fprintf(&b, "Reviewer (turn %d): %s\n", i+1, strings.TrimSpace(t.Question))
			decision := string(t.Response.Decision)
			if decision == "" {
				decision = string(ChallengeUphold)
			}
			fmt.Fprintf(&b, "You (turn %d, %s): %s\n", i+1, decision, strings.TrimSpace(t.Response.Justification))
		}
		b.WriteString("\n")
	}

	b.WriteString("--- the reviewer's message ---\n")
	b.WriteString(strings.TrimSpace(question))
	b.WriteString("\n\n")

	b.WriteString(challengeOutputContract)
	return b.String()
}
