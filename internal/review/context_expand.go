package review

import (
	"context"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/review/contextexpand"
)

// context_expand.go is the review-package glue over internal/review/contextexpand
// (B5). It threads the deterministic context expander into the review pipeline:
// it applies the RepoTools capability gate, derives the changed line ranges
// from the diff, sizes the expander's byte budget from the provider (reusing
// R3's per-provider budgeting), and renders the result into the prompt section
// the specialist builder injects.
//
// The whole feature exists to close the biggest off-Claude quality gap: HTTP
// providers (RepoTools == false) review the diff BLIND. The Claude subprocess
// (RepoTools == true) already reads the worktree with live tools, so for it
// this is a no-op — buildExpandedContextSection returns "" and the specialist
// prompts are byte-identical to before B5.

// expandContextByteBudget sizes the expander's total byte budget from the
// provider's R3 diff budget. The expanded context is a SUPPLEMENT to the diff,
// so it takes a small slice of the provider's budget (an eighth), clamped to a
// sane window so a huge-context provider doesn't inject a novel and a
// small-context one still gets something useful.
func expandContextByteBudget(cfg *aiconfig.Config) int {
	const (
		minBudget = 16384
		maxBudget = 65536
	)
	b := providerDiffByteBudget(cfg) / 8
	if b < minBudget {
		b = minBudget
	}
	if b > maxBudget {
		b = maxBudget
	}
	return b
}

// buildExpandedContextSection runs the deterministic context expander for a
// backend WITHOUT repo tools and returns the ready-to-inject prompt section
// (heading + body) plus the raw Result for telemetry. It is gated and
// fail-open:
//
//   - When cfg's provider has RepoTools == true (the Claude subprocess), it
//     returns "" immediately — that backend reads the worktree live, so no
//     expansion is injected and the prompt is unchanged.
//   - When the diff carries no changed Go lines, or the expander gathers
//     nothing, it returns "".
//
// The caller injects the returned section into every code specialist's prompt
// (shared, computed once per run), matching the staticpass injection pattern.
func buildExpandedContextSection(ctx context.Context, cfg *aiconfig.Config, worktree, diff string) (string, contextexpand.Result) {
	if cfg == nil || ai.CapabilitiesFor(cfg).RepoTools {
		return "", contextexpand.Result{}
	}
	if strings.TrimSpace(worktree) == "" || strings.TrimSpace(diff) == "" {
		return "", contextexpand.Result{}
	}
	changed := changedGoLineRanges(diff)
	if len(changed) == 0 {
		return "", contextexpand.Result{}
	}
	res := contextexpand.Expand(ctx, contextexpand.Options{
		Worktree:   worktree,
		Changed:    changed,
		ByteBudget: expandContextByteBudget(cfg),
	})
	return contextexpand.WrapSection(contextexpand.FormatSection(res)), res
}

// changedGoLineRanges parses diff and returns, for each changed .go file, the
// new-image line numbers that were added/modified (the lines that drive which
// functions are treated as changed). Non-Go files are skipped: the AST
// baseline is Go-only, and the ctags enricher (when present) still resolves
// cross-file references without a per-file line range here.
func changedGoLineRanges(diff string) []contextexpand.ChangedFile {
	files := ParseDiff(diff)
	var out []contextexpand.ChangedFile
	for _, f := range files {
		path := strings.TrimSpace(f.Path)
		if path == "" || !strings.HasSuffix(path, ".go") {
			continue
		}
		var lines []int
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if l.Kind == DiffAdded && l.NewNo > 0 {
					lines = append(lines, l.NewNo)
				}
			}
		}
		if len(lines) == 0 {
			continue
		}
		out = append(out, contextexpand.ChangedFile{Path: path, ChangedLines: lines})
	}
	return out
}
