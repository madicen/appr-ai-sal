// Package contextexpand builds a deterministic "expanded context" block for
// review backends that do NOT get live repo tools (B5).
//
// Only the Claude subprocess provider gets Read/Glob/Grep tools; every HTTP
// provider (OpenAI-compatible/Ollama, Gemini) reviews the diff BLIND — it sees
// only the hunks, not the enclosing function bodies, the types the changed
// code references, or who calls the changed functions. That is the biggest
// quality gap for the design/security lanes off-Claude. This package closes
// part of it deterministically: given the PR worktree and the changed line
// ranges, it gathers, in order of relevance,
//
//  1. the ENCLOSING full function body for each changed hunk (a hunk usually
//     shows only part of a function),
//  2. the TYPE DEFINITIONS the changed code references (same package), and
//  3. CALLERS / CALLEES of the changed functions.
//
// Source ladder (most authoritative first): for Go it prefers a hermetic
// go/parser + go/ast pass over the worktree files — no external binary, fully
// deterministic, always available — as the baseline for (1), (2) and
// same-package (3). gopls and ctags are OPTIONAL enrichers (behind Available()
// checks + timeouts, injectable for tests) used only to add cross-file
// callers/callees the AST baseline cannot see cheaply. Everything is fail-open:
// a parser error, a missing tool, a non-Go repo, or a timeout contributes
// nothing and never breaks a review.
//
// The whole result is capped under a byte budget (reusing R3's byte-counting
// philosophy): items are added greedily by relevance and truncation is
// disclosed, so the expansion can never blow the provider context window.
package contextexpand

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ItemKind classifies one gathered context piece. The order of the constants
// is the relevance order the budgeter fills in.
type ItemKind int

const (
	// KindEnclosingFunc is the full body of a function a changed hunk lives in.
	KindEnclosingFunc ItemKind = iota
	// KindTypeDef is a type definition referenced by the changed code.
	KindTypeDef
	// KindCallee is a same-package function the changed code calls.
	KindCallee
	// KindCaller is a function that calls a changed function (cross-file;
	// surfaced only via a gopls/ctags enricher).
	KindCaller
)

func (k ItemKind) label() string {
	switch k {
	case KindEnclosingFunc:
		return "enclosing function"
	case KindTypeDef:
		return "referenced type"
	case KindCallee:
		return "callee"
	case KindCaller:
		return "caller"
	default:
		return "context"
	}
}

// Item is one gathered context piece, already rendered to source text.
type Item struct {
	Kind ItemKind
	// Symbol is the function/type name (best-effort; may be "" for anonymous).
	Symbol string
	// Path is the repo-relative (forward-slashed) file the item came from.
	Path string
	// Line is the 1-indexed line the symbol is declared on.
	Line int
	// Code is the source text of the item (may be per-item-cap truncated).
	Code string
	// truncated marks that Code was shortened by the per-item cap.
	truncated bool
}

// ChangedFile is one changed file plus the NEW-image line numbers that were
// added or modified in the PR. Only these lines drive which functions are
// treated as "changed" (and therefore expanded).
type ChangedFile struct {
	// Path is the repo-relative (forward-slashed) post-image path.
	Path string
	// ChangedLines are the 1-indexed new-image line numbers the hunks touched.
	ChangedLines []int
}

// Options configures one expansion pass. The zero value is usable: Expand
// fills sensible defaults and returns an empty Result when Worktree is empty
// or there is nothing to expand.
type Options struct {
	// Worktree is the checked-out PR directory (absolute or relative path).
	Worktree string
	// Changed lists the changed files and their touched line ranges.
	Changed []ChangedFile
	// ByteBudget caps the TOTAL rendered code across all items. <= 0 resolves
	// to DefaultByteBudget.
	ByteBudget int
	// PerItemBytes caps a single item's rendered code. <= 0 resolves to
	// DefaultPerItemBytes.
	PerItemBytes int
	// EnricherTimeout bounds each external (gopls/ctags) invocation. <= 0
	// resolves to DefaultEnricherTimeout.
	EnricherTimeout time.Duration
	// DisableEnrichers turns off the gopls/ctags cross-reference enrichers so
	// only the hermetic AST baseline runs. Tests that want a fully offline,
	// binary-independent pass set this; production leaves it false.
	DisableEnrichers bool

	// crossRef overrides the cross-reference finder (tests inject canned
	// output). Nil uses the package default (gopls → ctags, fail-open).
	crossRef crossRefFunc
}

const (
	// DefaultByteBudget is the conservative total byte cap for the expansion.
	// The expanded context is a SUPPLEMENT to the diff (which has its own R3
	// budget), so it is intentionally small: ~32 KiB leaves ample room under
	// every provider context window while still fitting several full function
	// bodies and the types they touch.
	DefaultByteBudget = 32768
	// DefaultPerItemBytes caps one function/type so a single enormous function
	// can't consume the whole budget and starve the rest.
	DefaultPerItemBytes = 6144
	// DefaultEnricherTimeout bounds each external tool call.
	DefaultEnricherTimeout = 10 * time.Second
	// maxChangedFiles caps how many changed files we index/expand, so a
	// thousand-file PR can't turn the pass into an O(files) parse storm.
	maxChangedFiles = 60
	// maxEnclosingForEnrichment caps how many changed functions we ask the
	// (expensive) enrichers about.
	maxEnclosingForEnrichment = 24
)

// Result is the outcome of one expansion. Items are in relevance order and
// already fit under the byte budget. Truncated is true when at least one item
// was per-item-cap truncated OR items were dropped to fit the total budget.
type Result struct {
	Items        []Item
	Truncated    bool
	OmittedItems int
	// EnrichersUsed lists which optional enrichers actually contributed
	// (e.g. "gopls", "ctags"); empty when only the AST baseline ran.
	EnrichersUsed []string
}

// HasContent reports whether the result carries any context to inject.
func (r Result) HasContent() bool { return len(r.Items) > 0 }

// Expand gathers the deterministic context for opts and returns a budgeted
// Result. It is fail-open end-to-end: a missing worktree, an unparseable file,
// a non-Go file, or a failing enricher contributes nothing rather than
// erroring. It never returns an error.
func Expand(ctx context.Context, opts Options) Result {
	if ctx == nil {
		ctx = context.Background()
	}
	res := Result{}
	worktree := strings.TrimSpace(opts.Worktree)
	if worktree == "" || len(opts.Changed) == 0 {
		return res
	}
	byteBudget := opts.ByteBudget
	if byteBudget <= 0 {
		byteBudget = DefaultByteBudget
	}
	perItem := opts.PerItemBytes
	if perItem <= 0 {
		perItem = DefaultPerItemBytes
	}
	timeout := opts.EnricherTimeout
	if timeout <= 0 {
		timeout = DefaultEnricherTimeout
	}
	crossRef := opts.crossRef
	if crossRef == nil {
		crossRef = defaultCrossReferences
	}

	changed := opts.Changed
	if len(changed) > maxChangedFiles {
		changed = changed[:maxChangedFiles]
	}

	// Gather candidate items (unbudgeted) from the hermetic Go AST baseline
	// plus, when enabled and available, the cross-reference enrichers.
	g := newGatherer(worktree)
	var enclosing []symbolRef
	for _, cf := range changed {
		enc := g.gatherGoFile(cf)
		enclosing = append(enclosing, enc...)
	}

	if !opts.DisableEnrichers && len(enclosing) > 0 {
		if len(enclosing) > maxEnclosingForEnrichment {
			enclosing = enclosing[:maxEnclosingForEnrichment]
		}
		used := g.gatherCallers(ctx, timeout, crossRef, enclosing)
		res.EnrichersUsed = used
	}

	// Budget: fill greedily by relevance (candidates already come in kind
	// order because gatherGoFile appends enclosing → types → callees and
	// gatherCallers appends callers last).
	kept, omitted, truncatedAny := budgetItems(g.items(), byteBudget, perItem)
	res.Items = kept
	res.OmittedItems = omitted
	res.Truncated = truncatedAny || omitted > 0
	return res
}

// budgetItems fills a byte budget greedily in the order candidates arrive
// (which is relevance order). Each item is first per-item-cap truncated, then
// dropped whole if it still would not fit the remaining total budget.
func budgetItems(candidates []Item, byteBudget, perItem int) (kept []Item, omitted int, truncatedAny bool) {
	used := 0
	for _, it := range candidates {
		code := it.Code
		if perItem > 0 && len(code) > perItem {
			code = truncateCode(code, perItem)
			it.truncated = true
			truncatedAny = true
		}
		// +2 for the blank-line separator the renderer adds between items.
		need := len(code) + 2
		if used+need > byteBudget {
			omitted++
			continue
		}
		it.Code = code
		used += need
		kept = append(kept, it)
	}
	return kept, omitted, truncatedAny
}

// truncateCode keeps whole leading lines of code up to maxBytes then appends a
// marker. Keeping whole lines avoids emitting a syntactically shredded tail.
func truncateCode(code string, maxBytes int) string {
	const marker = "\n\t// … truncated by appr-ai-sal context budget …"
	budget := maxBytes - len(marker)
	if budget <= 0 {
		return marker
	}
	lines := strings.Split(code, "\n")
	var b strings.Builder
	for _, ln := range lines {
		if b.Len()+len(ln)+1 > budget {
			break
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(ln)
	}
	return b.String() + marker
}

// SectionHeading is the title the expanded context is placed under when
// injected into a specialist prompt (parallel to staticpass's heading).
const SectionHeading = "## Expanded code context (read-only, for understanding)"

// FormatSection renders the result as the markdown body injected into a
// specialist prompt. It clearly frames the block as read-only supplementary
// context so the model does not treat the surrounding code as "changed" — the
// diff remains the authority for what changed. Returns "" when there is
// nothing to inject, so a prompt gains no empty section.
func FormatSection(r Result) string {
	if !r.HasContent() {
		return ""
	}
	var b strings.Builder
	b.WriteString("_The unified diff below shows only the changed hunks. Because this backend cannot read the repository directly, appr-ai-sal deterministically gathered the surrounding code the change touches so you can review it in context._\n\n")
	b.WriteString("This is **read-only context for understanding — it is NOT part of the change.** The diff is the authority for what changed; do not file findings against unchanged lines shown here, and do not propose suggestions anchored to them.\n")

	// Group items by kind so the section reads enclosing → types → callees →
	// callers, each under a short label.
	byKind := map[ItemKind][]Item{}
	order := []ItemKind{KindEnclosingFunc, KindTypeDef, KindCallee, KindCaller}
	for _, it := range r.Items {
		byKind[it.Kind] = append(byKind[it.Kind], it)
	}
	for _, k := range order {
		items := byKind[k]
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### %s%s\n\n", strings.ToUpper(k.label()[:1]), k.label()[1:]+plural(len(items)))
		for _, it := range items {
			loc := it.Path
			if it.Line > 0 {
				loc = fmt.Sprintf("%s:%d", it.Path, it.Line)
			}
			name := it.Symbol
			if name == "" {
				name = "(anonymous)"
			}
			fmt.Fprintf(&b, "`%s` — `%s`\n\n", name, loc)
			b.WriteString("```go\n")
			b.WriteString(strings.TrimRight(it.Code, "\n"))
			b.WriteString("\n```\n")
		}
	}
	if r.Truncated {
		b.WriteString("\n_(Expanded context was capped to fit the review budget; some enclosing code was truncated or omitted.)_\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// WrapSection wraps a non-empty FormatSection body under SectionHeading with
// the leading/trailing spacing the prompt builder expects; returns "" for an
// empty body. Mirrors staticpass.WrapSpecialistSection.
func WrapSection(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	return "\n\n" + SectionHeading + "\n\n" + body + "\n"
}

// dedupKey identifies a symbol so the same function/type is never emitted twice
// across kinds (an enclosing function that is also a callee, etc.).
type dedupKey struct {
	path string
	name string
}
