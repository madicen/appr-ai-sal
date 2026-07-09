package review

import (
	"context"
	"fmt"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/applog"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/llmjson"
)

// intent.go implements the Q8 PR-author intent extraction pre-pass.
//
// A single cheap LLM call runs at the very start of a review — before the
// specialists / PR-agents — over the PR description PLUS any linked issues
// (fetched via gh.GetLinkedIssues: closing keywords in the body +
// closingIssuesReferences). It produces a structured PRIntent
// {intent, acceptance_criteria, non_goals, linked_issues} that is injected as a
// `## PR author intent` section into the stages that otherwise GUESS intent
// from the title alone:
//
//   - scope     — stops inferring the change's boundaries from the title.
//   - testing   — turns acceptance_criteria into expected test cases.
//   - vibe-coach — grounds its verdict / "done-when" prompts in the stated goal.
//
// The whole pre-pass is fail-open. If there is no description AND no linked
// issue, or the fetch / model call / parse fails, RunIntentPrepass returns nil,
// FormatIntentSection(nil) returns "", and every downstream builder appends
// nothing — so a run behaves EXACTLY as it did before Q8 (proven byte-for-byte
// in intent_test.go). The pre-pass is routed as its own stage ("intent") so Q7
// can send it to a cheap model via stage_models.

// SpecIntent is the intent pre-pass agent name. It doubles as:
//   - the embedded prompt name (prompts/intent.md), and
//   - the Q7 stage_models / ForStage routing key ("intent"), so a profile can
//     route the pre-pass to a cheap model independently of the review model.
const SpecIntent = "intent"

// PRIntent is the structured author-intent object the pre-pass extracts. Every
// field is optional; an all-empty PRIntent is treated as "no intent" (nil).
type PRIntent struct {
	// Intent is a 1–2 sentence statement of what the PR is trying to achieve,
	// grounded in the description and any linked issues.
	Intent string `json:"intent"`
	// AcceptanceCriteria are the concrete conditions the author (or the linked
	// issues) state must hold for the change to be complete/correct.
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	// NonGoals are things the author explicitly says are out of scope for this
	// PR (so scope doesn't flag them and the vibe-coach doesn't demand them).
	NonGoals []string `json:"non_goals"`
	// LinkedIssues summarises each issue the PR is linked to and how it relates.
	LinkedIssues []IntentLinkedIssue `json:"linked_issues"`
}

// IntentLinkedIssue is one linked issue as summarised by the pre-pass.
type IntentLinkedIssue struct {
	Reference string `json:"reference"` // "owner/repo#123"
	Title     string `json:"title"`
	Relevance string `json:"relevance"` // one line: how it relates to this PR
}

// HasContent reports whether the intent carries any usable signal. An intent
// with nothing in any field renders no section (identical to no pre-pass).
func (in *PRIntent) HasContent() bool {
	if in == nil {
		return false
	}
	if strings.TrimSpace(in.Intent) != "" {
		return true
	}
	if len(in.AcceptanceCriteria) > 0 || len(in.NonGoals) > 0 || len(in.LinkedIssues) > 0 {
		return true
	}
	return false
}

// linkedIssuesFetcher is the gh linked-issue fetch, injectable so the review
// tests can exercise the pre-pass without a live gh / network.
var linkedIssuesFetcher = gh.GetLinkedIssues

// RunIntentPrepass runs the intent extraction pre-pass for one PR and returns
// the parsed PRIntent, or nil when there is nothing to extract or anything
// fails (fully fail-open — see the file comment). It fetches the linked issues
// itself (delegating to gh.GetLinkedIssues), builds the prompt from the
// description + issues, and makes ONE schema-backed model call routed as the
// "intent" stage (Q7).
func RunIntentPrepass(ctx context.Context, cfg *aiconfig.Config, ref gh.Ref, pr *gh.PR) *PRIntent {
	if pr == nil {
		return nil
	}
	if cfg == nil {
		cfg = aiconfig.DefaultConfig()
	}

	// Linked-issue fetch is fail-open: a fetch error just means "no issues".
	issues, err := linkedIssuesFetcher(ctx, ref, pr.Body)
	if err != nil {
		applog.Info("intent pre-pass: linked-issue fetch failed (continuing)", "ref", ref.String(), "err", err.Error())
		issues = nil
	}

	// Nothing to extract from: no description and no issues → behave as before.
	if strings.TrimSpace(pr.Body) == "" && len(issues) == 0 {
		return nil
	}

	sys, err := SpecialistPrompt(SpecIntent)
	if err != nil {
		applog.Info("intent pre-pass: prompt load failed (skipping)", "err", err.Error())
		return nil
	}

	// Q7: route the pre-pass to its configured model (stage_models["intent"] /
	// "default"); a no-op clone when unrouted.
	cfg = cfg.ForStage(SpecIntent)

	user := buildIntentUserPrompt(pr, issues)
	ictx := applog.WithStage(ctx, SpecIntent)
	out, err := completeJSONWithSchema(ictx, cfg, sys, user, "", intentSchema())
	if err != nil {
		applog.Info("intent pre-pass: model call failed (skipping)", "ref", ref.String(), "err", err.Error())
		return nil
	}
	parsed, err := llmjson.Parse[PRIntent](out)
	if err != nil {
		applog.Info("intent pre-pass: parse failed (skipping)", "ref", ref.String(), "err", err.Error())
		return nil
	}
	if !parsed.HasContent() {
		return nil
	}
	return &parsed
}

// buildIntentUserPrompt assembles the pre-pass user message: PR framing, the
// description, the fetched linked issues, and the output contract.
func buildIntentUserPrompt(pr *gh.PR, issues []gh.LinkedIssue) string {
	var b strings.Builder
	b.WriteString("Extract the pull-request author's intent from the description and linked issues below.\n\n")
	b.WriteString("PR: " + pr.Repository + "#")
	fmt.Fprintf(&b, "%d", pr.Number)
	b.WriteString("\nTitle: " + pr.Title + "\n\n")

	b.WriteString("PR description:\n")
	if strings.TrimSpace(pr.Body) != "" {
		b.WriteString(pr.Body)
	} else {
		b.WriteString("(no description provided)")
	}
	b.WriteString("\n\n")

	if len(issues) > 0 {
		fmt.Fprintf(&b, "Linked issues (%d):\n\n", len(issues))
		for _, is := range issues {
			b.WriteString("--- " + is.Ref())
			if strings.TrimSpace(is.State) != "" {
				b.WriteString(" [" + is.State + "]")
			}
			b.WriteString(" ---\n")
			b.WriteString("Title: " + is.Title + "\n")
			if body := strings.TrimSpace(is.Body); body != "" {
				b.WriteString("Body: " + truncate(body, 4000) + "\n")
			}
			b.WriteString("\n")
		}
	} else {
		b.WriteString("Linked issues: none found.\n\n")
	}

	b.WriteString(intentOutputContract)
	return b.String()
}

// FormatIntentSection renders the extracted intent as the `## PR author intent`
// section injected into the intent-aware stages. Returns "" when intent is nil
// or empty, so a stage's prompt is byte-for-byte unchanged when no intent was
// extracted (the Q8 backward-compat guarantee).
func FormatIntentSection(in *PRIntent) string {
	if !in.HasContent() {
		return ""
	}
	var b strings.Builder
	b.WriteString("## PR author intent\n\n")
	b.WriteString("The following intent was extracted from the PR description and any linked issues by a pre-pass. ")
	b.WriteString("Treat it as the authoritative statement of what the author is TRYING to do and what \"done\" means — ")
	b.WriteString("use it to calibrate your findings (do not flag something the author explicitly listed as a non-goal). ")
	b.WriteString("The unified diff remains the authority for what the PR actually changed.\n\n")

	if s := strings.TrimSpace(in.Intent); s != "" {
		b.WriteString("Intent: " + s + "\n\n")
	}
	if len(in.AcceptanceCriteria) > 0 {
		b.WriteString("Acceptance criteria:\n")
		for _, c := range in.AcceptanceCriteria {
			if s := strings.TrimSpace(c); s != "" {
				b.WriteString("- " + s + "\n")
			}
		}
		b.WriteString("\n")
	}
	if len(in.NonGoals) > 0 {
		b.WriteString("Non-goals (explicitly out of scope for this PR):\n")
		for _, g := range in.NonGoals {
			if s := strings.TrimSpace(g); s != "" {
				b.WriteString("- " + s + "\n")
			}
		}
		b.WriteString("\n")
	}
	if len(in.LinkedIssues) > 0 {
		b.WriteString("Linked issues:\n")
		for _, is := range in.LinkedIssues {
			ref := strings.TrimSpace(is.Reference)
			title := strings.TrimSpace(is.Title)
			rel := strings.TrimSpace(is.Relevance)
			line := "- "
			if ref != "" {
				line += ref
				if title != "" {
					line += " — " + title
				}
			} else if title != "" {
				line += title
			}
			if rel != "" {
				line += ": " + rel
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// intentOutputContract is the strict-JSON instruction appended to the intent
// pre-pass user prompt. The shape matches PRIntent / intentSchema.
const intentOutputContract = `Return your answer as a single JSON object and nothing else — no prose before, no prose after, no markdown fencing. The object must conform to:

{
  "intent": "<1–2 sentences: what this PR is trying to achieve, grounded in the description and linked issues. Empty string if you genuinely cannot tell.>",
  "acceptance_criteria": ["<concrete condition that must hold for the change to be complete/correct — derived from the description or a linked issue. Prefer the author's / issue's own words. Empty array if none stated.>"],
  "non_goals": ["<something the author explicitly says is OUT of scope for this PR. Empty array if none stated. Do NOT invent non-goals.>"],
  "linked_issues": [
    { "reference": "<owner/repo#number>",
      "title":     "<issue title>",
      "relevance": "<one line: how this issue relates to the PR>" }
  ]
}

Rules:
- Extract ONLY what the description and linked issues actually say. Do NOT infer requirements from the diff (you are not shown it) and do NOT invent acceptance criteria or non-goals the author never stated — an empty array is the correct answer when nothing is stated.
- Keep every string short and factual. All prose must be in English.
- "acceptance_criteria" are testable conditions ("returns 404 for an unknown id", "the migration is reversible"), not a restatement of "intent".
- Only list issues under "linked_issues" that appear in the "Linked issues" section above; use their exact reference.
- String values must be valid JSON: escape newlines as \n and quotes as \". No comments, no trailing commas, no triple-quoted strings.`
