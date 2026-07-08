package review

import (
	"fmt"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/applog"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review/memory"
)

// This file is the seam between the review pipeline and the reviewer-memory
// store (internal/review/memory, the B1 "moat" feature). It keeps all
// finding→fingerprint construction in one place so the TUI and the runner
// never have to know the fingerprint shape — they hand this package a Draft
// and it does the right thing. Everything here is fail-open: a missing or
// corrupt store logs and degrades to "no memory", never breaking a review.

// MemorySuppressedFinding is a finding the pre-arbiter memory suppressor held
// back, carried on the Draft so the TUI can disclose and resurface it.
type MemorySuppressedFinding struct {
	Specialist string
	Finding    Finding
	// SkipCount is how many times the reviewer skipped a near-identical
	// finding in this repo — surfaced verbatim in the disclosure ("you've
	// skipped this N×").
	SkipCount int
	// Resurfaced is set by the TUI when the reviewer pressed `x` to bring the
	// finding back. RecordReviewerMemory reads it to log a demote_reversed
	// (positive) signal so a resurfaced-and-posted finding stops being
	// suppressed on future runs.
	Resurfaced bool
}

// fingerprintForFinding builds the memory fingerprint for a finding filed by
// specialist. Centralised here so every call site (record, suppress, list)
// generalizes identically.
func fingerprintForFinding(specialist string, f Finding) memory.Fingerprint {
	return memory.NewFingerprint(specialist, f.Path, f.Comment, string(f.Severity))
}

// ApplyMemorySuppression is the deterministic pre-arbiter suppressor. For each
// inline-postable finding it asks the store whether the reviewer has skipped a
// near-identical finding at least memory.DefaultSuppressThreshold times (and
// more often than they've kept it); if so the finding is pulled out of specs
// and returned in suppressed instead. It returns specs unchanged (and a nil
// suppressed slice) when there is nothing to suppress, so a repo with no
// memory behaves exactly as before.
//
// Only inline findings are considered: the reviewer skip signal (and thus the
// memory) only ever comes from inline approval cards, so PR-wide findings can
// never accumulate skips and are left untouched.
func ApplyMemorySuppression(mem *memory.Memory, specs []SpecialistResult) (kept []SpecialistResult, suppressed []MemorySuppressedFinding) {
	if mem == nil || len(mem.Records) == 0 {
		return specs, nil
	}
	changed := false
	out := make([]SpecialistResult, len(specs))
	for si, s := range specs {
		out[si] = s
		if s.Err != nil {
			continue
		}
		var keptF []Finding
		dropped := false
		for _, f := range s.Findings {
			if findingIsInlinePostable(f) {
				fp := fingerprintForFinding(s.Specialist, f)
				if mem.ShouldSuppress(fp, memory.DefaultSuppressThreshold) {
					suppressed = append(suppressed, MemorySuppressedFinding{
						Specialist: s.Specialist,
						Finding:    f,
						SkipCount:  mem.SkipCount(fp),
					})
					dropped = true
					continue
				}
			}
			keptF = append(keptF, f)
		}
		if dropped {
			changed = true
			out[si].Findings = keptF
		}
	}
	if !changed {
		return specs, nil
	}
	return out, suppressed
}

// LoadRepoMemory loads the reviewer memory for pr's repo, fail-open: any error
// (missing store, corrupt file) logs a warning and returns an empty Memory so
// the caller can proceed as if there were no memory.
func LoadRepoMemory(pr *gh.PR) *memory.Memory {
	owner, repo := memoryRepoKey(pr)
	if owner == "" || repo == "" {
		return &memory.Memory{}
	}
	mem, err := memory.NewStore().Load(owner, repo)
	if err != nil {
		applog.Warn("reviewer memory: load failed (continuing without it)", "repo", owner+"/"+repo, "err", err.Error())
		return &memory.Memory{}
	}
	return mem
}

// RejectedPatternsSection renders the "previously rejected patterns" block for
// the arbiter prompt from mem, or "" when there is nothing worth reporting.
// When it returns "" the arbiter prompt is byte-identical to a no-memory run.
func RejectedPatternsSection(mem *memory.Memory) string {
	if mem == nil {
		return ""
	}
	patterns := mem.RejectedPatterns()
	if len(patterns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("The human reviewer of this repository has repeatedly declined findings matching the patterns below. ")
	b.WriteString("Treat this as strong local evidence that these patterns are noise here: prefer to SUPPRESS or DEMOTE a new finding that matches one of them, ")
	b.WriteString("UNLESS it is clearly higher-severity or materially different from what was rejected before. ")
	b.WriteString("Never suppress a security finding or an error/critical-severity finding on this basis.\n\n")
	for _, r := range patterns {
		fmt.Fprintf(&b, "- specialist=%s path=%s severity=%s — reviewer skipped %d× (last %s)\n",
			r.Fingerprint.Specialist,
			globForDisplay(r.Fingerprint.PathGlob),
			severityForDisplay(r.Fingerprint.Severity),
			r.Count,
			r.Last.Format("2006-01-02"))
	}
	return b.String()
}

func globForDisplay(g string) string {
	if strings.TrimSpace(g) == "" {
		return "*"
	}
	return g
}

func severityForDisplay(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(any)"
	}
	return s
}

// RecordReviewerMemory folds a completed review's decisions into the per-repo
// memory store at post time. It is the single call the TUI makes; everything
// about what counts as posted/skipped/reversed lives here. Fail-open: any
// error logs and is swallowed so a memory-write problem never blocks the post.
//
// Decisions recorded:
//   - posted: every inline finding actually posted this session
//     (FlatPostableFindingsForPost — i.e. minus arbiter suppressions and
//     minus the reviewer's skips).
//   - skipped: every inline finding the reviewer skipped
//     (UserSkipPostKeys) plus every finding the memory suppressor held back
//     that the reviewer did NOT resurface (a reinforcing reject signal).
//   - demote_reversed: every arbiter-demoted PR-wide/inline finding the
//     reviewer opted back into the body (UserPostDemotedKeys), plus every
//     memory-suppressed finding the reviewer resurfaced (they overrode the
//     suppressor — back off next time).
func RecordReviewerMemory(d *Draft) {
	if d == nil {
		return
	}
	owner, repo := memoryRepoKey(d.PR)
	if owner == "" || repo == "" {
		return
	}
	entries := collectMemoryEntries(d)
	if len(entries) == 0 {
		return
	}
	if err := memory.NewStore().Record(owner, repo, entries...); err != nil {
		applog.Warn("reviewer memory: record failed", "repo", owner+"/"+repo, "err", err.Error())
	}
}

// collectMemoryEntries derives the memory entries from a draft's final state.
// Pure (no IO) so it is directly unit-testable.
func collectMemoryEntries(d *Draft) []memory.Entry {
	if d == nil {
		return nil
	}
	var entries []memory.Entry

	// posted: what actually shipped inline this session.
	for _, ff := range d.FlatPostableFindingsForPost() {
		entries = append(entries, memory.Entry{
			Fingerprint: fingerprintForFinding(ff.Specialist, ff.Finding),
			Decision:    memory.DecisionPosted,
		})
	}

	// skipped: inline findings the reviewer explicitly skipped.
	if len(d.UserSkipPostKeys) > 0 {
		for _, ff := range d.FlatPostableFindings() {
			k := suppressionKey(ff.Specialist, ff.Finding.Path, ff.Finding.Line, ff.Finding.Side)
			if _, skipped := d.UserSkipPostKeys[k]; skipped {
				entries = append(entries, memory.Entry{
					Fingerprint: fingerprintForFinding(ff.Specialist, ff.Finding),
					Decision:    memory.DecisionSkipped,
				})
			}
		}
	}

	// demote_reversed: arbiter-demoted findings the reviewer opted back in.
	for _, ff := range d.DemotedHidden {
		if d.DemotedPostingEnabled(ff.Specialist, ff.Finding) {
			entries = append(entries, memory.Entry{
				Fingerprint: fingerprintForFinding(ff.Specialist, ff.Finding),
				Decision:    memory.DecisionDemoteReversed,
			})
		}
	}

	// memory-suppressed findings: resurfaced ones are a reversal (positive)
	// signal; the rest reinforce the skip that produced the suppression.
	for _, ms := range d.MemorySuppressed {
		dec := memory.DecisionSkipped
		if ms.Resurfaced {
			dec = memory.DecisionDemoteReversed
		}
		entries = append(entries, memory.Entry{
			Fingerprint: fingerprintForFinding(ms.Specialist, ms.Finding),
			Decision:    dec,
		})
	}

	return entries
}

// memoryRepoKey extracts a lower-cased owner/repo from a PR, falling back to
// splitting Repository ("owner/name") when the dedicated fields are blank.
func memoryRepoKey(pr *gh.PR) (owner, repo string) {
	if pr == nil {
		return "", ""
	}
	owner = strings.ToLower(strings.TrimSpace(pr.Owner))
	repo = strings.ToLower(strings.TrimSpace(pr.Repo))
	if owner != "" && repo != "" {
		return owner, repo
	}
	if parts := strings.SplitN(strings.TrimSpace(pr.Repository), "/", 2); len(parts) == 2 {
		return strings.ToLower(strings.TrimSpace(parts[0])), strings.ToLower(strings.TrimSpace(parts[1]))
	}
	return "", ""
}
