package evals

import (
	"context"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

// buildEvalInput projects a corpus Case into a review.EvalInput.
func buildEvalInput(c Case) review.EvalInput {
	m := c.Meta
	repo := m.Repo
	owner, name := splitRepo(repo)
	pr := &gh.PR{
		Number:       m.Number,
		Title:        m.Title,
		Repository:   repo,
		Owner:        owner,
		Repo:         name,
		Author:       m.Author,
		Body:         m.Body,
		BaseRef:      firstNonEmpty(m.BaseRef, "main"),
		HeadRef:      firstNonEmpty(m.HeadRef, "feature"),
		Additions:    m.Additions,
		Deletions:    m.Deletions,
		ChangedFiles: m.ChangedFiles,
	}
	return review.EvalInput{
		PR:             pr,
		Diff:           c.Diff,
		RepoContext:    c.RepoContext,
		PerAgentBriefs: c.Briefs,
		Evidence:       c.Evidence,
		LangSection:    c.LangSection,
		TechSection:    c.TechSection,
		TechConfigured: m.TechConfigured,
		RunPRAgents:    m.RunPRAgents,
		RunWitness:     m.RunWitness,
		RunArbiter:     m.RunArbiter,
		RunVibeCoach:   m.RunVibeCoach,
	}
}

// caseConfig clones cfg and applies the case's strictness override.
func caseConfig(cfg *aiconfig.Config, c Case) *aiconfig.Config {
	rc := cfg.Clone()
	if s := strings.TrimSpace(c.Meta.Strictness); s != "" {
		if rs, err := aiconfig.ParseReviewStrictness(s); err == nil {
			rc.ReviewStrictness = rs
		}
	}
	return rc
}

// RunCase runs one case through the review pipeline with cfg's provider and
// scores it. The provider is whatever cfg selects (a live backend for
// `make evals`, or a ReplayProvider injected by tests via
// ai.SetBaseProviderForTest).
func RunCase(ctx context.Context, cfg *aiconfig.Config, c Case) CaseScore {
	cs, _ := runCase(ctx, cfg, c, "")
	return cs
}

func runCase(ctx context.Context, cfg *aiconfig.Config, c Case, dumpDir string) (CaseScore, error) {
	rc := caseConfig(cfg, c)
	obs := review.EvalRun(ctx, rc, buildEvalInput(c))
	cs := ScoreCase(c, obs)
	// Dump after scoring so a write failure never changes CaseScore.
	return cs, writeCaseDumps(dumpDir, rc, c, obs)
}

// RunCorpus runs every case and returns a labelled CorpusScore. label is the
// A/B slot ("A"/"B") or "" for a single run.
func RunCorpus(ctx context.Context, cfg *aiconfig.Config, cases []Case, label string) CorpusScore {
	cs, _ := runCorpus(ctx, cfg, cases, label, "")
	return cs
}

func runCorpus(ctx context.Context, cfg *aiconfig.Config, cases []Case, label, dumpDir string) (CorpusScore, error) {
	out := CorpusScore{
		Provider: string(cfg.Provider),
		Model:    cfg.AIModelOrDefault(),
		Label:    label,
	}
	var dumpErr error
	for _, c := range cases {
		cs, err := runCase(ctx, cfg, c, dumpDir)
		out.Cases = append(out.Cases, cs)
		if err != nil && dumpErr == nil {
			dumpErr = err
		}
	}
	return out, dumpErr
}

// RunCorpusReplay runs every case against its own deterministic
// ReplayProvider — no provider config, no network. It is how `make evals
// --replay` (and the nightly CI job) produce a real, reproducible report
// offline: the corpus's canned model outputs drive the whole pipeline, so the
// report reflects the gates + scorer exactly, just not a live model's quality.
//
// It installs the base-provider test hook per case; that hook is process-wide
// and not concurrency-safe, so this runs the corpus sequentially and must not
// be called from multiple goroutines.
func RunCorpusReplay(ctx context.Context, cases []Case) CorpusScore {
	cs, _ := runCorpusReplay(ctx, cases, "")
	return cs
}

func runCorpusReplay(ctx context.Context, cases []Case, dumpDir string) (CorpusScore, error) {
	out := CorpusScore{Provider: "replay", Model: "replay"}
	cfg := aiconfig.DefaultConfig()
	cfg.Provider = aiconfig.Provider("replay")
	cfg.Model = "replay"
	var dumpErr error
	for _, c := range cases {
		restore := ai.SetBaseProviderForTest(func(*aiconfig.Config) (ai.Provider, error) {
			return NewReplayProvider(c), nil
		})
		cs, err := runCase(ctx, cfg, c, dumpDir)
		out.Cases = append(out.Cases, cs)
		restore()
		if err != nil && dumpErr == nil {
			dumpErr = err
		}
	}
	return out, dumpErr
}

func splitRepo(repo string) (owner, name string) {
	if i := strings.Index(repo, "/"); i >= 0 {
		return repo[:i], repo[i+1:]
	}
	return "", repo
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
