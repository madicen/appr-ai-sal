package review

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review/conventionwitness"
)

const (
	specRepoArbiter = "repo-arbiter"
)

func buildSpecialistDigestForRepoExperts(specialists []SpecialistResult) string {
	var b strings.Builder
	for _, s := range specialists {
		b.WriteString("--- " + s.Specialist + " ---\n")
		if s.Err != nil {
			b.WriteString("(failed: " + s.Err.Error() + ")\n\n")
			continue
		}
		if s.Summary != "" {
			b.WriteString("Summary (CONTEXT ONLY — what this specialist observed; NOT a finding and NOT actionable): " + s.Summary + "\n")
		}
		for _, f := range s.Findings {
			side := f.Side
			if side == "" {
				side = "RIGHT"
			}
			if strings.TrimSpace(f.Path) == "" || f.Line <= 0 {
				// PR-wide / whole-PR finding (the PR agents' usual output).
				// It has no diff anchor; reference it in suppress/demote with
				// path "" and line 0.
				fmt.Fprintf(&b, "  [%s] (PR-wide, no diff anchor — use path \"\" line 0) side=%s — %s\n", f.Severity, side, f.Comment)
				continue
			}
			fmt.Fprintf(&b, "  [%s] %s:%d side=%s — %s\n", f.Severity, f.Path, f.Line, side, f.Comment)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// formatPerAgentBriefs renders the per-specialist repo-agent briefs as a
// labelled section for the arbiter prompt. Specialists without a brief are
// listed as "(no brief)" so the arbiter knows the absence is intentional.
func formatPerAgentBriefs(perAgent map[string]string) string {
	if perAgent == nil {
		return ""
	}
	var b strings.Builder
	keys := make([]string, 0, len(AllSpecialists))
	keys = append(keys, AllSpecialists...)
	sort.Strings(keys)
	for _, name := range keys {
		brief := strings.TrimSpace(perAgent[name])
		fmt.Fprintf(&b, "### Repo agent: %s\n\n", name)
		if brief == "" {
			b.WriteString("_(no brief on file for this repo + specialist.)_\n\n")
			continue
		}
		b.WriteString(brief)
		b.WriteString("\n\n")
	}
	return b.String()
}

func buildRepoArbiterUserPrompt(pr *gh.PR, specialistDigest string, perAgent map[string]string, techSection string, witnesses []conventionwitness.Witness) string {
	var b strings.Builder
	b.WriteString("PR: " + pr.Repository + "#")
	fmt.Fprintf(&b, "%d %s\n\n", pr.Number, pr.Title)
	briefs := formatPerAgentBriefs(perAgent)
	if strings.TrimSpace(briefs) != "" {
		b.WriteString("## Per-specialist repo-agent briefs\n\n")
		b.WriteString(briefs)
	}
	if ts := strings.TrimSpace(techSection); ts != "" {
		b.WriteString("## Technology experts (cross-specialist briefs)\n\n")
		b.WriteString(ts)
		b.WriteString("\n\n")
	}
	if md := strings.TrimSpace(conventionwitness.FormatMarkdown(witnesses)); md != "" {
		b.WriteString("## Convention witness (per-finding evidence verdicts)\n\n")
		b.WriteString(md)
		b.WriteString("\n")
	}
	// The vibe-coach runs AFTER the arbiter, so its output is never part of
	// this digest — the heading names only what is actually present.
	b.WriteString("## Specialist findings digest (authoritative for finding paths/lines)\n\n")
	b.WriteString(specialistDigest)
	b.WriteString("\n")
	b.WriteString(repoArbiterOutputContract)
	return b.String()
}

const repoArbiterOutputContract = `

Return only the JSON object specified in your system instructions.`

type repoArbiterJSON struct {
	UserSummary      string                 `json:"user_summary"`
	RationaleBullets []string               `json:"rationale_bullets"`
	VerdictOverride  string                 `json:"verdict_override"`
	SummaryMode      string                 `json:"summary_mode"`
	SummaryText      string                 `json:"summary_text"`
	Suppress         []SuppressedFindingRef `json:"suppress"`
	Demote           []DemotedFindingRef    `json:"demote,omitempty"`
}

func parseRepoArbiterJSON(s string) (*repoArbiterJSON, error) {
	s = strings.TrimSpace(s)
	var v repoArbiterJSON
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		return &v, nil
	}
	if obj := extractJSONObject(s); obj != "" {
		if err := json.Unmarshal([]byte(obj), &v); err == nil {
			return &v, nil
		}
	}
	// Include a bounded raw-output excerpt so a stage-retry log / progress
	// line names what the model actually returned instead of an opaque
	// "parse repo arbiter JSON".
	return nil, fmt.Errorf("parse repo arbiter JSON (raw: %s)", truncate(s, 500))
}

// FinalizeRepoArbiter validates suppressions and demotions against the
// draft's findings, then fills suppressKeySet, demoteKeySet, and
// EffectiveVerdict. It matches both inline findings (path + line) and
// PR-wide / general findings (empty path, line 0 — the PR agents' usual
// output); a PR-wide ref is matched per (specialist, side) and applies to
// every PR-wide finding that agent emitted under that side. Demotions
// mutate Finding.Severity in place on
// d.Specialists and re-run the strictness floor on each specialist's
// findings, so a demoted-to-info finding can naturally disappear under
// non-strict review intensities.
//
// Hard rules (mirror the prompt):
//   - Never suppress a security finding or any error/critical severity.
//   - Never demote a security finding or any critical severity.
//   - Demotion is STRICTLY DOWNWARD — the target severity must be lower than
//     the current one. Upward "demotions" and same-severity no-ops are
//     rejected into DroppedDemotions. The drop CAN span multiple ranks
//     (e.g. error→info) when the arbiter is confident the finding is fully
//     tolerated; the one-rank-at-a-time rule that used to live here was
//     forcing arbiter-acknowledged errors into a halfway "warning" state
//     that vibe-coach still treated as blocking, defeating the whole point
//     of the demote.
//   - When dem.To is empty the legacy one-rank-drop default kicks in
//     (error→warning, warning→info), preserving compatibility with
//     models that haven't been re-prompted yet.
func FinalizeRepoArbiter(ar *RepoArbiterResult, d *Draft) {
	if ar == nil || d == nil {
		return
	}
	ar.suppressKeySet = make(map[string]struct{})
	ar.demoteKeySet = make(map[string]Severity)
	ar.DroppedSuppressions = nil
	ar.DroppedDemotions = nil
	flat := d.FlatPostableFindings()
	index := make(map[string]FlatFinding, len(flat))
	for _, f := range flat {
		side := f.Finding.Side
		if side == "" {
			side = "RIGHT"
		}
		index[suppressionKey(f.Specialist, f.Finding.Path, f.Finding.Line, side)] = f
	}
	// PR-wide / general findings (path "", line 0) — the PR agents' usual
	// output. They have no unique path:line, so they're indexed per
	// (specialist, side); an arbiter ref with an empty path / line 0 matches
	// every PR-wide finding the agent emitted under that side. Multiple
	// PR-wide findings from one agent are rare, but when present a suppress
	// or demote applies to all of them together.
	generalIndex := make(map[string][]FlatFinding)
	for _, f := range d.flatGeneralFindings() {
		side := f.Finding.Side
		if side == "" {
			side = "RIGHT"
		}
		k := suppressionKey(f.Specialist, "", 0, side)
		generalIndex[k] = append(generalIndex[k], f)
	}
	var keptSup []SuppressedFindingRef
	for _, sup := range ar.Suppressed {
		side := sup.Side
		if side == "" {
			side = "RIGHT"
		}
		if isGeneralRef(sup.Path, sup.Line) {
			k := suppressionKey(sup.Specialist, "", 0, side)
			matches := generalIndex[k]
			if len(matches) == 0 {
				ar.DroppedSuppressions = append(ar.DroppedSuppressions, "no matching PR-wide finding: "+k)
				continue
			}
			if reason := suppressGuardForGeneral(matches); reason != "" {
				ar.DroppedSuppressions = append(ar.DroppedSuppressions, reason+": "+k)
				continue
			}
			ar.suppressKeySet[k] = struct{}{}
			keptSup = append(keptSup, sup)
			continue
		}
		k := suppressionKey(sup.Specialist, sup.Path, sup.Line, side)
		ff, ok := index[k]
		if !ok {
			ar.DroppedSuppressions = append(ar.DroppedSuppressions, "no matching inline finding: "+k)
			continue
		}
		if strings.EqualFold(strings.TrimSpace(ff.Specialist), SpecSecurity) {
			ar.DroppedSuppressions = append(ar.DroppedSuppressions, "cannot suppress security finding: "+k)
			continue
		}
		if ff.Finding.Severity == SeverityError || ff.Finding.Severity == SeverityCritical {
			ar.DroppedSuppressions = append(ar.DroppedSuppressions, "cannot suppress error-or-critical-severity finding: "+k)
			continue
		}
		ar.suppressKeySet[k] = struct{}{}
		keptSup = append(keptSup, sup)
	}
	ar.Suppressed = keptSup

	var keptDem []DemotedFindingRef
	for _, dem := range ar.Demoted {
		side := dem.Side
		if side == "" {
			side = "RIGHT"
		}
		if isGeneralRef(dem.Path, dem.Line) {
			k := suppressionKey(dem.Specialist, "", 0, side)
			matches := generalIndex[k]
			if len(matches) == 0 {
				ar.DroppedDemotions = append(ar.DroppedDemotions, "no matching PR-wide finding: "+k)
				continue
			}
			if _, suppressed := ar.suppressKeySet[k]; suppressed {
				ar.DroppedDemotions = append(ar.DroppedDemotions, "already suppressed; demote would be a no-op: "+k)
				continue
			}
			applied, orig, reason := demoteGeneralFindings(d, matches, dem, side)
			if reason != "" {
				ar.DroppedDemotions = append(ar.DroppedDemotions, reason+": "+k)
				continue
			}
			ar.demoteKeySet[k] = orig
			keptDem = append(keptDem, applied...)
			continue
		}
		k := suppressionKey(dem.Specialist, dem.Path, dem.Line, side)
		ff, ok := index[k]
		if !ok {
			ar.DroppedDemotions = append(ar.DroppedDemotions, "no matching inline finding: "+k)
			continue
		}
		if strings.EqualFold(strings.TrimSpace(ff.Specialist), SpecSecurity) {
			ar.DroppedDemotions = append(ar.DroppedDemotions, "cannot demote security finding: "+k)
			continue
		}
		if ff.Finding.Severity == SeverityCritical {
			ar.DroppedDemotions = append(ar.DroppedDemotions, "cannot demote critical-severity finding: "+k)
			continue
		}
		// Resolve the target severity. An empty dem.To means "one-rank
		// drop" for backward compatibility with models trained on the
		// old prompt. A non-empty dem.To is honoured iff it's a known
		// severity AND strictly lower than the current one — that's the
		// only invariant we still enforce (no upward "demotes", no
		// "demote to the same severity", no garbage values).
		var next Severity
		if dem.To == "" {
			n, ok := demotedSeverity(ff.Finding.Severity)
			if !ok {
				ar.DroppedDemotions = append(ar.DroppedDemotions, "no lower severity to demote into: "+k)
				continue
			}
			next = n
		} else {
			next = dem.To
			if severityRank(next) == 0 {
				ar.DroppedDemotions = append(ar.DroppedDemotions, "unknown target severity: "+k)
				continue
			}
			if severityRank(next) >= severityRank(ff.Finding.Severity) {
				ar.DroppedDemotions = append(ar.DroppedDemotions, "demote must move strictly downward: "+k)
				continue
			}
			if next == SeverityCritical {
				// Belt-and-suspenders: severityRank(critical)=4 is the top
				// rank, so this branch is unreachable given the
				// strictly-downward check above. Kept here so the rule
				// "never demote *into* critical" is explicit and survives
				// any future shuffling of severity ranks.
				ar.DroppedDemotions = append(ar.DroppedDemotions, "cannot demote into critical: "+k)
				continue
			}
		}
		// Avoid demoting something the arbiter is also suppressing — the
		// suppression already wins.
		if _, suppressed := ar.suppressKeySet[k]; suppressed {
			ar.DroppedDemotions = append(ar.DroppedDemotions, "already suppressed; demote would be a no-op: "+k)
			continue
		}
		applied := DemotedFindingRef{
			Specialist: dem.Specialist,
			Path:       dem.Path,
			Line:       dem.Line,
			Side:       side,
			From:       ff.Finding.Severity,
			To:         next,
			Reason:     dem.Reason,
		}
		ar.demoteKeySet[k] = ff.Finding.Severity
		// Mutate the severity in place on the draft so downstream renderers,
		// vibe-coach input, and the strictness re-filter all see the demoted
		// value.
		d.Specialists[ff.SpecIndex].Findings[ff.FindIndex].Severity = next
		keptDem = append(keptDem, applied)
	}
	ar.Demoted = keptDem

	// Re-apply the strictness floor across all specialists so a demoted
	// finding that fell below the floor (e.g. demoted to info under
	// balanced) is dropped before rendering. A specialist's findings were
	// already filtered to the floor before the arbiter ran, so anything
	// below it here was pushed there by a demotion this pass. Both inline
	// and PR-wide ones are retained on d.DemotedHidden (full data) so the
	// approval overlay / agent tabs can offer them as opt-in, post-anyway
	// items; they stay out of Specialists[].Findings so the verdict,
	// summary body, and vibe-coach never see them — the arbiter's "don't
	// block on this" intent holds. (Inline vs PR-wide is derivable from the
	// retained Finding via findingIsInlinePostable.)
	floor := d.severityFloor()
	if floor != "" && floor != SeverityInfo {
		min := severityRank(floor)
		for i := range d.Specialists {
			if d.Specialists[i].Err != nil {
				continue
			}
			kept := make([]Finding, 0, len(d.Specialists[i].Findings))
			for _, f := range d.Specialists[i].Findings {
				r := severityRank(f.Severity)
				if r == 0 {
					r = severityRank(SeverityWarning)
				}
				if r >= min {
					kept = append(kept, f)
					continue
				}
				// Retain anything that carries a comment — inline-postable
				// findings and PR-wide (body-only) findings alike — so a
				// demoted-below-floor finding is never silently lost.
				if strings.TrimSpace(f.Comment) != "" {
					d.DemotedHidden = append(d.DemotedHidden, FlatFinding{
						Specialist: d.Specialists[i].Specialist,
						SpecIndex:  i,
						Finding:    f,
					})
				}
			}
			d.Specialists[i].Findings = kept
		}
	}

	orig := ""
	if d.VibeCoach != nil {
		orig = NormalizeVibeVerdict(d.VibeCoach.Verdict)
	}
	if v := NormalizeVibeVerdict(ar.VerdictOverride); v != "" {
		ar.EffectiveVerdict = v
	} else {
		ar.EffectiveVerdict = orig
	}
}

// isGeneralRef reports whether an arbiter suppress/demote ref targets a
// PR-wide (body-only) finding rather than an inline one. PR-wide findings
// carry an empty path / line 0; the arbiter prompt instructs the model to
// reference them that way.
func isGeneralRef(path string, line int) bool {
	return strings.TrimSpace(path) == "" || line <= 0
}

// suppressGuardForGeneral returns a non-empty drop reason when any of the
// PR-wide findings matched by a single ref must not be suppressed (a
// security finding, or error/critical severity). It mirrors the inline
// suppression guards and is conservative: one disallowed match rejects the
// whole ref so a coarse (specialist, side) key can't sneak a blocking
// finding out of the review.
func suppressGuardForGeneral(matches []FlatFinding) string {
	for _, ff := range matches {
		if strings.EqualFold(strings.TrimSpace(ff.Specialist), SpecSecurity) {
			return "cannot suppress security finding"
		}
		if ff.Finding.Severity == SeverityError || ff.Finding.Severity == SeverityCritical {
			return "cannot suppress error-or-critical-severity finding"
		}
	}
	return ""
}

// demoteGeneralFindings validates and applies an arbiter demote to every
// PR-wide finding matched by a single ref. It is atomic: if any matched
// finding fails a guard (security, critical, no-lower-rank, non-downward,
// unknown target) the whole ref is rejected with a reason and nothing is
// mutated. On success it mutates each finding's Severity in place on the
// draft and returns the applied refs plus the (first) original severity for
// demoteKeySet bookkeeping. The empty-To one-rank-drop default matches the
// inline path.
func demoteGeneralFindings(d *Draft, matches []FlatFinding, dem DemotedFindingRef, side string) ([]DemotedFindingRef, Severity, string) {
	type plan struct {
		ff   FlatFinding
		next Severity
	}
	plans := make([]plan, 0, len(matches))
	for _, ff := range matches {
		if strings.EqualFold(strings.TrimSpace(ff.Specialist), SpecSecurity) {
			return nil, "", "cannot demote security finding"
		}
		if ff.Finding.Severity == SeverityCritical {
			return nil, "", "cannot demote critical-severity finding"
		}
		var next Severity
		if dem.To == "" {
			n, ok := demotedSeverity(ff.Finding.Severity)
			if !ok {
				return nil, "", "no lower severity to demote into"
			}
			next = n
		} else {
			next = dem.To
			if severityRank(next) == 0 {
				return nil, "", "unknown target severity"
			}
			if severityRank(next) >= severityRank(ff.Finding.Severity) {
				return nil, "", "demote must move strictly downward"
			}
			if next == SeverityCritical {
				return nil, "", "cannot demote into critical"
			}
		}
		plans = append(plans, plan{ff: ff, next: next})
	}
	orig := matches[0].Finding.Severity
	applied := make([]DemotedFindingRef, 0, len(plans))
	for _, p := range plans {
		applied = append(applied, DemotedFindingRef{
			Specialist: dem.Specialist,
			Path:       p.ff.Finding.Path,
			Line:       p.ff.Finding.Line,
			Side:       side,
			From:       p.ff.Finding.Severity,
			To:         p.next,
			Reason:     dem.Reason,
		})
		d.Specialists[p.ff.SpecIndex].Findings[p.ff.FindIndex].Severity = p.next
	}
	return applied, orig, ""
}

// demotedSeverity returns the next-lower rank for one-step demotion. info has
// no lower rank; critical is rejected upstream.
func demotedSeverity(s Severity) (Severity, bool) {
	switch s {
	case SeverityError:
		return SeverityWarning, true
	case SeverityWarning:
		return SeverityInfo, true
	default:
		return "", false
	}
}

func runRepoArbiter(ctx context.Context, cfg *aiconfig.Config, worktree string, pr *gh.PR, specialistDigest string, perAgent map[string]string, techSection string, witnesses []conventionwitness.Witness) *RepoArbiterResult {
	ar := &RepoArbiterResult{}
	sys, err := SpecialistPrompt(specRepoArbiter)
	if err != nil {
		ar.Err = err
		return ar
	}
	user := buildRepoArbiterUserPrompt(pr, specialistDigest, perAgent, techSection, witnesses)
	sys, user = augmentPromptsForProvider(ai.CapabilitiesFor(cfg).RepoTools, sys, user, true)
	out, err := Complete(ctx, cfg, sys, user, worktree)
	if err != nil {
		ar.Err = err
		return ar
	}
	parsed, err := parseRepoArbiterJSON(out)
	if err != nil {
		ar.Err = fmt.Errorf("parse repo arbiter: %w", err)
		return ar
	}
	ar.UserSummary = strings.TrimSpace(parsed.UserSummary)
	ar.RationaleBullets = parsed.RationaleBullets
	ar.VerdictOverride = strings.TrimSpace(parsed.VerdictOverride)
	ar.SummaryMode = strings.TrimSpace(parsed.SummaryMode)
	ar.SummaryReplace = parsed.SummaryText
	ar.SummaryAddendum = parsed.SummaryText
	if strings.EqualFold(ar.SummaryMode, "replace") {
		ar.SummaryAddendum = ""
	} else if strings.EqualFold(ar.SummaryMode, "append") {
		ar.SummaryReplace = ""
	} else {
		ar.SummaryReplace = ""
		ar.SummaryAddendum = ""
	}
	ar.Suppressed = parsed.Suppress
	ar.Demoted = parsed.Demote
	return ar
}

// RunRepoArbiter is the post-specialists arbiter that may suppress noisy
// inline findings, demote findings that diverge mildly from repo norms, or
// override the merge verdict. It consumes the per-agent repo briefs
// (already injected into specialist prompts), the cross-specialist
// technology experts section, and optional convention witnesses produced
// between the specialists and this pass.
func RunRepoArbiter(ctx context.Context, cfg *aiconfig.Config, worktree string, pr *gh.PR, specialists []SpecialistResult, perAgent map[string]string, techSection string, witnesses []conventionwitness.Witness) *RepoArbiterResult {
	specDigest := buildSpecialistDigestForRepoExperts(specialists)
	return runRepoArbiter(ctx, cfg, worktree, pr, specDigest, perAgent, techSection, witnesses)
}
