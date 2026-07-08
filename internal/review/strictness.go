package review

import (
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// MinSeverityForStrictness is the lowest severity that survives the post-parse
// strictness filter. Four levels (most lenient → strictest):
// critical_only → only "critical"; lenient → error+; balanced → warning+; strict → all including info.
//
// Two layers enforce the floor: the prompt block tells the model what to file,
// and FilterFindingsBySeverity drops anything that snuck past the floor. The
// renderer never sees suppressed findings, so the approval card, the body, and
// the repo experts all see the same set.
func MinSeverityForStrictness(s aiconfig.ReviewStrictness) Severity {
	switch s {
	case aiconfig.ReviewCriticalOnly:
		return SeverityCritical
	case aiconfig.ReviewLenient:
		return SeverityError
	case aiconfig.ReviewStrict:
		return SeverityInfo
	default:
		return SeverityWarning
	}
}

// normalizeSeverity maps a model-supplied severity string to a canonical
// Severity. Canonical values pass through; common synonyms are folded; any
// unrecognised value (including empty) becomes SeverityWarning — the same
// coercion FilterFindingsBySeverity applies at rank 0, but done at parse time
// so an unknown string (e.g. "high", "nit", "blocker") never renders verbatim
// in the review body.
func normalizeSeverity(sv Severity) Severity {
	switch strings.ToLower(strings.TrimSpace(string(sv))) {
	case "info", "informational", "low", "minor", "nit", "note", "trivial", "style", "suggestion":
		return SeverityInfo
	case "warning", "warn", "medium", "moderate", "med":
		return SeverityWarning
	case "error", "high", "major", "bug":
		return SeverityError
	case "critical", "crit", "blocker", "fatal", "severe":
		return SeverityCritical
	default:
		return SeverityWarning
	}
}

// severityRank maps a severity to numeric ordering:
// info(1) < warning(2) < error(3) < critical(4).
func severityRank(sv Severity) int {
	switch sv {
	case SeverityCritical:
		return 4
	case SeverityError:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	}
	return 0
}

// FilterFindingsBySeverity returns the findings whose severity is at or above
// the floor. Findings with an unknown severity are kept (treated as warning).
func FilterFindingsBySeverity(findings []Finding, floor Severity) []Finding {
	if floor == SeverityInfo || floor == "" {
		return findings
	}
	min := severityRank(floor)
	out := make([]Finding, 0, len(findings))
	for _, f := range findings {
		r := severityRank(f.Severity)
		if r == 0 {
			r = severityRank(SeverityWarning)
		}
		if r < min {
			continue
		}
		out = append(out, f)
	}
	return out
}

// strictnessBlockForSpecialists is appended to each code-reviewing specialist
// user prompt. The wording mirrors the deterministic post-parse filter so the
// model wastes fewer tokens on findings that will be filtered.
func strictnessBlockForSpecialists(s aiconfig.ReviewStrictness) string {
	switch s {
	case aiconfig.ReviewCriticalOnly:
		return `## Review intensity: critical-only — severity floor: critical

Only file findings at severity "critical" (merge-blocking: security disaster, data loss, broken build, production outage class). Findings at "error", "warning", or "info" will be DROPPED before the human sees them; do not produce them. If nothing rises to that bar in your specialty, return an empty findings array and say so in summary.

`
	case aiconfig.ReviewLenient:
		return `## Review intensity: lenient — severity floor: error

Only file findings at severity "error" or "critical". Findings at "warning" or "info" will be DROPPED before the human sees them; do not produce them. If everything in your specialty is at "warning" or "info" only, return an empty findings array and say so in summary.

`
	case aiconfig.ReviewStrict:
		return `## Review intensity: strict (make it right) — severity floor: info

Surface every actionable issue from your specialty, including info-level nits, as long as each one meets the actionability bar (concrete comment, plus an exact suggestion when the fix is local — see the suggestion contract). Do not invent issues to look thorough.

`
	default: // balanced
		return `## Review intensity: balanced — severity floor: warning

File findings at severity "warning", "error", or "critical". Findings at "info" will be DROPPED before the human sees them; only produce them if they are clearly substantive (i.e. they ought to be "warning" anyway). When in doubt, leave them out.

`
	}
}

// strictnessBlockForVibeCoachUser is prepended after the PR header in the vibe-coach user message.
func strictnessBlockForVibeCoachUser(s aiconfig.ReviewStrictness) string {
	var b strings.Builder
	b.WriteString(strictnessBlockForSpecialists(s))
	switch s {
	case aiconfig.ReviewCriticalOnly:
		b.WriteString(`For vibe-coach: default to approve when specialists only filed sub-critical noise; request_changes only if a critical-severity issue truly blocks merge.

`)
	case aiconfig.ReviewLenient:
		b.WriteString(`For vibe-coach: only emit prompts if several findings genuinely need a follow-up AI iteration; one short prompt is enough, and an empty "prompts" array is correct when a rubber stamp is appropriate.

`)
	case aiconfig.ReviewStrict:
		b.WriteString(`For vibe-coach: you may emit up to four prompts if needed so the author can drive their AI through most substantive fixes—each prompt should still bundle related work.

`)
	default:
		b.WriteString(`For vibe-coach: aim for 1–4 prompts that match the severity of the combined findings—enough to fix real issues without inventing busywork.

`)
	}
	return b.String()
}

// strictnessBlockForArbiter is inserted near the top of the repo-arbiter user
// prompt (Q3.5) so the arbiter can calibrate how aggressively it demotes /
// suppresses to the intensity the user actually chose. It returns "" for the
// default (balanced) level so a balanced run's arbiter prompt is byte-identical
// to pre-Q3.5 — the embedded arbiter prompt was authored against the balanced
// floor, so the extra section would be pure noise there. The block only appears
// at the off-default intensities where the calibration genuinely differs.
func strictnessBlockForArbiter(s aiconfig.ReviewStrictness) string {
	switch s {
	case aiconfig.ReviewCriticalOnly:
		return "## Review intensity: critical-only — demote decisively\n\n" +
			"The user chose the **critical-only** intensity. Only critical-severity findings survive the post-arbiter strictness floor; everything at error, warning, or info is dropped before the human sees it. Demoting a non-critical finding below error is therefore effectively the same as suppressing it. When a brief or a `contradicts_finding` convention witness calibrates a finding away, lean hard toward **demote** (straight to info) — the user has signalled they only want merge-blocking issues. The hard rules still hold: never soften a security finding or a critical-severity finding.\n\n"
	case aiconfig.ReviewLenient:
		return "## Review intensity: lenient — demote freely below error\n\n" +
			"The user chose the **lenient** intensity. Only error- and critical-severity findings survive the floor; warnings and info are dropped. Be generous with **demote** for warning-level findings that a brief or a `contradicts_finding` convention witness calibrates away — they will fall below the floor and disappear. Keep error/critical findings unless a hard rule or an explicit brief says otherwise.\n\n"
	case aiconfig.ReviewStrict:
		return "## Review intensity: strict — demote conservatively\n\n" +
			"The user chose the **strict** intensity. Every severity down to info survives the floor, and the user explicitly wants to see all actionable findings. Demote **conservatively**: a demotion here only re-grades severity (it does not hide the finding), so reserve suppress/demote for findings a brief or a `contradicts_finding` convention witness genuinely calibrates away. Do not soften findings merely to shorten the review — the user opted into thoroughness.\n\n"
	default: // balanced — byte-identical to pre-Q3.5
		return ""
	}
}

// vibeCoachSystemAddendum is appended to the embedded vibe-coach system prompt.
const vibeCoachSystemAddendum = `

## Output contract (non-negotiable)

Every entry in the "prompts" array MUST contain three distinct strings:

- "title" — short imperative label the HUMAN reviewer reads first.
- "rationale" — 1–2 sentences for the HUMAN, naming which specialist findings this prompt addresses and what they amount to. Do NOT put AI instructions here.
- "agent_prompt" — the verbatim text the PR author will paste into their AI coding assistant (Claude Code, Cursor, etc.) to change THIS repository. Self-contained, second-person, references concrete paths/symbols/acceptance criteria. The AI receiving this string has no other context — it does NOT see the rationale, summary, specialist findings, or repo metadata around it. Treat agent_prompt as a complete instruction.

Do not output generic coding tips, interview questions, or advice that isn't clearly tied to fixing the specialist findings. Do not place rationale-style explanation inside agent_prompt. Do not place AI-instruction text inside rationale.

Your JSON must include the required "verdict" field ("approve" | "request_changes" | "comment"); the review UI surfaces it at the top of the posted summary.

If verdict is "request_changes", include at least one prompt with a substantive agent_prompt unless every blocker is already an inline code suggestion on the diff.`

// strictnessSummaryForProgress returns a short label for progress UI.
func strictnessSummaryForProgress(s aiconfig.ReviewStrictness) string {
	switch s {
	case aiconfig.ReviewCriticalOnly:
		return "critical-only"
	case aiconfig.ReviewLenient:
		return "lenient"
	case aiconfig.ReviewStrict:
		return "strict"
	default:
		return "balanced"
	}
}
