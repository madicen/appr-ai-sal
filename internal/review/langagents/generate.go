package langagents

import (
	"context"
	"embed"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/agentstore"
	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/llmjson"
)

// promptFS embeds the generator system prompt. Only the meta-prompt
// (lang-generator.md) ships in the codebase; per-language briefs are
// LLM-authored at generation time and stored under <cache>/lang-agents/.
//
//go:embed prompts/lang-generator.md
var promptFS embed.FS

// GenerateOpts collects inputs for a single language brief regeneration.
type GenerateOpts struct {
	AICfg    *aiconfig.Config
	Language Language
	Complete ai.CompleteFunc
	// Worktree is optional; some backends (Claude) want it set so the
	// CLI runs in a writable directory. Empty is fine — a temp dir is
	// created and cleaned up automatically.
	Worktree string
	// ReferenceBrief, when non-empty, is included verbatim in the user
	// prompt as a shape reference for the LLM. The TUI passes an
	// existing cached brief here when generating a new language so the
	// model has an in-context example to mirror; first-run generation
	// passes "" and the model relies solely on the system prompt.
	ReferenceBrief string
	// ReferenceLanguage labels ReferenceBrief in the user prompt (e.g.
	// "Go", "Python"). Ignored when ReferenceBrief is empty.
	ReferenceLanguage Language
}

// Generate runs the language-brief generator agent and returns a
// populated Agent. It does NOT persist; the caller calls SaveAgent.
// Generation is idempotent for a given input set (SourceHash captures
// the inputs the LLM saw, so a regenerate on the same inputs produces
// the same hash).
func Generate(ctx context.Context, opts GenerateOpts) (*Agent, error) {
	if opts.Complete == nil {
		return nil, fmt.Errorf("langagents.Generate: Complete is required")
	}
	if opts.AICfg == nil {
		return nil, fmt.Errorf("langagents.Generate: AICfg is required")
	}
	lang := Canonical(opts.Language)
	if lang == "" {
		return nil, fmt.Errorf("langagents.Generate: unknown language %q", opts.Language)
	}

	worktree := strings.TrimSpace(opts.Worktree)
	cleanupTmp := ""
	if worktree == "" {
		tmp, err := os.MkdirTemp("", "appr-ai-sal-langagent-")
		if err != nil {
			return nil, fmt.Errorf("create temp worktree: %w", err)
		}
		worktree = tmp
		cleanupTmp = tmp
	}
	if cleanupTmp != "" {
		defer os.RemoveAll(cleanupTmp)
	}

	systemPrompt, err := loadGeneratorPrompt()
	if err != nil {
		return nil, err
	}
	userPrompt, srcHashInputs := buildGeneratorUserPrompt(lang, opts.ReferenceLanguage, opts.ReferenceBrief)

	out, err := opts.Complete(ctx, opts.AICfg, systemPrompt, userPrompt, worktree)
	if err != nil {
		return nil, fmt.Errorf("complete %s language agent: %w", lang, err)
	}
	body := strings.TrimSpace(out)
	if body == "" {
		return nil, fmt.Errorf("langagents.Generate %s: empty model output", lang)
	}
	// Be tolerant of accidental markdown fencing — strip a single
	// outer ```markdown or ``` wrapper if present, but never modify the
	// inner content. Shared with the JSON parse paths via llmjson.
	body = llmjson.StripCodeFence(body)

	agent := &Agent{
		Language:    lang,
		Context:     body,
		GeneratedAt: time.Now().UTC(),
		Manual:      false,
		Provider:    string(opts.AICfg.Provider),
		Model:       opts.AICfg.AIModelOrDefault(),
		SourceHash:  agentstore.SourceHash(srcHashInputs...),
	}
	return agent, nil
}

func loadGeneratorPrompt() (string, error) {
	return agentstore.LoadPrompt(promptFS, "prompts/lang-generator.md", "lang-generator.md")
}

// PromptOverridePath is where users may write a custom generator prompt
// to replace the embedded one.
func PromptOverridePath() string {
	return agentstore.PromptOverridePath("lang-generator.md")
}

// buildGeneratorUserPrompt assembles the user message the generator
// model sees. Returns both the rendered prompt and the slice of inputs
// that feed sourceHash (so a regenerate with identical inputs produces
// the same hash).
//
// refLang/refBody, when non-empty, embed an existing brief verbatim as
// a shape reference for the LLM. The TUI typically passes the user's
// own most-recent cached brief here so the structure stays consistent
// across the user's generated set; first-run generation passes empty
// and the model relies on the system prompt alone.
func buildGeneratorUserPrompt(lang, refLang Language, refBody string) (string, []string) {
	var b strings.Builder
	fmt.Fprintf(&b, "Target language: %s\n\n", LabelFor(lang))
	b.WriteString("Produce a language brief for **")
	b.WriteString(LabelFor(lang))
	b.WriteString("** following the shape described in your system prompt. The brief will be injected into every code-review specialist's prompt whenever a PR touches ")
	b.WriteString(LabelFor(lang))
	b.WriteString(" files.\n\n")
	hashInputs := []string{lang}
	if strings.TrimSpace(refBody) != "" {
		refLabel := LabelFor(refLang)
		if refLabel == "" {
			refLabel = "the existing brief below"
		}
		b.WriteString("## Reference brief (shape, not content)\n\n")
		fmt.Fprintf(&b, "Use the structure and depth of this %s brief as a guide. Do NOT copy its content; produce %s-specific guidance.\n\n",
			refLabel, LabelFor(lang))
		fmt.Fprintf(&b, "### Reference: %s\n\n", refLabel)
		b.WriteString(refBody)
		b.WriteString("\n\n")
		hashInputs = append(hashInputs, refLang+":"+refBody)
	}
	b.WriteString("## Output\n\n")
	b.WriteString("Return markdown only. Start at the first `## Section` heading. Do not include a top-level `# ...` title or any prose preamble.\n")
	return b.String(), hashInputs
}
