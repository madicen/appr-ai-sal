// Package review orchestrates specialist AI agents over a checked-out PR
// and assembles their output into a single draft GitHub review.
//
// The package's core types are split across focused files:
//   - finding.go        — domain types (Finding, Severity, specialist consts,
//     SpecialistResult, RepoArbiterResult and its refs).
//   - draft.go          — the Draft aggregate plus its suppression/demotion
//     key bookkeeping (built on the unified findingkey.Key).
//   - verdict.go        — the merge-verdict state machine: one explicit
//     reducer (reduceMergeVerdict) plus the vibe-coach domain types.
//   - render.go         — the markdown review-body rendering.
//   - github_payload.go — GitHub review-payload construction (ToReview*,
//     EffectiveReviewEventAndBody, self-author downgrade).
//   - fallback_prompts.go — vibe-coach fallback-prompt bookkeeping.
package review

import (
	"strings"
)

// Severity is the importance of a single finding.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityError    Severity = "error"
	SeverityCritical Severity = "critical" // show-stopper / merge-blocking; stricter than error
)

// Specialist names. Edit the corresponding file in prompts/specialists/ to
// change a specialist's behavior.
const (
	SpecFormatting = "formatting"
	SpecDesign     = "design"
	SpecTesting    = "testing"
	SpecDocs       = "docs"
	SpecSecurity   = "security"
	SpecTech       = "tech"
	SpecVibeCoach  = "vibe-coach"
)

// AllSpecialists is the set of code-reviewing specialists, ordered roughly
// from most local (formatting) to most cross-cutting (security), followed by
// the tech specialist that enforces the configured technology-expert briefs
// against the diff. The vibe coach is intentionally omitted — it runs after
// the others as a second pass.
var AllSpecialists = []string{
	SpecFormatting,
	SpecDesign,
	SpecTesting,
	SpecDocs,
	SpecSecurity,
	SpecTech,
}

// Finding is one item from a specialist. Use a concrete path and line > 0 for
// feedback tied to a location in the diff (posted as a GitHub inline review
// comment). Use path "" and line 0 for PR-level / general feedback (included
// only in the review body, not as an inline comment).
type Finding struct {
	Path     string   `json:"path"`
	Line     int      `json:"line"`
	Side     string   `json:"side,omitempty"` // LEFT or RIGHT; default RIGHT
	Severity Severity `json:"severity"`
	Comment  string   `json:"comment"`
	// Suggestion is optional: only GitHub-ready replacement text for ```suggestion
	// (see SuggestionPostsToGitHub). Narrative belongs in Comment alone.
	Suggestion string `json:"suggestion,omitempty"`
	// AnchorExcerpt is the model's verbatim copy of the post-image line at
	// Path:Line. The reviewOutputContract asks specialists to include it on
	// every inline finding so we can deterministically check that the model
	// anchored where it thinks it did. Empty when the model omitted the
	// field (older runs / backends that strip unknown keys); the
	// validateAnchorExcerpt gate is silent in that case. Never posted to
	// GitHub — diagnostic field only.
	AnchorExcerpt string `json:"anchor_excerpt,omitempty"`
	// SuggestionStrippedReason is set when validateAndPruneSuggestions cleared
	// a non-empty Suggestion because applying it would clearly break the file
	// (no-op replace, duplicates a nearby line, anchor-vs-comment mismatch),
	// or when validateAnchorExcerpt cleared a suggestion because the model's
	// AnchorExcerpt did not match the line at Path:Line and could not be
	// uniquely relocated within the same hunk.
	// Carried through to the TUI so the human reviewer can see why the
	// one-click fix is missing instead of guessing the model "forgot". Never
	// posted to GitHub.
	SuggestionStrippedReason string `json:"-"`
	// ActionabilityNote is set when validateActionability flags the finding's
	// comment as a bare deficiency statement ("lacks a comment", "missing
	// docs") with no proposed wording. The validator demotes severity to
	// info in that case; this field records why so the TUI can hint at the
	// reason. Never posted to GitHub.
	ActionabilityNote string `json:"-"`
	// AnchorRelocatedFrom records the original (wrong) line number when
	// validateAnchorExcerpt moved this finding to a different line in the
	// same hunk because the model's AnchorExcerpt uniquely matched there.
	// Zero when no relocation happened. Used by the TUI to render an
	// "auto-corrected from line N → M" note so the reviewer can sanity-check
	// the new position before accepting. Never posted to GitHub.
	AnchorRelocatedFrom int `json:"-"`
	// SuggestionSynthesized is true when synthesizeSuggestions built the
	// Suggestion from the finding's comment (the model named the corrected
	// token but emitted no suggestion of its own). The text is a string
	// substitution on the anchor line, not the model's verbatim output, so
	// the TUI card and the posted GitHub comment disclose it as
	// appr-ai-sal-derived and ask the reviewer to check it before applying.
	// Never posted to GitHub as a field.
	SuggestionSynthesized bool `json:"-"`
	// SuggestionRepaired is true when the batched suggestion-repair pass
	// (repairMissingSuggestions) generated this Suggestion: a focused second
	// model call picked the anchor line and wrote the replacement for a
	// finding the first pass left without a usable fix. Like
	// SuggestionSynthesized it drives a disclosure note on the TUI card and
	// the posted comment so the reviewer knows to check it. Never posted to
	// GitHub as a field.
	SuggestionRepaired bool `json:"-"`
}

// findingIsInlinePostable reports whether f should become a GitHub inline comment.
func findingIsInlinePostable(f Finding) bool {
	return strings.TrimSpace(f.Path) != "" && f.Line > 0 && strings.TrimSpace(f.Comment) != ""
}

// generalFindings returns findings meant for the review body (no inline anchor).
func generalFindings(findings []Finding) []Finding {
	var out []Finding
	for _, f := range findings {
		if strings.TrimSpace(f.Comment) == "" {
			continue
		}
		if findingIsInlinePostable(f) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// SuggestionPostsToGitHub reports whether the finding's suggestion field is
// emitted as a GitHub ```suggestion block (as opposed to comment-only).
func SuggestionPostsToGitHub(f Finding) bool {
	s := strings.TrimSpace(f.Suggestion)
	if s == "" {
		return false
	}
	if strings.Contains(s, "```") {
		return false
	}
	comment := strings.TrimSpace(f.Comment)
	if s == comment {
		return false
	}
	// Avoid treating huge pasted explanations as code hunks.
	if len(s) > 8192 {
		return false
	}
	return true
}

// SpecialistResult is the output of one specialist over the PR.
type SpecialistResult struct {
	Specialist string    `json:"specialist"`
	Summary    string    `json:"summary"`
	Findings   []Finding `json:"findings"`
	// Err is non-nil if the specialist failed to run; Summary/Findings will be
	// empty in that case.
	Err error `json:"-"`
	// RepairFired / RepairSucceeded record the suggestion-repair pass's
	// hidden second LLM call: how many suggestion-less findings were sent to
	// the repair model and how many came back with a re-validated one-click
	// fix. Surfaced as Progress telemetry so the run's repair activity is
	// observable. Never posted to GitHub.
	RepairFired     int `json:"-"`
	RepairSucceeded int `json:"-"`
}

// SpecialistsHaveAnyFindings reports whether any specialist produced at least
// one finding (after strictness filtering). When false, the runner skips
// vibe-coach and the repo expert panel — nothing for those passes to synthesize.
func SpecialistsHaveAnyFindings(specialists []SpecialistResult) bool {
	for _, r := range specialists {
		if len(r.Findings) > 0 {
			return true
		}
	}
	return false
}

// SuppressedFindingRef identifies an inline finding the repo arbiter recommends not posting.
type SuppressedFindingRef struct {
	Specialist string `json:"specialist"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Side       string `json:"side,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// DemotedFindingRef identifies an inline finding whose severity the repo
// arbiter recommends dropping by exactly one rank (error→warning, warning→info).
// From and To are recorded so the TUI can show "was: error, now: warning".
type DemotedFindingRef struct {
	Specialist string   `json:"specialist"`
	Path       string   `json:"path"`
	Line       int      `json:"line"`
	Side       string   `json:"side,omitempty"`
	From       Severity `json:"from,omitempty"`
	To         Severity `json:"to,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

// RepoArbiterResult merges repo experts with specialist output; may adjust verdict and suppress inline posts.
type RepoArbiterResult struct {
	UserSummary      string
	RationaleBullets []string
	VerdictOverride  string // empty = keep vibe-coach verdict
	EffectiveVerdict string // filled at apply time: override or original
	SummaryMode      string // none | append | replace
	SummaryAddendum  string
	SummaryReplace   string
	Suppressed       []SuppressedFindingRef
	suppressKeySet   map[string]struct{} // populated by ApplyToDraft
	// Demoted lists arbiter-recommended one-rank severity drops. Validated
	// and applied by FinalizeRepoArbiter (mutates Finding.Severity in place,
	// then re-runs the strictness floor so demoted-to-info findings can
	// disappear under balanced/lenient/critical-only).
	Demoted             []DemotedFindingRef
	demoteKeySet        map[string]Severity // populated by FinalizeRepoArbiter; key→original severity
	DroppedDemotions    []string            // human-readable reject reasons
	DroppedSuppressions []string            // human-readable reject reasons
	Err                 error
}

// RepoArbiterSuppressionCount returns how many inline findings were suppressed for posting.
func (r *RepoArbiterResult) RepoArbiterSuppressionCount() int {
	if r == nil {
		return 0
	}
	return len(r.Suppressed)
}

// FlatFinding is one postable inline finding with its specialist context.
type FlatFinding struct {
	Specialist string
	SpecIndex  int
	FindIndex  int
	Finding    Finding
}
