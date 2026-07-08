// Package cli implements appr-ai-sal's headless (non-TUI) subcommands so the
// tool can run in CI. It deliberately imports NO bubbletea (or any
// internal/tui code): CI images stay lean, and the review-execution +
// posting path is exercised entirely through internal/review, internal/gh and
// internal/aiconfig. A structural test (deps_test.go) fails the build if any
// TUI dependency ever creeps in.
//
// The headless `review` command:
//
//	appr-ai-sal review owner/repo#123 --json [--post] [--dry-run] [--fail-on request_changes]
//
// Contract:
//   - Progress is streamed to STDERR as NDJSON (one JSON object per line).
//   - The final review result is written to STDOUT as a single JSON object
//     (with --json) so it pipes cleanly into `jq`; without --json a short
//     human summary is written instead.
//   - Exit codes gate CI (see the Exit* constants).
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/applog"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

// Exit codes for the headless `review` command. They are deliberately
// distinct so a CI job can tell a review that gates the PR (ExitFailOn) apart
// from a tool/operational failure (ExitError).
const (
	// ExitOK: the review ran and its verdict is under the --fail-on threshold
	// (or no threshold was set). Also used for successful --post/--dry-run.
	ExitOK = 0
	// ExitFailOn: the review ran cleanly but its verdict is at or over the
	// --fail-on threshold — the CI gate should fail the PR.
	ExitFailOn = 1
	// ExitUsage: bad flags or arguments (also flag-parse errors).
	ExitUsage = 2
	// ExitConfig: the active AI profile is not validly configured
	// (ValidateForProvider failed) — a misconfiguration the operator must fix.
	ExitConfig = 3
	// ExitError: an operational failure — auth check, review run, head drift,
	// or a GitHub post error.
	ExitError = 4
)

// reviewDeps are the injectable seams the headless review flow talks to. In
// production they wrap internal/review + internal/gh; tests substitute fakes
// so the whole flow (NDJSON, stdout JSON, fail-on, post/dry-run) runs
// hermetically with no network, git, or live model.
type reviewDeps struct {
	loadConfig func() (*aiconfig.Config, error)
	checkAuth  func() error
	run        func(context.Context, gh.Ref, *aiconfig.Config) (<-chan review.Progress, error)
	poster     poster
}

// poster is the minimal GitHub write/read surface the headless --post /
// --dry-run paths need. It mirrors the subset of internal/tui/data.Backend the
// posting orchestration uses (F7) without dragging in bubbletea.
type poster interface {
	HeadSHA(ctx context.Context, ref gh.Ref) (string, error)
	ReviewThreads(ctx context.Context, ref gh.Ref) ([]gh.ReviewThread, error)
	ViewerLogin(ctx context.Context) string
	PostReview(ctx context.Context, ref gh.Ref, rev gh.Review) error
	PostInlineComment(ctx context.Context, ref gh.Ref, commitID string, c gh.ReviewComment) error
	ReplyToThread(ctx context.Context, ref gh.Ref, threadID, body string) error
}

func defaultReviewDeps() reviewDeps {
	return reviewDeps{
		loadConfig: aiconfig.Load,
		checkAuth:  gh.CheckAuth,
		run:        review.Run,
		poster:     ghPoster{},
	}
}

// RunReview is the entry point for `appr-ai-sal review …`. It parses argv,
// runs a headless review, streams NDJSON progress to stderr, writes the final
// result to stdout, optionally posts (or previews) it, and returns the process
// exit code. main.go passes os.Stdout/os.Stderr and calls os.Exit with the
// result.
func RunReview(ctx context.Context, argv []string, stdout, stderr io.Writer) int {
	return runReview(ctx, argv, stdout, stderr, defaultReviewDeps())
}

// reviewFlags is the parsed headless-review invocation.
type reviewFlags struct {
	ref      gh.Ref
	json     bool
	post     bool
	dryRun   bool
	failOn   string // canonical verdict or ""
	profile  string
	provider string
	baseURL  string
	model    string
	apiKey   string
	strict   string
	timeout  int
}

func runReview(ctx context.Context, argv []string, stdout, stderr io.Writer, deps reviewDeps) int {
	fl, code := parseReviewFlags(argv, stderr)
	if code != ExitOK || fl == nil {
		return code
	}

	// Logging goes to a file (never stderr), so it never corrupts the NDJSON
	// stream. Failure to open the log is non-fatal.
	if err := applog.Init("headless"); err == nil {
		applog.Info("headless review start", "ref", fl.ref.String(), "post", fl.post, "dry_run", fl.dryRun, "fail_on", fl.failOn)
	}

	cfg, code := resolveConfig(fl, stderr, deps)
	if code != ExitOK {
		return code
	}

	// A real review always fetches the PR + diff from GitHub, so auth is
	// required even for a report-only run. In dry-run we still need read
	// access; only true --post needs write scope, which GitHub enforces.
	if err := deps.checkAuth(); err != nil {
		fmt.Fprintf(stderr, "appr-ai-sal: gh auth check failed: %v\nRun `gh auth login` and try again.\n", err)
		return ExitError
	}

	draft, ok := drainReview(ctx, fl.ref, cfg, stderr, deps)
	if !ok {
		return ExitError
	}

	// Posting / dry-run preview (reuses the F7 payload builders + B3
	// thread-aware routing). Errors here are operational.
	post, code := handlePosting(ctx, fl, draft, stderr, deps)
	if code != ExitOK {
		return code
	}

	writeResult(stdout, fl, draft, post)

	// CI gate: fail non-zero when the reconciled (posted) verdict is at or
	// over the --fail-on threshold.
	if fl.failOn != "" {
		reconciled := review.NormalizeVibeVerdict(draft.ReconciledMergeVerdict())
		if verdictRank(reconciled) >= verdictRank(fl.failOn) {
			fmt.Fprintf(stderr, "appr-ai-sal: verdict %q meets --fail-on %q — failing.\n",
				verdictLabel(reconciled), verdictLabel(fl.failOn))
			return ExitFailOn
		}
	}
	return ExitOK
}

// parseReviewFlags parses the review subcommand's argv. It returns a nil
// reviewFlags with ExitOK for -h/--help (usage already printed), or a non-OK
// code on a usage error.
func parseReviewFlags(argv []string, stderr io.Writer) (*reviewFlags, int) {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, reviewUsage)
	}

	jsonOut := fs.Bool("json", false, "emit the final review result as JSON on stdout (NDJSON progress always goes to stderr)")
	post := fs.Bool("post", false, "post the review to GitHub (thread-aware; reuses the F7/B3 posting path)")
	dryRun := fs.Bool("dry-run", false, "print what would be posted without posting")
	failOn := fs.String("fail-on", "", "exit non-zero when the verdict is at/over this threshold: approve | comment | request_changes")
	profile := fs.String("profile", "", "AI config profile to use (defaults to APPR_AI_SAL_PROFILE or the active profile)")
	provider := fs.String("ai-provider", "", "AI backend: claude, gemini, ollama, openai_compatible (overrides env / config file)")
	baseURL := fs.String("ai-base-url", "", "HTTP base URL for gemini / ollama / openai_compatible")
	model := fs.String("ai-model", "", "model id for the selected provider")
	apiKey := fs.String("ai-api-key", "", "API key for HTTP providers (prefer env APPR_AI_SAL_AI_API_KEY)")
	strict := fs.String("review-strictness", "", "review intensity: critical_only | lenient | balanced | strict")
	timeout := fs.Int("ai-timeout-sec", -1, "timeout in seconds for AI HTTP calls and the review context (default 300)")

	if err := fs.Parse(argv); err != nil {
		// flag already printed the error + usage.
		if err == flag.ErrHelp {
			return nil, ExitOK
		}
		return nil, ExitUsage
	}

	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintf(stderr, "appr-ai-sal: review requires exactly one PR reference (owner/repo#123 or a PR URL)\n\n%s", reviewUsage)
		return nil, ExitUsage
	}
	ref, err := gh.ParsePRURL(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "appr-ai-sal: invalid PR reference %q: %v\n", rest[0], err)
		return nil, ExitUsage
	}

	if *post && *dryRun {
		fmt.Fprintf(stderr, "appr-ai-sal: --post and --dry-run are mutually exclusive\n")
		return nil, ExitUsage
	}

	canonicalFailOn := ""
	if strings.TrimSpace(*failOn) != "" {
		canonicalFailOn = review.NormalizeVibeVerdict(*failOn)
		if canonicalFailOn == "" {
			fmt.Fprintf(stderr, "appr-ai-sal: invalid --fail-on %q (expected approve, comment, or request_changes)\n", *failOn)
			return nil, ExitUsage
		}
	}

	prof := strings.TrimSpace(*profile)
	if prof == "" {
		prof = strings.TrimSpace(os.Getenv("APPR_AI_SAL_PROFILE"))
	}

	return &reviewFlags{
		ref:      ref,
		json:     *jsonOut,
		post:     *post,
		dryRun:   *dryRun,
		failOn:   canonicalFailOn,
		profile:  prof,
		provider: strings.TrimSpace(*provider),
		baseURL:  strings.TrimSpace(*baseURL),
		model:    strings.TrimSpace(*model),
		apiKey:   strings.TrimSpace(*apiKey),
		strict:   strings.TrimSpace(*strict),
		timeout:  *timeout,
	}, ExitOK
}

// resolveConfig loads the AI config, applies the profile + flag overrides, and
// validates it up front (R8) — unlike the TUI, a headless run cannot open the
// settings tab, so a misconfigured profile is a hard ExitConfig failure.
func resolveConfig(fl *reviewFlags, stderr io.Writer, deps reviewDeps) (*aiconfig.Config, int) {
	cfg, err := deps.loadConfig()
	if err != nil {
		fmt.Fprintf(stderr, "appr-ai-sal: AI config: %v\n", err)
		return nil, ExitConfig
	}
	if fl.profile != "" {
		if err := cfg.SetActive(fl.profile); err != nil {
			fmt.Fprintf(stderr, "appr-ai-sal: %v\n", err)
			return nil, ExitConfig
		}
	}
	if err := cfg.MergeFlags(fl.provider, fl.baseURL, fl.model, fl.apiKey, fl.strict, fl.timeout); err != nil {
		fmt.Fprintf(stderr, "appr-ai-sal: %v\n", err)
		return nil, ExitConfig
	}
	if err := cfg.ValidateForProvider(); err != nil {
		fmt.Fprintf(stderr, "appr-ai-sal: active AI profile is not configured: %v\n", err)
		return nil, ExitConfig
	}
	return cfg, ExitOK
}

// drainReview runs the review and drains its Progress channel, emitting one
// NDJSON object per event to stderr. It returns the final Draft, or ok=false
// when the run produced no final draft (a fatal early-stage error). Fatal
// stage errors are also written to stderr as NDJSON so CI logs capture them.
func drainReview(ctx context.Context, ref gh.Ref, cfg *aiconfig.Config, stderr io.Writer, deps reviewDeps) (*review.Draft, bool) {
	ch, err := deps.run(ctx, ref, cfg)
	if err != nil {
		emitProgress(stderr, review.Progress{Stage: "fetch-pr", Err: err})
		fmt.Fprintf(stderr, "appr-ai-sal: review failed to start: %v\n", err)
		return nil, false
	}

	var final *review.Draft
	var lastErr error
	for p := range ch {
		emitProgress(stderr, p)
		if p.Err != nil {
			lastErr = p.Err
		}
		if p.Final != nil {
			final = p.Final
		}
	}
	if final == nil {
		if lastErr != nil {
			fmt.Fprintf(stderr, "appr-ai-sal: review did not complete: %v\n", lastErr)
		} else {
			fmt.Fprintf(stderr, "appr-ai-sal: review did not complete (no final draft)\n")
		}
		return nil, false
	}
	return final, true
}

// verdictRank orders merge verdicts most-permissive → most-strict so --fail-on
// can compare thresholds. Mirrors review.verdictRank (unexported) using the
// exported verdict consts.
func verdictRank(v string) int {
	switch review.NormalizeVibeVerdict(v) {
	case review.VibeVerdictRequestChanges:
		return 2
	case review.VibeVerdictComment:
		return 1
	default:
		return 0
	}
}

func verdictLabel(v string) string {
	if l := review.VibeVerdictShortLabel(review.NormalizeVibeVerdict(v)); l != "" {
		return l
	}
	return "approve"
}

const reviewUsage = `usage: appr-ai-sal review <owner/repo#123 | PR URL> [flags]

Run a headless PR review for CI. Progress streams to stderr as NDJSON; the
final result is written to stdout (JSON with --json, else a short summary).

Flags:
  --json                 emit the final review result as JSON on stdout
  --post                 post the review to GitHub (thread-aware)
  --dry-run              print what would be posted without posting
  --fail-on <verdict>    exit non-zero when the verdict is at/over the given
                         threshold (approve | comment | request_changes)
  --profile <name>       AI config profile (also APPR_AI_SAL_PROFILE)
  --ai-provider <p>      claude | gemini | ollama | openai_compatible
  --ai-base-url <url>    HTTP base URL for gemini / ollama / openai_compatible
  --ai-model <id>        model id for the selected provider
  --ai-api-key <key>     API key (prefer env APPR_AI_SAL_AI_API_KEY)
  --review-strictness <s> critical_only | lenient | balanced | strict
  --ai-timeout-sec <n>   timeout for AI calls and the review context

Exit codes:
  0  ok (verdict under --fail-on, or no threshold)
  1  verdict at/over --fail-on (CI gate)
  2  usage error
  3  config validation error
  4  operational error (auth, review run, head drift, post)

--post and --dry-run are mutually exclusive.
`

// ghPoster wraps the real internal/gh calls for the production posting path.
type ghPoster struct{}

func (ghPoster) HeadSHA(ctx context.Context, ref gh.Ref) (string, error) {
	return gh.GetPRHeadSHA(ctx, ref)
}

func (ghPoster) ReviewThreads(ctx context.Context, ref gh.Ref) ([]gh.ReviewThread, error) {
	return gh.GetReviewThreads(ctx, ref)
}

func (ghPoster) ViewerLogin(ctx context.Context) string {
	v, _ := gh.ViewerLogin(ctx)
	return v
}

func (ghPoster) PostReview(ctx context.Context, ref gh.Ref, rev gh.Review) error {
	return gh.PostReview(ctx, ref, rev)
}

func (ghPoster) PostInlineComment(ctx context.Context, ref gh.Ref, commitID string, c gh.ReviewComment) error {
	return gh.CreatePullReviewComment(ctx, ref, commitID, c)
}

func (ghPoster) ReplyToThread(ctx context.Context, ref gh.Ref, threadID, body string) error {
	return gh.ReplyToReviewThread(ctx, ref, threadID, body)
}

var _ poster = ghPoster{}
