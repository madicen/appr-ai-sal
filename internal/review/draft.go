package review

import (
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review/conventionwitness"
	"github.com/madicen/appr-ai-sal/internal/review/findingkey"
)

// Draft is what the TUI renders and (on confirm) posts to GitHub.
type Draft struct {
	Ref      gh.Ref
	PR       *gh.PR
	Diff     string
	Worktree string
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
	// arbiter. Scoped to testing/docs/tech findings; empty when the witness
	// is disabled or no findings qualified.
	ConventionWitness []conventionwitness.Witness
	// UserSkipPostKeys holds suppressionKey entries for inline findings the reviewer chose
	// not to post (TUI skip). Used when rendering/parsing the summary body and inline batch.
	UserSkipPostKeys map[string]struct{} `json:"-"`
	// DemotedHidden holds findings the repo arbiter demoted below the active
	// strictness floor (e.g. a warning demoted to info under balanced).
	// FinalizeRepoArbiter removes these from Specialists[].Findings so they
	// don't count toward the verdict, the summary body, or the vibe-coach
	// input — the arbiter's "don't block on this" intent is preserved. They're
	// retained here with full Specialist+Finding data so the overlay can offer
	// them as opt-in, post-anyway items the reviewer may still surface by hand.
	// Both inline-postable findings (path + line) and PR-wide / body-only
	// findings are retained; the TUI distinguishes them via
	// findingIsInlinePostable (inline → opt-in cards; PR-wide → opt-in body
	// inclusion toggled from the agent tab, gated by UserPostDemotedKeys).
	DemotedHidden []FlatFinding `json:"-"`
	// UserPostDemotedKeys holds DemotedFindingKey entries for demoted PR-wide
	// findings the reviewer explicitly opted to include in the posted review
	// body despite the arbiter demoting them below the floor. Inline demoted
	// findings use the opt-in card flow instead and are not tracked here.
	UserPostDemotedKeys map[string]struct{} `json:"-"`
	// DiffBudget records how the R3 diff budgeter shaped the diff before it was
	// inlined into specialist / PR-agent prompts. Nil means the full diff was
	// reviewed; when non-nil (and Truncated) RenderBody discloses that the
	// review ran on a truncated diff so PR authors know it didn't see
	// everything.
	DiffBudget *BudgetReport `json:"-"`
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

// ---------------------------------------------------------------------------
// Finding-identity key bookkeeping
//
// All three legacy key formats (suppressionKey, the finding_dedupe grouping
// key, and the conventionwitness alignment key) are now built from the single
// findingkey.Key. suppressionKey / FindingSuppressionKey / DemotedFindingKey
// keep their names and byte-for-byte output because the TUI depends on them.
// ---------------------------------------------------------------------------

// suppressionKey builds a stable key for matching arbiter suppressions to flat findings.
func suppressionKey(specialist, path string, line int, side string) string {
	return findingkey.New(specialist, path, line, side).String()
}

// FindingSuppressionKey returns suppressionKey for a finding (exported for TUI skip wiring).
func FindingSuppressionKey(specialist string, f Finding) string {
	return findingkey.New(specialist, f.Path, f.Line, f.Side).String()
}

// DemotedFindingKey returns a stable key identifying one demoted finding for
// the opt-in "post anyway" flow. PR-wide findings from a single agent share
// the (specialist, "", 0, side) suppressionKey, so the comment is folded in
// (via findingkey.Key.PerFinding) to keep distinct PR-wide findings
// independently toggleable.
func DemotedFindingKey(specialist string, f Finding) string {
	return findingkey.New(specialist, f.Path, f.Line, f.Side).PerFinding(f.Comment)
}

// ToggleDemotedPosting flips whether a demoted finding is opted in for posting
// in the review body, returning the new state (true = will be posted).
func (d *Draft) ToggleDemotedPosting(specialist string, f Finding) bool {
	if d == nil {
		return false
	}
	k := DemotedFindingKey(specialist, f)
	if d.UserPostDemotedKeys == nil {
		d.UserPostDemotedKeys = make(map[string]struct{})
	}
	if _, on := d.UserPostDemotedKeys[k]; on {
		delete(d.UserPostDemotedKeys, k)
		return false
	}
	d.UserPostDemotedKeys[k] = struct{}{}
	return true
}

// DemotedPostingEnabled reports whether the reviewer opted this demoted
// finding into the posted review body.
func (d *Draft) DemotedPostingEnabled(specialist string, f Finding) bool {
	if d == nil || len(d.UserPostDemotedKeys) == 0 {
		return false
	}
	_, on := d.UserPostDemotedKeys[DemotedFindingKey(specialist, f)]
	return on
}

// generalFindingSuppressed reports whether the repo arbiter suppressed this
// PR-wide / general finding. PR-wide findings are matched per
// (specialist, side) — see FinalizeRepoArbiter — so this checks the
// (specialist, "", 0, side) key against the arbiter's suppress set.
func (d *Draft) generalFindingSuppressed(specialist string, f Finding) bool {
	if d == nil || d.RepoArbiter == nil || d.RepoArbiter.Err != nil || len(d.RepoArbiter.suppressKeySet) == 0 {
		return false
	}
	_, drop := d.RepoArbiter.suppressKeySet[suppressionKey(specialist, "", 0, f.Side)]
	return drop
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

// flatGeneralFindings returns PR-wide / general findings (no inline anchor —
// empty path or line <= 0) with their specialist context, mirroring
// FlatPostableFindings for inline findings. These are the PR agents' usual
// output; FinalizeRepoArbiter uses this so the arbiter can suppress/demote
// them. Findings with an empty Comment are skipped (nothing to act on).
func (d *Draft) flatGeneralFindings() []FlatFinding {
	if d == nil {
		return nil
	}
	var out []FlatFinding
	for si, s := range d.Specialists {
		if s.Err != nil {
			continue
		}
		for fi, f := range s.Findings {
			if strings.TrimSpace(f.Path) != "" && f.Line > 0 {
				continue
			}
			if strings.TrimSpace(f.Comment) == "" {
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

// FlatPostableFindingsForPost returns inline findings minus repo-arbiter suppressions.
func (d *Draft) FlatPostableFindingsForPost() []FlatFinding {
	all := d.FlatPostableFindings()
	if d == nil {
		return nil
	}
	var out []FlatFinding
	for _, f := range all {
		k := suppressionKey(f.Specialist, f.Finding.Path, f.Finding.Line, f.Finding.Side)
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
// Note that PR-wide findings (empty path / line 0) are kept regardless of
// the user skip set — the skip flow only targets inline cards. They CAN,
// however, be suppressed by the repo arbiter (the PR agents file PR-wide
// findings, and the arbiter may demote/suppress them), so an arbiter
// suppression on a PR-wide finding is honoured here.
//
// When any inline finding is filtered out for a given specialist, we
// ALSO clear that specialist's Summary in the returned slice. The
// specialist-output contract asks for an aggregate Summary that
// describes the findings ("Found 3 issues with label naming…"); leaving
// the original Summary intact after filtering would let the vibe-coach
// LLM re-surface the suppressed findings via the summary text — which
// is exactly the leak that prompted this filter to exist. The PR-wide
// findings (path "", line 0) carry their own narrative, so dropping the
// Summary doesn't strand anything the vibe-coach legitimately needs.
func SpecialistsForVibeCoach(d *Draft, specialists []SpecialistResult) []SpecialistResult {
	if d == nil {
		return specialists
	}
	var sup map[string]struct{}
	if d.RepoArbiter != nil && d.RepoArbiter.Err == nil && len(d.RepoArbiter.suppressKeySet) > 0 {
		sup = d.RepoArbiter.suppressKeySet
	}
	// Specialists whose findings the arbiter demoted. A demotion mutates the
	// finding's severity in place and may drop it under the strictness floor
	// (e.g. warning → info under balanced), so by the time we get here the
	// finding is already gone from s.Findings. But the specialist's Summary
	// still describes it ("this PR mixes unrelated concerns, split it"), and
	// feeding that prose to the vibe-coach lets it re-block on a finding the
	// arbiter just demoted out of existence — exactly the leak the summary
	// clear below exists to prevent. So we treat a demotion like a drop:
	// clear the summary so the vibe-coach reasons from the effective finding
	// set, not stale prose. (This is the only path where the arbiter changes
	// findings WITHOUT touching suppressKeySet, so it needs explicit handling.)
	var demotedSpecs map[string]struct{}
	if d.RepoArbiter != nil && d.RepoArbiter.Err == nil && len(d.RepoArbiter.Demoted) > 0 {
		demotedSpecs = make(map[string]struct{}, len(d.RepoArbiter.Demoted))
		for _, dm := range d.RepoArbiter.Demoted {
			demotedSpecs[strings.ToLower(strings.TrimSpace(dm.Specialist))] = struct{}{}
		}
	}
	skips := d.UserSkipPostKeys
	if len(sup) == 0 && len(skips) == 0 && len(demotedSpecs) == 0 {
		return specialists
	}
	out := make([]SpecialistResult, len(specialists))
	for i, s := range specialists {
		out[i] = s
		if s.Err != nil {
			continue
		}
		var kept []Finding
		dropped := false
		for _, f := range s.Findings {
			if strings.TrimSpace(f.Path) != "" && f.Line > 0 {
				k := FindingSuppressionKey(s.Specialist, f)
				if _, drop := sup[k]; drop {
					dropped = true
					continue
				}
				if _, drop := skips[k]; drop {
					dropped = true
					continue
				}
			} else if len(sup) > 0 {
				// PR-wide finding: the user-skip flow can't target these (no
				// inline card), but the repo arbiter CAN suppress them (PR
				// agents), so drop them here too — otherwise the vibe-coach
				// would re-block on a finding the arbiter just excused.
				gk := suppressionKey(s.Specialist, "", 0, f.Side)
				if _, drop := sup[gk]; drop {
					dropped = true
					continue
				}
			}
			kept = append(kept, f)
		}
		out[i].Findings = kept
		if _, demoted := demotedSpecs[strings.ToLower(strings.TrimSpace(s.Specialist))]; dropped || demoted {
			out[i].Summary = ""
		}
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
	k := suppressionKey(specialist, f.Path, f.Line, f.Side)
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
	// A demoted PR-wide finding the reviewer opted to include is real,
	// postable content even though it was held out of the verdict-bearing set.
	for _, ff := range d.DemotedHidden {
		if findingIsInlinePostable(ff.Finding) {
			continue
		}
		if d.DemotedPostingEnabled(ff.Specialist, ff.Finding) && strings.TrimSpace(ff.Finding.Comment) != "" {
			return false
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
