package techagents

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
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/review/repocontext"
)

//go:embed all:prompts
var promptFS embed.FS

// HistoryFetcher returns a markdown review-history digest for owner/repo
// (capped to maxBytes). Pass gh.BuildReviewHistoryDigest. May be nil — in
// which case the generator skips the history section.
type HistoryFetcher func(ctx context.Context, owner, repo string, prLimit, maxBytes int) (string, error)

// GenerateOpts collects inputs for a single tech-agent regeneration. Tech
// is the canonical key (caller-normalised or not — Generate canonicalises
// again). Label is the user-facing display name; Seed is the user's short
// description that primes the LLM ("Kestra workflow engine, YAML-based").
type GenerateOpts struct {
	AICfg    *aiconfig.Config
	RC       *repoconfig.Config
	Owner    string
	Repo     string
	Worktree string // optional; LocalRoot or temp dir is used when empty
	Tech     string
	Label    string
	Seed     string
	Complete ai.CompleteFunc
	History  HistoryFetcher // optional
}

// Generate runs the tech-expert generator and returns a populated Agent.
// It does NOT persist; caller calls SaveAgent. SourceHash captures the
// inputs that fed the LLM so the freshness UI can spot drift.
func Generate(ctx context.Context, opts GenerateOpts) (*Agent, error) {
	if opts.Complete == nil {
		return nil, fmt.Errorf("techagents.Generate: Complete is required")
	}
	if opts.AICfg == nil {
		return nil, fmt.Errorf("techagents.Generate: AICfg is required")
	}
	tech := CanonicalTech(opts.Tech)
	if tech == "" {
		return nil, fmt.Errorf("techagents.Generate: empty tech")
	}
	owner := strings.ToLower(strings.TrimSpace(opts.Owner))
	repo := strings.ToLower(strings.TrimSpace(opts.Repo))
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("techagents.Generate: empty owner/repo")
	}
	label := strings.TrimSpace(opts.Label)
	if label == "" {
		label = strings.TrimSpace(opts.Tech)
	}
	if label == "" {
		label = tech
	}
	seed := strings.TrimSpace(opts.Seed)

	rc := opts.RC
	if rc == nil {
		rc = repoconfig.Default()
	}

	worktree := strings.TrimSpace(opts.Worktree)
	cleanupTmp := ""
	if worktree == "" {
		worktree = rc.LocalRootFor(owner, repo)
	}
	if worktree == "" {
		tmp, err := os.MkdirTemp("", "appr-ai-sal-techagent-")
		if err != nil {
			return nil, fmt.Errorf("create temp worktree: %w", err)
		}
		worktree = tmp
		cleanupTmp = tmp
	}
	if cleanupTmp != "" {
		defer os.RemoveAll(cleanupTmp)
	}

	bundle, _ := repocontext.Build(ctx, repocontext.Options{
		Worktree:  worktree,
		LocalRoot: rc.LocalRootFor(owner, repo),
		MaxBytes:  rc.MaxBytes,
	})

	historyDigest := ""
	if opts.History != nil {
		prLimit := rc.RepoExpertReviewPRs
		if prLimit < 1 {
			prLimit = 8
		}
		maxB := rc.RepoExpertMaxBytes
		if maxB < 1024 {
			maxB = 12000
		}
		hctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		dig, err := opts.History(hctx, owner, repo, prLimit, maxB)
		cancel()
		if err == nil {
			historyDigest = strings.TrimSpace(dig)
		}
	}

	system, err := loadGeneratorPrompt()
	if err != nil {
		return nil, err
	}
	user := buildGeneratorUserPrompt(tech, label, seed, owner, repo, bundle, historyDigest)

	out, err := opts.Complete(ctx, opts.AICfg, system, user, worktree)
	if err != nil {
		return nil, fmt.Errorf("complete %s tech agent: %w", tech, err)
	}
	body := strings.TrimSpace(out)
	if body == "" {
		return nil, fmt.Errorf("techagents.Generate %s: empty model output", tech)
	}

	return &Agent{
		Tech:        tech,
		Label:       label,
		Seed:        seed,
		Context:     body,
		GeneratedAt: time.Now().UTC(),
		Manual:      false,
		Provider:    string(opts.AICfg.Provider),
		Model:       opts.AICfg.AIModelOrDefault(),
		SourceHash:  agentstore.SourceHash(tech, seed, bundle, historyDigest),
	}, nil
}

func loadGeneratorPrompt() (string, error) {
	return agentstore.LoadPrompt(promptFS, "prompts/tech-generator.md", "tech-generator.md")
}

// PromptOverridePath is where users may write a custom generator prompt
// to replace the embedded one. Single override (not per-tech) since one
// generator handles every technology.
func PromptOverridePath() string {
	return agentstore.PromptOverridePath("tech-generator.md")
}

func buildGeneratorUserPrompt(tech, label, seed, owner, repo, bundle, history string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Repository: %s/%s\n", owner, repo)
	fmt.Fprintf(&b, "Technology: %s\n", label)
	if tech != strings.ToLower(label) {
		fmt.Fprintf(&b, "Canonical key: %s\n", tech)
	}
	b.WriteString("\n")
	b.WriteString("You are extracting a tight technology-specific brief for **")
	b.WriteString(label)
	b.WriteString("** as it is used in this repository. The brief will be injected verbatim into every code-review specialist's prompt for every PR against this repo, so reviewers can spot tech-specific idioms and pitfalls. Ground every claim in the inputs below; if a section is empty, say so explicitly rather than inventing context.\n\n")

	b.WriteString("## User-supplied seed\n\n")
	if seed == "" {
		b.WriteString("_(no seed description was provided — infer the technology's purpose from the name alone and from the repository convention bundle below.)_\n\n")
	} else {
		b.WriteString(seed)
		b.WriteString("\n\n")
	}

	b.WriteString("## Repository convention bundle (auto-harvested)\n\n")
	if strings.TrimSpace(bundle) == "" {
		b.WriteString("_(no convention files were found under the worktree.)_\n\n")
	} else {
		b.WriteString(bundle)
		b.WriteString("\n\n")
	}

	b.WriteString("## Past PR review digest (optional)\n\n")
	if strings.TrimSpace(history) == "" {
		b.WriteString("_(no review history was provided.)_\n\n")
	} else {
		b.WriteString(history)
		b.WriteString("\n\n")
	}

	b.WriteString("## Output\n\n")
	b.WriteString("Return **markdown only** (no JSON, no surrounding code fence). Keep it tight: 200–600 words, scannable bullets and short subsections. Do **not** restate generic ")
	b.WriteString(label)
	b.WriteString(" tutorials — the reviewer already knows the basics. Focus on **how this repo uses ")
	b.WriteString(label)
	b.WriteString("** so the specialist can flag issues that matter here without crying wolf about idiomatic usage. End at the last bullet.\n")
	return b.String()
}
