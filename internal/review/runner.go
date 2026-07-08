package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/appdirs"
	"github.com/madicen/appr-ai-sal/internal/applog"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/review/conventionwitness"
	"github.com/madicen/appr-ai-sal/internal/review/langagents"
	"github.com/madicen/appr-ai-sal/internal/review/repoagents"
	"github.com/madicen/appr-ai-sal/internal/review/repocontext"
	"github.com/madicen/appr-ai-sal/internal/review/staticpass"
	"github.com/madicen/appr-ai-sal/internal/review/techagents"
)

// Progress messages are emitted on the channel returned by Run so the TUI can
// stream updates to the user as specialists complete.
type Progress struct {
	Stage   string // "checkout", "diff", "repo-context", "repo-agents", "tech-agents", "lang-agents", "repo-evidence", "context-summary", "convention-witness", "specialist", "pr-agent", "vibe-coach", "repo-arbiter", "circuit-breaker", "degraded", "usage", "done"; specialist/pr-agent Detail is "<name>:start"/"<name>:done"/"<name>:retry N (...)"/"<name>:skipped" (pr-agent also emits "warning: ..." for fetch failures); vibe-coach Detail is "start"/"done"/"retry N (...)" or "skipped" when downstream agents are bypassed; circuit-breaker/degraded (R4) carry the abort reason / partial-degradation summary in Detail
	Detail  string // free-form detail about the stage
	Err     error  // non-nil if this stage hit an error worth surfacing
	Result  *SpecialistResult
	Vibe    *VibeCoachResult
	Arbiter *RepoArbiterResult // populated on Stage="repo-arbiter" Detail="done"
	Final   *Draft             // populated only on the final "done" message
	// Usage carries a running snapshot of aggregated inference usage/cost.
	// It rides Stage="usage" events (running totals as calls complete) and the
	// final Stage="done" event (the run total). Nil on every other event.
	Usage *RunUsage
}

// perStageBudget is the max wall-clock per individual AI stage (specialist,
// vibe-coach, expert, arbiter). It's intentionally generous — the user can
// always abort with q from the running overlay, and we no longer wrap the
// whole review in a single context.WithTimeout that cuts off downstream
// stages mid-pipeline.
func perStageBudget(cfg *aiconfig.Config) time.Duration {
	d := cfg.RunContextTimeout()
	if d < 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

// Run prepares a worktree for the PR and dispatches the specialist panel.
// Caller should drain the returned channel until it closes; the last message
// will have Stage=="done" and Final set.
//
// The runner does NOT wrap ctx in a single deadline — long stages no longer
// starve downstream stages. Each AI stage gets its own per-stage budget and
// stage-level retry; the user can abort the run from the TUI overlay.
func Run(ctx context.Context, ref gh.Ref, cfg *aiconfig.Config) (<-chan Progress, error) {
	if cfg == nil {
		cfg = aiconfig.DefaultConfig()
	}
	runCfg := cfg.Clone()

	out := make(chan Progress, 16)

	go func() {
		defer close(out)

		applog.Info("review run start", "ref", ref.String(), "provider", string(runCfg.Provider), "strictness", string(runCfg.ReviewStrictness))

		// R1: meter every inference call this run makes. The observer is
		// installed on the run's context, so every stage that derives from it
		// (specialists, PR agents, arbiter, witness, vibe-coach, the repair
		// pass, the context-vs-change summary) reports usage/cost through the
		// single ai provider layer — no per-signature sink threading. The
		// accumulator is concurrency-safe because parallel specialists/PR
		// agents report from their own goroutines.
		runStart := time.Now()
		usageAcc := newUsageAccumulator()
		ctx = ai.WithUsageObserver(ctx, func(r ai.CallReport) {
			usageAcc.record(r)
			// Emit a running-total snapshot so the overlay can show usage
			// climbing live. Sends are safe alongside the parallel stage
			// goroutines already writing to out; the channel is only closed
			// after every inference call has returned.
			snap := usageAcc.snapshot(time.Since(runStart))
			out <- Progress{Stage: "usage", Usage: &snap}
		})

		pr, err := gh.GetPR(ctx, ref)
		if err != nil {
			applog.Error("review stage failed", "stage", "fetch-pr", "ref", ref.String(), "err", err.Error())
			out <- Progress{Stage: "fetch-pr", Err: fmt.Errorf("fetch PR: %w", err)}
			return
		}

		worktree, err := prepareWorktree(ctx, ref)
		if err != nil {
			applog.Error("review stage failed", "stage", "checkout", "ref", ref.String(), "err", err.Error())
			out <- Progress{Stage: "checkout", Err: fmt.Errorf("prepare worktree: %w", err)}
			return
		}
		out <- Progress{Stage: "checkout", Detail: fmt.Sprintf("%s · review %s", worktree, strictnessSummaryForProgress(runCfg.ReviewStrictness))}

		diff, err := gh.GetDiff(ctx, ref)
		if err != nil {
			out <- Progress{Stage: "diff", Err: fmt.Errorf("fetch diff: %w", err)}
			return
		}
		out <- Progress{Stage: "diff", Detail: fmt.Sprintf("%d bytes", len(diff))}

		rc, rerr := repoconfig.Load()
		if rerr != nil {
			rc = repoconfig.Default()
			out <- Progress{Stage: "repo-context", Detail: "config: " + rerr.Error()}
		}
		repoconfig.ApplyParallelExecutionEnv(rc)

		// R2: install a single per-run weighted semaphore on the context so no
		// more than MaxConcurrentInference (default 3) inference calls run
		// concurrently across the WHOLE run — every stage goroutine derives
		// from this ctx, so the cap is shared across specialists, PR agents,
		// the hidden repair pass, and the arbiter/witness regardless of the
		// parallel toggles. This is the client-side rate limit that makes the
		// parallel defaults safe. No inference has happened yet at this point
		// (only gh/git fetches), so no call escapes the cap.
		ctx = ai.WithConcurrencyLimit(ctx, rc.MaxConcurrentInferenceOrDefault())

		// Q8: PR-author intent pre-pass. One cheap LLM call over the PR
		// description + linked issues, extracted into a structured section
		// injected into the intent-aware stages (scope, testing, vibe-coach).
		// Launched here — after the usage observer + concurrency cap are on
		// ctx, so the call is metered and capped — and collected just before
		// the specialist / PR-agent phases, overlapping with repo-context
		// composition so it rarely adds wall-clock. Fully fail-open: a nil
		// result means the intent-aware stages behave exactly as before Q8.
		intentCh := make(chan *PRIntent, 1)
		go func() { intentCh <- RunIntentPrepass(ctx, runCfg, ref, pr) }()

		// R4: aggregate run circuit breaker. Trips when too many AI stages fail
		// in a row OR the whole run exceeds a wall-clock cap; once tripped the
		// remaining stages are skipped (never interrupted mid-call) and marked
		// degraded so the summary can disclose the partial review. Both arms are
		// configurable via repo-context.json (see RunWallClockCap /
		// MaxConsecutiveStageFailuresOrDefault; a negative value disables an arm).
		breaker := newRunBreaker(runStart, rc.RunWallClockCap(), rc.MaxConsecutiveStageFailuresOrDefault())

		// R3: shape the diff BEFORE it is inlined into any prompt. The budgeter
		// drops lockfiles/vendored/generated/minified/binary files (manifest
		// entry only), applies a per-file line cap, and enforces a conservative
		// per-provider whole-diff byte cap so a multi-megabyte PR can never blow
		// the provider context window / trigger a 400. A diff that fits under
		// all caps with nothing to elide passes through byte-identical, so
		// ordinary small PRs are unaffected. The RAW diff is kept for the TUI /
		// GitHub (final.Diff) — surviving lines keep their real line numbers, so
		// findings the model files against the shaped diff still anchor
		// correctly against the full diff.
		shapedDiff, budgetReport := budgetDiff(diff, newBudgetConfig(rc, runCfg))
		if budgetReport.Truncated {
			applog.Info("review diff budgeted",
				"ref", ref.String(),
				"original_bytes", budgetReport.OriginalBytes,
				"shaped_bytes", budgetReport.ShapedBytes,
				"elided", len(budgetReport.Elided),
				"truncated", len(budgetReport.Truncations))
			out <- Progress{Stage: "diff", Detail: "warning: " + budgetReport.DisclosureLine()}
		}

		// PR-level agents (description / checks / discussion / scope) read the
		// PR's CI checks, review threads, and conversation. Fetch those signals
		// in the background so they overlap with repo-context composition and the
		// specialist phase; each fetch degrades gracefully (an empty input just
		// means the matching agent runs without that section).
		prAgentsEnabled := rc == nil || rc.PRAgents
		var prDataCh chan PRAgentInput
		if prAgentsEnabled {
			prDataCh = make(chan PRAgentInput, 1)
			go func() {
				var in PRAgentInput
				// R6.1: one fused GraphQL call fetches checks + review threads
				// + discussion (was three separate execs). On error the
				// individual sections stay empty and the matching agent runs
				// without that signal, matching the prior fail-open behavior.
				if data, derr := gh.GetPRAgentData(ctx, ref); derr == nil {
					in.Checks = data.Checks
					in.Threads = data.Threads
					in.Discussion = data.Discussion
				} else {
					out <- Progress{Stage: "pr-agent", Detail: "warning: pr-agent data fetch: " + derr.Error()}
				}
				prDataCh <- in
			}()
		}

		repoBlock, composeErr := ComposeRepositoryContextBlock(ctx, runCfg, rc, pr, worktree, false)
		if composeErr != nil {
			out <- Progress{Stage: "repo-context", Detail: "warning: " + composeErr.Error()}
			repoBlock = ""
		} else if strings.TrimSpace(repoBlock) == "" {
			out <- Progress{Stage: "repo-context", Detail: "skipped (empty)"}
		} else {
			out <- Progress{Stage: "repo-context", Detail: fmt.Sprintf("%d bytes", len(repoBlock))}
		}
		repoCtx := repoBlock

		// Per-specialist repo-agent briefs: each specialist gets the brief
		// generated by the matching repo agent for this owner/repo (or "" if
		// none). vibe-coach receives no brief — it is a synthesis pass.
		perAgent := map[string]string{}
		if pr != nil {
			ra, raErr := repoagents.Load(pr.Owner, pr.Repo)
			if raErr != nil {
				out <- Progress{Stage: "repo-agents", Detail: "warning: " + raErr.Error()}
			} else if ra != nil {
				for _, name := range AllSpecialists {
					if ctx := strings.TrimSpace(ra.ContextFor(name)); ctx != "" {
						perAgent[name] = ctx
					}
				}
				if len(perAgent) > 0 {
					out <- Progress{Stage: "repo-agents", Detail: fmt.Sprintf("loaded %d brief(s)", len(perAgent))}
				} else {
					out <- Progress{Stage: "repo-agents", Detail: "none"}
				}
			}
		}

		// Per-repo technology expert briefs: one brief per technology, shared
		// across every specialist for this repo. Composed into a single
		// section so the prompt order stays deterministic. The runner emits
		// a tech-agents Progress so the overlay's "Context injection" group
		// can show what was loaded (or "none configured / disabled").
		techSection := composeTechSection(pr, rc, out)

		type cvOutcome struct {
			text string
			err  error
		}
		cvCh := make(chan cvOutcome, 1)
		go func() {
			if rc == nil || !rc.ContextVersusChangeSummary {
				cvCh <- cvOutcome{}
				return
			}
			s, err := SummarizeContextVersusChange(applog.WithStage(ctx, "context-summary"), runCfg, pr, shapedDiff, repoCtx, worktree)
			cvCh <- cvOutcome{text: s, err: err}
		}()

		// Per-PR static + history evidence for the testing/docs specialists.
		// Built once and reused across both specialists. Empty when
		// rc.IncludeRepoEvidence is false or when the harvester finds nothing.
		prEvidence := BuildPRReviewEvidence(ctx, rc, pr, diff, worktree)
		if strings.TrimSpace(prEvidence) != "" {
			out <- Progress{Stage: "repo-evidence", Detail: fmt.Sprintf("%d bytes", len(prEvidence))}
		}

		// Q5: static-analysis pre-pass. Runs cheap deterministic tools (gofmt,
		// go vet, plus any configured golangci-lint / ruff / eslint /
		// terraform validate) over the changed files in the worktree BEFORE the
		// specialists, each behind its own timeout and fully fail-open (a
		// missing binary / config / slow tool contributes nothing and never
		// errors a run). Its output is injected into every code specialist's
		// prompt (staticSection: "the linter already flags X — don't re-report;
		// report what linters can't see") and the checks agent (staticChecks),
		// and its formatter-clean-file set (staticCleanFiles) drives the Q5.d
		// "linter is silent" downgrade of hand-rolled formatting nits.
		staticSection := ""
		staticChecks := ""
		var staticCleanFiles map[string]bool
		{
			changed := changedPathsFromDiff(diff)
			var localRoot string
			if rc != nil {
				localRoot = rc.LocalRootFor(pr.Owner, pr.Repo)
			}
			sp := staticpass.Run(ctx, worktree, changed, staticpass.Options{
				Lint: repocontext.DetectLintConfigs(worktree, localRoot),
			})
			staticSection = staticpass.WrapSpecialistSection(staticpass.FormatSpecialistSection(sp))
			staticChecks = staticpass.FormatChecksAnnotations(sp)
			staticCleanFiles = sp.FormatterCleanFiles()
			if anns := sp.Annotations(); len(anns) > 0 || len(staticCleanFiles) > 0 {
				out <- Progress{Stage: "static-analysis", Detail: fmt.Sprintf("%d annotation(s), %d clean file(s)", len(anns), len(staticCleanFiles))}
			}
		}

		// Language briefs: pick the dominant language(s) the PR touches
		// and inject the matching brief(s). Bundled briefs are
		// guaranteed available for our top-5 languages; the runner
		// emits a warning when the PR exercises a language with no
		// bundle and no user-generated cache entry. See
		// internal/review/langagents for the selection rule.
		langSection := composeLangBriefSection(diff, out)

		// PR-level agents run against the PR metadata fetched above. They reuse
		// the SpecialistResult pipeline, so their findings post like a
		// specialist's. They're kept in a separate slice from the code
		// specialists so the convention witness (which reasons about code
		// testing/docs findings) keeps seeing only the code specialists. The
		// repo arbiter, by contrast, IS informed of the PR-agent findings (see
		// allSpecialists below) so it can suppress/demote them too.
		//
		// PR agents share no data dependency with the specialists (they read PR
		// metadata + the diff, not specialist findings), so when
		// rc.ParallelPRAgents is set we run their phase concurrently with the
		// specialists phase to cut wall-clock time. Both phases only need to be
		// done before the arbiter. Default (flag off): PR agents run after the
		// specialists, sequentially, to keep concurrent LLM calls (and
		// rate-limit bursts) low — mirroring ParallelSpecialists.
		// Collect the intent pre-pass result (launched above). Renders to an
		// empty section on a fail-open nil, so the intent-aware stages stay
		// byte-identical to pre-Q8 in that case.
		prIntent := <-intentCh
		intentSection := FormatIntentSection(prIntent)
		if prIntent.HasContent() {
			out <- Progress{Stage: "intent", Detail: fmt.Sprintf("%d bytes", len(intentSection))}
		} else {
			out <- Progress{Stage: "intent", Detail: "skipped (no description / issues)"}
		}

		var prAgents []SpecialistResult
		prParallel := prAgentsEnabled && rc != nil && rc.ParallelPRAgents
		var prWG sync.WaitGroup
		if prParallel {
			prWG.Add(1)
			go func() {
				defer prWG.Done()
				prData := <-prDataCh
				prData.StaticAnnotations = staticChecks
				prAgents = sortedPRAgentResults(runPRAgentsPhase(ctx, runCfg, rc, worktree, pr, shapedDiff, prData, intentSection, breaker, out))
			}()
		}

		// Specialists: sequential by default (repo-context.json parallel_specialists),
		// or parallel when configured / env override — see runSpecialistsPhase.
		// They receive the shaped diff (R3) so no single call can overflow the
		// provider context window.
		specialists := runSpecialistsPhase(ctx, runCfg, rc, worktree, pr, shapedDiff, perAgent, prEvidence, staticSection, staticCleanFiles, langSection, techSection, intentSection, breaker, out)

		if prParallel {
			prWG.Wait()
		} else if prAgentsEnabled {
			prData := <-prDataCh
			prData.StaticAnnotations = staticChecks
			prAgents = sortedPRAgentResults(runPRAgentsPhase(ctx, runCfg, rc, worktree, pr, shapedDiff, prData, intentSection, breaker, out))
		}

		cv := <-cvCh
		cvSummary := strings.TrimSpace(cv.text)
		if cv.err != nil {
			out <- Progress{Stage: "context-summary", Detail: "warning: " + cv.err.Error()}
			cvSummary = "_Could not generate relation summary: " + cv.err.Error() + "_"
		} else if cvSummary != "" {
			out <- Progress{Stage: "context-summary", Detail: fmt.Sprintf("%d chars", len(cvSummary))}
		}

		allSpecialists := make([]SpecialistResult, 0, len(specialists)+len(prAgents))
		allSpecialists = append(allSpecialists, specialists...)
		allSpecialists = append(allSpecialists, prAgents...)
		// Collapse the same inline finding when several specialists filed it
		// on one line, so it posts once (from the most-relevant lane) instead
		// of once per agent. Runs before the arbiter/vibe-coach so finding
		// counts are consistent across every surface. See finding_dedupe.go.
		allSpecialists = dedupeInlineFindingsAcrossSpecialists(allSpecialists)

		// B1 reviewer memory: load once for this repo (fail-open → empty).
		// The deterministic pre-arbiter suppressor holds back inline findings
		// matching a pattern the reviewer has skipped N≥3 times; the held-back
		// findings are carried on the draft (MemorySuppressed) so the TUI can
		// disclose and resurface them — never silently dropped. The rejected-
		// patterns section (built below) is injected into the arbiter prompt.
		repoMemory := LoadRepoMemory(pr)
		var memSuppressed []MemorySuppressedFinding
		allSpecialists, memSuppressed = ApplyMemorySuppression(repoMemory, allSpecialists)
		if len(memSuppressed) > 0 {
			out <- Progress{Stage: "reviewer-memory", Detail: fmt.Sprintf("suppressed %d finding(s) matching repeatedly-skipped patterns", len(memSuppressed))}
		}

		skipDownstream := !SpecialistsHaveAnyFindings(allSpecialists)

		final := &Draft{
			Ref:                        ref,
			PR:                         pr,
			Diff:                       diff,
			Worktree:                   worktree,
			Strictness:                 runCfg.ReviewStrictness,
			Specialists:                allSpecialists,
			RepositoryContext:          repoBlock,
			ContextVersusChangeSummary: cvSummary,
			PRIntent:                   prIntent,
			MemorySuppressed:           memSuppressed,
		}
		// Carry the diff-budget report so the rendered body can disclose that
		// the review ran on a truncated diff (R3). Only set when shaping
		// actually happened; nil means the full diff was reviewed.
		if budgetReport.Truncated {
			br := budgetReport
			final.DiffBudget = &br
		}

		breakerTripped, breakerJust, breakerReason := breaker.check(time.Now())
		announceBreakerTrip(out, breakerJust, breakerReason)
		if skipDownstream || breakerTripped {
			// No findings to synthesize, OR the circuit breaker tripped during
			// the specialist/PR-agent phases — either way skip the synthesis
			// stages (don't pile more calls onto a wedged run).
			out <- Progress{Stage: "vibe-coach", Detail: "skipped"}
			if rc != nil && rc.RepoExpertPanel {
				out <- Progress{Stage: "repo-arbiter", Detail: "skipped"}
			}
		} else {
			// Convention witness: classifies testing/docs findings against
			// the per-PR evidence pack so the arbiter has structured grounds
			// for demote/suppress decisions. Skipped when the flag is off,
			// when no testing/docs findings exist, or when the evidence pack
			// is empty (no signal to evidence against).
			var witnesses []conventionwitness.Witness
			if rc != nil && rc.ConventionWitness {
				witnesses = runConventionWitnessPhase(ctx, runCfg, rc, worktree, pr, specialists, prEvidence, out)
				final.ConventionWitness = witnesses
			}
			if rc != nil && rc.RepoExpertPanel {
				out <- Progress{Stage: "repo-arbiter", Detail: "start"}
				// Pass the combined slice (code specialists + PR agents) so the
				// arbiter sees the whole-PR findings and can suppress/demote
				// them, not just the code specialists'. Wrapped in
				// stageWithRetry so a transient parse/transport glitch on this
				// (previously non-retried) path re-runs like every other AI
				// stage — isRetryableStageError already lists "parse repo
				// arbiter".
				var arb *RepoArbiterResult
				arbNotify := func(attempt int, err error) {
					out <- Progress{Stage: "repo-arbiter", Detail: fmt.Sprintf("retry %d (%s)", attempt, retryReason(err))}
				}
				_ = stageWithRetry(ctx, runCfg, "repo-arbiter", arbNotify, func(sctx context.Context) error {
					stCtx, cancel := context.WithTimeout(applog.WithStage(sctx, "repo-arbiter"), perStageBudget(runCfg))
					defer cancel()
					arb = RunRepoArbiter(stCtx, runCfg, worktree, pr, allSpecialists, perAgent, techSection, witnesses, RejectedPatternsSection(repoMemory))
					if arb != nil && arb.Err != nil {
						return arb.Err
					}
					return nil
				})
				if arb != nil && rc != nil && !rc.RepoArbiterDemotions {
					arb.Demoted = nil
				}
				final.RepoArbiter = arb
				if arb != nil && arb.Err == nil {
					FinalizeRepoArbiter(arb, final)
				}
				// Feed the arbiter's outcome to the breaker so a synthesis-stage
				// failure counts toward the consecutive-failure abort too.
				_, aJust, aReason := breaker.recordStage(arb == nil || arb.Err != nil, time.Now())
				announceBreakerTrip(out, aJust, aReason)
				out <- Progress{Stage: "repo-arbiter", Detail: "done", Arbiter: arb}
			}

			// Vibe-coach runs as part of the pipeline against the
			// post-arbiter specialist set so the user sees a final summary
			// the moment they reach the approve phase. The TUI re-runs it
			// lazily only if the user changes the skip set during approve
			// (see reviewOverlay.enterSummary + RunVibeCoachForDraft).
			if tripped, just, reason := breaker.check(time.Now()); tripped {
				announceBreakerTrip(out, just, reason)
				out <- Progress{Stage: "vibe-coach", Detail: "skipped"}
			} else {
				out <- Progress{Stage: "vibe-coach", Detail: "start"}
				vibe := RunVibeCoachForDraft(ctx, runCfg, final, func(attempt int, err error) {
					out <- Progress{Stage: "vibe-coach", Detail: fmt.Sprintf("retry %d (%s)", attempt, retryReason(err))}
				})
				final.VibeCoach = vibe
				out <- Progress{Stage: "vibe-coach", Detail: "done", Vibe: vibe}
			}
		}

		// R4: surface partial-degradation so the user (and the posted body) can
		// see which stages failed after retries vs which were skipped by the
		// circuit breaker.
		if failedStages, skippedStages := final.DegradedStages(); len(failedStages)+len(skippedStages) > 0 {
			applog.Info("review run degraded",
				"ref", ref.String(),
				"failed", strings.Join(failedStages, ","),
				"skipped", strings.Join(skippedStages, ","))
			out <- Progress{Stage: "degraded", Detail: degradedDetail(failedStages, skippedStages)}
		}

		runUsage := usageAcc.snapshot(time.Since(runStart))
		applog.Info("review run usage",
			"ref", ref.String(),
			"calls", runUsage.Calls,
			"input_tokens", runUsage.InputTokens,
			"output_tokens", runUsage.OutputTokens,
			"cost_usd", runUsage.CostUSD,
			"cost_known", runUsage.CostKnown,
			"wall_ms", runUsage.WallClock.Milliseconds())
		out <- Progress{Stage: "done", Final: final, Usage: &runUsage}
	}()

	return out, nil
}

// ActiveSpecialists returns the specialists to run (and to surface in the
// overlay) for one review. It consults the registry: every KindCode spec runs,
// in registry order (built-ins first, then any user-defined code specialists),
// except specs marked RequiresTechBriefs (the tech specialist), which are
// dropped when techConfigured is false — the tech specialist exists only to
// enforce the repo's technology-expert briefs, so with none configured it has
// nothing to do and would otherwise cost a guaranteed-empty API call.
//
// The returned slice is freshly allocated, so callers may mutate it without
// disturbing the registry or AllSpecialists.
func ActiveSpecialists(techConfigured bool) []string {
	r := getRegistry()
	out := make([]string, 0, len(r.order))
	for _, name := range r.order {
		s := r.byName[name]
		if s.Kind != KindCode {
			continue
		}
		if s.RequiresTechBriefs && !techConfigured {
			continue
		}
		out = append(out, s.Name)
	}
	return out
}

// HasUsableTechExperts reports whether this repo has technology-expert briefs
// the tech specialist could enforce: the toggle is on, the briefs load, and at
// least one carries non-empty content. It mirrors composeTechSection's gating
// (minus the diff and the progress events) so callers — notably the TUI, which
// must decide before the run whether to surface the tech tab — agree with the
// runner on whether the tech specialist is active.
func HasUsableTechExperts(pr *gh.PR, rc *repoconfig.Config) bool {
	if pr == nil {
		return false
	}
	if rc != nil && !rc.TechAgents {
		return false
	}
	ta, err := techagents.Load(pr.Owner, pr.Repo)
	if err != nil || ta == nil || !ta.HasAny() {
		return false
	}
	for _, k := range ta.SortedTechs() {
		if ta.ContextFor(k) != "" {
			return true
		}
	}
	return false
}

// runSpecialistsPhase runs the active specialists (see ActiveSpecialists)
// either sequentially or in parallel depending on repoconfig /
// APPR_AI_SAL_PARALLEL_SPECIALISTS. Specialist order in the returned slice
// always matches the active-specialist order.
//
// perAgent maps specialist name → repo-agent brief; missing keys mean "no
// brief — use the no-repo intro for that specialist".
//
// prEvidence is per-PR static + history evidence (currently injected only
// for testing and docs). Empty when rc.IncludeRepoEvidence is false or the
// harvester returned nothing.
//
// techSection is the rendered technology-experts section (one labelled block
// per configured tech for this repo); shared across every specialist.
// intentSection is the Q8 rendered `## PR author intent` block; it is injected
// only into intent-aware specialists (testing) and is "" for the rest and for a
// no-op pre-pass, keeping every other specialist's prompt byte-identical.
func runSpecialistsPhase(ctx context.Context, runCfg *aiconfig.Config, rc *repoconfig.Config, worktree string, pr *gh.PR, diff string, perAgent map[string]string, prEvidence string, staticSection string, staticCleanFiles map[string]bool, langSection string, techSection string, intentSection string, breaker *runBreaker, out chan<- Progress) []SpecialistResult {
	runOne := func(name string) SpecialistResult {
		out <- Progress{Stage: "specialist", Detail: name + ":start"}
		notify := func(attempt int, err error) {
			out <- Progress{Stage: "specialist", Detail: fmt.Sprintf("%s:retry %d (%s)", name, attempt, retryReason(err))}
		}
		repoCtx := ""
		if perAgent != nil {
			repoCtx = perAgent[name]
		}
		ev := ""
		if specWantsEvidence(name) {
			ev = prEvidence
		}
		intent := ""
		if specWantsIntent(name) {
			intent = intentSection
		}
		// One member run against the given (possibly stage-routed / ensemble)
		// config, with the stage's retry + per-stage timeout budget. Budget is
		// read from the stage cfg, which is a clone of runCfg differing only in
		// Model, so retry/timeout knobs are identical to runCfg.
		runWith := func(stageCfg *aiconfig.Config) SpecialistResult {
			var r SpecialistResult
			_ = stageWithRetry(ctx, stageCfg, "specialist "+name, notify, func(sctx context.Context) error {
				stCtx, cancel := context.WithTimeout(applog.WithStage(sctx, "specialist "+name), perStageBudget(stageCfg))
				defer cancel()
				r = runReviewSpecialist(stCtx, stageCfg, name, worktree, pr, diff, repoCtx, ev, staticSection, staticCleanFiles, langSection, techSection, intent)
				if r.Err != nil {
					return r.Err
				}
				return nil
			})
			return r
		}
		// Q7: an ensemble stage runs once per configured model and unions the
		// findings; otherwise the stage runs once on its per-stage-routed model
		// (ForStage returns runCfg unchanged when no routing applies, so the
		// common path is byte-for-byte as before).
		var r SpecialistResult
		if models := runCfg.EnsembleModels(name); len(models) >= 2 {
			r = runEnsemble(name, models, runCfg, runWith)
		} else {
			r = runWith(runCfg.ForStage(name))
		}
		if r.RepairFired > 0 {
			out <- Progress{Stage: "specialist", Detail: fmt.Sprintf("%s:repair fired=%d succeeded=%d", name, r.RepairFired, r.RepairSucceeded)}
		}
		cp := r
		out <- Progress{Stage: "specialist", Detail: name + ":done", Result: &cp}
		return r
	}

	// Only run the specialists active for this repo. The tech specialist is
	// dropped when no technology-expert briefs are configured (techSection is
	// empty in exactly that case), so it neither costs an API call nor shows
	// an empty tab.
	active := ActiveSpecialists(strings.TrimSpace(techSection) != "")
	specialists := make([]SpecialistResult, len(active))
	parallel := rc != nil && rc.ParallelSpecialists
	if !parallel {
		for i, name := range active {
			// R4: before starting each specialist, honour the circuit breaker.
			// Once tripped, mark this specialist and every remaining one skipped
			// (not failed — they never ran) and stop, so a run that's already
			// wedged doesn't grind through the whole panel.
			if tripped, just, reason := breaker.check(time.Now()); tripped {
				announceBreakerTrip(out, just, reason)
				for j := i; j < len(active); j++ {
					specialists[j] = skippedSpecialistResult(active[j], "circuit breaker: "+reason)
					out <- Progress{Stage: "specialist", Detail: active[j] + ":skipped"}
				}
				return specialists
			}
			specialists[i] = runOne(name)
			if specialists[i].Err != nil {
				specialists[i].Outcome = OutcomeFailed
			}
			_, just, reason := breaker.recordStage(specialists[i].Err != nil, time.Now())
			announceBreakerTrip(out, just, reason)
		}
		return specialists
	}

	// Parallel dispatch: the breaker can't abort an in-flight call, so we run
	// the phase, then feed each result (in slice order, deterministic) to the
	// breaker so a downstream phase (PR agents / witness / arbiter / vibe) is
	// aborted if this phase tripped it.
	if tripped, just, reason := breaker.check(time.Now()); tripped {
		announceBreakerTrip(out, just, reason)
		for i, name := range active {
			specialists[i] = skippedSpecialistResult(name, "circuit breaker: "+reason)
			out <- Progress{Stage: "specialist", Detail: name + ":skipped"}
		}
		return specialists
	}
	var wg sync.WaitGroup
	for i, name := range active {
		wg.Add(1)
		i, name := i, name
		go func() {
			defer wg.Done()
			specialists[i] = runOne(name)
		}()
	}
	wg.Wait()
	for i := range specialists {
		if specialists[i].Err != nil {
			specialists[i].Outcome = OutcomeFailed
		}
		_, just, reason := breaker.recordStage(specialists[i].Err != nil, time.Now())
		announceBreakerTrip(out, just, reason)
	}
	return specialists
}

// runPRAgentsPhase runs AllPRAgents over the PR metadata. It mirrors
// runSpecialistsPhase (per-stage retry + budget) and emits Stage="pr-agent"
// progress so the overlay's "PR agents" group can track each one. The agents
// run sequentially by default, or concurrently among themselves when
// rc.ParallelPRAgents is set. Results are returned in AllPRAgents order;
// callers may re-sort with sortedPRAgentResults after parallel runs.
// intentSection is the Q8 rendered `## PR author intent` block; injected only
// into intent-aware PR agents (scope) and "" for the rest / a no-op pre-pass.
func runPRAgentsPhase(ctx context.Context, runCfg *aiconfig.Config, rc *repoconfig.Config, worktree string, pr *gh.PR, diff string, in PRAgentInput, intentSection string, breaker *runBreaker, out chan<- Progress) []SpecialistResult {
	runOne := func(name string) SpecialistResult {
		out <- Progress{Stage: "pr-agent", Detail: name + ":start"}
		notify := func(attempt int, err error) {
			out <- Progress{Stage: "pr-agent", Detail: fmt.Sprintf("%s:retry %d (%s)", name, attempt, retryReason(err))}
		}
		intent := ""
		if specWantsIntent(name) {
			intent = intentSection
		}
		runWith := func(stageCfg *aiconfig.Config) SpecialistResult {
			var r SpecialistResult
			_ = stageWithRetry(ctx, stageCfg, "pr-agent "+name, notify, func(sctx context.Context) error {
				stCtx, cancel := context.WithTimeout(applog.WithStage(sctx, "pr-agent "+name), perStageBudget(stageCfg))
				defer cancel()
				r = runPRAgent(stCtx, stageCfg, name, worktree, pr, diff, in, intent)
				if r.Err != nil {
					return r.Err
				}
				return nil
			})
			return r
		}
		// Q7: PR agents honour the same per-stage routing / ensemble mode as
		// the code specialists (see runSpecialistsPhase).
		var r SpecialistResult
		if models := runCfg.EnsembleModels(name); len(models) >= 2 {
			r = runEnsemble(name, models, runCfg, runWith)
		} else {
			r = runWith(runCfg.ForStage(name))
		}
		if r.RepairFired > 0 {
			out <- Progress{Stage: "pr-agent", Detail: fmt.Sprintf("%s:repair fired=%d succeeded=%d", name, r.RepairFired, r.RepairSucceeded)}
		}
		cp := r
		out <- Progress{Stage: "pr-agent", Detail: name + ":done", Result: &cp}
		return r
	}

	results := make([]SpecialistResult, len(AllPRAgents))
	parallel := rc != nil && rc.ParallelPRAgents
	if !parallel {
		for i, name := range AllPRAgents {
			if tripped, just, reason := breaker.check(time.Now()); tripped {
				announceBreakerTrip(out, just, reason)
				for j := i; j < len(AllPRAgents); j++ {
					results[j] = skippedSpecialistResult(AllPRAgents[j], "circuit breaker: "+reason)
					out <- Progress{Stage: "pr-agent", Detail: AllPRAgents[j] + ":skipped"}
				}
				return results
			}
			results[i] = runOne(name)
			if results[i].Err != nil {
				results[i].Outcome = OutcomeFailed
			}
			_, just, reason := breaker.recordStage(results[i].Err != nil, time.Now())
			announceBreakerTrip(out, just, reason)
		}
		return results
	}

	if tripped, just, reason := breaker.check(time.Now()); tripped {
		announceBreakerTrip(out, just, reason)
		for i, name := range AllPRAgents {
			results[i] = skippedSpecialistResult(name, "circuit breaker: "+reason)
			out <- Progress{Stage: "pr-agent", Detail: name + ":skipped"}
		}
		return results
	}
	var wg sync.WaitGroup
	for i, name := range AllPRAgents {
		wg.Add(1)
		i, name := i, name
		go func() {
			defer wg.Done()
			results[i] = runOne(name)
		}()
	}
	wg.Wait()
	for i := range results {
		if results[i].Err != nil {
			results[i].Outcome = OutcomeFailed
		}
		_, just, reason := breaker.recordStage(results[i].Err != nil, time.Now())
		announceBreakerTrip(out, just, reason)
	}
	return results
}

// announceBreakerTrip emits the one-time circuit-breaker Progress event when a
// recordStage/check call is the one that tripped the breaker.
func announceBreakerTrip(out chan<- Progress, justTripped bool, reason string) {
	if justTripped {
		out <- Progress{Stage: "circuit-breaker", Detail: "aborting remaining stages: " + reason}
	}
}

// runConventionWitnessPhase calls the convention-witness agent for any
// testing/docs/tech findings the specialists produced and returns the
// per-finding witness list. Returns nil when there is nothing to classify or
// when the witness call fails (failure is reported via the progress channel;
// it never blocks the arbiter).
//
// Tech findings are included because they are the class most prone to citing
// a repo convention that doesn't actually exist (e.g. "all resources must set
// `tags = var.common_tags`"). For them we additionally harvest sibling-file
// evidence (see BuildTechConventionEvidence) so the witness can judge whether
// the cited convention is a real repo norm or a hallucination.
func runConventionWitnessPhase(ctx context.Context, runCfg *aiconfig.Config, rc *repoconfig.Config, worktree string, pr *gh.PR, specialists []SpecialistResult, evidence string, out chan<- Progress) []conventionwitness.Witness {
	_ = rc
	if pr == nil {
		return nil
	}
	inputs, techFindings, formattingFindings := witnessInputsForSpecialists(specialists)
	if len(inputs) == 0 {
		return nil
	}
	// Append tech-specific sibling-sampling evidence so tech findings have
	// repo-grounding signal of their own; the shared prEvidence pack is
	// testing/docs-oriented and rarely covers IaC findings. Formatting
	// findings get their own identifier-style census evidence (Q6.5).
	evidence = appendTechConventionEvidence(evidence, worktree, techFindings)
	evidence = appendFormattingConventionEvidence(evidence, worktree, formattingFindings)
	out <- Progress{Stage: "convention-witness", Detail: fmt.Sprintf("start (%d findings)", len(inputs))}
	// Wrap in stageWithRetry so a transient parse/transport glitch on this
	// (previously non-retried) path re-runs like every other AI stage.
	notify := func(attempt int, err error) {
		out <- Progress{Stage: "convention-witness", Detail: fmt.Sprintf("retry %d (%s)", attempt, retryReason(err))}
	}
	var res conventionwitness.Result
	_ = stageWithRetry(ctx, runCfg, "convention-witness", notify, func(sctx context.Context) error {
		wctx, cancel := context.WithTimeout(applog.WithStage(sctx, "convention-witness"), perStageBudget(runCfg))
		defer cancel()
		res = conventionwitness.Run(wctx, runCfg, Complete, worktree,
			conventionwitness.PrWideRef{Repository: pr.Repository, Number: pr.Number, Title: pr.Title},
			inputs, evidence)
		return res.Err
	})
	if res.Err != nil {
		out <- Progress{Stage: "convention-witness", Detail: "warning: " + res.Err.Error()}
		return nil
	}
	out <- Progress{Stage: "convention-witness", Detail: fmt.Sprintf("done (%d witnesses)", len(res.Witnesses))}
	return res.Witnesses
}

// witnessInputsForSpecialists collects the convention-witness inputs from a
// specialist set: one FindingInput per witnessable finding, plus the tech and
// formatting findings that get their own harvested evidence blocks. Both
// inline (path+line) and PR-wide (path "", line 0) comment-bearing findings
// are included (Q6.5) — path-history evidence speaks to PR-wide testing/docs
// findings, and formatting findings get an identifier-style census. Extracted
// from runConventionWitnessPhase so the selection logic is unit-testable.
func witnessInputsForSpecialists(specialists []SpecialistResult) (inputs []conventionwitness.FindingInput, techFindings, formattingFindings []Finding) {
	for _, s := range specialists {
		if s.Err != nil || !specWitnessable(s.Specialist) {
			continue
		}
		for _, f := range s.Findings {
			if !findingIsInlinePostable(f) && strings.TrimSpace(f.Comment) == "" {
				continue
			}
			inputs = append(inputs, conventionwitness.FindingInput{
				Specialist: s.Specialist,
				Path:       f.Path,
				Line:       f.Line,
				Side:       f.Side,
				Severity:   string(f.Severity),
				Comment:    f.Comment,
			})
			if specWantsConventionEvidence(s.Specialist) {
				techFindings = append(techFindings, f)
			}
			if specWantsFormattingEvidence(s.Specialist) {
				formattingFindings = append(formattingFindings, f)
			}
		}
	}
	return inputs, techFindings, formattingFindings
}

// degradedDetail formats a one-line summary of degraded stages for the
// Stage="degraded" Progress event, e.g. "failed after retries: security;
// skipped: docs, tech".
func degradedDetail(failed, skipped []string) string {
	var parts []string
	if len(failed) > 0 {
		parts = append(parts, "failed after retries: "+strings.Join(failed, ", "))
	}
	if len(skipped) > 0 {
		parts = append(parts, "skipped: "+strings.Join(skipped, ", "))
	}
	return strings.Join(parts, "; ")
}

// retryReason produces a short, log-friendly label for the cause of a retry.
func retryReason(err error) string {
	if err == nil {
		return "unknown"
	}
	msg := err.Error()
	if len(msg) > 60 {
		msg = msg[:60] + "…"
	}
	return msg
}

const (
	// worktreeMarkerName marks a cache dir as one appr-ai-sal created, so the
	// GC only ever deletes its own worktrees.
	worktreeMarkerName = ".appr-ai-sal-worktree"
	// worktreeKeepDays purges any marked worktree older than this many days.
	worktreeKeepDays = 7
	// worktreeKeepPerPR keeps at most this many of the newest worktrees for
	// any single PR; older ones are purged even if under worktreeKeepDays.
	worktreeKeepPerPR = 2
)

// prepareWorktree returns a working tree with the PR's head checked out under
// the user's cache. It prefers the R7 shared bare-repo cache — maintain one
// bare mirror per owner/repo, fetch only the PR head delta, and `git worktree
// add` a per-run tree (reusing an existing tree when the head SHA is
// unchanged) — and falls open to the historical fresh-full-clone-per-run
// behavior if anything in the cache path fails, so a run never dies because
// of the cache. Either way the returned directory contains the PR head
// exactly as before and carries the GC marker.
//
// The two strategies are indirected through package-level vars so the
// fail-open wiring can be unit-tested without a live network.
var (
	cacheWorktreeStrategy = prepareWorktreeFromCache
	freshCloneStrategy    = prepareFreshCloneWorktree
)

func prepareWorktree(ctx context.Context, ref gh.Ref) (string, error) {
	base := cacheDir()
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	// Purge stale worktrees before adding a new one so the cache doesn't grow
	// without bound, then prune the bare repos' worktree bookkeeping so git
	// forgets the dirs the purge removed. Both are best-effort: GC failures
	// never block a review.
	purgeStaleWorktrees(base)
	pruneBareRepoWorktrees(ctx)

	if dir, err := cacheWorktreeStrategy(ctx, ref, base); err == nil {
		return dir, nil
	} else {
		applog.Warn("worktree cache unavailable; falling back to fresh clone",
			"ref", ref.String(), "err", err.Error())
	}
	return freshCloneStrategy(ctx, ref, base)
}

// prepareFreshCloneWorktree is the historical strategy: a fresh full clone of
// the PR head into a timestamp-suffixed directory. It is the fail-open path
// when the shared bare-repo cache can't be used.
func prepareFreshCloneWorktree(ctx context.Context, ref gh.Ref, base string) (string, error) {
	dir := filepath.Join(base, worktreeDirName(ref))
	if err := gh.CheckoutPR(ctx, ref, dir); err != nil {
		return "", err
	}
	// Record the checked-out head SHA in the marker when we can resolve it so
	// a later cache-path run could reuse this tree; empty is fine (the marker
	// only needs to exist for the GC to recognise the dir as ours).
	sha, _ := gh.WorktreeHeadSHA(ctx, dir)
	writeWorktreeMarker(dir, sha)
	return dir, nil
}

// purgeStaleWorktrees best-effort deletes old review worktrees under base:
// any dir bearing the marker file whose marker mtime is older than
// worktreeKeepDays, plus — per PR — all but the newest worktreeKeepPerPR
// dirs. It only ever removes dirs carrying the appr-ai-sal marker, so a
// user-created directory sharing the cache is never touched. Fail-open: any
// error on a single dir is ignored so GC never blocks a run.
func purgeStaleWorktrees(base string) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	type wt struct {
		name  string
		group string
		unix  int64
	}
	var owned []wt
	cutoff := time.Now().Add(-worktreeKeepDays * 24 * time.Hour)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, e.Name())
		info, err := os.Stat(filepath.Join(dir, worktreeMarkerName))
		if err != nil {
			continue // no marker → not ours; leave it alone
		}
		if info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(dir)
			continue
		}
		group, unix := splitWorktreeName(e.Name())
		owned = append(owned, wt{name: e.Name(), group: group, unix: unix})
	}
	byGroup := map[string][]wt{}
	for _, w := range owned {
		byGroup[w.group] = append(byGroup[w.group], w)
	}
	for _, ws := range byGroup {
		if len(ws) <= worktreeKeepPerPR {
			continue
		}
		sort.Slice(ws, func(i, j int) bool { return ws[i].unix > ws[j].unix }) // newest first
		for _, w := range ws[worktreeKeepPerPR:] {
			_ = os.RemoveAll(filepath.Join(base, w.name))
		}
	}
}

// splitWorktreeName splits "<owner>-<repo>-<number>-<unix>" into the PR group
// prefix ("<owner>-<repo>-<number>") and the trailing unix timestamp. Owner
// and repo may contain hyphens, so we split only on the final "-<digits>".
func splitWorktreeName(name string) (group string, unix int64) {
	i := strings.LastIndex(name, "-")
	if i < 0 || i == len(name)-1 {
		return name, 0
	}
	n, err := strconv.ParseInt(name[i+1:], 10, 64)
	if err != nil {
		return name, 0
	}
	return name[:i], n
}

func cacheDir() string {
	return appdirs.CacheSubdir(appdirs.WorktreesSubdir)
}

// RepoProfilesDir returns the directory for cached repository context bundles
// (sibling of the worktrees folder when using the default XDG layout).
func RepoProfilesDir() string {
	return appdirs.CacheSubdir("repo-profiles")
}

// composeLangBriefSection picks the dominant-language briefs for the PR
// and renders them as a single user-prompt section. Emits a Progress
// event reporting which briefs were loaded (from bundle vs cache) and
// which languages had no brief available.
//
// The returned string is empty when the PR touches no recognised
// languages OR no recognised language has a brief — both are safe
// no-ops for buildReviewUserPrompt's caller.
func composeLangBriefSection(diff string, out chan<- Progress) string {
	touches := map[string]int{}
	for _, f := range ParseDiff(diff) {
		if f.Path == "" {
			continue
		}
		touches[f.Path] += f.Additions + f.Deletions
	}
	summary := langagents.SummariseForDiff(touches)
	switch {
	case len(summary.Briefs) == 0 && len(summary.Missing) == 0:
		// No recognised languages in the diff (e.g. all README / YAML
		// touches without a brief in scope) — keep quiet.
		return ""
	case len(summary.Briefs) == 0:
		out <- Progress{Stage: "lang-agents", Detail: fmt.Sprintf("none injected; missing: %s", joinLangs(summary.Missing))}
		return ""
	}
	names := make([]string, 0, len(summary.Briefs))
	for _, b := range summary.Briefs {
		names = append(names, langagents.LabelFor(b.Language))
	}
	detail := "injected " + strings.Join(names, "+")
	if len(summary.Missing) > 0 {
		detail += "; missing: " + joinLangs(summary.Missing)
	}
	out <- Progress{Stage: "lang-agents", Detail: detail}
	return langagents.FormatBriefsSection(summary.Briefs)
}

// composeTechSection loads the per-repo technology experts and renders
// them as a single user-prompt section. Emits a Progress event reporting
// what was injected (or "none" / "disabled" / a load warning) so the
// overlay's Context-injection group can resolve.
//
// The returned string is empty when the toggle is off, no PR is in
// scope, no techs are configured, or every configured brief is empty.
// All four cases are safe no-ops for buildReviewUserPrompt's caller.
func composeTechSection(pr *gh.PR, rc *repoconfig.Config, out chan<- Progress) string {
	if pr == nil {
		return ""
	}
	if rc != nil && !rc.TechAgents {
		out <- Progress{Stage: "tech-agents", Detail: "disabled"}
		return ""
	}
	ta, err := techagents.Load(pr.Owner, pr.Repo)
	if err != nil {
		out <- Progress{Stage: "tech-agents", Detail: "warning: " + err.Error()}
		return ""
	}
	if ta == nil || !ta.HasAny() {
		out <- Progress{Stage: "tech-agents", Detail: "none"}
		return ""
	}
	keys := ta.SortedTechs()
	var b strings.Builder
	labels := make([]string, 0, len(keys))
	bodies := make([]string, 0, len(keys))
	for _, k := range keys {
		body := ta.ContextFor(k)
		if body == "" {
			continue
		}
		label := ta.LabelFor(k)
		bodies = append(bodies, fmt.Sprintf("## Technology context: %s\n\n%s", label, body))
		labels = append(labels, label)
	}
	if len(labels) == 0 {
		out <- Progress{Stage: "tech-agents", Detail: "none"}
		return ""
	}
	// Prepend an authoritative-framing preamble that rides with the per-tech
	// blocks. Mirrors langagents/brief.go:124-126: the briefs are not just
	// "context" — specialists must not file findings that contradict them.
	b.WriteString("## Technology conventions\n\n")
	b.WriteString("The brief(s) below describe how each technology is conventionally used in this repo. ")
	b.WriteString("Treat them as authoritative for tech-specific conventions: do not file findings that contradict the conventions stated here, and use them to calibrate the severity of borderline findings. ")
	b.WriteString("The unified diff remains the authority for what changed in this PR.\n\n")
	for i, blk := range bodies {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(blk)
		b.WriteString("\n\n")
	}
	out <- Progress{Stage: "tech-agents", Detail: "injected " + strings.Join(labels, "+")}
	return strings.TrimRight(b.String(), "\n")
}

func joinLangs(langs []langagents.Language) string {
	if len(langs) == 0 {
		return "none"
	}
	labels := make([]string, len(langs))
	for i, l := range langs {
		labels[i] = langagents.LabelFor(l)
	}
	return strings.Join(labels, ", ")
}

// ErrNoPR is returned if the user tries to operate on a Draft that hasn't
// been initialized.
var ErrNoPR = errors.New("no PR loaded")
