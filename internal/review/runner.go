package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/review/conventionwitness"
	"github.com/madicen/appr-ai-sal/internal/review/langagents"
	"github.com/madicen/appr-ai-sal/internal/review/repoagents"
	"github.com/madicen/appr-ai-sal/internal/review/techagents"
)

// Progress messages are emitted on the channel returned by Run so the TUI can
// stream updates to the user as specialists complete.
type Progress struct {
	Stage   string // "checkout", "diff", "repo-context", "repo-agents", "tech-agents", "lang-agents", "repo-evidence", "context-summary", "convention-witness", "specialist", "pr-agent", "vibe-coach", "repo-arbiter", "done"; specialist/pr-agent Detail is "<name>:start"/"<name>:done"/"<name>:retry N (...)" (pr-agent also emits "warning: ..." for fetch failures); vibe-coach Detail is "start"/"done"/"retry N (...)" or "skipped" when downstream agents are bypassed
	Detail  string // free-form detail about the stage
	Err     error  // non-nil if this stage hit an error worth surfacing
	Result  *SpecialistResult
	Vibe    *VibeCoachResult
	Arbiter *RepoArbiterResult // populated on Stage="repo-arbiter" Detail="done"
	Final   *Draft             // populated only on the final "done" message
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

		pr, err := gh.GetPR(ctx, ref)
		if err != nil {
			out <- Progress{Stage: "fetch-pr", Err: fmt.Errorf("fetch PR: %w", err)}
			return
		}

		worktree, err := prepareWorktree(ctx, ref)
		if err != nil {
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
				if checks, cerr := gh.GetChecks(ctx, ref); cerr == nil {
					in.Checks = checks
				} else {
					out <- Progress{Stage: "pr-agent", Detail: "warning: checks fetch: " + cerr.Error()}
				}
				if threads, terr := gh.GetReviewThreads(ctx, ref); terr == nil {
					in.Threads = threads
				} else {
					out <- Progress{Stage: "pr-agent", Detail: "warning: review-threads fetch: " + terr.Error()}
				}
				if disc, derr := gh.GetDiscussion(ctx, ref); derr == nil {
					in.Discussion = disc
				} else {
					out <- Progress{Stage: "pr-agent", Detail: "warning: discussion fetch: " + derr.Error()}
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
			s, err := SummarizeContextVersusChange(ctx, runCfg, pr, diff, repoCtx, worktree)
			cvCh <- cvOutcome{text: s, err: err}
		}()

		// Per-PR static + history evidence for the testing/docs specialists.
		// Built once and reused across both specialists. Empty when
		// rc.IncludeRepoEvidence is false or when the harvester finds nothing.
		prEvidence := BuildPRReviewEvidence(ctx, rc, pr, diff, worktree)
		if strings.TrimSpace(prEvidence) != "" {
			out <- Progress{Stage: "repo-evidence", Detail: fmt.Sprintf("%d bytes", len(prEvidence))}
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
		var prAgents []SpecialistResult
		prParallel := prAgentsEnabled && rc != nil && rc.ParallelPRAgents
		var prWG sync.WaitGroup
		if prParallel {
			prWG.Add(1)
			go func() {
				defer prWG.Done()
				prData := <-prDataCh
				prAgents = sortedPRAgentResults(runPRAgentsPhase(ctx, runCfg, rc, worktree, pr, diff, prData, out))
			}()
		}

		// Specialists: sequential by default (repo-context.json parallel_specialists),
		// or parallel when configured / env override — see runSpecialistsPhase.
		specialists := runSpecialistsPhase(ctx, runCfg, rc, worktree, pr, diff, perAgent, prEvidence, langSection, techSection, out)

		if prParallel {
			prWG.Wait()
		} else if prAgentsEnabled {
			prData := <-prDataCh
			prAgents = sortedPRAgentResults(runPRAgentsPhase(ctx, runCfg, rc, worktree, pr, diff, prData, out))
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
		}

		if skipDownstream {
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
				// them, not just the code specialists'.
				arb := RunRepoArbiter(ctx, runCfg, worktree, pr, allSpecialists, perAgent, techSection, witnesses)
				if arb != nil && rc != nil && !rc.RepoArbiterDemotions {
					arb.Demoted = nil
				}
				final.RepoArbiter = arb
				if arb != nil && arb.Err == nil {
					FinalizeRepoArbiter(arb, final)
				}
				out <- Progress{Stage: "repo-arbiter", Detail: "done", Arbiter: arb}
			}

			// Vibe-coach runs as part of the pipeline against the
			// post-arbiter specialist set so the user sees a final summary
			// the moment they reach the approve phase. The TUI re-runs it
			// lazily only if the user changes the skip set during approve
			// (see reviewOverlay.enterSummary + RunVibeCoachForDraft).
			out <- Progress{Stage: "vibe-coach", Detail: "start"}
			vibe := RunVibeCoachForDraft(ctx, runCfg, final, func(attempt int, err error) {
				out <- Progress{Stage: "vibe-coach", Detail: fmt.Sprintf("retry %d (%s)", attempt, retryReason(err))}
			})
			final.VibeCoach = vibe
			out <- Progress{Stage: "vibe-coach", Detail: "done", Vibe: vibe}
		}

		out <- Progress{Stage: "done", Final: final}
	}()

	return out, nil
}

// ActiveSpecialists returns the specialists to run (and to surface in the
// overlay) for one review. The tech specialist exists only to enforce the
// repo's technology-expert briefs, so when none are configured it has nothing
// to do, ever — exclude it entirely rather than run a guaranteed-empty pass or
// show a permanently empty tab. Every other specialist is a universal baseline
// reviewer and is always included.
func ActiveSpecialists(techConfigured bool) []string {
	if techConfigured {
		return append([]string(nil), AllSpecialists...)
	}
	out := make([]string, 0, len(AllSpecialists))
	for _, s := range AllSpecialists {
		if s == SpecTech {
			continue
		}
		out = append(out, s)
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
func runSpecialistsPhase(ctx context.Context, runCfg *aiconfig.Config, rc *repoconfig.Config, worktree string, pr *gh.PR, diff string, perAgent map[string]string, prEvidence string, langSection string, techSection string, out chan<- Progress) []SpecialistResult {
	runOne := func(name string) SpecialistResult {
		out <- Progress{Stage: "specialist", Detail: name + ":start"}
		notify := func(attempt int, err error) {
			out <- Progress{Stage: "specialist", Detail: fmt.Sprintf("%s:retry %d (%s)", name, attempt, retryReason(err))}
		}
		var r SpecialistResult
		repoCtx := ""
		if perAgent != nil {
			repoCtx = perAgent[name]
		}
		ev := ""
		if name == SpecTesting || name == SpecDocs {
			ev = prEvidence
		}
		_ = stageWithRetry(ctx, runCfg, "specialist "+name, notify, func(sctx context.Context) error {
			stCtx, cancel := context.WithTimeout(sctx, perStageBudget(runCfg))
			defer cancel()
			r = runReviewSpecialist(stCtx, runCfg, name, worktree, pr, diff, repoCtx, ev, langSection, techSection)
			if r.Err != nil {
				return r.Err
			}
			return nil
		})
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
			specialists[i] = runOne(name)
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
	return specialists
}

// runPRAgentsPhase runs AllPRAgents over the PR metadata. It mirrors
// runSpecialistsPhase (per-stage retry + budget) and emits Stage="pr-agent"
// progress so the overlay's "PR agents" group can track each one. The agents
// run sequentially by default, or concurrently among themselves when
// rc.ParallelPRAgents is set. Results are returned in AllPRAgents order;
// callers may re-sort with sortedPRAgentResults after parallel runs.
func runPRAgentsPhase(ctx context.Context, runCfg *aiconfig.Config, rc *repoconfig.Config, worktree string, pr *gh.PR, diff string, in PRAgentInput, out chan<- Progress) []SpecialistResult {
	runOne := func(name string) SpecialistResult {
		out <- Progress{Stage: "pr-agent", Detail: name + ":start"}
		notify := func(attempt int, err error) {
			out <- Progress{Stage: "pr-agent", Detail: fmt.Sprintf("%s:retry %d (%s)", name, attempt, retryReason(err))}
		}
		var r SpecialistResult
		_ = stageWithRetry(ctx, runCfg, "pr-agent "+name, notify, func(sctx context.Context) error {
			stCtx, cancel := context.WithTimeout(sctx, perStageBudget(runCfg))
			defer cancel()
			r = runPRAgent(stCtx, runCfg, name, worktree, pr, diff, in)
			if r.Err != nil {
				return r.Err
			}
			return nil
		})
		cp := r
		out <- Progress{Stage: "pr-agent", Detail: name + ":done", Result: &cp}
		return r
	}

	results := make([]SpecialistResult, len(AllPRAgents))
	parallel := rc != nil && rc.ParallelPRAgents
	if !parallel {
		for i, name := range AllPRAgents {
			results[i] = runOne(name)
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
	return results
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
	var inputs []conventionwitness.FindingInput
	var techFindings []Finding
	for _, s := range specialists {
		if s.Err != nil {
			continue
		}
		if s.Specialist != SpecTesting && s.Specialist != SpecDocs && s.Specialist != SpecTech {
			continue
		}
		for _, f := range s.Findings {
			if strings.TrimSpace(f.Path) == "" || f.Line <= 0 {
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
			if s.Specialist == SpecTech {
				techFindings = append(techFindings, f)
			}
		}
	}
	if len(inputs) == 0 {
		return nil
	}
	// Append tech-specific sibling-sampling evidence so tech findings have
	// repo-grounding signal of their own; the shared prEvidence pack is
	// testing/docs-oriented and rarely covers IaC findings.
	evidence = appendTechConventionEvidence(evidence, worktree, techFindings)
	out <- Progress{Stage: "convention-witness", Detail: fmt.Sprintf("start (%d findings)", len(inputs))}
	wctx, cancel := context.WithTimeout(ctx, perStageBudget(runCfg))
	defer cancel()
	res := conventionwitness.Run(wctx, runCfg, conventionwitness.CompleteFunc(Complete), worktree,
		conventionwitness.PrWideRef{Repository: pr.Repository, Number: pr.Number, Title: pr.Title},
		inputs, evidence)
	if res.Err != nil {
		out <- Progress{Stage: "convention-witness", Detail: "warning: " + res.Err.Error()}
		return nil
	}
	out <- Progress{Stage: "convention-witness", Detail: fmt.Sprintf("done (%d witnesses)", len(res.Witnesses))}
	return res.Witnesses
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

// prepareWorktree clones the PR's head into a fresh directory under the
// user's cache. The directory is named so that repeated runs against the
// same PR get distinct worktrees (timestamp-suffixed) — keeps things simple
// and avoids "directory not empty" failures on retries.
func prepareWorktree(ctx context.Context, ref gh.Ref) (string, error) {
	base := cacheDir()
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	dir := filepath.Join(base, fmt.Sprintf("%s-%s-%d-%d",
		ref.Owner, ref.Repo, ref.Number, time.Now().Unix()))
	if err := gh.CheckoutPR(ctx, ref, dir); err != nil {
		return "", err
	}
	// Drop a marker so we know the dir is ours when we eventually purge old
	// worktrees in a future version.
	_ = os.WriteFile(filepath.Join(dir, ".appr-ai-sal-worktree"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
	return dir, nil
}

func cacheDir() string {
	if v := os.Getenv("APPR_AI_SAL_CACHE_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return filepath.Join(v, "appr-ai-sal", "worktrees")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".cache", "appr-ai-sal", "worktrees")
	}
	return filepath.Join(home, ".cache", "appr-ai-sal", "worktrees")
}

// RepoProfilesDir returns the directory for cached repository context bundles
// (sibling of the worktrees folder when using the default XDG layout).
func RepoProfilesDir() string {
	return filepath.Clean(filepath.Join(cacheDir(), "..", "repo-profiles"))
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
