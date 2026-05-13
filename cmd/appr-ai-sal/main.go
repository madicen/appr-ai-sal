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
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/theme"
	"github.com/madicen/appr-ai-sal/internal/tui"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "repo-context" {
		ctx := context.Background()
		if err := review.RunRepoContextCLI(ctx, os.Args[2:]); err != nil {
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
	flag.Parse()
	dry := *dryRun
	if os.Getenv("APPR_AI_SAL_DRY") == "1" {
		dry = true
	}

	aiCfg, err := aiconfig.Load()
	if err != nil {
		return fmt.Errorf("AI config: %w", err)
	}
	if err := aiCfg.MergeFlags(strings.TrimSpace(*aiProvider), strings.TrimSpace(*aiBaseURL), strings.TrimSpace(*aiModel), strings.TrimSpace(*aiAPIKey), strings.TrimSpace(*reviewStrictness), *aiTimeout); err != nil {
		return err
	}

	// Apply any user-saved theme overrides before the TUI renders its first
	// frame so colour-keyed rows match the user's palette from the start.
	if t, err := theme.Load(); err == nil && t != nil {
		theme.Apply(t)
	}

	// Quick auth sanity check before launching the UI so failures surface
	// with a readable message rather than an empty list.
	if err := gh.CheckAuth(); err != nil {
		return fmt.Errorf("gh auth check failed: %w\n\nRun `gh auth login` and try again.", err)
	}

	defer tui.FlushMouse()

	mouseYAdj := 0
	if v := strings.TrimSpace(os.Getenv("APPR_AI_SAL_MOUSE_Y_ADJUST")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			mouseYAdj = n
		}
	}

	model := tui.New(tui.Options{
		DryRun:         dry,
		AIConfig:       aiCfg,
		DebugMouse:     os.Getenv("APPR_AI_SAL_DEBUG_MOUSE") == "1",
		MouseYAdjust:   mouseYAdj,
	})
	prog := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := prog.Run(); err != nil {
		return err
	}
	return nil
}
