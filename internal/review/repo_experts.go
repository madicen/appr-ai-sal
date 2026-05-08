package review

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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
			b.WriteString("Summary: " + s.Summary + "\n")
		}
		for _, f := range s.Findings {
			side := f.Side
			if side == "" {
				side = "RIGHT"
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

func buildRepoArbiterUserPrompt(pr *gh.PR, specialistDigest string, perAgent map[string]string, witnesses []conventionwitness.Witness) string {
	var b strings.Builder
	b.WriteString("PR: " + pr.Repository + "#")
	fmt.Fprintf(&b, "%d %s\n\n", pr.Number, pr.Title)
	briefs := formatPerAgentBriefs(perAgent)
	if strings.TrimSpace(briefs) != "" {
		b.WriteString("## Per-specialist repo-agent briefs\n\n")
		b.WriteString(briefs)
	}
	if md := strings.TrimSpace(conventionwitness.FormatMarkdown(witnesses)); md != "" {
		b.WriteString("## Convention witness (per-finding evidence verdicts)\n\n")
		b.WriteString(md)
		b.WriteString("\n")
	}
	b.WriteString("## Specialist + vibe digest (authoritative for finding paths/lines)\n\n")
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
	return nil, fmt.Errorf("parse repo arbiter JSON")
}

// FinalizeRepoArbiter validates suppressions and demotions against the
// draft's inline findings, then fills suppressKeySet, demoteKeySet, and
// EffectiveVerdict. Demotions mutate Finding.Severity in place on
// d.Specialists and re-run the strictness floor on each specialist's
// findings, so a demoted-to-info finding can naturally disappear under
// non-strict review intensities.
//
// Hard rules (mirror the prompt):
//   - Never suppress a security finding or any error/critical severity.
//   - Never demote a security finding or any critical severity (error→warning
//     is allowed; warning→info is allowed; demote of info is a no-op).
//   - Demotion always drops exactly one rank; multi-rank or upward "demotions"
//     are rejected into DroppedDemotions.
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
	var keptSup []SuppressedFindingRef
	for _, sup := range ar.Suppressed {
		side := sup.Side
		if side == "" {
			side = "RIGHT"
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
		next, ok := demotedSeverity(ff.Finding.Severity)
		if !ok {
			ar.DroppedDemotions = append(ar.DroppedDemotions, "no lower severity to demote into: "+k)
			continue
		}
		// Reject demote entries whose declared "to" disagrees with the
		// computed one-rank drop — the model tried to skip a rank.
		if dem.To != "" && dem.To != next {
			ar.DroppedDemotions = append(ar.DroppedDemotions, "demote must drop exactly one rank: "+k)
			continue
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
	// balanced) is dropped before rendering.
	floor := d.severityFloor()
	if floor != "" && floor != SeverityInfo {
		for i := range d.Specialists {
			if d.Specialists[i].Err != nil {
				continue
			}
			d.Specialists[i].Findings = FilterFindingsBySeverity(d.Specialists[i].Findings, floor)
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

func runRepoArbiter(ctx context.Context, cfg *aiconfig.Config, worktree string, pr *gh.PR, specialistDigest string, perAgent map[string]string, witnesses []conventionwitness.Witness) *RepoArbiterResult {
	ar := &RepoArbiterResult{}
	sys, err := SpecialistPrompt(specRepoArbiter)
	if err != nil {
		ar.Err = err
		return ar
	}
	user := buildRepoArbiterUserPrompt(pr, specialistDigest, perAgent, witnesses)
	sys, user = augmentPromptsForProvider(cfg.Provider, sys, user, true)
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
// (already injected into specialist prompts) plus optional convention
// witnesses produced between the specialists and this pass.
func RunRepoArbiter(ctx context.Context, cfg *aiconfig.Config, worktree string, pr *gh.PR, specialists []SpecialistResult, perAgent map[string]string, witnesses []conventionwitness.Witness) *RepoArbiterResult {
	specDigest := buildSpecialistDigestForRepoExperts(specialists)
	return runRepoArbiter(ctx, cfg, worktree, pr, specDigest, perAgent, witnesses)
}
