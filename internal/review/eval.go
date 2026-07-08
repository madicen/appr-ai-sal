package review

import (
	"context"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/applog"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review/conventionwitness"
)

// eval.go is the in-memory review entrypoint the evals harness
// (internal/evals) drives. Unlike Run it performs NO gh / git I/O: the caller
// supplies the PR metadata, the unified diff, the repo-context / brief blocks,
// and the PR-agent inputs directly (loaded from a fixture corpus). Everything
// downstream is the REAL review pipeline — the same runReviewSpecialist /
// runPRAgent, the same deterministic gates, the same arbiter / witness /
// vibe-coach — so an eval measures exactly what a live review would produce
// for the same model output.
//
// Each agent runs a SINGLE time (no stageWithRetry): the harness wants to
// observe the model's first-attempt behaviour (JSON-parse-first-try rate),
// not the retry machinery's eventual success. Usage/cost is metered through
// the same ai.WithUsageObserver seam a live run uses.

// EvalInput is one in-memory PR to review, assembled from a corpus fixture.
// Any field may be zero — an empty brief simply means that section is omitted,
// matching the live runner's fail-open composition.
type EvalInput struct {
	// PR is the PR metadata (title/body/author/refs/stats). Required.
	PR *gh.PR
	// Diff is the raw unified diff the specialists review. Required.
	Diff string
	// Worktree is a directory the gates may read (e.g. the IaC schema gate
	// reads the enclosing resource type). For a diff-only fixture it can be
	// the corpus case directory or "".
	Worktree string
	// RepoContext / PerAgentBriefs / Evidence / LangSection / TechSection are
	// the optional brief blocks injected into the specialist prompts (see
	// buildReviewUserPrompt). PerAgentBriefs is keyed by specialist name.
	RepoContext    string
	PerAgentBriefs map[string]string
	Evidence       string
	LangSection    string
	TechSection    string
	// Checks / Threads / Discussion are the PR-agent inputs (see PRAgentInput).
	Checks     *gh.ChecksReport
	Threads    []gh.ReviewThread
	Discussion []gh.DiscussionEvent
	// TechConfigured controls whether the tech specialist runs (it needs
	// technology-expert briefs; see ActiveSpecialists). Set true when the
	// fixture supplies a TechSection.
	TechConfigured bool
	// RunPRAgents / RunWitness / RunArbiter / RunVibeCoach select which
	// synthesis stages run. The specialists always run; the rest are opt-in so
	// a scoring-only corpus need not supply arbiter/vibe canned responses.
	RunPRAgents  bool
	RunWitness   bool
	RunArbiter   bool
	RunVibeCoach bool
}

// EvalAgentObservation is one agent's single-attempt outcome, carrying the
// post-gate findings plus the signals the scorer needs.
type EvalAgentObservation struct {
	// Agent is the specialist / PR-agent name.
	Agent string
	// Kind is KindCode for specialists, KindPRWide for PR agents.
	Kind Kind
	// Summary / Findings are the agent's post-gate output.
	Summary  string
	Findings []Finding
	// ParsedOK is true when the model's output parsed on the first attempt
	// (the JSON-parse-first-try signal). False when the single call failed to
	// produce parseable JSON (or otherwise errored).
	ParsedOK bool
	// RawSuggestionAttempts is how many inline suggestions the model emitted
	// before the gates ran — the denominator for suggestion-survival and
	// anchor-hit rates.
	RawSuggestionAttempts int
	// Err is the agent's error, if any (parse failure, prompt-load failure).
	Err error
}

// EvalObservation is the full result of running one case: every agent's
// observation, the optional synthesis verdicts, and the metered usage.
type EvalObservation struct {
	Agents  []EvalAgentObservation
	Witness []conventionwitness.Witness
	Arbiter *RepoArbiterResult
	Vibe    *VibeCoachResult
	Usage   RunUsage
}

// FinalVerdict resolves the case's merge verdict the way the rendered review
// would: the vibe-coach verdict, overridden by the arbiter when it set one.
// Empty when no vibe-coach stage ran.
func (o EvalObservation) FinalVerdict() string {
	v := ""
	if o.Vibe != nil {
		v = NormalizeVibeVerdict(o.Vibe.Verdict)
	}
	if o.Arbiter != nil {
		if ov := NormalizeVibeVerdict(o.Arbiter.EffectiveVerdict); ov != "" {
			v = ov
		} else if ov := NormalizeVibeVerdict(o.Arbiter.VerdictOverride); ov != "" {
			v = ov
		}
	}
	return v
}

// SpecialistObservations returns only the KindCode agents (the specialists),
// preserving order.
func (o EvalObservation) SpecialistObservations() []EvalAgentObservation {
	var out []EvalAgentObservation
	for _, a := range o.Agents {
		if a.Kind == KindCode {
			out = append(out, a)
		}
	}
	return out
}

// EvalRun runs one in-memory case through the real review pipeline and returns
// the per-agent observations plus metered usage. It is deterministic given a
// deterministic provider (the harness injects one for tests / offline replay);
// with a live provider it is the same call graph a review makes.
func EvalRun(ctx context.Context, cfg *aiconfig.Config, in EvalInput) EvalObservation {
	if cfg == nil {
		cfg = aiconfig.DefaultConfig()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var obs EvalObservation

	// Meter usage/cost through the same seam a live run uses.
	start := time.Now()
	acc := newUsageAccumulator()
	ctx = ai.WithUsageObserver(ctx, acc.record)

	// --- Code specialists ------------------------------------------------
	specResults := make([]SpecialistResult, 0)
	for _, name := range ActiveSpecialists(in.TechConfigured) {
		sctx := applog.WithStage(ctx, "specialist "+name)
		brief := ""
		if in.PerAgentBriefs != nil {
			brief = in.PerAgentBriefs[name]
		}
		ev := ""
		if specWantsEvidence(name) {
			ev = in.Evidence
		}
		// The evals harness intentionally runs no static-analysis pre-pass:
		// it must be deterministic and independent of which external tools are
		// installed, so staticSection / staticCleanFiles are empty.
		r := runReviewSpecialist(sctx, cfg, name, in.Worktree, in.PR, in.Diff, brief, ev, "", nil, in.LangSection, in.TechSection)
		specResults = append(specResults, r)
		obs.Agents = append(obs.Agents, agentObservation(name, KindCode, r))
	}

	// --- PR-wide agents --------------------------------------------------
	var prResults []SpecialistResult
	if in.RunPRAgents {
		prIn := PRAgentInput{Checks: in.Checks, Threads: in.Threads, Discussion: in.Discussion}
		for _, name := range AllPRAgents {
			pctx := applog.WithStage(ctx, "pr-agent "+name)
			r := runPRAgent(pctx, cfg, name, in.Worktree, in.PR, in.Diff, prIn)
			prResults = append(prResults, r)
			obs.Agents = append(obs.Agents, agentObservation(name, KindPRWide, r))
		}
	}

	// Combine + dedupe exactly as the live runner does before synthesis.
	all := make([]SpecialistResult, 0, len(specResults)+len(prResults))
	all = append(all, specResults...)
	all = append(all, prResults...)
	all = dedupeInlineFindingsAcrossSpecialists(all)

	// --- Convention witness (optional) -----------------------------------
	if in.RunWitness && in.PR != nil {
		var inputs []conventionwitness.FindingInput
		var techFindings []Finding
		var formattingFindings []Finding
		for _, s := range specResults {
			if s.Err != nil || !specWitnessable(s.Specialist) {
				continue
			}
			for _, f := range s.Findings {
				// Q6.5: include PR-wide witnessable findings (comment-bearing)
				// alongside inline ones, matching runConventionWitnessPhase.
				if !findingIsInlinePostable(f) && strings.TrimSpace(f.Comment) == "" {
					continue
				}
				inputs = append(inputs, conventionwitness.FindingInput{
					Specialist: s.Specialist, Path: f.Path, Line: f.Line,
					Side: f.Side, Severity: string(f.Severity), Comment: f.Comment,
				})
				if specWantsConventionEvidence(s.Specialist) {
					techFindings = append(techFindings, f)
				}
				if specWantsFormattingEvidence(s.Specialist) {
					formattingFindings = append(formattingFindings, f)
				}
			}
		}
		if len(inputs) > 0 {
			evidence := appendTechConventionEvidence(in.Evidence, in.Worktree, techFindings)
			evidence = appendFormattingConventionEvidence(evidence, in.Worktree, formattingFindings)
			wctx := applog.WithStage(ctx, "convention-witness")
			res := conventionwitness.Run(wctx, cfg, Complete, in.Worktree,
				conventionwitness.PrWideRef{Repository: in.PR.Repository, Number: in.PR.Number, Title: in.PR.Title},
				inputs, evidence)
			if res.Err == nil {
				obs.Witness = res.Witnesses
			}
		}
	}

	// --- Repo arbiter (optional) -----------------------------------------
	if in.RunArbiter {
		actx := applog.WithStage(ctx, "repo-arbiter")
		obs.Arbiter = RunRepoArbiter(actx, cfg, in.Worktree, in.PR, all, in.PerAgentBriefs, in.TechSection, obs.Witness)
	}

	// --- Vibe-coach (optional) -------------------------------------------
	if in.RunVibeCoach {
		d := &Draft{PR: in.PR, Diff: in.Diff, Worktree: in.Worktree, Strictness: cfg.ReviewStrictness, Specialists: all}
		if obs.Arbiter != nil && obs.Arbiter.Err == nil {
			d.RepoArbiter = obs.Arbiter
			FinalizeRepoArbiter(obs.Arbiter, d)
		}
		vctx := applog.WithStage(ctx, "vibe-coach")
		obs.Vibe = RunVibeCoachForDraft(vctx, cfg, d, nil)
	}

	obs.Usage = acc.snapshot(time.Since(start))
	return obs
}

// agentObservation projects a SpecialistResult into the eval observation,
// deriving ParsedOK from whether the single attempt errored.
func agentObservation(name string, kind Kind, r SpecialistResult) EvalAgentObservation {
	return EvalAgentObservation{
		Agent:                 name,
		Kind:                  kind,
		Summary:               r.Summary,
		Findings:              r.Findings,
		ParsedOK:              r.Err == nil,
		RawSuggestionAttempts: r.RawSuggestionAttempts,
		Err:                   r.Err,
	}
}

// countInlineSuggestionAttempts counts findings that carry a model-proposed
// one-click suggestion on an inline anchor. It is the pre-gate denominator the
// evals harness uses for suggestion-survival / anchor-hit rates; it never
// affects the live review path.
func countInlineSuggestionAttempts(findings []Finding) int {
	n := 0
	for _, f := range findings {
		if findingIsInlinePostable(f) && strings.TrimSpace(f.Suggestion) != "" {
			n++
		}
	}
	return n
}
