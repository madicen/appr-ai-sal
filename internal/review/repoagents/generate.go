package repoagents

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/review/repocontext"
)

//go:embed all:prompts
var promptFS embed.FS

// CompleteFunc runs LLM inference. Callers pass review.Complete here; the
// indirection avoids an import cycle (review needs to import repoagents to
// load briefs at review time).
type CompleteFunc func(ctx context.Context, cfg *aiconfig.Config, system, user, worktree string) (string, error)

// HistoryFetcher returns a markdown review-history digest for owner/repo
// (capped to maxBytes). Pass gh.BuildReviewHistoryDigest. May be nil — in
// which case the generator skips the history section.
type HistoryFetcher func(ctx context.Context, owner, repo string, prLimit, maxBytes int) (string, error)

// PathHistoryFetcher returns a markdown bullet block summarising recent
// merged PRs across the repo (or under specific paths). Used by the testing
// and docs generators to evidence whether prior PRs added tests/docs. May
// be nil; the section is then skipped.
type PathHistoryFetcher func(ctx context.Context, owner, repo string) (string, error)

// GenerateOpts collects inputs for a single agent regeneration.
type GenerateOpts struct {
	AICfg     *aiconfig.Config
	RC        *repoconfig.Config
	Owner     string
	Repo      string
	Worktree  string // optional; LocalRoot or temp dir is used when empty
	Specialist string
	Complete  CompleteFunc
	History   HistoryFetcher // optional
	// PathHistory returns repo-wide PR-touch evidence (testing and docs only).
	// Skipped when nil or specialist is not "testing" / "docs".
	PathHistory PathHistoryFetcher
}

// Generate runs the per-topic generator agent and returns a populated Agent.
// It does NOT persist; caller calls SaveAgent. Generation is idempotent for a
// given input set (SourceHash captures the inputs that fed the LLM).
func Generate(ctx context.Context, opts GenerateOpts) (*Agent, error) {
	if opts.Complete == nil {
		return nil, fmt.Errorf("repoagents.Generate: Complete is required")
	}
	if opts.AICfg == nil {
		return nil, fmt.Errorf("repoagents.Generate: AICfg is required")
	}
	specialist := strings.ToLower(strings.TrimSpace(opts.Specialist))
	if !IsKnownSpecialist(specialist) {
		return nil, fmt.Errorf("repoagents.Generate: unknown specialist %q", opts.Specialist)
	}
	owner := strings.ToLower(strings.TrimSpace(opts.Owner))
	repo := strings.ToLower(strings.TrimSpace(opts.Repo))
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("repoagents.Generate: empty owner/repo")
	}

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
		tmp, err := os.MkdirTemp("", "appr-ai-sal-repoagent-")
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

	// Repo evidence section: only computed for testing and docs agents,
	// since those are the specialties whose briefs benefit from concrete
	// "does this repo actually test/doc things?" numbers. Other agents see
	// no behavior change.
	repoEvidence := ""
	pathHistory := ""
	if rc.IncludeRepoEvidence && (specialist == "testing" || specialist == "docs") {
		if rwe, err := repocontext.BuildRepoWideEvidence(ctx, repocontext.RepoWideEvidenceOptions{Worktree: worktree}); err == nil {
			repoEvidence = repocontext.FormatRepoWideEvidenceMarkdown(rwe)
		}
		if opts.PathHistory != nil {
			hctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			ph, err := opts.PathHistory(hctx, owner, repo)
			cancel()
			if err == nil {
				pathHistory = strings.TrimSpace(ph)
			}
		}
	}

	system, err := loadGeneratorPrompt(specialist)
	if err != nil {
		return nil, err
	}
	user := buildGeneratorUserPrompt(specialist, owner, repo, bundle, historyDigest, repoEvidence, pathHistory)

	out, err := opts.Complete(ctx, opts.AICfg, system, user, worktree)
	if err != nil {
		return nil, fmt.Errorf("complete %s repo agent: %w", specialist, err)
	}
	body := strings.TrimSpace(out)
	if body == "" {
		return nil, fmt.Errorf("repoagents.Generate %s: empty model output", specialist)
	}

	agent := &Agent{
		Specialist:  specialist,
		Context:     body,
		GeneratedAt: time.Now().UTC(),
		Manual:      false,
		Provider:    string(opts.AICfg.Provider),
		Model:       opts.AICfg.AIModelOrDefault(),
		SourceHash:  sourceHash(bundle, historyDigest, repoEvidence, pathHistory),
	}
	return agent, nil
}

func sourceHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

func loadGeneratorPrompt(specialist string) (string, error) {
	if override, ok, err := readPromptOverride(specialist); err != nil {
		return "", err
	} else if ok {
		return override, nil
	}
	name := "prompts/repo-agent-" + specialist + ".md"
	b, err := fs.ReadFile(promptFS, name)
	if err != nil {
		return "", fmt.Errorf("load repo-agent prompt %q: %w", specialist, err)
	}
	return string(b), nil
}

// PromptOverridePath is where users may write a custom generator prompt to
// replace the embedded one for a specialist.
func PromptOverridePath(specialist string) string {
	return filepath.Join(configDir(), "prompts", "repo-agent-"+strings.ToLower(strings.TrimSpace(specialist))+".md")
}

func readPromptOverride(specialist string) (string, bool, error) {
	p := PromptOverridePath(specialist)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read override %s: %w", p, err)
	}
	return string(b), true, nil
}

func configDir() string {
	if v := os.Getenv("APPR_AI_SAL_CONFIG_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "appr-ai-sal")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".appr-ai-sal"
	}
	return filepath.Join(home, ".config", "appr-ai-sal")
}

func buildGeneratorUserPrompt(specialist, owner, repo, bundle, history, repoEvidence, pathHistory string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Repository: %s/%s\n", owner, repo)
	fmt.Fprintf(&b, "Topic: %s\n\n", specialist)
	b.WriteString("You are extracting a tight repo-specific brief that will be injected into the **")
	b.WriteString(specialist)
	b.WriteString("** specialist's prompt for every code review against this repository. Ground every claim in the inputs below; if a section is empty, say so.\n\n")

	b.WriteString("## Repository convention bundle (auto-harvested)\n\n")
	if strings.TrimSpace(bundle) == "" {
		b.WriteString("_(no convention files were found under the worktree.)_\n\n")
	} else {
		b.WriteString(bundle)
		b.WriteString("\n\n")
	}

	if specialist == "testing" || specialist == "docs" {
		b.WriteString("## Repo evidence (auto-harvested)\n\n")
		if strings.TrimSpace(repoEvidence) == "" && strings.TrimSpace(pathHistory) == "" {
			b.WriteString("_(no static evidence was harvested.)_\n\n")
		} else {
			if strings.TrimSpace(repoEvidence) != "" {
				b.WriteString(repoEvidence)
				b.WriteString("\n")
			}
			if strings.TrimSpace(pathHistory) != "" {
				b.WriteString(pathHistory)
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## Past PR review digest (optional)\n\n")
	if strings.TrimSpace(history) == "" {
		b.WriteString("_(no review history was provided.)_\n\n")
	} else {
		b.WriteString(history)
		b.WriteString("\n\n")
	}

	b.WriteString("## Output\n\n")
	b.WriteString("Return **markdown only** (no JSON, no fences around the whole output). Keep it tight: 200–600 words, scannable bullets and short subsections. Do **not** restate the diff (there is none); the only consumer of this brief is a code reviewer who needs to know how *this* repo handles ")
	b.WriteString(specialist)
	b.WriteString(" so they can avoid filing findings that conflict with established convention.\n")
	return b.String()
}
