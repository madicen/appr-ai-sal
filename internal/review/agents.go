package review

import (
	"context"
	"fmt"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/applog"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/llmjson"
)

const nonClaudeToolingHint = "You do not have access to repository tools or the local filesystem. Base your answer on the PR metadata, the unified diff, and the \"Repository context\" section in the user message below. Treat that section as the authoritative summary of repo conventions for this review (the diff remains the authority for what changed).\n\n"

const nonClaudeToolingHintNoRepo = "You do not have access to repository tools or the local filesystem. Base your answer on the PR metadata and the unified diff in the user message below (the diff is the authority for what changed).\n\n"

// claudeReviewIntro is used when a repository-context section is included.
const claudeReviewIntro = "You are reviewing a pull request. Read code from the working directory as needed for context. The PR's head branch is checked out there.\n\nTreat the \"Repository context\" section in this message as the baseline summary of repo conventions shared with every backend; you may still read files for extra depth.\n\n"

const diffOnlyReviewIntro = "You are reviewing a pull request. You only have the unified diff, PR metadata, and the Repository context section in this message — do not assume you can open files on disk.\n\n"

// claudeReviewIntroNoRepo is used for specialists/vibe when repo context is not inlined (repo experts own that).
const claudeReviewIntroNoRepo = "You are reviewing a pull request. Read code from the working directory as needed for context. The PR's head branch is checked out there.\n\nRepo-wide convention notes are not inlined in this message; focus on the diff and any files you read under the worktree.\n\n"

const diffOnlyReviewIntroNoRepo = "You are reviewing a pull request. You only have the unified diff and PR metadata in this message — do not assume you can open files on disk.\n\n"

func rewriteReviewIntroForDiffOnly(userPrompt string) string {
	s := strings.Replace(userPrompt, claudeReviewIntro, diffOnlyReviewIntro, 1)
	if s != userPrompt {
		return s
	}
	s = strings.Replace(userPrompt, claudeReviewIntroNoRepo, diffOnlyReviewIntroNoRepo, 1)
	return s
}

// augmentPromptsForProvider prepends guidance for backends that cannot use
// repository tools. It branches on the provider's capability (repoTools), not
// on the provider enum, so a new tool-capable backend needs no change here.
// hasRepoContext should be true when the user message includes a repository-context section (specialists omit it today).
func augmentPromptsForProvider(repoTools bool, systemPrompt, userPrompt string, hasRepoContext bool) (string, string) {
	if repoTools {
		return systemPrompt, userPrompt
	}
	userPrompt = rewriteReviewIntroForDiffOnly(userPrompt)
	if hasRepoContext {
		return nonClaudeToolingHint + systemPrompt, userPrompt
	}
	return nonClaudeToolingHintNoRepo + systemPrompt, userPrompt
}

// runReviewSpecialist runs a code-reviewing specialist (formatting, design,
// testing, docs, security) over the PR worktree and returns its findings.
//
// repoContext is the per-specialist repo-agent brief (may be empty).
// evidence is an optional per-PR static + history evidence block (currently
// only built for testing and docs); when non-empty it is appended after the
// brief so the model has concrete neighbours/aggregates to calibrate against.
// langSection is the rendered language-conventions section (one or two
// langagents briefs joined by FormatBriefsSection), shared across every
// specialist — repo-independent and computed once per review.
// techSection is the rendered technology-experts section (one labelled
// block per configured tech for this repo); shared across every specialist
// and computed once per review.
// staticSection is the pre-rendered static-analysis pre-pass block (Q5): what
// the deterministic tools already flag, injected into EVERY code specialist so
// they don't re-report tool findings. staticCleanFiles is the set of files a
// formatter passed clean, driving the Q5.d "linter is silent" downgrade.
func runReviewSpecialist(ctx context.Context, cfg *aiconfig.Config, name string, worktree string, pr *gh.PR, diff string, repoContext string, evidence string, staticSection string, staticCleanFiles map[string]bool, langSection string, techSection string) SpecialistResult {
	res := SpecialistResult{Specialist: name, Findings: []Finding{}}

	systemPrompt, err := SpecialistPrompt(name)
	if err != nil {
		res.Err = err
		return res
	}

	userPrompt := buildReviewUserPrompt(pr, diff, cfg.ReviewStrictness, repoContext, evidence, staticSection, langSection, techSection)
	hasContext := strings.TrimSpace(repoContext) != "" || strings.TrimSpace(evidence) != "" || strings.TrimSpace(langSection) != "" || strings.TrimSpace(techSection) != ""
	systemPrompt, userPrompt = augmentPromptsForProvider(ai.CapabilitiesFor(cfg).RepoTools, systemPrompt, userPrompt, hasContext)

	// R5: constrain the output shape on schema-capable providers with the
	// registry-derived code-specialist schema (user-defined code specialists
	// share it). Schema-less JSON providers ignore it and use json_object.
	out, err := completeJSONWithSchema(ctx, cfg, systemPrompt, userPrompt, worktree, schemaForAgent(name))
	if err != nil {
		res.Err = err
		return res
	}

	parsed, err := parseSpecialistJSON(out)
	if err != nil {
		res.Err = fmt.Errorf("parse specialist output: %w (raw: %s)", err, truncate(out, 500))
		return res
	}
	res.Summary = parsed.Summary
	if parsed.Findings != nil {
		// Apply the strictness severity floor before anyone else sees the
		// findings (vibe-coach, repo experts, the approval card, the rendered
		// body). Lenient surfaces only error; balanced surfaces error+warning;
		// strict surfaces all. The model is asked to obey the same floor in the
		// strictness block, but we filter unconditionally to make the contract
		// hard.
		floor := MinSeverityForStrictness(cfg.ReviewStrictness)
		res.Findings = FilterFindingsBySeverity(parsed.Findings, floor)
		// Capture the model's raw inline-suggestion count before any gate
		// runs so the evals harness can measure suggestion survival. No-op
		// for the normal path (the field is unread there).
		res.RawSuggestionAttempts = countInlineSuggestionAttempts(res.Findings)
		// Parse the diff once and share the result across every gate
		// below — diffs can be megabytes for large PRs and re-parsing
		// per validator adds up.
		parsedFiles := ParseDiff(diff)
		// Defensive: clear suggestions whose post-image substitution would
		// obviously break the file (anchor mismatch that would duplicate a
		// nearby line, etc.). We keep the comment so the human still sees
		// the issue; only the one-click fix is dropped.
		res.Findings = validateAndPruneSuggestions(res.Findings, parsedFiles)
		// Strip suggestions whose anchor line is the wrong kind for the
		// kind of change the comment proposes (e.g. comment claims a
		// declaration needs renaming, but the anchor is a comment-only
		// line). See anchor_kind.go.
		res.Findings = validateAnchorKind(res.Findings, parsedFiles)
		// Anchor-excerpt cross-check: when the model's quoted excerpt
		// doesn't match the line at path:line we try to re-anchor on a
		// unique excerpt match elsewhere in the same hunk (recording
		// AnchorRelocatedFrom for the TUI to surface), and fall back to
		// stripping the suggestion when no/ambiguous match. See
		// anchor_excerpt.go for the full posture.
		res.Findings = validateAnchorExcerpt(res.Findings, parsedFiles)
		// Demote bare "X lacks a comment / lacks tests" findings to info
		// when no proposed wording or suggestion is present. Docs/testing
		// only — see actionability.go for the rationale.
		res.Findings = validateActionability(name, res.Findings)
		// Demote findings that prescribe a naming convention which is
		// wrong for the file's language (e.g. "should be snake_case" on
		// a .go file). See convention_gate.go.
		res.Findings = validateNamingConvention(res.Findings)
		// Strip suggestions and demote findings that tell the author to add
		// an argument the enclosing Terraform resource type does not accept
		// (e.g. `tags = var.common_tags` on `aws_s3_bucket_policy`), which
		// would fail terraform validate. See iac_schema_gate.go.
		res.Findings = validateTechResourceArguments(res.Findings, parsedFiles, worktree)
		// Q5.d: "linter is silent" false-positive filter. When the static
		// pre-pass ran a formatter clean over a file, demote mechanical
		// formatting findings there (formatting specialist only). Runs before
		// synthesis/repair so we never build a one-click fix for a nit a
		// formatter already deemed unnecessary. See static_silence.go.
		res.Findings = downgradeFormatterSilencedFindings(name, res.Findings, staticCleanFiles)
		// Last chance: synthesize a one-click suggestion from the comment
		// for any inline finding the model left suggestion-less but whose
		// comment unambiguously names the corrected token. Runs after the
		// strip gates so it only fills genuinely empty suggestions and never
		// resurrects a stripped one; the synthesized line is re-validated by
		// the same safety checks. See suggestion_synthesize.go.
		res.Findings = synthesizeSuggestions(res.Findings, parsedFiles)
		// Batched AI fallback: for inline findings still missing a usable
		// one-click fix, a focused second model call picks the anchor line
		// and writes the replacement, re-validated by the same safety gates.
		// Fail-open and only fires when there is at least one candidate. See
		// suggestion_repair.go.
		res.Findings, res.RepairFired, res.RepairSucceeded = repairMissingSuggestions(ctx, cfg, worktree, name, res.Findings, parsedFiles)
		// Re-apply the floor: a finding demoted to info above must obey
		// the same strictness gate the model originally got.
		res.Findings = FilterFindingsBySeverity(res.Findings, floor)
	}
	return res
}

// RunVibeCoachForDraft runs vibe-coach against the draft's *current*
// post-arbiter, post-user-skip specialist set. It is invoked both as
// part of the streaming pipeline (initial generation, no user skips
// yet) and lazily by the TUI at the approve->summary transition when
// the user has changed the skip set since the last run, so the LLM
// only ever sees the findings that are actually going to ship.
//
// notify is invoked once per retry attempt with the attempt number
// and the underlying error; pass nil when retries don't need to be
// surfaced (e.g. the TUI's lazy re-run).
//
// Wraps the call with stageWithRetry + perStageBudget so retry/timeout
// behavior matches an inline call. Safe to call multiple times on the
// same draft — each call overwrites d.VibeCoach.
//
// Returns a non-nil result even on error (Err is set); callers can
// surface the error in the UI without nil-checking.
func RunVibeCoachForDraft(ctx context.Context, cfg *aiconfig.Config, d *Draft, notify func(attempt int, err error)) *VibeCoachResult {
	if d == nil || cfg == nil {
		return &VibeCoachResult{Err: fmt.Errorf("vibe-coach: nil draft or config"), Prompts: []AuthorPrompt{}}
	}
	if notify == nil {
		notify = func(int, error) {}
	}
	vibeInput := SpecialistsForVibeCoach(d, d.Specialists)
	var res *VibeCoachResult
	_ = stageWithRetry(ctx, cfg, "vibe-coach", notify, func(sctx context.Context) error {
		stCtx, cancel := context.WithTimeout(applog.WithStage(sctx, "vibe-coach"), perStageBudget(cfg))
		defer cancel()
		res = runVibeCoach(stCtx, cfg, d.Worktree, d.PR, vibeInput, "")
		if res != nil && res.Err != nil {
			return res.Err
		}
		return nil
	})
	if res == nil {
		res = &VibeCoachResult{Err: fmt.Errorf("vibe-coach: no result"), Prompts: []AuthorPrompt{}}
	}
	return res
}

// runVibeCoach runs the vibe-coach specialist over the collected findings of
// the other specialists, producing a small set of high-leverage prompts the
// PR author can paste back into their AI assistant.
func runVibeCoach(ctx context.Context, cfg *aiconfig.Config, worktree string, pr *gh.PR, specialists []SpecialistResult, repoContext string) *VibeCoachResult {
	res := &VibeCoachResult{Prompts: []AuthorPrompt{}}

	systemPrompt, err := SpecialistPrompt(SpecVibeCoach)
	if err != nil {
		res.Err = err
		return res
	}
	systemPrompt += vibeCoachSystemAddendum

	userPrompt := buildVibeCoachUserPrompt(pr, specialists, cfg.ReviewStrictness, repoContext)
	systemPrompt, userPrompt = augmentPromptsForProvider(ai.CapabilitiesFor(cfg).RepoTools, systemPrompt, userPrompt, strings.TrimSpace(repoContext) != "")

	// R5: hand the vibe-coach's registry-derived schema to schema-capable
	// providers.
	out, err := completeJSONWithSchema(ctx, cfg, systemPrompt, userPrompt, worktree, vibeCoachSchema())
	if err != nil {
		res.Err = err
		return res
	}

	parsed, err := parseVibeCoachJSON(out)
	if err != nil {
		res.Err = fmt.Errorf("parse vibe-coach output: %w (raw: %s)", err, truncate(out, 500))
		return res
	}
	res.Verdict = NormalizeVibeVerdict(parsed.Verdict)
	res.Summary = parsed.Summary
	if parsed.Prompts != nil {
		res.Prompts = parsed.Prompts
	}
	if res.Verdict == VibeVerdictRequestChanges && len(res.Prompts) == 0 {
		res.RequestChangesWithoutPrompts = true
	}
	return res
}

func buildReviewUserPrompt(pr *gh.PR, diff string, strict aiconfig.ReviewStrictness, repoContext string, evidence string, staticSection string, langSection string, techSection string) string {
	var b strings.Builder
	hasContext := strings.TrimSpace(repoContext) != "" || strings.TrimSpace(evidence) != "" || strings.TrimSpace(langSection) != "" || strings.TrimSpace(techSection) != ""
	if hasContext {
		b.WriteString(claudeReviewIntro)
	} else {
		b.WriteString(claudeReviewIntroNoRepo)
	}
	b.WriteString("PR: " + pr.Repository + "#")
	fmt.Fprintf(&b, "%d", pr.Number)
	b.WriteString("\nTitle: " + pr.Title + "\n")
	b.WriteString("Author: " + pr.Author + "\n")
	b.WriteString("Base: " + pr.BaseRef + " → Head: " + pr.HeadRef + "\n\n")
	if strings.TrimSpace(pr.Body) != "" {
		b.WriteString("PR description:\n")
		b.WriteString(pr.Body)
		b.WriteString("\n\n")
	}
	b.WriteString(strictnessBlockForSpecialists(strict))
	// Section ordering is intentional. Language conventions are universal
	// facts (no repo, no tech). Technology experts are cross-repo
	// conventions for one stack inside this repo. The repo-agent brief is
	// per-(repo, specialist) and may override either; repo evidence is
	// per-PR and is the most local of all. The downstream prompt reads
	// from broadest to narrowest scope.
	if s := strings.TrimSpace(langSection); s != "" {
		b.WriteString(s)
		b.WriteString("\n\n")
	}
	if s := strings.TrimSpace(techSection); s != "" {
		b.WriteString(s)
		b.WriteString("\n\n")
	}
	b.WriteString(FormatRepoContextSection(repoContext))
	b.WriteString(FormatPRReviewEvidenceSection(evidence))
	// Static-analysis pre-pass (Q5): what the deterministic tools already
	// flag, and which files a formatter passed clean. Pre-rendered (heading +
	// body) by the runner via staticpass; empty when nothing ran.
	if s := strings.TrimSpace(staticSection); s != "" {
		b.WriteString(s)
		b.WriteString("\n\n")
	}
	b.WriteString("Unified diff (line numbers in `+` hunks are the lines you cite in findings, with side=\"RIGHT\"):\n\n")
	b.WriteString("```diff\n")
	b.WriteString(diff)
	b.WriteString("\n```\n\n")
	if hasContext {
		b.WriteString(briefsReReadReminder(repoContext, evidence, langSection, techSection))
	}
	b.WriteString(reviewOutputContract)
	return b.String()
}

// briefsReReadReminder is a short stanza appended after the diff and before
// the JSON output contract. The diff dominates attention when the model
// emits findings; this reminder names the brief sections that are actually
// present in this message and tells the model to re-check them before
// finalising each finding, so a brief that endorses the diff's pattern can
// actually neutralise a false-positive prior. Mirrors the authority framing
// in repo_context.go / langagents/brief.go / runner.go's tech section.
func briefsReReadReminder(repoContext, evidence, langSection, techSection string) string {
	var names []string
	if strings.TrimSpace(langSection) != "" {
		names = append(names, "`## Language conventions`")
	}
	if strings.TrimSpace(techSection) != "" {
		names = append(names, "`## Technology conventions`")
	}
	if strings.TrimSpace(repoContext) != "" {
		names = append(names, "`## Repository context`")
	}
	if strings.TrimSpace(evidence) != "" {
		names = append(names, "`## Repo evidence for this PR`")
	}
	if len(names) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Before emitting findings — re-check the brief section(s) above (")
	b.WriteString(strings.Join(names, ", "))
	b.WriteString("). They are authoritative for repo-specific conventions: drop any draft finding that the briefs explicitly endorse as the local pattern, and calibrate severity for the rest. The diff is the authority for what changed; the briefs are the authority for whether that change is conventional here.\n\n")
	return b.String()
}

func buildVibeCoachUserPrompt(pr *gh.PR, specialists []SpecialistResult, strict aiconfig.ReviewStrictness, repoContext string) string {
	var b strings.Builder
	b.WriteString("You are the vibe coach for an AI-assisted code review. Other specialists have already reviewed the PR. Your job is to read their combined output and produce a small set of high-leverage prompts the PR author can paste into their own AI assistant to fix the most important issues in one or two iterations.\n\n")
	b.WriteString(strictnessBlockForVibeCoachUser(strict))
	b.WriteString("PR: " + pr.Repository + "#")
	fmt.Fprintf(&b, "%d", pr.Number)
	b.WriteString("\nTitle: " + pr.Title + "\n\n")
	if sec := strings.TrimSpace(FormatRepoContextSection(repoContext)); sec != "" {
		b.WriteString(sec)
		b.WriteString("\n\n")
	}
	b.WriteString("Specialist findings:\n\n")
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
			fmt.Fprintf(&b, "  [%s] %s:%d — %s\n", f.Severity, f.Path, f.Line, f.Comment)
		}
		b.WriteString("\n")
	}
	b.WriteString(vibeCoachOutputContract)
	return b.String()
}

// parseSpecialistJSON parses a code specialist's textual output via the shared
// llmjson salvage ladder (fence/extract/comment/triple-quote/trailing-comma),
// then applies domain normalization (severity canonicalisation) to the typed
// result. The salvage is generic; only the severity fixup is review-specific.
func parseSpecialistJSON(s string) (*specialistJSON, error) {
	o, err := llmjson.Parse[specialistJSON](s)
	if err != nil {
		return nil, err
	}
	normalizeSpecialistSeverities(&o)
	return &o, nil
}

// normalizeSpecialistSeverities canonicalises each finding's severity at parse
// time so unknown / synonym strings (e.g. "high", "nit") don't render verbatim
// in the body. See normalizeSeverity.
func normalizeSpecialistSeverities(o *specialistJSON) {
	if o == nil {
		return
	}
	for i := range o.Findings {
		o.Findings[i].Severity = normalizeSeverity(o.Findings[i].Severity)
	}
}

type specialistJSON struct {
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings"`
}

func parseVibeCoachJSON(s string) (*vibeCoachJSON, error) {
	v, err := llmjson.Parse[vibeCoachJSON](s)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

type vibeCoachJSON struct {
	Verdict string         `json:"verdict"`
	Summary string         `json:"summary"`
	Prompts []AuthorPrompt `json:"prompts"`
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
