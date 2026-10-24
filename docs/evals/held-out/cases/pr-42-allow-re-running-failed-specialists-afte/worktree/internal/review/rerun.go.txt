package review

import (
	"context"
	"fmt"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/applog"
	"github.com/madicen/appr-ai-sal/internal/review/conventionwitness"
)

// RerunContext carries the exact inputs the pipeline used for agent calls so
// post-run re-runs stay faithful to the original pass. Never serialized —
// restored draft-cache sessions leave Draft.Rerun nil and disable re-run.
type RerunContext struct {
	SpecialistDiff   string
	PRAgentDiff      string
	PerAgent         map[string]string
	PREvidence       string
	StaticSection    string
	StaticCleanFiles map[string]bool
	LangSection      string
	TechSection      string
	IntentSection    string
	ExpandedSection  string
	RejectedMemory   string
	PRAgentInput     PRAgentInput
	// PreArbiterSpecialists is the specialist+PR-agent slice after dedupe +
	// memory suppression and before FinalizeRepoArbiter. Re-runs rebase from here.
	PreArbiterSpecialists []SpecialistResult
	ParallelSpecialists   bool
	ParallelPRAgents      bool
	ConventionWitness     bool
	RepoExpertPanel       bool
	RepoArbiterDemotions  bool
}

// CanRerunAgent reports whether name is a failed specialist/PR-agent eligible
// for manual re-run. Arbiter and vibe-coach are gated in the TUI (always when
// their rows exist and Rerun != nil). Skipped (circuit-breaker) agents are not
// re-runnable.
func (d *Draft) CanRerunAgent(name string) bool {
	if d == nil || d.Rerun == nil {
		return false
	}
	name = strings.TrimSpace(name)
	if name == "" || (!IsPRAgent(name) && !isCodeSpecialistName(name)) {
		return false
	}
	for _, s := range d.Specialists {
		if s.Specialist != name {
			continue
		}
		return s.EffectiveOutcome() == OutcomeFailed
	}
	return false
}

func isCodeSpecialistName(name string) bool {
	for _, n := range AllSpecialists {
		if n == name {
			return true
		}
	}
	return false
}

// FailedAgentNames returns specialist/PR-agent names that finished failed
// (not skipped), in Draft.Specialists order.
func (d *Draft) FailedAgentNames() []string {
	if d == nil {
		return nil
	}
	var out []string
	for _, s := range d.Specialists {
		if s.EffectiveOutcome() == OutcomeFailed {
			out = append(out, s.Specialist)
		}
	}
	return out
}

// ReplaceAgent swaps the named specialist/PR-agent result in place on both
// Specialists and Rerun.PreArbiterSpecialists. Returns an error when the
// draft cannot re-run (nil Rerun or unknown name).
func (d *Draft) ReplaceAgent(name string, res SpecialistResult) error {
	if d == nil {
		return fmt.Errorf("replace agent: nil draft")
	}
	if d.Rerun == nil {
		return fmt.Errorf("replace agent: re-run unavailable (no RerunContext)")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("replace agent: empty name")
	}
	res.Specialist = name
	if !replaceInSlice(&d.Rerun.PreArbiterSpecialists, name, res) {
		// Added after a partial snapshot — append so re-runs still stick.
		d.Rerun.PreArbiterSpecialists = append(d.Rerun.PreArbiterSpecialists, res)
	}
	if !replaceInSlice(&d.Specialists, name, res) {
		d.Specialists = append(d.Specialists, res)
	}
	return nil
}

func replaceInSlice(slice *[]SpecialistResult, name string, res SpecialistResult) bool {
	for i := range *slice {
		if (*slice)[i].Specialist == name {
			(*slice)[i] = res
			return true
		}
	}
	return false
}

// MarkSynthesisStale flags arbiter and vibe as out of date after a
// specialist/PR-agent re-run and clears demotion bookkeeping that depended on
// the previous arbiter pass.
func (d *Draft) MarkSynthesisStale() {
	if d == nil {
		return
	}
	d.ArbiterStale = true
	d.VibeStale = true
	d.DemotedHidden = nil
	d.UserPostDemotedKeys = nil
}

// ClearArbiterStale clears the arbiter stale flag and dirties vibe (downstream).
func (d *Draft) ClearArbiterStale() {
	if d == nil {
		return
	}
	d.ArbiterStale = false
	d.VibeStale = true
}

// ClearVibeStale clears the vibe stale flag.
func (d *Draft) ClearVibeStale() {
	if d == nil {
		return
	}
	d.VibeStale = false
}

// SynthesisStale reports whether arbiter or vibe need a manual refresh.
func (d *Draft) SynthesisStale() bool {
	return d != nil && (d.ArbiterStale || d.VibeStale)
}

// RefreshDraftAfterAgentReplace installs res for name, rebuilds Specialists
// from the pre-arbiter snapshot (dedupe + memory), and marks synthesis stale.
func RefreshDraftAfterAgentReplace(d *Draft, name string, res SpecialistResult) error {
	if err := d.ReplaceAgent(name, res); err != nil {
		return err
	}
	base := append([]SpecialistResult(nil), d.Rerun.PreArbiterSpecialists...)
	base = dedupeInlineFindingsAcrossSpecialists(base)
	mem := LoadRepoMemory(d.PR)
	var memSup []MemorySuppressedFinding
	base, memSup = ApplyMemorySuppression(mem, base)
	d.Specialists = base
	d.MemorySuppressed = memSup
	d.MarkSynthesisStale()
	return nil
}

// RunSpecialistForDraft re-runs one code specialist using Draft.Rerun inputs.
// No circuit breaker (user-initiated). notify may be nil.
func RunSpecialistForDraft(ctx context.Context, cfg *aiconfig.Config, d *Draft, name string, notify func(attempt int, err error)) SpecialistResult {
	res := SpecialistResult{Specialist: name}
	if d == nil || cfg == nil || d.Rerun == nil {
		res.Err = fmt.Errorf("specialist re-run: nil draft, config, or RerunContext")
		res.Outcome = OutcomeFailed
		return res
	}
	if !isCodeSpecialistName(name) {
		res.Err = fmt.Errorf("specialist re-run: %q is not a code specialist", name)
		res.Outcome = OutcomeFailed
		return res
	}
	if notify == nil {
		notify = func(int, error) {}
	}
	rc := d.Rerun
	repoCtx := ""
	if rc.PerAgent != nil {
		repoCtx = rc.PerAgent[name]
	}
	ev := ""
	if specWantsEvidence(name) {
		ev = rc.PREvidence
	}
	intent := ""
	if specWantsIntent(name) {
		intent = rc.IntentSection
	}
	runWith := func(stageCfg *aiconfig.Config) SpecialistResult {
		var r SpecialistResult
		_ = stageWithRetry(ctx, stageCfg, "specialist "+name, notify, func(sctx context.Context) error {
			stCtx, cancel := context.WithTimeout(applog.WithStage(sctx, "specialist "+name), perStageBudget(stageCfg))
			defer cancel()
			r = runReviewSpecialist(stCtx, stageCfg, name, d.Worktree, d.PR, rc.SpecialistDiff, repoCtx, ev, rc.StaticSection, rc.StaticCleanFiles, rc.LangSection, rc.TechSection, intent, rc.ExpandedSection)
			if r.Err != nil {
				return r.Err
			}
			return nil
		})
		return r
	}
	if models := cfg.EnsembleModels(name); len(models) >= 2 {
		res = runEnsemble(name, models, cfg, runWith)
	} else {
		res = runWith(cfg.ForStage(name))
	}
	if res.Err != nil {
		res.Outcome = OutcomeFailed
	}
	return res
}

// RunPRAgentForDraft re-runs one PR agent using Draft.Rerun inputs.
func RunPRAgentForDraft(ctx context.Context, cfg *aiconfig.Config, d *Draft, name string, notify func(attempt int, err error)) SpecialistResult {
	res := SpecialistResult{Specialist: name}
	if d == nil || cfg == nil || d.Rerun == nil {
		res.Err = fmt.Errorf("pr-agent re-run: nil draft, config, or RerunContext")
		res.Outcome = OutcomeFailed
		return res
	}
	if !IsPRAgent(name) {
		res.Err = fmt.Errorf("pr-agent re-run: %q is not a PR agent", name)
		res.Outcome = OutcomeFailed
		return res
	}
	if notify == nil {
		notify = func(int, error) {}
	}
	rc := d.Rerun
	intent := ""
	if specWantsIntent(name) {
		intent = rc.IntentSection
	}
	runWith := func(stageCfg *aiconfig.Config) SpecialistResult {
		var r SpecialistResult
		_ = stageWithRetry(ctx, stageCfg, "pr-agent "+name, notify, func(sctx context.Context) error {
			stCtx, cancel := context.WithTimeout(applog.WithStage(sctx, "pr-agent "+name), perStageBudget(stageCfg))
			defer cancel()
			r = runPRAgent(stCtx, stageCfg, name, d.Worktree, d.PR, rc.PRAgentDiff, rc.PRAgentInput, intent)
			if r.Err != nil {
				return r.Err
			}
			return nil
		})
		return r
	}
	if models := cfg.EnsembleModels(name); len(models) >= 2 {
		res = runEnsemble(name, models, cfg, runWith)
	} else {
		res = runWith(cfg.ForStage(name))
	}
	if res.Err != nil {
		res.Outcome = OutcomeFailed
	}
	return res
}

// RunRepoArbiterForDraft re-runs the repo arbiter (and convention witness when
// enabled) against the draft's current specialists. Clears ArbiterStale and
// marks vibe stale. Returns a non-nil result; Err is set on failure.
func RunRepoArbiterForDraft(ctx context.Context, cfg *aiconfig.Config, d *Draft, notify func(attempt int, err error)) *RepoArbiterResult {
	if d == nil || cfg == nil {
		return &RepoArbiterResult{Err: fmt.Errorf("arbiter re-run: nil draft or config")}
	}
	if d.Rerun == nil || !d.Rerun.RepoExpertPanel {
		return &RepoArbiterResult{Err: fmt.Errorf("arbiter re-run: unavailable")}
	}
	if notify == nil {
		notify = func(int, error) {}
	}
	rc := d.Rerun
	var witnesses []conventionwitness.Witness
	if rc.ConventionWitness {
		// code specialists only for witness inputs (same as pipeline)
		var codeOnly []SpecialistResult
		for _, s := range d.Specialists {
			if isCodeSpecialistName(s.Specialist) {
				codeOnly = append(codeOnly, s)
			}
		}
		witnesses = runConventionWitnessPhase(ctx, cfg, nil, d.Worktree, d.PR, codeOnly, rc.PREvidence, nil)
		d.ConventionWitness = witnesses
	}
	var arb *RepoArbiterResult
	_ = stageWithRetry(ctx, cfg, "repo-arbiter", notify, func(sctx context.Context) error {
		stCtx, cancel := context.WithTimeout(applog.WithStage(sctx, "repo-arbiter"), perStageBudget(cfg))
		defer cancel()
		arb = RunRepoArbiter(stCtx, cfg, d.Worktree, d.PR, d.Specialists, rc.PerAgent, rc.TechSection, witnesses, rc.RejectedMemory)
		if arb != nil && arb.Err != nil {
			return arb.Err
		}
		return nil
	})
	if arb == nil {
		arb = &RepoArbiterResult{Err: fmt.Errorf("arbiter re-run: no result")}
	}
	if !rc.RepoArbiterDemotions {
		arb.Demoted = nil
	}
	d.RepoArbiter = arb
	if arb.Err == nil {
		FinalizeRepoArbiter(arb, d)
	}
	d.ClearArbiterStale()
	return arb
}
