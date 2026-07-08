// Package staticpass runs cheap, deterministic static-analysis tools over a
// PR worktree BEFORE the LLM specialists run (Q5), and turns their output into
// evidence the review engine can ground findings against.
//
// The contract is strict: this package never blocks or fails a review. Every
// tool is modelled as a small adapter with an Available() check and a
// timeout-bounded Run(); a missing binary, an absent config, a slow tool, or a
// broken invocation all degrade to "this tool contributed nothing" rather than
// an error. Callers get a Result they can:
//
//   - render into a specialist prompt section (FormatSpecialistSection) so the
//     models are told "the linter already flags X — don't re-report it; report
//     what linters can't see";
//   - render into the checks agent's prompt (FormatChecksAnnotations) so it
//     reasons over real tool annotations;
//   - query for formatter-clean files (Result.FormatterCleanFiles) so the
//     review engine can treat "the formatter ran clean here" as a
//     false-positive signal for hand-rolled formatting findings.
//
// The tool set: gofmt -l and go vet (ship with the Go toolchain), golangci-lint
// (only when a .golangci config exists), ruff, eslint, and terraform validate.
// Availability is probed via exec.LookPath; config-gated tools additionally
// require the detected config (see repocontext.DetectLintConfigs).
package staticpass

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/review/repocontext"
)

// Level classifies an annotation's severity, normalised across tools to the
// GitHub Checks vocabulary (notice < warning < error).
type Level string

const (
	LevelNotice  Level = "notice"
	LevelWarning Level = "warning"
	LevelError   Level = "error"
)

// Annotation is one issue a static tool reported, normalised across tools.
// Path is repo-relative (forward slashes); Line is the 1-indexed new-side
// line (0 when the tool reported no line / a file-level issue).
type Annotation struct {
	Tool    string
	Path    string
	Line    int
	Level   Level
	Message string
}

// ToolReport is the outcome of one tool adapter for one pass.
type ToolReport struct {
	// Tool is the adapter's stable name (e.g. "gofmt", "golangci-lint").
	Tool string
	// Available is whether the tool (and its required config, if any) was
	// present in the environment.
	Available bool
	// Ran is whether the adapter actually executed the tool. False when the
	// tool was unavailable or there were no relevant changed files.
	Ran bool
	// TimedOut records that the tool exceeded its per-tool timeout and was
	// abandoned (fail-open: its output is discarded).
	TimedOut bool
	// Err is a non-fatal diagnostic recorded when a tool ran but produced
	// unusable output (e.g. unparseable JSON). Never surfaced as a review
	// error; kept for logging/telemetry.
	Err error
	// CheckedFiles are the changed files this tool examined. Used together
	// with Annotations to derive which files a formatter passed clean.
	CheckedFiles []string
	// Annotations are the issues the tool reported on the changed files.
	Annotations []Annotation
	// isFormatter marks a formatter/linter whose silence on a file is a
	// meaningful "this file's formatting is already correct" signal.
	isFormatter bool
}

// Result aggregates every tool's report for one pass.
type Result struct {
	Tools []ToolReport
}

// Annotations returns every tool's annotations, flattened and stably sorted
// (by path, then line, then tool) so rendering and tests are deterministic.
func (r Result) Annotations() []Annotation {
	var out []Annotation
	for _, t := range r.Tools {
		out = append(out, t.Annotations...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Tool < out[j].Tool
	})
	return out
}

// HasAnnotations reports whether any tool produced at least one annotation.
func (r Result) HasAnnotations() bool {
	for _, t := range r.Tools {
		if len(t.Annotations) > 0 {
			return true
		}
	}
	return false
}

// FormatterCleanFiles returns the set of changed files that a formatter-class
// tool examined AND on which no formatter-class tool reported any annotation.
// This is the "linter is silent" signal: a file in this set is already
// correctly formatted, so a hand-rolled whitespace/indentation/style finding
// there is very likely a false positive. Paths are forward-slashed.
func (r Result) FormatterCleanFiles() map[string]bool {
	checked := map[string]bool{}
	dirty := map[string]bool{}
	for _, t := range r.Tools {
		if !t.isFormatter || !t.Ran {
			continue
		}
		for _, f := range t.CheckedFiles {
			checked[slash(f)] = true
		}
		for _, a := range t.Annotations {
			dirty[slash(a.Path)] = true
		}
	}
	out := map[string]bool{}
	for f := range checked {
		if !dirty[f] {
			out[f] = true
		}
	}
	return out
}

// ranTools / cleanTools / unavailableTools drive the prompt sections.
func (r Result) ranWithFindings() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range r.Tools {
		if t.Ran && len(t.Annotations) > 0 && !seen[t.Tool] {
			seen[t.Tool] = true
			out = append(out, t.Tool)
		}
	}
	sort.Strings(out)
	return out
}

func (r Result) ranClean() []string {
	var out []string
	for _, t := range r.Tools {
		if t.Ran && len(t.Annotations) == 0 {
			out = append(out, t.Tool)
		}
	}
	sort.Strings(out)
	return out
}

func (r Result) unavailable() []string {
	var out []string
	for _, t := range r.Tools {
		if !t.Available {
			out = append(out, t.Tool)
		}
	}
	sort.Strings(out)
	return out
}

// Tool is one static-analysis adapter. Adapters are intentionally tiny:
// Available() probes the environment (binary on PATH, config present) and Run
// executes the tool under ctx (which carries the per-tool timeout) against the
// changed files in the worktree. Run must be fail-open: on any error it should
// return what it can (often nothing) plus the error for telemetry, never
// panic, and never block past ctx.
type Tool interface {
	Name() string
	// Available reports whether the tool can run in this environment.
	Available() bool
	// Formatter reports whether a clean pass by this tool is a meaningful
	// "formatting is already correct" signal for the changed files it checked.
	Formatter() bool
	// Run executes the tool. changedFiles are repo-relative post-image paths.
	// It returns the annotations found and the changed files it actually
	// examined (for the clean-file signal).
	Run(ctx context.Context, worktree string, changedFiles []string) (annotations []Annotation, checked []string, err error)
}

// Options configures a pass. The zero value is valid: sensible defaults are
// filled in by Run.
type Options struct {
	// PerToolTimeout bounds each individual tool invocation. Default 20s.
	PerToolTimeout time.Duration
	// Lint carries the detected linter configs so config-gated tools
	// (golangci-lint, eslint, ruff) know whether to run. When zero, those
	// tools stay off (fail-open: no config → nothing to enforce).
	Lint repocontext.LintConfigs
	// tools overrides the default adapter set (tests inject fakes). Nil means
	// "use the real default tool set".
	tools []Tool
}

const defaultPerToolTimeout = 20 * time.Second

// Run executes every available tool over changedFiles in worktree and returns
// the aggregated Result. It is fail-open end-to-end: a nil/empty worktree or
// no changed files yields an empty Result; individual tool failures/timeouts
// are recorded on their ToolReport and never propagate. Run honours ctx for
// overall cancellation while giving each tool its own timeout.
func Run(ctx context.Context, worktree string, changedFiles []string, opts Options) Result {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.PerToolTimeout <= 0 {
		opts.PerToolTimeout = defaultPerToolTimeout
	}
	res := Result{}
	worktree = strings.TrimSpace(worktree)
	if worktree == "" || len(changedFiles) == 0 {
		return res
	}
	tools := opts.tools
	if tools == nil {
		tools = defaultTools(opts.Lint)
	}
	for _, t := range tools {
		rep := ToolReport{Tool: t.Name(), isFormatter: t.Formatter()}
		if !t.Available() {
			res.Tools = append(res.Tools, rep)
			continue
		}
		rep.Available = true
		if ctx.Err() != nil {
			// Overall context cancelled — stop launching more tools.
			res.Tools = append(res.Tools, rep)
			continue
		}
		tctx, cancel := context.WithTimeout(ctx, opts.PerToolTimeout)
		anns, checked, err := t.Run(tctx, worktree, changedFiles)
		timedOut := tctx.Err() == context.DeadlineExceeded
		cancel()
		rep.Ran = true
		rep.TimedOut = timedOut
		rep.CheckedFiles = checked
		if timedOut {
			// Fail-open: discard whatever partial output a timed-out tool
			// produced; we can't trust it.
			rep.Annotations = nil
			rep.CheckedFiles = nil
			res.Tools = append(res.Tools, rep)
			continue
		}
		rep.Annotations = anns
		rep.Err = err
		res.Tools = append(res.Tools, rep)
	}
	return res
}

// FormatSpecialistSection renders the pre-pass into the markdown block injected
// into every code specialist's prompt. It tells the models what the
// deterministic tools already flag (don't re-report), which tools passed clean
// (don't hand-flag formatting there), and which were unavailable. Returns ""
// when nothing meaningful ran (so the prompt gains no empty section).
func FormatSpecialistSection(r Result) string {
	ranClean := r.ranClean()
	anns := r.Annotations()
	if len(ranClean) == 0 && len(anns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("_A static-analysis pre-pass ran deterministic tools over the changed files before this review._\n\n")
	b.WriteString("Treat their output as already-known ground truth:\n\n")
	b.WriteString("- **Do not re-report anything a tool below already flags** — it will be fixed mechanically. Spend your findings on what these tools cannot see (logic, design, intent, semantics).\n")
	b.WriteString("- Where a formatter/linter ran **clean** on a file, that file's formatting is already correct; do **not** hand-flag whitespace, indentation, or gofmt-style issues there — a formatting finding on a linter-clean file is a false positive.\n\n")

	if len(anns) > 0 {
		b.WriteString("Tools already flag the following (do not duplicate):\n\n")
		const maxAnns = 60
		for i, a := range anns {
			if i >= maxAnns {
				fmt.Fprintf(&b, "- …and %d more.\n", len(anns)-maxAnns)
				break
			}
			loc := a.Path
			if a.Line > 0 {
				loc = fmt.Sprintf("%s:%d", a.Path, a.Line)
			}
			fmt.Fprintf(&b, "- `%s` [%s] %s — %s\n", a.Tool, a.Level, loc, collapseWS(a.Message))
		}
		b.WriteString("\n")
	} else {
		b.WriteString("No tool reported any issue on the changed files.\n\n")
	}
	if len(ranClean) > 0 {
		fmt.Fprintf(&b, "Tools that ran and passed the changed files clean: %s.\n", strings.Join(ranClean, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// SpecialistSectionHeading is the section title FormatSpecialistSection's body
// is placed under when injected into a prompt (kept parallel to the repo
// evidence section wrapper in path_history.go).
const SpecialistSectionHeading = "## Static analysis pre-pass (auto-run before this review)"

// WrapSpecialistSection wraps a non-empty specialist section body with the
// conventional heading; returns "" for an empty body.
func WrapSpecialistSection(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	return "\n\n" + SpecialistSectionHeading + "\n\n" + body + "\n"
}

// FormatChecksAnnotations renders the pre-pass annotations for the checks
// agent, which reasons over CI failures + tool annotations. Returns "" when no
// tool produced an annotation, so the checks prompt gains nothing empty.
func FormatChecksAnnotations(r Result) string {
	anns := r.Annotations()
	if len(anns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Static analysis annotations (appr-ai-sal pre-pass)\n\n")
	b.WriteString("These are annotations from deterministic tools run locally on the changed files (they are not GitHub check runs). Fold them into your judgement of the PR's mechanical health; propose concrete fixes where useful and do not duplicate what CI already reports.\n\n")
	const maxAnns = 80
	for i, a := range anns {
		if i >= maxAnns {
			fmt.Fprintf(&b, "- …and %d more.\n", len(anns)-maxAnns)
			break
		}
		loc := a.Path
		if a.Line > 0 {
			loc = fmt.Sprintf("%s:%d", a.Path, a.Line)
		}
		fmt.Fprintf(&b, "- [%s] %s %s — %s\n", a.Level, a.Tool, loc, collapseWS(a.Message))
	}
	b.WriteString("\n")
	return b.String()
}

func slash(p string) string {
	return strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
}

// collapseWS flattens whitespace runs so multi-line tool messages render as a
// single scannable line inside a prompt.
func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
