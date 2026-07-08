package review

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// contracts.go generates the strict-JSON output contracts and the per-agent
// JSON schemas from the declarative registry (registry.go) and the Severity
// ladder, instead of hand-maintaining them as consts (Q2).
//
// Why generate them?
//   - Enum drift becomes structurally impossible. The severity ladder used in
//     every contract and schema is sourced from severityLadder(); the vibe
//     coach's finding_refs.specialist enum and the vibe schema's specialist
//     enum are sourced from AllSpecialists + AllPRAgents. Adding a specialist
//     or a severity updates every contract and schema at once — the 0.4 item
//     1–2 class of bug (an enum that forgot `tech` / the PR agents) can never
//     return. A test (contracts_test.go) proves the generated enums equal the
//     registry-derived sets.
//   - The built-in code-specialist contract keeps every structural section
//     the built-ins relied on, and its severity enum is spliced in from
//     severityLadderEnum() (so the ladder can never drift). Q3 augmented the
//     shared suggestion/anchor guidance in reviewOutputContract with worked
//     multi-line and adversarial examples and a corrected (non-double-negative)
//     anchor_excerpt rule; the contracts_test markers pin the structural
//     sections and the registry-sourced severity line so the additions cannot
//     silently drop content.
//   - PR agents get a dedicated slim contract (prAgentOutputContract) that
//     drops the ~2.5k-token suggestion machinery they never use (their inline
//     suggestions are force-stripped / never produced — see
//     constrainPRAgentScope), a real token saving on ~4 of ~10 calls per run.
//   - Each JSON stage can hand its schema to a schema-capable provider
//     (Gemini responseSchema) for R5 native JSON mode.

// severityLadder is the ordered severity enum, ascending in importance. It is
// the single source of truth for the "info | warning | error | critical"
// ladder that appears in every output contract and JSON schema, so the ladder
// can never drift between the Severity constants, the prompts, and the
// schemas. Its order matches severityRank.
func severityLadder() []Severity {
	return []Severity{SeverityInfo, SeverityWarning, SeverityError, SeverityCritical}
}

// severityStrings returns the severity ladder as plain strings for a JSON
// schema enum.
func severityStrings() []string {
	l := severityLadder()
	out := make([]string, len(l))
	for i, s := range l {
		out[i] = string(s)
	}
	return out
}

// severityLadderEnum renders the severity ladder as the contract enum string
// `"info" | "warning" | "error" | "critical"`. Splicing this into the
// contracts (instead of hard-coding the four values) is what makes the ladder
// drift-proof.
func severityLadderEnum() string {
	l := severityLadder()
	parts := make([]string, len(l))
	for i, s := range l {
		parts[i] = `"` + string(s) + `"`
	}
	return strings.Join(parts, " | ")
}

// reviewOutputContract is the strict-JSON instruction appended to every
// code-reviewing specialist's user prompt. Don't mince words here; if the
// model returns prose around the JSON, parsing fails.
//
// Its only registry-sourced substitution is the severity enum
// (severityLadderEnum), so the severity ladder can never drift from the
// Severity constants. The surrounding suggestion/anchor guidance is
// hand-maintained prose (Q3 sharpened it — see the head-of-file note); the
// contracts_test markers guard that no structural section is lost.
var reviewOutputContract = reviewOutputContractHead + severityLadderEnum() + reviewOutputContractTail

const reviewOutputContractHead = `Return your review as a single JSON object and nothing else — no prose before, no prose after, no markdown fencing. The object must conform to:

{
  "summary": "<1–3 sentences. STAY STRICTLY IN YOUR LANE — your summary must speak ONLY to your specialty as defined in the system prompt above (e.g. a security specialist's summary covers security and only security; a docs specialist's covers documentation and only documentation). DO NOT describe the PR's overall scope, recap unrelated concerns, or comment on areas owned by other specialists (test coverage, design, documentation, formatting, etc. unless that IS your specialty). If you have no findings in your specialty, say exactly that ('No <specialty> concerns in this diff' or similar) and nothing else. Do NOT repeat or list findings that appear in \"findings\"; if you filed inline findings, the summary should be a single aggregate sentence or \"See inline comments on the diff.\" Avoid duplicating bullet points from findings.>",
  "findings": [
    {
      "path": "<file path relative to the repo root, or empty string for PR-wide / general feedback>",
      "line": <integer: RIGHT-side line number in the unified diff for inline comments; use 0 with empty path for general feedback only>,
      "side": "RIGHT",
      "severity": `

const reviewOutputContractTail = `,
      "comment": "<the full human-readable review text: finding, rationale, and what to do. Use plain text or simple markdown. Put ALL narrative, questions, and explanations here.>",
      "suggestion": "<EXACT literal replacement text GitHub will apply at path/line — see the suggestion contract below. Required for any local fix; empty string for findings whose fix is non-local or non-mechanical.>",
      "anchor_excerpt": "<VERBATIM copy of the post-image line at path:line, including leading whitespace exactly as it appears in the diff. REQUIRED whenever you fill in 'suggestion' on an inline finding — the review tool deterministically compares this against the actual line and STRIPS your suggestion on mismatch (or auto-relocates the finding when the excerpt uniquely matches a different line in the same hunk), so getting this wrong is worse than leaving suggestion empty. Use empty string for general findings (path '', line 0) and for inline findings whose 'suggestion' is empty.>"
    }
  ]
}

SPECIALTY SCOPE (HARD — this gates EVERY finding, not just the summary):

You own exactly one lens, defined in the system prompt above. Every finding you file MUST be an issue *in that specialty*. This is not a duplicate tie-breaker — it decides whether you file at all:
   - Do NOT file a finding that belongs to another specialist's lane, even when it is real, obvious, and nobody else has flagged it. Another specialist owns it and will catch it. Concretely: a wrong Kubernetes memory unit suffix ("memory: 717M" → "717Mi"), a misconfigured value, a logic bug in application code, or a missing doc are NOT testing findings, NOT formatting findings, NOT security findings unless that specific lens is yours. Raising someone else's issue makes the panel noisier and the review worse.
   - A change can fall entirely outside your lens. A pure config / YAML / infra / data PR often contains nothing in your specialty at all. When that is the case, return an EMPTY "findings" array and say so in one sentence in "summary". An empty result from the right specialist is the correct, useful answer.
   - If the only issue you can find is one you'd have to reach outside your lane to file, file NOTHING. The absence of in-lane issues is itself a valid finding-free result — do not manufacture coverage by grabbing another lane's problem.

ANCHOR PROOF (anchor_excerpt — DETERMINISTICALLY ENFORCED):

Whenever "suggestion" is non-empty, "anchor_excerpt" MUST be the exact post-image text of the diff line at "line" (the line that will be DELETED when the author clicks Apply). The unified diff prepends a one-character marker ("+", "-", or " ") to the front of every line — that marker is part of the DIFF FORMAT, not part of the source line. STRIP that single leading marker and copy everything after it verbatim, INCLUDING the line's own leading indentation. Example: for the diff row that reads (with its leading plus) "+    name := strings.toLower(s)", the correct "anchor_excerpt" is "    name := strings.toLower(s)" — the four spaces of indentation are KEPT, only the "+" is dropped. Do NOT leave the "+"/"-"/" " diff marker on the front (that guarantees a mismatch and your suggestion is stripped), and do NOT strip the code's real indentation. The tool normalises interior and trailing whitespace before comparing, but the safest excerpt is a clean copy of the line as it exists in the file.

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
   - VERIFY BEFORE YOU FILE: if a finding depends on how a symbol is defined, called, or set elsewhere, confirm it with Read/Grep before filing. If the tools are unavailable (you were told you have no repo access) or you cannot confirm it from the diff and context provided, either drop the finding or file it at reduced severity with "comment" stating it is unverified and what would confirm it. A confidently-wrong finding costs the panel more trust than a missed nit.
   - STATE THE IMPACT, not just the rule: every "comment" must name the consequence if the code ships as-is (what breaks, regresses, misleads a reader, or risks at runtime). One clause suffices (e.g. "...which silently drops the context-cancellation error"). A finding asserting a rule with no stated consequence is "info" at most.

If you cannot meet that bar, do not file the finding.

WRITING LANGUAGE (the natural language your prose is written in — NOT to be confused with the programming-language guidance further below):

All natural-language prose you emit — "summary", every finding's "comment", and any English wording inside "suggestion" — MUST be written in English. The only non-English text permitted anywhere in your output is verbatim source code, identifiers, or string literals that already appear in another script in the diff (preserve those exactly when you reproduce them inside "suggestion"). NEVER mix scripts within a single sentence; an English sentence with a Chinese, Japanese, Korean, Cyrillic, Arabic, or any other non-Latin word substituted for an English word ("Consider using 加密方法 to secure the token") is a bug, not a stylistic choice. When you reach for a foreign-language term mid-sentence, replace it with its English equivalent ("an encryption method", "the algorithm", etc.).

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
   - Correcting a wrong literal, constant, enum value, or unit suffix on the anchor line WHEN that correction is in your specialty (e.g. "memory: 717M" → "memory: 717Mi", a flipped boolean default, an off-by-one bound). If the fix is in your lane and your "comment" names the exact corrected value, you can — and MUST — put it in "suggestion"; do not let "this is a bigger note" talk you out of an obvious one-token fix. (If the issue is NOT in your specialty, do not file it at all — see SPECIALTY SCOPE above. This rule is about completeness of an in-lane finding, never a licence to reach into another lane.)
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

Worked example (multi-line suggestion WITH indentation — Python, testing specialist, anchored at the closing bracket line "    ]" of a table of cases):

  "comment": "The cases table has no row for empty input; add one asserting parse_timeout(\"\") raises. Anchor at the table's closing bracket and replay it so the new row lands inside the list.",
  "anchor_excerpt": "    ]",
  "suggestion": "        (\"empty\", \"\", None, True),\n    ]"

The suggestion keeps the file's own indentation — 8 spaces for the new row (matching the sibling rows), 4 spaces for the closing bracket — and REPLAYS the "    ]" anchor line at the end so the bracket is not deleted. Indentation is literal: a row indented wrong will not parse. (A bare "    ]" excerpt is short and may be treated as un-relocatable; quote a more distinctive line when one is available, or accept the tool may keep only the prose.)

Adversarial example (a WRONG suggestion — study why it fails, then never do it):

  The diff shows an added line whose diff row reads "+    result := compute(x)".
  A finding that sets "anchor_excerpt": "+    result := compute(x)" (WRONG — it kept the leading "+") will NOT match the real line "    result := compute(x)", so the tool strips the suggestion entirely and only the prose survives. A second common form of the same mistake is anchoring one line ABOVE the target so you can "insert before" it: there is no insert in this contract — your suggestion REPLACES the anchor line, so anchoring above silently deletes the wrong line. Both failures share one root cause: the "anchor_excerpt" did not equal the exact post-image line at "line". Quote the line you actually mean — marker-stripped, indentation intact — and only fill "suggestion" once you are certain it matches.

Inline vs general: prefer a non-empty path and line > 0 whenever the issue ties to a changed line in the diff (those become GitHub inline review comments). Use path "" and line 0 only for repo-wide or cross-cutting notes that truly cannot be anchored to one line; suggestion must be empty for general findings.

If you have nothing to say, return an empty findings array. Stay strictly in your specialty — do not report findings that another specialist would handle.

String values must be valid JSON only: use one pair of double quotes per string and escape internal newlines as \n and quotes as \". Never use Python-style """ triple-quoted strings inside the JSON. Do not use // or /* */ comments, JSON5/JSONC extensions, or trailing commas — the tool parses strict JSON.`

// prAgentOutputContract is the dedicated, slim strict-JSON instruction for the
// whole-PR agents (description, checks, discussion, scope). Unlike the code
// specialists they do not author one-click code suggestions — description /
// scope findings are forced PR-wide and discussion findings are thread-scoped
// by constrainPRAgentScope, and any suggestion a PR agent does emit is
// re-derived deterministically by the synthesize/repair gates — so the whole
// SUGGESTION CONTRACT / ANCHOR PROOF / LANGUAGE-AWARENESS machinery is dead
// weight in their prompt. This contract keeps exactly what they need (summary,
// findings with path/line/side/severity/comment, PR-wide framing, the
// specialty/actionability/language bars, strict-JSON rules) and drops the rest,
// a material token saving on ~4 of ~10 calls per run.
//
// The severity enum is spliced in from severityLadderEnum so it can never
// drift from the code specialists' ladder.
var prAgentOutputContract = prAgentOutputContractHead + severityLadderEnum() + prAgentOutputContractTail

const prAgentOutputContractHead = `Return your review as a single JSON object and nothing else — no prose before, no prose after, no markdown fencing. The object must conform to:

{
  "summary": "<1–3 sentences. STAY STRICTLY IN YOUR LANE — your summary must speak ONLY to your specialty as defined in the system prompt above. DO NOT recap the PR's overall scope or comment on areas owned by other agents. If you have no findings in your specialty, say exactly that ('No <specialty> concerns in this PR' or similar) and nothing else. Do NOT repeat or list the entries in \"findings\".>",
  "findings": [
    {
      "path": "<file path relative to the repo root, or empty string for PR-wide / general feedback>",
      "line": <integer: RIGHT-side line number in the unified diff for an inline comment; use 0 with empty path for PR-wide feedback>,
      "side": "RIGHT",
      "severity": `

const prAgentOutputContractTail = `,
      "comment": "<the full human-readable review text: finding, rationale, and what to do. Use plain text or simple markdown. Put ALL narrative, questions, and explanations here.>"
    }
  ]
}

You review the pull request as a WHOLE — its description, CI checks, discussion, or scope — not line by line. You do NOT author one-click code edits, so there is no replacement-text field to fill. State the fix in prose in "comment"; when your comment names an unambiguous local change the review tool attaches a concrete code fix for you.

INLINE VS PR-WIDE:
   - Use a non-empty "path" and "line" > 0 only when the issue genuinely ties to one changed line (e.g. a failing-check annotation, or an unresolved review thread the author has not addressed). Those post as GitHub inline comments.
   - Use "path" "" and "line" 0 for the usual whole-PR judgment (a missing description section, an unaddressed conversation ask, an out-of-scope change). Most of your findings are PR-wide.

SPECIALTY SCOPE (HARD — this gates EVERY finding, not just the summary):

You own exactly one lens, defined in the system prompt above. Every finding you file MUST be an issue *in that specialty*:
   - Do NOT file a finding that belongs to a code specialist's lane — a logic bug, a wrong literal or unit, a style nit, a security hole. Another agent owns it and will catch it. Raising someone else's issue makes the panel noisier and the review worse.
   - A PR can fall entirely outside your lens. When that is the case, return an EMPTY "findings" array and say so in one sentence in "summary". An empty result from the right agent is the correct, useful answer.

ACTIONABILITY BAR (HARD REQUIREMENT — applies to EVERY finding):

Every finding's "comment" MUST be concrete enough that a reviewer reading it can act without a follow-up question:
   - It names the file / section / rule to change.
   - It states what the new shape or behaviour should be — not just what's wrong.
   - It avoids hedging ("consider", "you might", "perhaps"). Hedged feedback is a non-finding.
   - STATE THE IMPACT, not just the rule: name the consequence if the PR merges as-is. A finding asserting a rule with no stated consequence is "info" at most.

If you cannot meet that bar, do not file the finding.

WRITING LANGUAGE:

All natural-language prose you emit — "summary" and every finding's "comment" — MUST be written in English. The only non-English text permitted is verbatim source code, identifiers, or string literals that already appear in another script in the PR (preserve those exactly). NEVER mix scripts within a single sentence; replace a foreign-language term mid-sentence with its English equivalent.

If you have nothing to say, return an empty "findings" array. Stay strictly in your specialty — do not report findings that a code specialist would handle.

String values must be valid JSON only: use one pair of double quotes per string and escape internal newlines as \n and quotes as \". Never use Python-style """ triple-quoted strings inside the JSON. Do not use // or /* */ comments, JSON5/JSONC extensions, or trailing commas — the tool parses strict JSON.`

// vibeFindingRefSpecialistEnum builds the finding_refs.specialist enum for the
// vibe-coach contract from the registry (all built-in code specialists in order
// followed by the PR agents), so the enum can never drift from the set of lanes
// the pipeline can actually emit — the enum-drift bug class that 0.4 fix #1 had
// to correct by hand. This is the "contract enum membership consults the
// registry" half of Q1.
func vibeFindingRefSpecialistEnum() string {
	names := make([]string, 0, len(AllSpecialists)+len(AllPRAgents))
	names = append(names, AllSpecialists...)
	names = append(names, AllPRAgents...)
	return strings.Join(names, " | ")
}

// vibeCoachOutputContract is the strict-JSON instruction for the vibe coach.
// The finding_refs.specialist enum is filled from the registry at init time
// (see vibeFindingRefSpecialistEnum) so it stays in lockstep with the set of
// specialists and PR agents.
var vibeCoachOutputContract = fmt.Sprintf(`Return your output as a single JSON object and nothing else — no prose before, no prose after, no markdown fencing. The object must conform to:

{
  "verdict": "approve" | "request_changes" | "comment",
  "summary": "<One short paragraph only: max 3 sentences. State verdict rationale and what blocks merge — do NOT restate each specialist or list findings (those are inline or in prompts).>",
  "prompts": [
    {
      "title": "<short imperative label the human reviewer reads first>",
      "rationale": "<1–2 sentences for the HUMAN reader: which specialist findings this bundles and what category of problem they amount to. Do NOT include any instructions for the AI assistant here.>",
      "agent_prompt": "<the verbatim block the author will paste into their AI coding assistant. Self-contained, second-person, references concrete paths/symbols. NOT a meta-summary; an actionable instruction. NEVER refer to the rationale or to other prompts; the AI receives only this string.>",
      "finding_refs": [
        { "specialist": "<%s>",
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

Verdict (required — human reviewer-facing merge recommendation, analogous to GitHub review states; these definitions are identical to the ones in your system prompt):

- "approve" — You would be comfortable merging if the specialists found nothing blocking; remaining notes are minor or optional.
- "request_changes" — At least one substantive issue (severity, design, security, tests, or docs) should be addressed before merge, or follow-up prompts are needed for non-trivial fixes.
- "comment" — Feedback is informational only; no strong merge gate from this pass (use sparingly).

The published review body leads with **Merge recommendation** (verdict heading + short summary) then **one combined suggested prompt** for the author's AI (if any). The "summary" must stay executive — no specialist-by-specialist recap.

Verdict vs prompts (critical):

- If "verdict" is **request_changes**, you MUST return at least one entry in "prompts" with a non-empty "agent_prompt" that tells the author exactly what to change (files, symbols, acceptance criteria), unless the ONLY blocking work is already covered by GitHub-ready code suggestion blocks on the diff with nothing left to instruct an AI — in that rare case, state that explicitly in "summary" and return an empty "prompts" array.
- If "verdict" is **approve** or **comment**, "prompts" may be empty.

Hard rules:

- **One prompts entry per distinct topic** the author would naturally hand to their AI in one go. A refactor is one topic; a README update is a separate topic; a CHANGELOG entry is a separate topic. The posted review concatenates all agent_prompt strings into a single paste block, separated by `+"`---`"+` between entries — that separator is how the human sees that there are multiple topics to address. Cramming unrelated work ("refactor X. Then update the README. Then add a CHANGELOG entry.") into a single agent_prompt hides everything but the first topic.
- **PR-wide findings get their own dedicated prompts.** Any specialist finding with empty path and line 0 MUST be the sole subject of its own prompts entry, with finding_refs listing exactly that one finding. Bundling a PR-wide finding alongside inline findings in the same prompt risks losing it: if any of the inline siblings is suppressed by the repo arbiter or skipped during review, the whole prompt can be filtered out and the PR-wide work silently disappears from the rendered review. A dedicated entry guarantees the PR-wide finding's prompt survives independently.
- **Coverage requirement.** Every error / critical-severity finding (inline OR PR-wide) that does NOT carry an inline one-click `+"`"+`suggestion`+"`"+` block MUST appear in some prompt's finding_refs, and that prompt's agent_prompt MUST give the author's AI a concrete instruction for fixing it. The renderer will auto-generate fallback prompts for any uncovered blocker, but a fallback is a safety net (it can only quote the specialist comment verbatim) and not a substitute for you writing a real instruction.
- Bundle related specialist findings inside a single prompt when they're on the same topic AND none of them is a PR-wide finding (e.g. four inline security findings about input validation in one handler family) — that reduces iteration count without hiding anything.
- Cap: at most 4 prompts entries. If you need more, consolidate within a topic, never across topics.
- Within a single agent_prompt, separate distinct steps with a blank line (\n\n in JSON) so the pasted block is scannable; don't return a wall-of-text paragraph.
- "rationale" is for the human reading the review; agent_prompt is for the AI; do not mix them.
- Every non-empty agent_prompt must specify the change concretely (paths, identifiers, acceptance criteria) so the author's AI can act on it without further context.
- Do not include literal fenced code blocks inside agent_prompt unless they are a patch the AI should apply verbatim.

Writing language:

All natural-language prose you emit — "summary", "title", "rationale", and "agent_prompt" — MUST be written in English. The only non-English text permitted is verbatim source code, identifiers, or string literals that already appear in another script in the specialist input you are summarising (preserve those exactly when you reproduce them). NEVER mix scripts within a single sentence; an English sentence with a Chinese, Japanese, Korean, Cyrillic, Arabic, or any other non-Latin word substituted for an English word is a bug, not a stylistic choice. When you reach for a foreign-language term mid-sentence, replace it with its English equivalent.

If a clean PR doesn't warrant any follow-up prompts and verdict is approve, return an empty "prompts" array. Don't manufacture work.

String values must be valid JSON only (escape newlines as \n); never use Python """ triple-quoted strings inside the JSON.`, vibeFindingRefSpecialistEnum())

// --- Per-agent JSON schemas (R5 native JSON mode) ------------------------
//
// Each JSON stage can hand its schema to a schema-capable provider (Gemini
// responseSchema) so the model is constrained to the exact output shape. The
// schemas are deliberately kept to the OpenAPI-3.0 subset Gemini accepts
// (object/array/string/integer types, string enums, properties, required,
// items — no $schema, no additionalProperties, no $ref) so passing them is
// safe; schema-less JSON providers (OpenAI-compatible/Ollama) ignore the
// schema and use plain json_object mode, and the llmjson salvage ladder still
// parses every response.
//
// The enums are registry-sourced (severity ladder, specialist + PR-agent
// names) so the schemas can never drift from the contracts.

// jsonObject is a tiny builder for the OpenAPI-subset schema fragments so the
// per-agent schema functions stay readable.
func jsonObject(props map[string]any, required ...string) map[string]any {
	obj := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		obj["required"] = required
	}
	return obj
}

func stringSchema() map[string]any  { return map[string]any{"type": "string"} }
func integerSchema() map[string]any { return map[string]any{"type": "integer"} }

func enumSchema(values []string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

func arraySchema(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

func mustSchema(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		// The schemas are static, closed-over literals; a marshal error would
		// be a programming bug, not a runtime condition. Fail closed to an
		// empty schema so the caller simply falls back to schema-less JSON
		// mode rather than crashing a review.
		return nil
	}
	return b
}

// findingItemProps returns the properties shared by every code-specialist
// finding. side/severity are the only enum-constrained fields; severity is
// registry-sourced.
func findingItemProps() map[string]any {
	return map[string]any{
		"path":           stringSchema(),
		"line":           integerSchema(),
		"side":           enumSchema([]string{"LEFT", "RIGHT"}),
		"severity":       enumSchema(severityStrings()),
		"comment":        stringSchema(),
		"suggestion":     stringSchema(),
		"anchor_excerpt": stringSchema(),
	}
}

// specialistSchema is the schema for a code specialist's output
// (specialistJSON: summary + findings with the full inline-suggestion fields).
var specialistSchema = sync.OnceValue(func() json.RawMessage {
	return mustSchema(jsonObject(map[string]any{
		"summary": stringSchema(),
		"findings": arraySchema(jsonObject(findingItemProps(),
			"path", "line", "severity", "comment")),
	}, "summary", "findings"))
})

// prAgentSchema is the slim schema for a whole-PR agent's output: the same
// finding shape MINUS the suggestion/anchor_excerpt fields the slim contract
// drops, so the model is not even offered them.
var prAgentSchema = sync.OnceValue(func() json.RawMessage {
	return mustSchema(jsonObject(map[string]any{
		"summary": stringSchema(),
		"findings": arraySchema(jsonObject(map[string]any{
			"path":     stringSchema(),
			"line":     integerSchema(),
			"side":     enumSchema([]string{"LEFT", "RIGHT"}),
			"severity": enumSchema(severityStrings()),
			"comment":  stringSchema(),
		}, "path", "line", "severity", "comment")),
	}, "summary", "findings"))
})

// vibeCoachSchema is the schema for the vibe coach's output (vibeCoachJSON).
// The finding_refs.specialist enum is registry-sourced (AllSpecialists +
// AllPRAgents), the same set the contract's textual enum uses.
var vibeCoachSchema = sync.OnceValue(func() json.RawMessage {
	names := make([]string, 0, len(AllSpecialists)+len(AllPRAgents))
	names = append(names, AllSpecialists...)
	names = append(names, AllPRAgents...)
	findingRef := jsonObject(map[string]any{
		"specialist": enumSchema(names),
		"path":       stringSchema(),
		"line":       integerSchema(),
		"side":       enumSchema([]string{"LEFT", "RIGHT"}),
	}, "specialist")
	prompt := jsonObject(map[string]any{
		"title":        stringSchema(),
		"rationale":    stringSchema(),
		"agent_prompt": stringSchema(),
		"finding_refs": arraySchema(findingRef),
	}, "agent_prompt")
	return mustSchema(jsonObject(map[string]any{
		"verdict": enumSchema([]string{"approve", "request_changes", "comment"}),
		"summary": stringSchema(),
		"prompts": arraySchema(prompt),
	}, "verdict", "summary", "prompts"))
})

// arbiterSchema is the schema for the repo arbiter's output (repoArbiterJSON).
// Suppress/demote refs carry the registry-sourced severity enum on their
// from/to fields.
var arbiterSchema = sync.OnceValue(func() json.RawMessage {
	suppressRef := jsonObject(map[string]any{
		"specialist": stringSchema(),
		"path":       stringSchema(),
		"line":       integerSchema(),
		"side":       stringSchema(),
		"reason":     stringSchema(),
	}, "specialist")
	demoteRef := jsonObject(map[string]any{
		"specialist": stringSchema(),
		"path":       stringSchema(),
		"line":       integerSchema(),
		"side":       stringSchema(),
		"from":       enumSchema(severityStrings()),
		"to":         enumSchema(severityStrings()),
		"reason":     stringSchema(),
	}, "specialist")
	return mustSchema(jsonObject(map[string]any{
		"user_summary":      stringSchema(),
		"rationale_bullets": arraySchema(stringSchema()),
		"verdict_override":  stringSchema(),
		"summary_mode":      enumSchema([]string{"none", "append", "replace"}),
		"summary_text":      stringSchema(),
		"suppress":          arraySchema(suppressRef),
		"demote":            arraySchema(demoteRef),
	}))
})

// AgentSchemas returns the per-agent JSON schema for every built-in JSON stage,
// keyed by the agent name (code specialists, PR agents, "vibe-coach",
// "repo-arbiter"). It is the registry-derived schema catalogue R5 wires into
// each JSON stage's native-JSON request. User-defined code specialists share
// the specialist schema; user-defined PR-wide agents share the slim schema.
func AgentSchemas() map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(AllSpecialists)+len(AllPRAgents)+2)
	for _, n := range AllSpecialists {
		out[n] = specialistSchema()
	}
	for _, n := range AllPRAgents {
		out[n] = prAgentSchema()
	}
	out[SpecVibeCoach] = vibeCoachSchema()
	out[specRepoArbiter] = arbiterSchema()
	return out
}

// schemaForAgent returns the JSON schema to constrain a stage's output. It
// consults the registry so a user-defined spec gets the schema for its Kind
// (code → specialist schema, pr-wide → slim schema); unknown names fall back
// to the specialist schema. Named agents (vibe-coach, arbiter) are handled by
// their own call sites, which pass their schema explicitly.
func schemaForAgent(name string) json.RawMessage {
	if s, ok := lookupSpec(name); ok && s.Kind == KindPRWide {
		return prAgentSchema()
	}
	return specialistSchema()
}
