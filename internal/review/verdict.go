package review

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Vibe-coach domain types
// ---------------------------------------------------------------------------

// Vibe verdict values for VibeCoachResult.Verdict. The persistent overlay's
// confirm-approve flow uses these to map to GitHub review events
// (Draft.PostEvent); the legacy bulk-post path keeps event=COMMENT.
const (
	VibeVerdictApprove        = "approve"
	VibeVerdictRequestChanges = "request_changes"
	VibeVerdictComment        = "comment"
)

// NormalizeVibeVerdict maps model output to a canonical verdict, or "" if unknown.
func NormalizeVibeVerdict(s string) string {
	v := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(s, "-", "_")))
	switch v {
	case "approve", "approved", "lgtm":
		return VibeVerdictApprove
	case "request_changes", "requestchanges", "changes_requested", "reject", "blocked":
		return VibeVerdictRequestChanges
	case "comment", "comment_only", "neutral", "none":
		return VibeVerdictComment
	default:
		return ""
	}
}

// VibeVerdictShortLabel is a short title for TUI banners (empty if unknown).
func VibeVerdictShortLabel(canonical string) string {
	switch canonical {
	case VibeVerdictApprove:
		return "Approve"
	case VibeVerdictRequestChanges:
		return "Request changes"
	case VibeVerdictComment:
		return "Comment only"
	default:
		return ""
	}
}

// VibeCoachResult wraps the vibe coach's pass over the other specialists'
// output.
type VibeCoachResult struct {
	// Verdict is the vibe-coach merge recommendation: approve, request_changes, or comment.
	Verdict string
	Summary string
	Prompts []AuthorPrompt
	// RequestChangesWithoutPrompts is true when verdict was request_changes but
	// the model returned no prompts (contract violation); RenderBody adds a notice.
	RequestChangesWithoutPrompts bool
	// Err is non-nil if the vibe-coach stage failed. json:"-" because an error
	// value is not round-trippable and would break the U1 headless Draft dump
	// and the U2 session snapshot.
	Err error `json:"-"`
}

// FindingRef identifies a specific specialist finding that an AuthorPrompt
// bundles. It mirrors the (specialist, path, line, side) tuple used by the
// repo arbiter's suppression set and the user-skip set, so the renderer can
// drop a vibe-coach prompt whose every referenced finding was suppressed by
// the arbiter or skipped by the reviewer in the approval flow.
//
// Vibe-coach is instructed to populate this list with the actual findings
// each prompt is meant to address. Legacy / general prompts that don't tie
// to specific findings can leave it empty — those are kept unconditionally.
type FindingRef struct {
	Specialist string `json:"specialist"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Side       string `json:"side,omitempty"` // LEFT or RIGHT; default RIGHT
}

// AuthorPrompt is one of the high-leverage prompts the vibe coach produces
// for the PR author to paste back into their own AI assistant.
//
// Vibe-coach output is deliberately split into two pieces so the TUI can
// distinguish the human-reader explanation from the verbatim text the author
// is meant to paste into an AI coding assistant. Older outputs may still come
// back with the legacy `Prompt` field — `AgentPromptText` falls back to that.
type AuthorPrompt struct {
	Title       string `json:"title"`
	Rationale   string `json:"rationale,omitempty"`
	AgentPrompt string `json:"agent_prompt,omitempty"`
	// FindingRefs lists the specialist findings this prompt bundles. When
	// non-empty, the renderer drops this prompt if every referenced finding
	// was suppressed by the repo arbiter or skipped by the user; an empty
	// list means "no specific anchor — keep unconditionally" (legacy
	// outputs and general prompts).
	FindingRefs []FindingRef `json:"finding_refs,omitempty"`
	// Prompt is the legacy single-field shape; treat it as agent text on read.
	Prompt string `json:"prompt,omitempty"`
}

// AgentPromptText returns the verbatim block to paste into an AI assistant,
// preferring the new agent_prompt field and falling back to the legacy prompt.
func (a AuthorPrompt) AgentPromptText() string {
	if strings.TrimSpace(a.AgentPrompt) != "" {
		return a.AgentPrompt
	}
	return a.Prompt
}

// RationaleText returns a short human-reader explanation of why this prompt
// matters. Empty when the model didn't supply one (legacy outputs).
func (a AuthorPrompt) RationaleText() string {
	return strings.TrimSpace(a.Rationale)
}

// ---------------------------------------------------------------------------
// The merge-verdict state machine
//
// The final merge verdict used to be spread across five interacting
// functions (EffectiveMergeVerdict / ReconciledMergeVerdict /
// hasBlockingContent / verdictRank / effectiveVibeCoach), whose comments
// reference a string of past bugs ("Approve at the top, Request changes at
// the bottom", a relaxing arbiter override silently outranking a live
// request_changes, and a request_changes verdict that stuck after the user
// skipped every backing finding).
//
// verdictInputs + reduceMergeVerdict collapse that logic into ONE pure,
// enumerable transition. Every field of verdictInputs ranges over a small
// finite set, so reduceMergeVerdict is exhaustively table-tested (see
// verdict_test.go). The Draft methods below are thin adapters that derive a
// verdictInputs from the live draft and read the outcome back out — the
// interesting behavior lives entirely in the pure reducer.
// ---------------------------------------------------------------------------

// verdictRank orders merge verdicts from most permissive to most strict so
// the reducer can tell whether the arbiter's override relaxes or tightens the
// vibe-coach's verdict. Unknown/empty rank lowest.
func verdictRank(v string) int {
	switch NormalizeVibeVerdict(v) {
	case VibeVerdictRequestChanges:
		return 2
	case VibeVerdictComment:
		return 1
	case VibeVerdictApprove:
		return 0
	}
	return 0
}

// verdictInputs is the complete, enumerable input to reduceMergeVerdict.
//
// Enumerable state space:
//   - Vibe:          "" | approve | comment | request_changes  (canonical)
//   - ArbiterActive: false | true
//   - Arbiter:       "" | approve | comment | request_changes  (canonical;
//     meaningful only when ArbiterActive)
//   - Blocking:      false | true
//
// That is 4 × 2 × 4 × 2 = 64 states, all covered by the table test.
type verdictInputs struct {
	// Vibe is the vibe-coach's canonical verdict (empty when there is no
	// vibe-coach result).
	Vibe string
	// ArbiterActive mirrors the production guard: the repo arbiter ran, did
	// not error, and produced a non-empty EffectiveVerdict. Only then does
	// the arbiter participate in the verdict.
	ArbiterActive bool
	// Arbiter is the arbiter's canonical EffectiveVerdict (meaningful only
	// when ArbiterActive).
	Arbiter string
	// Blocking reports whether substantive blocking content survives: an
	// error/critical finding (inline post-skip or PR-wide), a surviving
	// paste-ready prompt, or an explicit arbiter request_changes override.
	Blocking bool
}

// verdictOutcome is the reducer's output: both the raw effective verdict and
// the reconciled verdict that actually gets posted.
type verdictOutcome struct {
	// Effective is the canonical verdict after the arbiter override guard
	// (the value EffectiveMergeVerdict returns).
	Effective string
	// Reconciled downgrades a request_changes Effective to comment when no
	// blocking content remains (the value ReconciledMergeVerdict, PostEvent,
	// and the rendered body all use).
	Reconciled string
}

// reduceMergeVerdict is the single, pure merge-verdict transition.
//
// Effective:
//   - With no active arbiter, the vibe-coach verdict stands.
//   - An active arbiter may make the verdict STRICTER for free, but may only
//     RELAX it (toward comment/approve) when no blocking content survives. A
//     relaxing override with live blockers is clamped back to the vibe-coach
//     verdict — the arbiter must suppress/demote the blockers first, and
//     Blocking already accounts for the arbiter's own suppressions/demotions,
//     so an arbiter that did the work still earns its relaxed verdict.
//
// Reconciled:
//   - A request_changes Effective is downgraded to comment when no blocking
//     content remains after user skips and arbiter suppressions. "Blocking
//     content" is intentionally narrow; a vague vibe-coach summary is not
//     enough on its own.
func reduceMergeVerdict(in verdictInputs) verdictOutcome {
	effective := in.Vibe
	if in.ArbiterActive {
		if verdictRank(in.Arbiter) < verdictRank(in.Vibe) && in.Blocking {
			effective = in.Vibe // relaxing override clamped by live blockers
		} else {
			effective = in.Arbiter
		}
	}

	reconciled := effective
	if NormalizeVibeVerdict(effective) == VibeVerdictRequestChanges && !in.Blocking {
		reconciled = VibeVerdictComment
	}

	return verdictOutcome{Effective: effective, Reconciled: reconciled}
}

// mergeVerdictInputs derives the reducer's enumerable input from the live
// draft. This is the ONLY place the draft's shape is translated into the
// verdict state; everything downstream reasons over verdictInputs.
func (d *Draft) mergeVerdictInputs() verdictInputs {
	in := verdictInputs{}
	if d.VibeCoach != nil {
		in.Vibe = NormalizeVibeVerdict(d.VibeCoach.Verdict)
	}
	if ar := d.RepoArbiter; ar != nil && ar.Err == nil && ar.EffectiveVerdict != "" {
		in.ArbiterActive = true
		in.Arbiter = NormalizeVibeVerdict(ar.EffectiveVerdict)
	}
	in.Blocking = d.hasBlockingContent()
	return in
}

// EffectiveMergeVerdict returns the canonical verdict after repo arbiter (or
// vibe-coach if none).
//
// This is the *raw* verdict — for the verdict that actually gets posted, use
// ReconciledMergeVerdict, which additionally downgrades request_changes when
// no blocking content remains after user skips.
func (d *Draft) EffectiveMergeVerdict() string {
	if d == nil {
		return ""
	}
	return reduceMergeVerdict(d.mergeVerdictInputs()).Effective
}

// ReconciledMergeVerdict returns EffectiveMergeVerdict but downgrades a
// request_changes verdict to comment when no blocking content remains in the
// body after user skips and arbiter suppressions. PostEvent and the rendered
// body both use this so the GitHub event and the displayed verdict stay in
// sync with what the user actually chose to post.
func (d *Draft) ReconciledMergeVerdict() string {
	if d == nil {
		return ""
	}
	return reduceMergeVerdict(d.mergeVerdictInputs()).Reconciled
}

// VerdictReconciliationNote returns a short markdown sentence explaining
// why the reconciled verdict differs from the raw effective verdict, or "" if
// no reconciliation happened. Rendered above the merge section so the user
// understands the downgrade.
func (d *Draft) VerdictReconciliationNote() string {
	raw := NormalizeVibeVerdict(d.EffectiveMergeVerdict())
	rec := NormalizeVibeVerdict(d.ReconciledMergeVerdict())
	if raw == rec || raw == "" || rec == "" {
		return ""
	}
	return fmt.Sprintf("**Verdict downgraded from %s to %s** — the inline findings supporting the original verdict were all suppressed or skipped during review, and no error/critical severity content remains.",
		VibeVerdictShortLabel(raw), VibeVerdictShortLabel(rec))
}

// hasBlockingContent reports whether the body still has substantive blockers
// for a request_changes verdict after user skips. It is the Blocking input to
// reduceMergeVerdict.
//
// "Blocking content" is intentionally narrow: error/critical-severity
// findings (inline post-skip or PR-wide), surviving paste-ready prompts, or
// an explicit arbiter request_changes override.
func (d *Draft) hasBlockingContent() bool {
	if d == nil {
		return false
	}
	for _, ff := range d.FlatPostableFindingsForPost() {
		sv := ff.Finding.Severity
		if sv == SeverityError || sv == SeverityCritical {
			return true
		}
	}
	for _, s := range d.Specialists {
		if s.Err != nil {
			continue
		}
		for _, f := range s.Findings {
			if findingIsInlinePostable(f) {
				continue
			}
			if strings.TrimSpace(f.Comment) == "" {
				continue
			}
			if f.Severity == SeverityError || f.Severity == SeverityCritical {
				return true
			}
		}
	}
	if d.VibeCoach != nil && d.VibeCoach.Err == nil {
		kept, _ := filterAuthorPrompts(d, d.VibeCoach.Prompts)
		if len(kept) > 0 {
			return true
		}
	}
	if d.RepoArbiter != nil && d.RepoArbiter.Err == nil &&
		NormalizeVibeVerdict(d.RepoArbiter.VerdictOverride) == VibeVerdictRequestChanges {
		return true
	}
	return false
}

// effectiveVibeCoach returns a copy of VibeCoach with arbiter verdict/summary
// overrides applied (for display/post body only). It also applies the
// user-skip reconciliation pass — see ReconciledMergeVerdict — so the
// verdict that gets rendered matches the GitHub event we'll post.
func (d *Draft) effectiveVibeCoach() *VibeCoachResult {
	if d == nil || d.VibeCoach == nil {
		return nil
	}
	vc := *d.VibeCoach
	if ar := d.RepoArbiter; ar != nil && ar.Err == nil {
		switch strings.ToLower(strings.TrimSpace(ar.SummaryMode)) {
		case "replace":
			if strings.TrimSpace(ar.SummaryReplace) != "" {
				vc.Summary = strings.TrimSpace(ar.SummaryReplace)
			}
		case "append":
			if strings.TrimSpace(ar.SummaryAddendum) != "" {
				base := strings.TrimSpace(vc.Summary)
				add := strings.TrimSpace(ar.SummaryAddendum)
				if base == "" {
					vc.Summary = "**Repo experts:** " + add
				} else {
					vc.Summary = base + "\n\n**Repo experts:** " + add
				}
			}
		}
	}
	// Display the SAME verdict that gets posted. ReconciledMergeVerdict folds
	// in the arbiter's override guard (a relaxing override is clamped while
	// blocking content survives) AND the request_changes→comment downgrade
	// when no blockers remain. Applying ar.VerdictOverride directly here was
	// the bug behind "Approve at the top, Request changes at the bottom": the
	// headline showed the arbiter's raw wish while the posted event, the TUI
	// card, and the arbiter panel all showed the guarded result. No recursion:
	// ReconciledMergeVerdict reads d.VibeCoach / d.RepoArbiter, not this copy.
	vc.Verdict = d.ReconciledMergeVerdict()
	if NormalizeVibeVerdict(vc.Verdict) != VibeVerdictRequestChanges {
		vc.RequestChangesWithoutPrompts = false
	}
	return &vc
}
