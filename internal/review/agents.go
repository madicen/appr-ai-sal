package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
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

// augmentPromptsForProvider prepends guidance for backends that cannot use Claude's repo tools.
// hasRepoContext should be true when the user message includes a repository-context section (specialists omit it today).
func augmentPromptsForProvider(p aiconfig.Provider, systemPrompt, userPrompt string, hasRepoContext bool) (string, string) {
	if p == aiconfig.ProviderClaude {
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
func runReviewSpecialist(ctx context.Context, cfg *aiconfig.Config, name string, worktree string, pr *gh.PR, diff string, repoContext string, evidence string, langSection string) SpecialistResult {
	res := SpecialistResult{Specialist: name, Findings: []Finding{}}

	systemPrompt, err := SpecialistPrompt(name)
	if err != nil {
		res.Err = err
		return res
	}

	userPrompt := buildReviewUserPrompt(pr, diff, cfg.ReviewStrictness, repoContext, evidence, langSection)
	hasContext := strings.TrimSpace(repoContext) != "" || strings.TrimSpace(evidence) != "" || strings.TrimSpace(langSection) != ""
	systemPrompt, userPrompt = augmentPromptsForProvider(cfg.Provider, systemPrompt, userPrompt, hasContext)

	out, err := Complete(ctx, cfg, systemPrompt, userPrompt, worktree)
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
		// Re-apply the floor: a finding demoted to info above must obey
		// the same strictness gate the model originally got.
		res.Findings = FilterFindingsBySeverity(res.Findings, floor)
	}
	return res
}

// RunVibeCoachForDraft runs vibe-coach against the draft's *current*
// post-arbiter, post-user-skip specialist set. This is the deferred
// entrypoint the TUI calls at the approve->summary transition (and on
// re-entry if the user changed skips), so the LLM only ever sees the
// findings that are actually going to ship.
//
// Wraps the call with stageWithRetry + perStageBudget so retry/timeout
// behavior matches the inline call the streaming runner used to make.
// Safe to call multiple times on the same draft — each call overwrites
// d.VibeCoach.
//
// Returns a non-nil result even on error (Err is set); callers can
// surface the error in the UI without nil-checking.
func RunVibeCoachForDraft(ctx context.Context, cfg *aiconfig.Config, d *Draft) *VibeCoachResult {
	if d == nil || cfg == nil {
		return &VibeCoachResult{Err: fmt.Errorf("vibe-coach: nil draft or config"), Prompts: []AuthorPrompt{}}
	}
	vibeInput := SpecialistsForVibeCoach(d, d.Specialists)
	var res *VibeCoachResult
	_ = stageWithRetry(ctx, cfg, "vibe-coach", func(int, error) {}, func(sctx context.Context) error {
		stCtx, cancel := context.WithTimeout(sctx, perStageBudget(cfg))
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
	systemPrompt, userPrompt = augmentPromptsForProvider(cfg.Provider, systemPrompt, userPrompt, strings.TrimSpace(repoContext) != "")

	out, err := Complete(ctx, cfg, systemPrompt, userPrompt, worktree)
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

func buildReviewUserPrompt(pr *gh.PR, diff string, strict aiconfig.ReviewStrictness, repoContext string, evidence string, langSection string) string {
	var b strings.Builder
	hasContext := strings.TrimSpace(repoContext) != "" || strings.TrimSpace(evidence) != "" || strings.TrimSpace(langSection) != ""
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
	// Language conventions go BEFORE the repo brief: language defaults
	// are universal facts the repo-agent brief is allowed to override
	// for repo-specific deltas, so the repo brief reads naturally after
	// the universal one.
	if s := strings.TrimSpace(langSection); s != "" {
		b.WriteString(s)
		b.WriteString("\n\n")
	}
	b.WriteString(FormatRepoContextSection(repoContext))
	b.WriteString(FormatPRReviewEvidenceSection(evidence))
	b.WriteString("Unified diff (line numbers in `+` hunks are the lines you cite in findings, with side=\"RIGHT\"):\n\n")
	b.WriteString("```diff\n")
	b.WriteString(diff)
	b.WriteString("\n```\n\n")
	b.WriteString(reviewOutputContract)
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
			b.WriteString("Summary: " + s.Summary + "\n")
		}
		for _, f := range s.Findings {
			fmt.Fprintf(&b, "  [%s] %s:%d — %s\n", f.Severity, f.Path, f.Line, f.Comment)
		}
		b.WriteString("\n")
	}
	b.WriteString(vibeCoachOutputContract)
	return b.String()
}

// reviewOutputContract is the strict-JSON instruction appended to every
// code-reviewing specialist's user prompt. Don't mince words here; if the
// model returns prose around the JSON, parsing fails.
const reviewOutputContract = `Return your review as a single JSON object and nothing else — no prose before, no prose after, no markdown fencing. The object must conform to:

{
  "summary": "<1–3 sentences. Do NOT repeat or list findings that appear in \"findings\"; if you filed inline findings, the summary should be a single aggregate sentence or \"See inline comments on the diff.\" Avoid duplicating bullet points from findings.>",
  "findings": [
    {
      "path": "<file path relative to the repo root, or empty string for PR-wide / general feedback>",
      "line": <integer: RIGHT-side line number in the unified diff for inline comments; use 0 with empty path for general feedback only>,
      "side": "RIGHT",
      "severity": "info" | "warning" | "error" | "critical",
      "comment": "<the full human-readable review text: finding, rationale, and what to do. Use plain text or simple markdown. Put ALL narrative, questions, and explanations here.>",
      "suggestion": "<EXACT literal replacement text GitHub will apply at path/line — see the suggestion contract below. Required for any local fix; empty string for findings whose fix is non-local or non-mechanical.>",
      "anchor_excerpt": "<VERBATIM copy of the post-image line at path:line, including leading whitespace exactly as it appears in the diff. REQUIRED whenever you fill in 'suggestion' on an inline finding — the review tool deterministically compares this against the actual line and STRIPS your suggestion on mismatch (or auto-relocates the finding when the excerpt uniquely matches a different line in the same hunk), so getting this wrong is worse than leaving suggestion empty. Use empty string for general findings (path '', line 0) and for inline findings whose 'suggestion' is empty.>"
    }
  ]
}

ANCHOR PROOF (anchor_excerpt — DETERMINISTICALLY ENFORCED):

Whenever "suggestion" is non-empty, "anchor_excerpt" MUST be the exact post-image text of the diff line at "line" (the line that will be DELETED when the author clicks Apply). Copy it character-for-character from the unified diff including its leading whitespace; do not strip leading "+", "-", or " " from the diff prefix (the diff shows them; the line itself does not have them). The tool normalises whitespace before comparing.

Mismatch handling (so you know what's at stake):
   - If your excerpt does not match the line at "line" but UNIQUELY matches a different line in the same hunk, we auto-relocate the finding to that line and surface a banner to the reviewer. Quote the line correctly and this is invisible.
   - If your excerpt does not match anywhere in the hunk, or matches multiple lines, your suggestion is stripped (the prose comment survives). A short excerpt (under 20 characters) is treated the same way — short lines like "}" or "return nil" match all over the file and can't be safely relocated.

If you are not certain which line you are anchoring at, leave BOTH "anchor_excerpt" and "suggestion" empty and let the prose in "comment" carry the load.

ACTIONABILITY BAR (HARD REQUIREMENT — applies to EVERY finding, inline or general):

Every finding's "comment" MUST be concrete enough that a reviewer reading it can act without a follow-up question:
   - It names the file/symbol/identifier to change.
   - It states what the new shape, signature, value, or behaviour should be — not just what's wrong.
   - It avoids hedging ("consider", "you might", "perhaps", "this could be cleaner"). Hedged feedback is a non-finding.
   - For PR-wide entries (path "", line 0), the same bar applies: spell out the rule being violated and what should change.

If you cannot meet that bar, do not file the finding.

SUGGESTION CONTRACT (default to filling this — it's the most valuable part of the review):

The "suggestion" field is the literal text GitHub will apply as a one-click "Apply suggestion" block at the commented line. When the author clicks Apply:

   1. The single line at "line" is DELETED.
   2. Your "suggestion" lines are inserted in its place.
   3. Every other line in the file is left exactly as-is.

REPLACEMENT, NOT INSERTION (read this twice):

There is no such thing as "insert before" or "insert after" in this contract. Your suggestion ALWAYS replaces the anchor line. If you want the anchor line to survive, you MUST include it (verbatim, with the exact indentation) in your "suggestion" in the right position. If you do not include it, it is GONE.

Concrete consequences:
   - Adding a doc comment ABOVE a declaration: anchor at the declaration line; "suggestion" = "<doc line(s)>\n<exact text of the declaration line>".
   - Inserting a new clause INSIDE an existing block: anchor at the line you want the new clause to sit next to; "suggestion" = the original anchor line + your new clause(s) in the desired final order.
   - You may NOT anchor at one line in order to add content that belongs near a different line. Doing so deletes the anchor line, which is almost always wrong (the line you "comment on" silently disappears from the file).
   - Never include lines that already exist in the diff above or below the anchor — they are NOT removed by your suggestion, so repeating them creates duplicates that break the build.

LANGUAGE AWARENESS:

Use the file's own language for everything in "suggestion": comment syntax, identifier conventions, indentation, and string-quoting rules. Infer the language from the file extension on the finding's "path":
   - .go → "// " line comments, godoc-style sentences starting with the identifier name
   - .py → "# " line comments or """docstrings""" inside the def/class body
   - .ts / .js / .tsx / .jsx / .java / .c / .cpp / .rs / .swift / .kt → "// " line comments
   - .tf / .hcl → "# " or "// " comments (HCL accepts both); HCL is NOT Go — do not use godoc framing
   - .yml / .yaml / .toml → "# " comments
   - .sh / .bash / .zsh / .rb → "# " comments
   - .sql → "-- " comments
   - Markdown / plain text → no comment syntax; just write the prose
Never apply Go's godoc framing to a non-Go file just because a Go example appears in your specialist prompt.

You MUST emit a non-empty "suggestion" whenever ALL of these hold:
   1. The fix is a contiguous replacement of <= 10 lines at the finding's anchor line.
   2. You can write the replacement so it compiles / parses / lints cleanly when substituted in (no placeholders, no TODOs, no "// FIXME").
   3. The replacement does not require imports, declarations, or symbols that don't already exist in the file (or that you can also include in the suggestion).
   4. The change is mechanical / non-controversial — fixing a typo, swapping an unsafe call for a safe equivalent, adding an obvious nil check, supplying a missing doc comment, renaming a single identifier within its scope, etc.

Examples that MUST have a suggestion (non-exhaustive):
   - Spelling/grammar fix in a comment, log line, error message, or doc string.
   - Replacing md5/sha1 with sha256 for a security finding when the call site is local.
   - Adding a missing doc comment on an exported identifier (in any language) — see "Adding a doc comment ABOVE a declaration" above for the anchor + replay rule.
   - Replacing string concatenation with a parameterised query when the params are already in scope.
   - Adding an early-return nil check at the top of a function.
   - Inserting a missing default case in a switch.

You MUST leave "suggestion" empty when ANY of these hold:
   - The fix requires changes across multiple files or non-contiguous lines.
   - The fix needs new imports, helpers, or types you cannot include in the same suggestion block.
   - The fix is a refactor with multiple acceptable shapes (let the human decide; explain in "comment").
   - You are unsure the replacement compiles / parses cleanly. A wrong suggestion is worse than none.
   - You cannot anchor at the line that should be replaced (e.g. the change wants to live between two lines and neither of them should be deleted, and you cannot reproduce both verbatim).

Suggestion formatting rules (silently dropped if violated):
   - Code (or prose, for doc comments) only — NO prose around the code, NO markdown fences (no leading "` + "```" + `"), NO "// fix:" placeholders, NO "Here is the fix" preambles.
   - The replacement must be drop-in valid at path/line. Match the existing indentation level. Preserve language syntax (semicolons, braces, etc.).
   - Multiple replacement lines are fine and replace the single anchor line; just do not include the original line in the suggestion unless your fix keeps it.
   - If you find yourself wanting to include English explanation, that text belongs in "comment", not "suggestion".

Worked example (Go, formatting specialist, finding anchored at "name := strings.toLower(s)"):

  "comment": "Go's strings package uses ToLower (capitalised). The current call won't compile.",
  "suggestion": "name := strings.ToLower(s)"

Worked example (security specialist, finding anchored at "h := md5.New()"):

  "comment": "MD5 is not collision-resistant; use SHA-256 for content hashing here. The sha256 package is already imported elsewhere in this file.",
  "suggestion": "h := sha256.New()"

Worked example (docs specialist, Terraform/HCL, anchored at the resource declaration line ` + "`resource \"aws_security_group\" \"web\" {`" + `):

  "comment": "The new aws_security_group resource lacks a leading comment explaining what it allows.",
  "suggestion": "# web is the security group attached to the public ALB; rules below open 80/443 to the world.\nresource \"aws_security_group\" \"web\" {"

Note how the original ` + "`resource \"aws_security_group\" \"web\" {`" + ` line is REPLAYED verbatim at the end of the suggestion — without that, applying the suggestion would delete the resource declaration. Note also that the comment uses HCL syntax (` + "`#`" + `) and not Go's godoc framing.

Inline vs general: prefer a non-empty path and line > 0 whenever the issue ties to a changed line in the diff (those become GitHub inline review comments). Use path "" and line 0 only for repo-wide or cross-cutting notes that truly cannot be anchored to one line; suggestion must be empty for general findings.

If you have nothing to say, return an empty findings array. Stay strictly in your specialty — do not report findings that another specialist would handle.

String values must be valid JSON only: use one pair of double quotes per string and escape internal newlines as \n and quotes as \". Never use Python-style """ triple-quoted strings inside the JSON. Do not use // or /* */ comments, JSON5/JSONC extensions, or trailing commas — the tool parses strict JSON.`

// vibeCoachOutputContract is the strict-JSON instruction for the vibe coach.
const vibeCoachOutputContract = `Return your output as a single JSON object and nothing else — no prose before, no prose after, no markdown fencing. The object must conform to:

{
  "verdict": "approve" | "request_changes" | "comment",
  "summary": "<One short paragraph only: max 3 sentences. State verdict rationale and what blocks merge — do NOT restate each specialist or list findings (those are inline or in prompts).>",
  "prompts": [
    {
      "title": "<short imperative label the human reviewer reads first>",
      "rationale": "<1–2 sentences for the HUMAN reader: which specialist findings this bundles and what category of problem they amount to. Do NOT include any instructions for the AI assistant here.>",
      "agent_prompt": "<the verbatim block the author will paste into their AI coding assistant. Self-contained, second-person, references concrete paths/symbols. NOT a meta-summary; an actionable instruction. NEVER refer to the rationale or to other prompts; the AI receives only this string.>",
      "finding_refs": [
        { "specialist": "<formatting | design | testing | docs | security>",
          "path":       "<exact path from the bundled specialist finding>",
          "line":       <integer line number from that finding>,
          "side":       "RIGHT" | "LEFT" }
      ]
    }
  ]
}

Finding refs (REQUIRED for prompts that bundle specialist findings):

- Each entry in "prompts" MUST list every specialist finding it bundles in "finding_refs", using the exact specialist name, path, and line number from the input you received. Side defaults to "RIGHT" — only set it explicitly when the finding's side is LEFT.
- Without these refs the rendered review cannot tell when a referenced finding was suppressed by the repo arbiter or skipped by the reviewer; prompts whose every referenced finding is suppressed/skipped will be silently dropped at render time, so the listed refs are how you guarantee a prompt survives.
- "finding_refs" may be empty only for prompts that don't tie to any specific specialist finding (general process advice). Such prompts are kept unconditionally, but use this case sparingly — most useful prompts bundle concrete findings.

Verdict (required — human reviewer-facing merge recommendation, analogous to GitHub review states):

- "approve" — You would be comfortable merging if the specialists found nothing blocking; remaining notes are minor or optional.
- "request_changes" — At least one substantive issue (severity, design, security, tests, or docs) should be addressed before merge, or follow-up prompts are needed for non-trivial fixes.
- "comment" — Feedback is informational only; no strong merge gate from this pass (use sparingly).

The published review body leads with **Merge recommendation** (verdict heading + short summary) then **one combined suggested prompt** for the author's AI (if any). The "summary" must stay executive — no specialist-by-specialist recap.

Verdict vs prompts (critical):

- If "verdict" is **request_changes**, you MUST return at least one entry in "prompts" with a non-empty "agent_prompt" that tells the author exactly what to change (files, symbols, acceptance criteria), unless the ONLY blocking work is already covered by GitHub-ready code suggestion blocks on the diff with nothing left to instruct an AI — in that rare case, state that explicitly in "summary" and return an empty "prompts" array.
- If "verdict" is **approve** or **comment**, "prompts" may be empty.

Hard rules:

- **One prompts entry per distinct topic** the author would naturally hand to their AI in one go. A refactor is one topic; a README update is a separate topic; a CHANGELOG entry is a separate topic. The posted review concatenates all agent_prompt strings into a single paste block, separated by ` + "`---`" + ` between entries — that separator is how the human sees that there are multiple topics to address. Cramming unrelated work ("refactor X. Then update the README. Then add a CHANGELOG entry.") into a single agent_prompt hides everything but the first topic.
- **PR-wide findings get their own dedicated prompts.** Any specialist finding with empty path and line 0 MUST be the sole subject of its own prompts entry, with finding_refs listing exactly that one finding. Bundling a PR-wide finding alongside inline findings in the same prompt risks losing it: if any of the inline siblings is suppressed by the repo arbiter or skipped during review, the whole prompt can be filtered out and the PR-wide work silently disappears from the rendered review. A dedicated entry guarantees the PR-wide finding's prompt survives independently.
- **Coverage requirement.** Every error / critical-severity finding (inline OR PR-wide) that does NOT carry an inline one-click ` + "`" + `suggestion` + "`" + ` block MUST appear in some prompt's finding_refs, and that prompt's agent_prompt MUST give the author's AI a concrete instruction for fixing it. The renderer will auto-generate fallback prompts for any uncovered blocker, but a fallback is a safety net (it can only quote the specialist comment verbatim) and not a substitute for you writing a real instruction.
- Bundle related specialist findings inside a single prompt when they're on the same topic AND none of them is a PR-wide finding (e.g. four inline security findings about input validation in one handler family) — that reduces iteration count without hiding anything.
- Cap: at most 4 prompts entries. If you need more, consolidate within a topic, never across topics.
- Within a single agent_prompt, separate distinct steps with a blank line (\n\n in JSON) so the pasted block is scannable; don't return a wall-of-text paragraph.
- "rationale" is for the human reading the review; agent_prompt is for the AI; do not mix them.
- Every non-empty agent_prompt must specify the change concretely (paths, identifiers, acceptance criteria) so the author's AI can act on it without further context.
- Do not include literal fenced code blocks inside agent_prompt unless they are a patch the AI should apply verbatim.

If a clean PR doesn't warrant any follow-up prompts and verdict is approve, return an empty "prompts" array. Don't manufacture work.

String values must be valid JSON only (escape newlines as \n); never use Python """ triple-quoted strings inside the JSON.`

// parseSpecialistJSON parses claude's textual output for a code specialist.
// It tries strict parse first; on failure, it extracts the first JSON object
// it finds in the text (handles cases where the model wraps JSON in fencing
// despite instructions).
func parseSpecialistJSON(s string) (*specialistJSON, error) {
	s = strings.TrimSpace(s)
	o, err := tryParseSpecialistJSON(s)
	if err == nil {
		return o, nil
	}
	if extractJSONObject(s) == "" {
		return nil, fmt.Errorf("no JSON object found")
	}
	return nil, err
}

func tryParseSpecialistJSON(s string) (*specialistJSON, error) {
	var lastErr error
	try := func(raw string) (*specialistJSON, error) { return parseStrict(raw) }

	for _, c := range specialistJSONVariants(s) {
		if o, err := try(c); err == nil {
			return o, nil
		} else {
			lastErr = err
		}
		if obj := extractJSONObject(c); obj != "" {
			if o, err := try(obj); err == nil {
				return o, nil
			} else {
				lastErr = err
			}
			fixed := sanitizeTripleQuotedStringValues(obj)
			if fixed != obj {
				if o, err := try(fixed); err == nil {
					return o, nil
				} else {
					lastErr = err
				}
			}
			com := stripJSONCStyleComments(obj)
			if com != obj {
				if o, err := try(com); err == nil {
					return o, nil
				} else {
					lastErr = err
				}
			}
		}
	}
	return nil, lastErr
}

func parseStrict(s string) (*specialistJSON, error) {
	var v specialistJSON
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, err
	}
	return &v, nil
}

type specialistJSON struct {
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings"`
}

func parseVibeCoachJSON(s string) (*vibeCoachJSON, error) {
	var lastErr error
	for _, c := range specialistJSONVariants(strings.TrimSpace(s)) {
		var v vibeCoachJSON
		if err := json.Unmarshal([]byte(c), &v); err != nil {
			lastErr = err
		} else {
			return &v, nil
		}
		if obj := extractJSONObject(c); obj != "" {
			var v2 vibeCoachJSON
			if err := json.Unmarshal([]byte(obj), &v2); err != nil {
				lastErr = err
			} else {
				return &v2, nil
			}
			fixed := sanitizeTripleQuotedStringValues(obj)
			if fixed != obj {
				if err := json.Unmarshal([]byte(fixed), &v2); err != nil {
					lastErr = err
				} else {
					return &v2, nil
				}
			}
			com := stripJSONCStyleComments(obj)
			if com != obj {
				if err := json.Unmarshal([]byte(com), &v2); err != nil {
					lastErr = err
				} else {
					return &v2, nil
				}
			}
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no JSON object found")
}

type vibeCoachJSON struct {
	Verdict string         `json:"verdict"`
	Summary string         `json:"summary"`
	Prompts []AuthorPrompt `json:"prompts"`
}

// extractJSONObject finds the first top-level {...} block in s. Naive but
// good enough — counts braces, ignoring those inside strings.
func extractJSONObject(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
