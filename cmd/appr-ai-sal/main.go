// Command appr-ai-sal is a TUI for running specialist AI reviews on GitHub PRs.
//
// It pulls PRs where the current user has been requested as a reviewer (via the
// gh CLI), runs a panel of specialist AI reviewers over the changed code (Claude
// CLI, Gemini, or OpenAI-compatible HTTP backends), and lets the user edit and
// post the review with a keypress.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/applog"
	"github.com/madicen/appr-ai-sal/internal/evals"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/theme"
	"github.com/madicen/appr-ai-sal/internal/tui"
)

// version is the release identifier, overridden at build time by goreleaser
// via -ldflags "-X main.version={{.Version}}". Defaults to "dev" for local
// `go run` / `go build` invocations.
var version = "dev"

func main() {
	// Bare `version` subcommand (mirrors the repo-context subcommand sniff).
	if len(os.Args) >= 2 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "repo-context" {
		ctx := context.Background()
		if err := review.RunRepoContextCLI(ctx, os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "appr-ai-sal: %v\n", err)
			os.Exit(1)
		}
		return
	}
	// `evals` runs the prompt-quality regression harness (Q4). It is a
	// developer/CI subcommand, not part of the interactive TUI flow.
	if len(os.Args) >= 2 && os.Args[1] == "evals" {
		ctx := context.Background()
		if err := evals.RunCLI(ctx, os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "appr-ai-sal: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "appr-ai-sal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	dryRun := flag.Bool("dry-run", false, "run agents and show GitHub payloads but do not post to GitHub")
	aiProvider := flag.String("ai-provider", "", "AI backend: claude, gemini, ollama, openai_compatible (overrides env / config file)")
	aiBaseURL := flag.String("ai-base-url", "", "HTTP base URL for gemini / ollama / openai_compatible")
	aiModel := flag.String("ai-model", "", "Model id for the selected provider (Claude: also see APPR_AI_SAL_MODEL)")
	aiAPIKey := flag.String("ai-api-key", "", "API key for HTTP providers (prefer env APPR_AI_SAL_AI_API_KEY)")
	aiTimeout := flag.Int("ai-timeout-sec", -1, "Timeout in seconds for AI HTTP calls and overall review context (default 300)")
	reviewStrictness := flag.String("review-strictness", "", "Review intensity: critical_only | lenient | balanced | strict (overrides env / config)")
	demoMode := flag.Bool("demo", false, "run in self-contained demo mode with mock services (for VHS screenshots / GIFs)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	// TUI apps cannot log to stderr (it corrupts the alt-screen), so route
	// structured diagnostics to a file. Failure to open the log is
	// non-fatal — the app still runs, just without a log.
	if err := applog.Init(version); err != nil {
		fmt.Fprintf(os.Stderr, "appr-ai-sal: logging disabled: %v\n", err)
	}

	dry := *dryRun
	if os.Getenv("APPR_AI_SAL_DRY") == "1" {
		dry = true
	}

	// Demo mode runs end-to-end against canned data so VHS can record
	// reproducible GIFs without touching gh / the network / the user's
	// real config or cache. We isolate config + cache to a fresh temp
	// directory so the recording can never overwrite the user's real
	// state, and force lipgloss into TrueColor so colours render even
	// when VHS reports a colourless TTY.
	if *demoMode {
		if err := configureDemoEnv(); err != nil {
			return fmt.Errorf("demo setup: %w", err)
		}
		// In demo mode --dry-run is implied so any synthetic post path
		// surfaces the inline payload preview rather than (silently)
		// failing on a missing real gh client.
		dry = true
	}

	aiCfg, err := aiconfig.Load()
	if err != nil {
		return fmt.Errorf("AI config: %w", err)
	}
	if err := aiCfg.MergeFlags(strings.TrimSpace(*aiProvider), strings.TrimSpace(*aiBaseURL), strings.TrimSpace(*aiModel), strings.TrimSpace(*aiAPIKey), strings.TrimSpace(*reviewStrictness), *aiTimeout); err != nil {
		return err
	}

	// Record the resolved AI config for diagnosability (keys masked — the
	// real key is only ever written by aiconfig.Save at 0600).
	applog.Debug("ai config resolved", "config", aiCfg.RedactedJSON())

	// R8: surface provider-specific misconfiguration early (base URL for
	// openai_compatible, a key for gemini, the claude CLI on PATH, well-formed
	// URLs) instead of failing at the first inference call. Non-fatal at
	// startup — the user can fix it in the settings tab — so we log a warning
	// rather than aborting; a review that truly cannot proceed still fails
	// loudly at run time.
	if err := aiCfg.ValidateForProvider(); err != nil {
		applog.Warn("active AI profile is not fully configured", "err", err.Error())
	}

	// Apply any user-saved theme overrides before the TUI renders its first
	// frame so colour-keyed rows match the user's palette from the start.
	if t, err := theme.Load(); err == nil && t != nil {
		theme.Apply(t)
	}

	// Quick auth sanity check before launching the UI so failures surface
	// with a readable message rather than an empty list. Skipped in demo
	// mode — the demo data layer never invokes the gh CLI.
	if !*demoMode {
		if err := gh.CheckAuth(); err != nil {
			return fmt.Errorf("gh auth check failed: %w\n\nRun `gh auth login` and try again.", err)
		}
	}

	defer tui.FlushMouse()

	mouseYAdj := 0
	if v := strings.TrimSpace(os.Getenv("APPR_AI_SAL_MOUSE_Y_ADJUST")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			mouseYAdj = n
		}
	}

	model := tui.New(tui.Options{
		DryRun:       dry,
		AIConfig:     aiCfg,
		DebugMouse:   os.Getenv("APPR_AI_SAL_DEBUG_MOUSE") == "1",
		MouseYAdjust: mouseYAdj,
		Demo:         *demoMode,
	})
	prog := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := prog.Run(); err != nil {
		return err
	}
	return nil
}

// configureDemoEnv pins lipgloss to TrueColor (so VHS captures colour even
// when its embedded terminal reports no colour support) and redirects the
// app's config + cache directories to a freshly-created temp directory so
// the recording session cannot mutate the user's real config or cache.
//
// We respect APPR_AI_SAL_DEMO_DIR when set so demo fixture scripts (see
// scripts/setup-demo-fixtures.sh) can pre-seed the cache with cached
// repo-agent / lang-agent briefs before the recording starts.
func configureDemoEnv() error {
	lipgloss.SetColorProfile(termenv.TrueColor)

	demoRoot := strings.TrimSpace(os.Getenv("APPR_AI_SAL_DEMO_DIR"))
	if demoRoot == "" {
		var err error
		demoRoot, err = os.MkdirTemp("", "appr-ai-sal-demo-")
		if err != nil {
			return fmt.Errorf("mkdir demo dir: %w", err)
		}
	}
	cfgDir := filepath.Join(demoRoot, "config")
	cacheDir := filepath.Join(demoRoot, "cache")
	for _, d := range []string{cfgDir, cacheDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	if err := os.Setenv("APPR_AI_SAL_CONFIG_DIR", cfgDir); err != nil {
		return err
	}
	if err := os.Setenv("APPR_AI_SAL_CACHE_DIR", cacheDir); err != nil {
		return err
	}
	return nil
}
