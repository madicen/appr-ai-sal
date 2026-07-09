package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

// incrementalPlan is the runner's handle on a re-review: the prior cached
// review, the computed interdiff, the carried-forward findings, and the
// discussion agent's prior-findings section. It is nil on a first review.
type incrementalPlan struct {
	prior       *CachedDraft
	interdiff   Interdiff
	carried     []SpecialistResult
	stats       carryStats
	priorStatus string
}

// planIncremental loads the most-recent prior review of ref cached under a
// DIFFERENT head SHA and, when found, builds the incremental plan. It returns
// nil — meaning "do a full review, byte-identical to pre-B2" — when there is no
// PR head SHA to key on, no prior cache, or the prior cache carries no diff to
// interdiff against. Fully fail-open (the cache layer never errors out).
func planIncremental(ref gh.Ref, pr *gh.PR, newDiff string) *incrementalPlan {
	if pr == nil || strings.TrimSpace(pr.HeadSHA) == "" {
		return nil
	}
	prior, ok := NewDraftCache().LoadPrior(ref, pr.HeadSHA)
	if !ok || prior == nil || strings.TrimSpace(prior.Diff) == "" {
		return nil
	}
	inter := computeInterdiff(prior.Diff, newDiff)
	carried, stats := carryForwardFindings(prior.Specialists, ParseDiff(newDiff), inter.Changed)
	return &incrementalPlan{
		prior:       prior,
		interdiff:   inter,
		carried:     carried,
		stats:       stats,
		priorStatus: FormatPriorFindingsStatus(prior, inter),
	}
}

// progressDetail is the one-line Detail for the runner's Stage="incremental"
// progress event.
func (p *incrementalPlan) progressDetail() string {
	return fmt.Sprintf("re-review vs %s: %d changed file(s); carried %d prior finding(s) (%d for re-review, %d resolved/gone)",
		shortSHA(p.prior.HeadSHA), len(p.interdiff.Changed), p.stats.Survived, p.stats.Review, p.stats.Gone)
}

// emptyActiveSpecialistResults returns a placeholder OK result per active
// specialist, used when a re-review has zero changed files so the specialist
// phase is skipped entirely (its findings are all carried forward). The
// placeholders keep the overlay's specialist tabs populated without an API
// call.
func emptyActiveSpecialistResults(techConfigured bool) []SpecialistResult {
	active := ActiveSpecialists(techConfigured)
	out := make([]SpecialistResult, len(active))
	for i, name := range active {
		out[i] = SpecialistResult{Specialist: name, Findings: []Finding{}}
	}
	return out
}

// incremental.go implements the B2 incremental re-review machinery that sits
// on top of the DraftCache (draft_cache.go): given the prior review's diff +
// findings and the new PR diff, it computes the interdiff (which files
// changed), carries surviving prior findings forward (re-anchored via the Q6
// excerpt-relocation), and renders a "prior findings status" section for the
// discussion agent.
//
// Everything here is pure (no IO, no model calls) so it is fully unit-testable
// and deterministic. The runner (runner.go) wires it in behind a cache-presence
// guard so a first review — with no prior cache — is byte-identical to pre-B2.

// Interdiff is the set of files the PR changed between the prior review's head
// SHA and the new one, computed by comparing the two diffs' per-file
// post-image content. Keyed by post-image path (the path findings anchor to on
// side=RIGHT).
type Interdiff struct {
	// Changed holds files whose diff content differs from the prior review
	// (or that are new to the diff). Specialists re-run over exactly these.
	Changed map[string]bool
	// Unchanged holds files whose diff content is byte-identical to the prior
	// review. Prior findings on these files are carried forward.
	Unchanged map[string]bool
}

// ChangedPaths returns the changed post-image paths, sorted (deterministic
// order for progress messages / tests).
func (id Interdiff) ChangedPaths() []string {
	out := make([]string, 0, len(id.Changed))
	for p := range id.Changed {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// fileContentSignature hashes the post-image content a file contributes to a
// diff: every added or context line's text (removed lines and line numbers are
// ignored, so a pure line-number shift caused by an UNRELATED earlier file
// does not by itself mark this file changed). Two diffs that show the same
// post-image for a file produce the same signature.
func fileContentSignature(f FileDiff) string {
	h := sha256.New()
	fmt.Fprintf(h, "binary=%t\ndeleted=%t\nnew=%t\n", f.IsBinary, f.IsDeleted, f.IsNewFile)
	for _, hunk := range f.Hunks {
		for _, l := range hunk.Lines {
			if l.Kind == DiffRemoved {
				continue
			}
			// Tag with kind so an added line and an otherwise-identical
			// context line can't collide.
			fmt.Fprintf(h, "%d:%s\n", l.Kind, l.Text)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// computeInterdiff compares the prior review's diff with the new diff and
// classifies every file in the NEW diff as Changed or Unchanged. Files that
// were in the old diff but are absent from the new one are simply not present
// in either set (carryForwardFindings drops their findings as "gone").
func computeInterdiff(oldDiff, newDiff string) Interdiff {
	oldSig := map[string]string{}
	for _, f := range ParseDiff(oldDiff) {
		if f.Path == "" {
			continue
		}
		oldSig[f.Path] = fileContentSignature(f)
	}
	id := Interdiff{Changed: map[string]bool{}, Unchanged: map[string]bool{}}
	for _, f := range ParseDiff(newDiff) {
		if f.Path == "" {
			continue
		}
		if sig, ok := oldSig[f.Path]; ok && sig == fileContentSignature(f) {
			id.Unchanged[f.Path] = true
		} else {
			id.Changed[f.Path] = true
		}
	}
	return id
}

// reduceDiffToFiles returns a unified diff containing only the stanzas of the
// given diff whose post-image path is in keep, preserving order. It re-splits
// the RAW diff text on "diff --git" boundaries so the surviving stanzas are
// byte-identical to their originals (line numbers intact → carried and freshly
// filed findings both anchor correctly against the full diff). An empty keep
// set yields "".
func reduceDiffToFiles(diff string, keep map[string]bool) string {
	if len(keep) == 0 {
		return ""
	}
	lines := strings.Split(diff, "\n")
	var out []string
	var cur []string
	curPath := ""
	flush := func() {
		if len(cur) > 0 && keep[curPath] {
			out = append(out, cur...)
		}
		cur = nil
		curPath = ""
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			curPath = postImagePathFromGitHeader(line)
			cur = append(cur, line)
			continue
		}
		if cur != nil {
			cur = append(cur, line)
		}
		// Lines before the first "diff --git" (preamble) are dropped, matching
		// ParseDiff's own tolerance for a preamble.
	}
	flush()
	return strings.Join(out, "\n")
}

// postImagePathFromGitHeader extracts the "b/<path>" post-image path from a
// "diff --git a/x b/y" header, mirroring ParseDiff's own field parsing.
func postImagePathFromGitHeader(header string) string {
	parts := strings.Fields(header)
	if len(parts) >= 4 {
		return strings.TrimPrefix(parts[3], "b/")
	}
	return ""
}

// carryDecision is the outcome of attempting to carry one prior finding
// forward onto the new diff.
type carryDecision int

const (
	// carrySurvive: the finding's anchored code is present and unchanged in
	// the new diff (re-anchored if its line moved) → carry it forward.
	carrySurvive carryDecision = iota
	// carryReview: the finding is on a file the PR changed since the prior
	// review → drop it and let the specialist re-run over that file re-emit,
	// so we never carry a stale finding across a changed hunk.
	carryReview
	// carryGone: the finding's file is no longer in the diff, or its anchored
	// excerpt no longer matches → the code is gone → drop it.
	carryGone
)

// carryStats summarises a carry-forward pass for progress reporting / tests.
type carryStats struct {
	Survived int // carried forward (re-anchored where needed)
	Review   int // dropped so the specialist re-reviews the changed file
	Gone     int // dropped because the anchored code disappeared
}

// classifyCarriedFinding decides what to do with one prior inline finding given
// the new diff's files and the interdiff's changed set. It returns the
// (possibly re-anchored) finding and the decision. Only inline-postable
// findings are handled here; the caller skips PR-wide findings (those come
// from the whole-PR agents, which simply re-run).
func classifyCarriedFinding(f Finding, newFiles []FileDiff, changed map[string]bool) (Finding, carryDecision) {
	file := FindFile(newFiles, f.Path)
	if file == nil {
		// The PR no longer touches this file (reverted / rebased away) → the
		// anchored code is out of review scope now.
		return f, carryGone
	}
	if changed[f.Path] {
		// File changed since the prior review — let the specialist re-run over
		// it and re-emit rather than blindly carrying a possibly-stale finding.
		return f, carryReview
	}
	// Unchanged file. Re-anchor against the new diff using the model's verbatim
	// excerpt (Q6). A unique match relocates the line (normally to the same
	// line, since the file is unchanged); a non-empty excerpt that no longer
	// matches means the anchored code is gone.
	if strings.TrimSpace(f.AnchorExcerpt) != "" {
		if line, ok := FindUniqueExcerptInFile(file, f.AnchorExcerpt); ok {
			if line != f.Line {
				f.AnchorRelocatedFrom = f.Line
				f.Line = line
			}
			return f, carrySurvive
		}
		return f, carryGone
	}
	// No excerpt to relocate against — the file is unchanged, so the original
	// line is still valid; carry it forward as-is (best effort).
	return f, carrySurvive
}

// carryForwardFindings re-anchors the prior review's surviving CODE-specialist
// findings onto the new diff. PR-wide findings and PR-agent findings are NOT
// carried — the whole-PR agents (description/checks/discussion/scope) re-run
// every time. Returns one SpecialistResult per code specialist that had at
// least one surviving finding, plus stats.
func carryForwardFindings(prior []SpecialistResult, newFiles []FileDiff, changed map[string]bool) ([]SpecialistResult, carryStats) {
	var stats carryStats
	byName := map[string]*SpecialistResult{}
	var order []string
	for _, s := range prior {
		if s.Err != nil {
			continue
		}
		if !isCodeSpecialist(s.Specialist) {
			continue // PR agents re-run; don't carry their findings forward.
		}
		for _, f := range s.Findings {
			if !findingIsInlinePostable(f) {
				// PR-wide code-specialist notes aren't line-anchored; the
				// specialist re-emits them when it re-runs over a changed file.
				continue
			}
			nf, decision := classifyCarriedFinding(f, newFiles, changed)
			switch decision {
			case carrySurvive:
				stats.Survived++
				res, ok := byName[s.Specialist]
				if !ok {
					res = &SpecialistResult{Specialist: s.Specialist}
					byName[s.Specialist] = res
					order = append(order, s.Specialist)
				}
				res.Findings = append(res.Findings, nf)
			case carryReview:
				stats.Review++
			case carryGone:
				stats.Gone++
			}
		}
	}
	out := make([]SpecialistResult, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out, stats
}

// isCodeSpecialist reports whether name is a KindCode specialist (as opposed to
// a whole-PR agent). Consults the registry so user-defined specialists are
// classified correctly; unknown names are treated as code specialists so a
// cache written by a build that knew a now-removed specialist still carries.
func isCodeSpecialist(name string) bool {
	if s, ok := lookupSpec(name); ok {
		return s.Kind == KindCode
	}
	return !IsPRAgent(name)
}

// mergeCarriedFindings folds carried-forward findings into the freshly-produced
// specialist results. Carried findings (on UNCHANGED files) never overlap the
// fresh findings (produced over the reduced diff of CHANGED files), so this is
// a straight append per specialist; the cross-specialist dedupe pass the runner
// already runs afterwards mops up any coincidental duplicates. A carried
// specialist absent from the fresh set (e.g. it produced nothing this run) is
// appended as a new result.
func mergeCarriedFindings(specialists, carried []SpecialistResult) []SpecialistResult {
	if len(carried) == 0 {
		return specialists
	}
	out := append([]SpecialistResult(nil), specialists...)
	idx := map[string]int{}
	for i, s := range out {
		idx[s.Specialist] = i
	}
	for _, c := range carried {
		if len(c.Findings) == 0 {
			continue
		}
		if i, ok := idx[c.Specialist]; ok {
			out[i].Findings = append(out[i].Findings, c.Findings...)
		} else {
			out = append(out, c)
			idx[c.Specialist] = len(out) - 1
		}
	}
	return out
}

// FormatPriorFindingsStatus renders the `## Prior review findings` section fed
// to the discussion agent on a re-review, so it can note which of the previous
// findings appear resolved by the new commits vs still present. It lists every
// prior inline finding with whether its file changed since (a strong hint the
// concern may have been addressed). Returns "" when there is no prior review or
// it produced no findings — so on a first review the discussion agent's prompt
// is byte-identical to pre-B2.
func FormatPriorFindingsStatus(prior *CachedDraft, id Interdiff) string {
	if prior == nil {
		return ""
	}
	type row struct {
		specialist string
		path       string
		line       int
		severity   string
		comment    string
		changed    bool
	}
	var rows []row
	for _, s := range prior.Specialists {
		if s.Err != nil {
			continue
		}
		for _, f := range s.Findings {
			if strings.TrimSpace(f.Comment) == "" {
				continue
			}
			r := row{
				specialist: s.Specialist,
				path:       f.Path,
				line:       f.Line,
				severity:   string(f.Severity),
				comment:    collapseWhitespace(f.Comment),
			}
			if strings.TrimSpace(f.Path) != "" {
				r.changed = id.Changed[f.Path]
			}
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Prior review findings\n\n")
	b.WriteString("A previous review of an earlier commit")
	if sha := strings.TrimSpace(prior.HeadSHA); sha != "" {
		b.WriteString(" (" + shortSHA(sha) + ")")
	}
	b.WriteString(" of this PR produced the findings below; new commits have since been pushed. ")
	b.WriteString("For each, judge from the current diff whether it now appears RESOLVED by the new commits or is STILL PRESENT, and only file a finding for concerns that remain outstanding. ")
	b.WriteString("A finding whose file changed since (marked `file changed`) is a strong candidate for having been addressed — verify against the diff.\n\n")
	for _, r := range rows {
		loc := "PR-wide"
		if strings.TrimSpace(r.path) != "" {
			if r.line > 0 {
				loc = fmt.Sprintf("%s:%d", r.path, r.line)
			} else {
				loc = r.path
			}
		}
		status := "file unchanged since"
		if r.changed {
			status = "file changed since"
		}
		sev := r.severity
		if sev == "" {
			sev = "info"
		}
		fmt.Fprintf(&b, "- [%s] %s (%s, %s): %s\n", status, loc, r.specialist, sev, truncate(r.comment, 400))
	}
	return strings.TrimRight(b.String(), "\n")
}
