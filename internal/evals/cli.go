package evals

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// RunCLI is the `appr-ai-sal evals` subcommand entrypoint (invoked by
// `make evals`). It loads the corpus, selects a provider through the SAME
// config path as a normal review (aiconfig.Load + MergeFlags, honouring
// PROVIDER / the --ai-* flags), runs the corpus, and writes a markdown report.
//
// Provider selection is intentionally the normal one: `make evals PROVIDER=ollama`
// exports APPR_AI_SAL_AI_PROVIDER=ollama, which aiconfig.Load picks up. When no
// provider is configured — or the active profile fails validation (e.g. the
// claude CLI is absent, or gemini has no key) — the command SKIPS with a clear
// message and exit 0, so a nightly/manual CI job never fails for lack of a live
// model.
//
// A/B mode: passing --prompts-a and --prompts-b (each a config dir containing a
// prompts/ override subdir) runs the corpus twice, once with each override set
// active (via APPR_AI_SAL_CONFIG_DIR — the existing prompt-override mechanism),
// and emits a delta report. Only meaningful against a live model, since the
// deterministic ReplayProvider ignores prompt text.
func RunCLI(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("evals", flag.ContinueOnError)
	var (
		provider   = fs.String("provider", "", "AI backend (claude|gemini|ollama|openai_compatible); also honoured via PROVIDER / APPR_AI_SAL_AI_PROVIDER")
		model      = fs.String("model", "", "model id for the selected provider")
		baseURL    = fs.String("base-url", "", "HTTP base URL for gemini/ollama/openai_compatible")
		strictness = fs.String("strictness", "", "default review strictness when a case does not pin one")
		out        = fs.String("out", "", "write the markdown report here (default: stdout)")
		promptsA   = fs.String("prompts-a", "", "config dir with a prompts/ override set for A/B slot A")
		promptsB   = fs.String("prompts-b", "", "config dir with a prompts/ override set for A/B slot B")
		force      = fs.Bool("force", false, "run even if the provider looks unconfigured (do not skip)")
		replay     = fs.Bool("replay", false, "run offline against the corpus's canned outputs (deterministic; no provider/network) — exercises the gates + scorer + report")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	// PROVIDER= is the make-friendly alias for --provider; map it onto the env
	// var aiconfig already reads so selection goes through the normal path.
	if p := strings.TrimSpace(os.Getenv("PROVIDER")); p != "" && os.Getenv("APPR_AI_SAL_AI_PROVIDER") == "" {
		_ = os.Setenv("APPR_AI_SAL_AI_PROVIDER", p)
	}

	cases, err := LoadCorpus()
	if err != nil {
		return fmt.Errorf("load corpus: %w", err)
	}

	// Offline replay: no provider selection, no skip logic — drive the
	// pipeline from the corpus's canned outputs and emit the report.
	if *replay {
		w, closeFn, err := openOut(*out)
		if err != nil {
			return err
		}
		defer closeFn()
		fmt.Fprint(w, RenderReport(RunCorpusReplay(ctx, cases)))
		return nil
	}

	cfg, err := aiconfig.Load()
	if err != nil {
		return fmt.Errorf("ai config: %w", err)
	}
	if err := cfg.MergeFlags(strings.TrimSpace(*provider), strings.TrimSpace(*baseURL), strings.TrimSpace(*model), "", strings.TrimSpace(*strictness), -1); err != nil {
		return err
	}

	if !*force {
		if reason, skip := skipReason(cfg); skip {
			fmt.Fprintf(os.Stderr, "evals: skipping — %s\n", reason)
			fmt.Fprintln(os.Stderr, "evals: (set a provider, e.g. `make evals PROVIDER=ollama`, or pass --force to run anyway)")
			return nil
		}
	}

	w, closeFn, err := openOut(*out)
	if err != nil {
		return err
	}
	defer closeFn()

	if strings.TrimSpace(*promptsA) != "" && strings.TrimSpace(*promptsB) != "" {
		runA := runWithPromptDir(ctx, cfg, cases, "A", *promptsA)
		runB := runWithPromptDir(ctx, cfg, cases, "B", *promptsB)
		fmt.Fprint(w, RenderABReport(runA, runB))
		return nil
	}

	report := RunCorpus(ctx, cfg, cases, "")
	fmt.Fprint(w, RenderReport(report))
	return nil
}

// runWithPromptDir points the prompt-override mechanism at dir (by setting
// APPR_AI_SAL_CONFIG_DIR) for the duration of one corpus run, then restores the
// previous value. This is how the A/B mode activates an alternate prompt set.
func runWithPromptDir(ctx context.Context, cfg *aiconfig.Config, cases []Case, label, dir string) CorpusScore {
	prev, had := os.LookupEnv("APPR_AI_SAL_CONFIG_DIR")
	_ = os.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)
	defer func() {
		if had {
			_ = os.Setenv("APPR_AI_SAL_CONFIG_DIR", prev)
		} else {
			_ = os.Unsetenv("APPR_AI_SAL_CONFIG_DIR")
		}
	}()
	return RunCorpus(ctx, cfg, cases, label)
}

// skipReason reports whether the run should skip for lack of a usable provider,
// and why. A provider explicitly selected via env/flag that then fails
// validation is still a skip (not an error) so CI stays green without a model.
func skipReason(cfg *aiconfig.Config) (string, bool) {
	if os.Getenv("APPR_AI_SAL_AI_PROVIDER") == "" && cfg.Provider == aiconfig.ProviderClaude && cfg.Model == "" {
		// The default (claude, no explicit selection) — only run if the CLI is
		// actually present; ValidateForProvider covers that below.
	}
	if err := cfg.ValidateForProvider(); err != nil {
		return "provider not configured: " + err.Error(), true
	}
	return "", false
}

func openOut(path string) (io.Writer, func(), error) {
	if strings.TrimSpace(path) == "" {
		return os.Stdout, func() {}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("create %s: %w", path, err)
	}
	return f, func() { _ = f.Close() }, nil
}
