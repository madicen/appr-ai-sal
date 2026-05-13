// Package review orchestrates specialist AI agents over a checked-out PR
// and assembles their output into a single draft GitHub review.
package review

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review/conventionwitness"
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
	SpecVibeCoach  = "vibe-coach"
)

// AllSpecialists is the set of code-reviewing specialists, ordered roughly
// from most local (formatting) to most cross-cutting (security). The vibe
// coach is intentionally omitted — it runs after the others as a second pass.
var AllSpecialists = []string{
	SpecFormatting,
	SpecDesign,
	SpecTesting,
	SpecDocs,
	SpecSecurity,
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

// inlineFindings returns findings that map to a diff line.
func inlineFindings(findings []Finding) []Finding {
	var out []Finding
	for _, f := range findings {
		if findingIsInlinePostable(f) {
			out = append(out, f)
		}
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
}

// severityFloor returns the strictness floor recorded on the Draft, or
// SeverityInfo when no strictness was set (treated as "keep everything").
// FinalizeRepoArbiter uses this to re-filter findings after demotions.
func (d *Draft) severityFloor() Severity {
	if d == nil || d.Strictness == "" {
		return SeverityInfo
	}
	return MinSeverityForStrictness(d.Strictness)
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
	Err                          error
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
	UserSummary         string
	RationaleBullets    []string
	VerdictOverride     string // empty = keep vibe-coach verdict
	EffectiveVerdict    string // filled at apply time: override or original
	SummaryMode         string // none | append | replace
	SummaryAddendum     string
	SummaryReplace      string
	Suppressed          []SuppressedFindingRef
	suppressKeySet      map[string]struct{} // populated by ApplyToDraft
	// Demoted lists arbiter-recommended one-rank severity drops. Validated
	// and applied by FinalizeRepoArbiter (mutates Finding.Severity in place,
	// then re-runs the strictness floor so demoted-to-info findings can
	// disappear under balanced/lenient/critical-only).
	Demoted          []DemotedFindingRef
	demoteKeySet     map[string]Severity // populated by FinalizeRepoArbiter; key→original severity
	DroppedDemotions []string            // human-readable reject reasons
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

const aiCommentLead = "**AI-generated review comment** — tool: **appr-ai-sal**, agent: **%s**\n\n"

// ReviewCommentBody formats the GitHub comment body for a finding (same as ToReview).
func ReviewCommentBody(specialist string, f Finding) string {
	body := fmt.Sprintf(aiCommentLead, specialist) + f.Comment
	if SuggestionPostsToGitHub(f) {
		s := strings.TrimRight(strings.TrimSpace(f.Suggestion), "\n")
		body += "\n\n```suggestion\n" + s + "\n```"
	}
	return body
}

// FlatPostableFindings returns inline findings that can be posted (path + line set).
func (d *Draft) FlatPostableFindings() []FlatFinding {
	if d == nil {
		return nil
	}
	var out []FlatFinding
	for si, s := range d.Specialists {
		if s.Err != nil {
			continue
		}
		for fi, f := range s.Findings {
			if strings.TrimSpace(f.Path) == "" || f.Line <= 0 {
				continue
			}
			out = append(out, FlatFinding{
				Specialist: s.Specialist,
				SpecIndex:  si,
				FindIndex:  fi,
				Finding:    f,
			})
		}
	}
	return out
}

// suppressionKey builds a stable key for matching arbiter suppressions to flat findings.
func suppressionKey(specialist, path string, line int, side string) string {
	side = strings.TrimSpace(side)
	if side == "" {
		side = "RIGHT"
	}
	return strings.ToLower(strings.TrimSpace(specialist)) + "|" + filepath.ToSlash(strings.TrimSpace(path)) + "|" + fmt.Sprintf("%d", line) + "|" + strings.ToUpper(side)
}

// FindingSuppressionKey returns suppressionKey for a finding (exported for TUI skip wiring).
func FindingSuppressionKey(specialist string, f Finding) string {
	side := f.Side
	if side == "" {
		side = "RIGHT"
	}
	return suppressionKey(specialist, f.Path, f.Line, side)
}

// FlatPostableFindingsForPost returns inline findings minus repo-arbiter suppressions.
func (d *Draft) FlatPostableFindingsForPost() []FlatFinding {
	all := d.FlatPostableFindings()
	if d == nil {
		return nil
	}
	var out []FlatFinding
	for _, f := range all {
		side := f.Finding.Side
		if side == "" {
			side = "RIGHT"
		}
		k := suppressionKey(f.Specialist, f.Finding.Path, f.Finding.Line, side)
		if d.RepoArbiter != nil && d.RepoArbiter.Err == nil && len(d.RepoArbiter.suppressKeySet) > 0 {
			if _, drop := d.RepoArbiter.suppressKeySet[k]; drop {
				continue
			}
		}
		if len(d.UserSkipPostKeys) > 0 {
			if _, skip := d.UserSkipPostKeys[k]; skip {
				continue
			}
		}
		out = append(out, f)
	}
	return out
}

// SpecialistsForVibeCoach returns a copy of specialists with inline
// findings removed when they are either suppressed by the repo arbiter
// (after FinalizeRepoArbiter) OR skipped by the user (d.UserSkipPostKeys).
// This is the canonical "post-pipeline" view that vibe-coach receives so
// its Summary / Prompts / Verdict reflect only the findings the reviewer
// is actually going to ship. Returns specialists unchanged when neither
// filter has anything to apply.
//
// Note that PR-wide findings (empty path / line 0) are intentionally
// kept regardless of the skip set — they cannot be inline-suppressed and
// the skip flow only targets inline cards.
func SpecialistsForVibeCoach(d *Draft, specialists []SpecialistResult) []SpecialistResult {
	if d == nil {
		return specialists
	}
	var sup map[string]struct{}
	if d.RepoArbiter != nil && d.RepoArbiter.Err == nil && len(d.RepoArbiter.suppressKeySet) > 0 {
		sup = d.RepoArbiter.suppressKeySet
	}
	skips := d.UserSkipPostKeys
	if len(sup) == 0 && len(skips) == 0 {
		return specialists
	}
	out := make([]SpecialistResult, len(specialists))
	for i, s := range specialists {
		out[i] = s
		if s.Err != nil {
			continue
		}
		var kept []Finding
		for _, f := range s.Findings {
			if strings.TrimSpace(f.Path) != "" && f.Line > 0 {
				k := FindingSuppressionKey(s.Specialist, f)
				if _, drop := sup[k]; drop {
					continue
				}
				if _, drop := skips[k]; drop {
					continue
				}
			}
			kept = append(kept, f)
		}
		out[i].Findings = kept
	}
	return out
}

// HasRepoExpertSuppressions reports whether any inline finding was marked suppressed for posting.
func (d *Draft) HasRepoExpertSuppressions() bool {
	if d == nil || d.RepoArbiter == nil || d.RepoArbiter.Err != nil {
		return false
	}
	return len(d.RepoArbiter.suppressKeySet) > 0
}

// HasRepoExpertDemotions reports whether the arbiter applied any demotions
// to the draft. Used by the TUI to decide whether to render the demoted
// badge column on approval cards.
func (d *Draft) HasRepoExpertDemotions() bool {
	if d == nil || d.RepoArbiter == nil || d.RepoArbiter.Err != nil {
		return false
	}
	return len(d.RepoArbiter.demoteKeySet) > 0
}

// FindingOriginalSeverity returns the severity that the matching finding
// carried before the arbiter demoted it (if any), and a flag indicating
// whether a demotion was recorded. Used by the TUI to render a "was: X,
// now: Y" badge.
func (d *Draft) FindingOriginalSeverity(specialist string, f Finding) (Severity, bool) {
	if d == nil || d.RepoArbiter == nil || d.RepoArbiter.Err != nil {
		return "", false
	}
	side := f.Side
	if side == "" {
		side = "RIGHT"
	}
	k := suppressionKey(specialist, f.Path, f.Line, side)
	orig, ok := d.RepoArbiter.demoteKeySet[k]
	return orig, ok
}

// HasNoFindings reports whether the entire pipeline came back clean — no
// inline findings, no general PR-wide notes, no vibe-coach paste-ready
// prompts (or substantive summary), no repo arbiter suppressions / panel
// content, no request-changes verdict, and no specialist failures. The TUI
// uses this to route directly to the APPROVE confirmation (with a "no issues
// found" body) instead of dumping the user on a near-empty post-summary
// screen when there is genuinely nothing to say.
//
// A request-changes verdict deliberately disqualifies a draft even without
// concrete findings: the vibe-coach is signalling "block this" and the
// reviewer should see the warning, not an auto-approve.
func (d *Draft) HasNoFindings() bool {
	if d == nil {
		return false
	}
	if len(d.FlatPostableFindings()) > 0 {
		return false
	}
	for _, s := range d.Specialists {
		if s.Err != nil {
			return false
		}
		for _, f := range s.Findings {
			if strings.TrimSpace(f.Comment) != "" {
				return false
			}
		}
	}
	if d.VibeCoach != nil {
		if d.VibeCoach.Err != nil {
			return false
		}
		if len(d.VibeCoach.Prompts) > 0 {
			return false
		}
		if strings.TrimSpace(d.VibeCoach.Summary) != "" {
			return false
		}
		if NormalizeVibeVerdict(d.VibeCoach.Verdict) == VibeVerdictRequestChanges {
			return false
		}
	}
	if d.RepoArbiter != nil {
		if d.RepoArbiter.Err != nil {
			return false
		}
		if len(d.RepoArbiter.Suppressed) > 0 {
			return false
		}
		if strings.TrimSpace(d.RepoArbiter.UserSummary) != "" {
			return false
		}
		if len(d.RepoArbiter.RationaleBullets) > 0 {
			return false
		}
		if NormalizeVibeVerdict(d.RepoArbiter.EffectiveVerdict) == VibeVerdictRequestChanges {
			return false
		}
	}
	return true
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
		if v := NormalizeVibeVerdict(ar.VerdictOverride); v != "" {
			vc.Verdict = v
		}
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
	if NormalizeVibeVerdict(vc.Verdict) == VibeVerdictRequestChanges && !d.hasBlockingContent() {
		vc.Verdict = VibeVerdictComment
	}
	if NormalizeVibeVerdict(vc.Verdict) != VibeVerdictRequestChanges {
		vc.RequestChangesWithoutPrompts = false
	}
	return &vc
}

// EffectiveMergeVerdict returns the canonical verdict after repo arbiter (or vibe-coach if none).
//
// This is the *raw* verdict — for the verdict that actually gets posted,
// use ReconciledMergeVerdict, which additionally downgrades request_changes
// when no blocking content remains after user skips.
func (d *Draft) EffectiveMergeVerdict() string {
	if d == nil {
		return ""
	}
	if d.RepoArbiter != nil && d.RepoArbiter.Err == nil && d.RepoArbiter.EffectiveVerdict != "" {
		return d.RepoArbiter.EffectiveVerdict
	}
	if d.VibeCoach != nil {
		return NormalizeVibeVerdict(d.VibeCoach.Verdict)
	}
	return ""
}

// ReconciledMergeVerdict returns EffectiveMergeVerdict but downgrades a
// request_changes verdict to comment when no blocking content remains in
// the body after user skips and arbiter suppressions. PostEvent and the
// rendered body both use this so the GitHub event and the displayed verdict
// stay in sync with what the user actually chose to post.
//
// "Blocking content" is intentionally narrow: error/critical-severity
// findings (inline post-skip or PR-wide), surviving paste-ready prompts, or
// an explicit arbiter request_changes override. A vague vibe-coach summary
// is *not* enough on its own — if every concrete blocker was removed, we
// trust the user's choices over the vibe-coach's prose.
func (d *Draft) ReconciledMergeVerdict() string {
	raw := d.EffectiveMergeVerdict()
	if NormalizeVibeVerdict(raw) != VibeVerdictRequestChanges {
		return raw
	}
	if d.hasBlockingContent() {
		return raw
	}
	return VibeVerdictComment
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
// for a request_changes verdict after user skips. Used by
// ReconciledMergeVerdict and effectiveVibeCoach.
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

// Draft is what the TUI renders and (on confirm) posts to GitHub.
type Draft struct {
	Ref         gh.Ref
	PR          *gh.PR
	Diff        string
	Worktree    string
	// Strictness is the review intensity the specialists ran under. Used by
	// FinalizeRepoArbiter to re-apply the severity floor after demotions.
	Strictness  aiconfig.ReviewStrictness
	Specialists []SpecialistResult
	VibeCoach   *VibeCoachResult
	// RepositoryContext is the composed repo convention + optional merged-PR
	// culture block surfaced to the human in the TUI. Specialists no longer
	// receive this raw blob — they get topic-specific repo-agent briefs (see
	// internal/review/repoagents) injected per specialist instead.
	RepositoryContext string
	// ContextVersusChangeSummary is an optional AI narrative linking that bundle to this PR's diff.
	ContextVersusChangeSummary string
	// RepoArbiter runs as a final pass when the repo expert panel is enabled;
	// it consumes the per-agent briefs (already injected into specialists)
	// plus specialist findings, and may suppress findings or override the
	// merge verdict before vibe-coach.
	RepoArbiter *RepoArbiterResult
	// ConventionWitness captures the per-finding verdicts produced by the
	// convention-witness pass that runs between the specialists and the
	// arbiter. Currently scoped to testing/docs findings; empty when the
	// witness is disabled or no findings qualified.
	ConventionWitness []conventionwitness.Witness
	// UserSkipPostKeys holds suppressionKey entries for inline findings the reviewer chose
	// not to post (TUI skip). Used when rendering/parsing the summary body and inline batch.
	UserSkipPostKeys map[string]struct{} `json:"-"`
}

// ToReview assembles the draft into a GitHub review payload. Only findings
// with a path and line > 0 become inline comments; general findings appear in
// RenderBody. Every inline body states that appr-ai-sal generated it and
// names the specialist agent.
//
// The default event is COMMENT — the safe choice for the legacy bulk-post
// path (P key) where the user only confirmed "post the whole review", not
// "submit my Approve / Request changes verdict". The persistent overlay's
// confirm-approve phase uses ToReviewForEvent to post with a verdict-driven
// event after explicit user confirmation.
func (d *Draft) ToReview() gh.Review {
	return d.ToReviewForEvent("COMMENT")
}

// ToReviewForEvent is the explicit-event variant of ToReview. event must be
// "COMMENT", "REQUEST_CHANGES", or "APPROVE". When event is "APPROVE" the
// body is intentionally empty per RenderBodyForEvent (no summary text).
func (d *Draft) ToReviewForEvent(event string) gh.Review {
	if event == "" {
		event = "COMMENT"
	}
	var comments []gh.ReviewComment
	for _, ff := range d.FlatPostableFindingsForPost() {
		f := ff.Finding
		body := ReviewCommentBody(ff.Specialist, f)
		side := f.Side
		if side == "" {
			side = "RIGHT"
		}
		comments = append(comments, gh.ReviewComment{
			Path: f.Path,
			Line: f.Line,
			Side: side,
			Body: body,
		})
	}
	return gh.Review{
		CommitID: d.PR.HeadSHA,
		Body:     d.RenderBodyForEvent(event),
		Event:    event,
		Comments: comments,
	}
}

// PostEvent maps the reconciled merge verdict to a GitHub review event
// ("APPROVE" | "REQUEST_CHANGES" | "COMMENT"). Defaults to COMMENT when no
// verdict has been resolved.
//
// PostEvent uses ReconciledMergeVerdict (not EffectiveMergeVerdict) so that
// when the user skips every inline finding backing a request_changes verdict
// and no other blockers remain, the GitHub event we send drops to COMMENT
// rather than asking the author to address objections we no longer make.
func (d *Draft) PostEvent() string {
	if d == nil {
		return "COMMENT"
	}
	switch NormalizeVibeVerdict(d.ReconciledMergeVerdict()) {
	case VibeVerdictApprove:
		return "APPROVE"
	case VibeVerdictRequestChanges:
		return "REQUEST_CHANGES"
	default:
		return "COMMENT"
	}
}

// RenderBodyForEvent returns the markdown body to attach to the GitHub review
// for the given event. APPROVE normally posts an empty body — the user only
// confirmed approval, there is no summary to read. The exception is the
// "no issues found" auto-approve path: when every agent came back clean we
// post the rendered body anyway so the GitHub review explains why we
// approved (instead of looking like a content-free thumbs-up). Other events
// always post the full RenderBody.
func (d *Draft) RenderBodyForEvent(event string) string {
	if event == "APPROVE" {
		if d != nil && d.HasNoFindings() {
			return d.RenderBody()
		}
		return ""
	}
	return d.RenderBody()
}

func normalizeGitHubLogin(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "@")
	return strings.TrimSpace(s)
}

// EffectiveReviewEventAndBody returns the GitHub pull request review event and
// body for POST .../pulls/{n}/reviews. If the authenticated viewer is the PR
// author, GitHub rejects event=APPROVE and event=REQUEST_CHANGES (HTTP 422); in
// that case this returns event COMMENT and the full rendered summary with a
// short preamble so the content still lands on the PR.
//
// intendedEvent is the resolved event before that downgrade (after defaulting an
// empty requestedEvent to draft.PostEvent()), and matches event when nothing was
// coerced.
func EffectiveReviewEventAndBody(d *Draft, requestedEvent, viewerLogin string) (event string, body string, intendedEvent string) {
	requestedEvent = strings.TrimSpace(strings.ToUpper(requestedEvent))
	if requestedEvent == "" && d != nil {
		requestedEvent = d.PostEvent()
	}
	if requestedEvent == "" {
		requestedEvent = "COMMENT"
	}
	intendedEvent = requestedEvent
	if d == nil || d.PR == nil {
		return requestedEvent, "", intendedEvent
	}
	body = d.RenderBodyForEvent(requestedEvent)
	event = requestedEvent
	author := normalizeGitHubLogin(d.PR.Author)
	viewer := normalizeGitHubLogin(viewerLogin)
	if author == "" || viewer == "" || !strings.EqualFold(author, viewer) {
		return event, body, intendedEvent
	}
	if event != "APPROVE" && event != "REQUEST_CHANGES" {
		return event, body, intendedEvent
	}
	const note = "_GitHub does not allow **approve** or **request changes** reviews on your own pull request. Posted as a **comment** review._\n\n"
	full := strings.TrimSpace(d.RenderBody())
	if full == "" {
		full = "_No rendered summary body was available._"
	}
	return "COMMENT", note + full, intendedEvent
}

// combineAuthorPrompts joins vibe-coach prompts into one paste block for the
// review body. Each prompt is rendered as "## <title>\n\n<text>" inside a
// plain ` + "`text`" + ` fence, separated from the next prompt by "---".
//
// The "## " title prefix is plain text inside the fence (markdown isn't
// rendered there), but it still gives the author and any AI assistant a
// strong visual / structural cue that each section is its own topic. This
// matters when the vibe-coach correctly returns one prompt per distinct
// topic (refactor + README + CHANGELOG = three sections) — the human
// glancing at the review needs to see at a glance that there is more than
// one piece of work to do.
func combineAuthorPrompts(prompts []AuthorPrompt) string {
	var segments []string
	for _, p := range prompts {
		text := strings.TrimSpace(p.AgentPromptText())
		if text == "" {
			continue
		}
		title := strings.TrimSpace(p.Title)
		if title != "" {
			segments = append(segments, "## "+title+"\n\n"+text)
		} else {
			segments = append(segments, text)
		}
	}
	return strings.Join(segments, "\n\n---\n\n")
}

// countNonEmptyPrompts returns the number of prompts that contribute a
// non-empty agent_prompt body to the rendered fenced block. Used by the
// renderer to size the "It contains N distinct topics" disclosure so the
// number always matches what the reader sees in the fence.
func countNonEmptyPrompts(prompts []AuthorPrompt) int {
	n := 0
	for _, p := range prompts {
		if strings.TrimSpace(p.AgentPromptText()) != "" {
			n++
		}
	}
	return n
}

// vibeCoachMergeSection renders the verdict + summary block. verdictNote is
// an optional one-liner shown right below the verdict heading (used when the
// reconciler downgraded the verdict because user skips removed every
// blocker — see VerdictReconciliationNote).
func vibeCoachMergeSection(vc *VibeCoachResult, verdictNote string) string {
	if vc == nil {
		return ""
	}
	v := NormalizeVibeVerdict(vc.Verdict)
	hasSummary := strings.TrimSpace(vc.Summary) != ""
	hasPrompts := len(vc.Prompts) > 0
	hasNote := strings.TrimSpace(verdictNote) != ""
	if v == "" && !hasSummary && !hasPrompts && !hasNote {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Merge recommendation _(AI agent: vibe-coach)_\n\n")
	if lbl := VibeVerdictShortLabel(v); lbl != "" {
		b.WriteString("## Verdict: " + lbl + "\n\n")
	}
	if hasNote {
		b.WriteString("> " + strings.TrimSpace(verdictNote) + "\n\n")
	}
	if hasSummary {
		b.WriteString(vc.Summary)
		b.WriteString("\n\n")
	}
	if vc.RequestChangesWithoutPrompts {
		b.WriteString("**Warning:** Verdict is **request changes**, but no paste-ready AI prompts were returned. Rely on **inline comments on the diff** and PR-wide notes below (if any).\n\n")
	}
	b.WriteString("_This is guidance for reviewers; it does not submit GitHub **Approve** or **Request changes** automatically._\n\n")
	return b.String()
}

// vibeCoachSuggestedPromptsSection renders one combined fenced prompt (after merge recommendation).
//
// Prompts are filtered against the draft: any prompt whose finding_refs are
// all arbiter-suppressed or user-skipped is dropped, so the rendered review
// only suggests AI prompts for findings that actually made it through to
// GitHub. Legacy / general prompts (no finding_refs) are kept unconditionally
// — those don't tie to a specific anchor.
//
// After filtering, if there are still error/critical-severity findings the
// vibe-coach didn't cover (typical failure: a PR-wide testing/docs error
// the model forgot to bundle, or a single bundled prompt that got dropped
// because its only inline ref was suppressed), synthesizeFallbackPrompts
// builds one fallback AuthorPrompt per uncovered finding so the author
// always has a paste-ready instruction for every blocker.
func vibeCoachSuggestedPromptsSection(d *Draft, vc *VibeCoachResult) string {
	if vc == nil || vc.Err != nil {
		return ""
	}
	kept, droppedTitles := filterAuthorPrompts(d, vc.Prompts)
	synthesized := synthesizeFallbackPrompts(d, kept)
	rendered := append([]AuthorPrompt(nil), kept...)
	rendered = append(rendered, synthesized...)
	if len(rendered) == 0 && len(droppedTitles) == 0 {
		return ""
	}
	combined := strings.TrimSpace(combineAuthorPrompts(rendered))
	var b strings.Builder
	if combined != "" {
		topicCount := countNonEmptyPrompts(rendered)
		b.WriteString("### Suggested prompt for your AI assistant _(AI agent: vibe-coach)_\n\n")
		switch {
		case topicCount <= 1:
			b.WriteString("_Paste the fenced block below into your coding assistant (Cursor, Claude Code, etc.)._\n\n")
		default:
			fmt.Fprintf(&b, "_Paste the fenced block below into your coding assistant (Cursor, Claude Code, etc.). "+
				"It contains **%d distinct topics** separated by `---`; the author's AI should address each one._\n\n", topicCount)
		}
		if len(synthesized) > 0 {
			fmt.Fprintf(&b, "_%s auto-built from blocking findings the vibe-coach didn't bundle into a paste-ready prompt; the wording is verbatim from the specialist's comment._\n\n",
				countLabel(len(synthesized), "1 topic was", "%d topics were"))
		}
		b.WriteString("```text\n")
		b.WriteString(combined)
		b.WriteString("\n```\n\n")
	}
	// Disclosure: the human reader needs to know that some follow-up
	// prompts were silently dropped because every inline finding they
	// bundled was suppressed or skipped — otherwise the verdict / summary
	// above can look mismatched ("request changes" + zero prompts). The
	// wording is deliberately scoped to "inline findings the prompt
	// pointed to" so the reader doesn't conclude that *every* finding was
	// dropped (PR-wide notes, repo-expert panel content, and general
	// findings can't be suppressed and still appear below).
	switch {
	case len(droppedTitles) == 0:
		// nothing dropped, no note.
	case combined == "":
		fmt.Fprintf(&b, "_%s dropped because the inline findings they pointed to were all suppressed by the repo arbiter or skipped during review. The verdict above is based on the broader review (PR-wide notes and repo-expert panel below, if any), not just those inline findings._\n\n",
			countLabel(len(droppedTitles), "The 1 paste-ready follow-up prompt was", "All %d paste-ready follow-up prompts were"))
	default:
		fmt.Fprintf(&b, "_%s dropped because the inline findings they pointed to were all suppressed or skipped: %s._\n\n",
			countLabel(len(droppedTitles), "1 paste-ready follow-up prompt was", "%d paste-ready follow-up prompts were"),
			joinQuoted(droppedTitles))
	}
	return b.String()
}

// countLabel returns one of two phrasings depending on whether n == 1.
// The plural template may use a single %d placeholder for the count; the
// singular template is taken verbatim.
//
// Existed because the previous "_All " + plural(n, "follow-up prompt") +
// " were dropped_" produced ungrammatical "All 1 paste-ready follow-up
// prompt were dropped" when n == 1 — singular noun, plural verb.
func countLabel(n int, singular, pluralTpl string) string {
	if n == 1 {
		return singular
	}
	if strings.Contains(pluralTpl, "%d") {
		return fmt.Sprintf(pluralTpl, n)
	}
	return pluralTpl
}

// synthesizeFallbackPrompts builds one AuthorPrompt per error/critical
// finding that survives the user's review and the repo arbiter but isn't
// covered by any of the model's surviving prompts.
//
// "Covered" means the finding appears in some surviving prompt's
// finding_refs. "Blocking" means severity error or critical. Inline
// findings that already carry a one-click GitHub `suggestion` block on the
// diff are considered self-actionable and are not synthesized for —
// duplicating them as paste-ready prompts would just add noise.
//
// Returned prompts have an empty FindingRefs so they survive any future
// filter pass unconditionally; the agent_prompt body quotes the finding's
// location and comment so the author's AI gets the same context the human
// reviewer has.
func synthesizeFallbackPrompts(d *Draft, surviving []AuthorPrompt) []AuthorPrompt {
	if d == nil {
		return nil
	}
	covered := coveredFindingKeys(surviving)
	var out []AuthorPrompt

	for _, ff := range d.FlatPostableFindingsForPost() {
		f := ff.Finding
		if f.Severity != SeverityError && f.Severity != SeverityCritical {
			continue
		}
		if SuggestionPostsToGitHub(f) {
			continue
		}
		key := suppressionKey(ff.Specialist, f.Path, f.Line, f.Side)
		if _, ok := covered[key]; ok {
			continue
		}
		out = append(out, fallbackPromptForInline(ff.Specialist, f))
	}

	for _, s := range d.Specialists {
		if s.Err != nil {
			continue
		}
		for _, f := range generalFindings(s.Findings) {
			if f.Severity != SeverityError && f.Severity != SeverityCritical {
				continue
			}
			key := suppressionKey(s.Specialist, f.Path, f.Line, f.Side)
			if _, ok := covered[key]; ok {
				continue
			}
			out = append(out, fallbackPromptForGeneral(s.Specialist, f))
		}
	}
	return out
}

// coveredFindingKeys collects the suppressionKey for every finding bundled
// into any of the supplied prompts via finding_refs. Used by
// synthesizeFallbackPrompts to skip blockers the model already addressed.
func coveredFindingKeys(prompts []AuthorPrompt) map[string]struct{} {
	out := map[string]struct{}{}
	for _, p := range prompts {
		for _, ref := range p.FindingRefs {
			out[suppressionKey(ref.Specialist, ref.Path, ref.Line, ref.Side)] = struct{}{}
		}
	}
	return out
}

// fallbackPromptForInline constructs a paste-ready AuthorPrompt for an
// uncovered inline blocking finding. The body names the file and line so
// the author's AI can navigate directly, and quotes the specialist's
// comment verbatim — synthesizers can't invent acceptance criteria the
// reviewer didn't write, so the safest move is to surface the original
// language.
func fallbackPromptForInline(specialist string, f Finding) AuthorPrompt {
	title := fmt.Sprintf("%s: address %s:%d", specialist, f.Path, f.Line)
	body := fmt.Sprintf("In `%s` around line %d, the %s specialist flagged a %s-severity issue:\n\n%s\n\nFix the file at that location so the issue no longer applies.",
		f.Path, f.Line, specialist, string(f.Severity), strings.TrimSpace(f.Comment))
	return AuthorPrompt{
		Title:       title,
		Rationale:   fmt.Sprintf("Auto-generated from a %s-severity %s finding the vibe-coach did not bundle into a paste-ready prompt.", string(f.Severity), specialist),
		AgentPrompt: body,
	}
}

// fallbackPromptForGeneral constructs a paste-ready AuthorPrompt for an
// uncovered PR-wide blocking finding. The location context is "the
// repository" rather than a specific file because the specialist did not
// anchor the finding to a line.
func fallbackPromptForGeneral(specialist string, f Finding) AuthorPrompt {
	title := fmt.Sprintf("%s: %s", specialist, firstSentence(f.Comment))
	body := fmt.Sprintf("Repository-wide note from the %s specialist (%s severity):\n\n%s\n\nIdentify what in this PR's diff triggers the note above and address it.",
		specialist, string(f.Severity), strings.TrimSpace(f.Comment))
	return AuthorPrompt{
		Title:       title,
		Rationale:   fmt.Sprintf("Auto-generated from a PR-wide %s-severity %s finding the vibe-coach did not bundle into a paste-ready prompt.", string(f.Severity), specialist),
		AgentPrompt: body,
	}
}

// firstSentence returns the first sentence of s (up to the first '.', '!',
// '?' followed by space or end-of-string), trimmed and capped at ~80
// characters so it works as a prompt title.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for i, r := range s {
		if r == '.' || r == '!' || r == '?' {
			next := i + 1
			if next >= len(s) || s[next] == ' ' || s[next] == '\n' {
				s = s[:i]
				break
			}
		}
	}
	if len(s) > 80 {
		s = strings.TrimSpace(s[:80]) + "…"
	}
	return s
}

// filterAuthorPrompts returns the prompts that should still render plus the
// titles of those that were dropped because every referenced finding was
// arbiter-suppressed or user-skipped. Prompts with no finding_refs are kept
// unconditionally (legacy outputs / general advice).
func filterAuthorPrompts(d *Draft, prompts []AuthorPrompt) (kept []AuthorPrompt, droppedTitles []string) {
	if len(prompts) == 0 {
		return nil, nil
	}
	for _, p := range prompts {
		if isAuthorPromptAlive(d, p) {
			kept = append(kept, p)
			continue
		}
		title := strings.TrimSpace(p.Title)
		if title == "" {
			title = "untitled"
		}
		droppedTitles = append(droppedTitles, title)
	}
	return kept, droppedTitles
}

// isAuthorPromptAlive returns true when an AuthorPrompt should still render.
//
// Rules:
//   - No finding_refs → keep (general prompt, no specific anchor to filter
//     against).
//   - At least one ref still appears in d.FlatPostableFindingsForPost (i.e.
//     an inline ref that is not arbiter-suppressed and not user-skipped) →
//     keep.
//   - At least one ref points to a PR-wide finding (path empty, line 0)
//     that the specialist actually emitted → keep. PR-wide findings can't
//     be arbiter-suppressed or user-skipped, so a prompt that addresses
//     one is still actionable even if its inline siblings were filtered.
//     Without this, a vibe-coach prompt that bundles "fix this inline +
//     fix this PR-wide README issue" silently disappears the moment the
//     inline finding is suppressed, which is exactly the failure mode the
//     screenshot showed (request_changes verdict, zero rendered prompts,
//     PR-wide testing error stranded in the notes section).
//   - Every ref was suppressed or skipped → drop.
func isAuthorPromptAlive(d *Draft, p AuthorPrompt) bool {
	if len(p.FindingRefs) == 0 {
		return true
	}
	if d == nil {
		return true
	}
	live := map[string]struct{}{}
	for _, ff := range d.FlatPostableFindingsForPost() {
		live[suppressionKey(ff.Specialist, ff.Finding.Path, ff.Finding.Line, ff.Finding.Side)] = struct{}{}
	}
	prWide := prWideFindingKeys(d)
	for _, ref := range p.FindingRefs {
		side := ref.Side
		if side == "" {
			side = "RIGHT"
		}
		k := suppressionKey(ref.Specialist, ref.Path, ref.Line, side)
		if _, ok := live[k]; ok {
			return true
		}
		if isPRWideRef(ref) {
			if _, ok := prWide[k]; ok {
				return true
			}
		}
	}
	return false
}

// isPRWideRef reports whether a finding_ref points to a PR-wide finding
// (no path, no line). PR-wide refs are never inline-suppressible, so they
// are tracked separately from FlatPostableFindingsForPost in
// isAuthorPromptAlive.
func isPRWideRef(ref FindingRef) bool {
	return strings.TrimSpace(ref.Path) == "" && ref.Line == 0
}

// prWideFindingKeys indexes every PR-wide finding the specialists actually
// emitted (with non-empty Comment), keyed the same way as inline findings
// so isAuthorPromptAlive can match a finding_ref against them. The
// suppression-key shape is reused even though PR-wide entries can't be
// suppressed — the key is just a convenient identity.
func prWideFindingKeys(d *Draft) map[string]struct{} {
	out := map[string]struct{}{}
	if d == nil {
		return out
	}
	for _, s := range d.Specialists {
		if s.Err != nil {
			continue
		}
		for _, f := range generalFindings(s.Findings) {
			out[suppressionKey(s.Specialist, f.Path, f.Line, f.Side)] = struct{}{}
		}
	}
	return out
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func joinQuoted(s []string) string {
	if len(s) == 0 {
		return ""
	}
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = "“" + v + "”"
	}
	return strings.Join(out, ", ")
}

// RenderBody renders the top-level review body: vibe-coach verdict + summary + one
// combined AI prompt, then optional consolidated PR-wide bullets and agent failures.
// Per-specialist headings are intentionally omitted — inline-only feedback lives on the diff.
func (d *Draft) RenderBody() string {
	if d != nil && d.HasNoFindings() {
		// The standard disclosure mentions "inline comments on the diff" and an
		// "executive summary" — both nonsensical when the pipeline returned
		// nothing. Use a tailored body so the GitHub APPROVE we post in this
		// case explains why instead of looking content-free.
		return "## appr-ai-sal review\n\n" +
			"✅ **No issues found by any agent** — every configured specialist reviewed this diff and produced no actionable feedback to leave on the diff or in a written summary. Approving this pull request.\n\n" +
			"> **AI disclosure:** This review was produced by **appr-ai-sal** (automated AI tools). Verify everything before merging.\n"
	}
	var b string
	b += "## appr-ai-sal review\n\n"
	b += "> **AI disclosure:** This review was produced by **appr-ai-sal** (automated AI tools). "
	b += "**Line-level feedback** appears as **inline comments on the diff** where agents cited paths and lines. "
	b += "This top-level comment is an executive summary plus optional paste-ready AI instructions. Verify everything before merging.\n\n"

	vcDisp := d.effectiveVibeCoach()
	if vcDisp != nil && vcDisp.Err == nil {
		if merge := vibeCoachMergeSection(vcDisp, d.VerdictReconciliationNote()); merge != "" {
			b += merge
		}
		if prompts := vibeCoachSuggestedPromptsSection(d, vcDisp); prompts != "" {
			b += prompts
		}
	}
	if d.RepoArbiter != nil {
		if panel := repoExpertPanelSection(d); panel != "" {
			b += panel
		}
	}

	type agentErr struct {
		name string
		msg  string
	}
	var failures []agentErr
	var prWide []struct {
		specialist string
		f          Finding
	}

	for _, s := range d.Specialists {
		if s.Err != nil {
			failures = append(failures, agentErr{s.Specialist, s.Err.Error()})
			continue
		}
		if len(s.Findings) == 0 {
			continue
		}
		for _, f := range generalFindings(s.Findings) {
			prWide = append(prWide, struct {
				specialist string
				f          Finding
			}{s.Specialist, f})
		}
	}

	if len(failures) > 0 {
		b += "### Agent failures\n\n"
		for _, e := range failures {
			b += "- **" + e.name + ":** _" + e.msg + "_\n"
		}
		b += "\n"
	}

	if len(prWide) > 0 {
		b += "### PR-wide notes _(no diff anchor)_\n\n"
		b += "_These could not be tied to a single changed line; every other finding from specialists is in **inline comments on the diff**._\n\n"
		for _, item := range prWide {
			b += "- **" + string(item.f.Severity) + " · " + item.specialist + ":** " + item.f.Comment + "\n"
		}
		b += "\n"
	}

	if d != nil && len(d.UserSkipPostKeys) > 0 {
		n := len(d.UserSkipPostKeys)
		s := "s"
		if n == 1 {
			s = ""
		}
		b += "### Reviewer choices\n\n"
		b += fmt.Sprintf("_%d inline suggestion%s skipped during review — not included in this GitHub post._\n\n", n, s)
	}

	return b
}

func repoExpertPanelSection(d *Draft) string {
	ar := d.RepoArbiter
	if ar == nil {
		return ""
	}
	if ar.Err != nil {
		return "### Repo expert panel _(failed)_\n\n_" + ar.Err.Error() + "_\n\n"
	}
	var b strings.Builder
	b.WriteString("### Repo expert panel _(arbiter over per-specialist briefs)_\n\n")
	if strings.TrimSpace(ar.UserSummary) != "" {
		b.WriteString(strings.TrimSpace(ar.UserSummary))
		b.WriteString("\n\n")
	}
	if len(ar.RationaleBullets) > 0 {
		b.WriteString("**Rationale:**\n")
		for _, r := range ar.RationaleBullets {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			b.WriteString("- " + r + "\n")
		}
		b.WriteString("\n")
	}
	orig := ""
	if d.VibeCoach != nil {
		orig = NormalizeVibeVerdict(d.VibeCoach.Verdict)
	}
	eff := ar.EffectiveVerdict
	if orig != "" && eff != "" && orig != eff {
		b.WriteString("**Merge recommendation adjustment:** vibe-coach suggested **" + VibeVerdictShortLabel(orig) +
			"**; repo experts set effective verdict **" + VibeVerdictShortLabel(eff) + "**.\n\n")
	}
	if len(ar.Suppressed) > 0 {
		fmt.Fprintf(&b, "**Inline comments not posted** (%d; repo arbiter):\n\n", len(ar.Suppressed))
		for _, s := range ar.Suppressed {
			fmt.Fprintf(&b, "- **%s** `%s:%d` — %s\n", s.Specialist, s.Path, s.Line, strings.TrimSpace(s.Reason))
		}
		b.WriteString("\n")
	}
	if len(ar.Demoted) > 0 {
		fmt.Fprintf(&b, "**Findings demoted by repo arbiter** (%d; severity dropped one rank):\n\n", len(ar.Demoted))
		for _, dm := range ar.Demoted {
			fmt.Fprintf(&b, "- **%s** `%s:%d` — %s → %s",
				dm.Specialist, dm.Path, dm.Line, string(dm.From), string(dm.To))
			if r := strings.TrimSpace(dm.Reason); r != "" {
				fmt.Fprintf(&b, " · %s", r)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(d.ConventionWitness) > 0 {
		divergent, congruent, unknown := 0, 0, 0
		for _, w := range d.ConventionWitness {
			switch w.Verdict {
			case "divergent":
				divergent++
			case "congruent":
				congruent++
			case "unknown":
				unknown++
			}
		}
		fmt.Fprintf(&b, "**Convention witness** (%d testing/docs finding(s) classified): %d divergent, %d congruent, %d unknown.\n\n",
			len(d.ConventionWitness), divergent, congruent, unknown)
	}
	// ar.DroppedSuppressions / DroppedDemotions are intentionally NOT
	// rendered: they leak the internal suppression-key shape
	// ("specialist|path|line|side") and add no actionable information for a
	// human reading the GitHub review. The fields are still populated for
	// tests and any future debug/log surfacing.
	out := b.String()
	if out == "### Repo expert panel _(arbiter over per-specialist briefs)_\n\n" {
		return ""
	}
	return out
}
