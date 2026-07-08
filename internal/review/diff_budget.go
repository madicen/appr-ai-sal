package review

import (
	"fmt"
	"path"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
)

// Diff budgeter (R3).
//
// buildReviewUserPrompt used to inline the ENTIRE diff, uncapped, into every
// specialist / PR-agent call — a 5 MB PR (regenerated lockfile, vendored tree,
// a bulk rename) would blow the provider context window and 400 the whole run.
// budgetDiff shapes the diff BEFORE it is inlined, using the existing
// ParseDiff/FileDiff structures:
//
//  1. Non-review-worthy files (lockfiles, vendored trees, generated code,
//     minified assets, binaries) are dropped and replaced by a one-line
//     manifest entry so the model still knows they changed.
//  2. A per-file line cap trims the tail of any single huge file, and a
//     whole-diff byte cap (a conservative per-provider default, overridable)
//     elides trailing files/hunks once the budget is exhausted.
//  3. What was shaped is reported back via BudgetReport so the runner can emit
//     a Progress warning and the rendered review body can disclose it.
//
// Correctness invariants:
//   - A diff that fits under all caps with nothing to elide passes through
//     BYTE-IDENTICAL (see the early return); the shaped string is the exact
//     input. This keeps ordinary small PRs completely unaffected.
//   - Surviving lines keep their real unified-diff line numbers. Files are
//     only ever trimmed from the TAIL (leading lines and their `@@` headers
//     are preserved verbatim) and elided files are removed whole, so a
//     finding the model files against a surviving line still anchors to the
//     correct line in the full diff the TUI/GitHub see.

const (
	// defaultDiffByteCap is the conservative whole-diff byte budget used when
	// no provider-specific budget and no config override apply. ~256 KiB of
	// diff leaves ample room for the system prompt, the repo/lang/tech briefs,
	// the ~3.5k-token output contract, and the model's own output inside a
	// 128k-token window.
	defaultDiffByteCap = 262144
	// defaultDiffPerFileLineCap caps the unified-diff lines kept per file
	// before the tail is elided. Generous on purpose: ordinary files pass
	// through untouched; only a genuinely enormous single file (a huge
	// generated file that dodged the glob list, a mega-refactor) is trimmed.
	defaultDiffPerFileLineCap = 1500
	// minKeepBytesForTruncatedFile is the smallest remaining byte budget worth
	// spending on a partial file. Below it we elide the whole file rather than
	// emit a stub with almost no reviewable content.
	minKeepBytesForTruncatedFile = 512
	// maxDisclosureNames caps how many file names a disclosure line lists
	// before collapsing the rest into "+N more" — a 5 MB diff can elide
	// hundreds of files and an unbounded list is noise in the review body.
	maxDisclosureNames = 8
)

// providerDiffByteBudget returns a conservative diff byte budget for cfg's
// provider. It intentionally under-shoots real context windows: the goal is to
// never trigger a 400, not to maximise how much diff we ship. Config's
// DiffByteCap (when set) overrides this.
func providerDiffByteBudget(cfg *aiconfig.Config) int {
	if cfg == nil {
		return defaultDiffByteCap
	}
	switch cfg.Provider {
	case aiconfig.ProviderGemini:
		// Gemini's context windows are the largest in the roster.
		return 786432 // 768 KiB
	case aiconfig.ProviderClaude:
		// Large window, and Claude reads files from the worktree directly, so
		// the inlined diff is a supplement rather than the sole context.
		return 524288 // 512 KiB
	case aiconfig.ProviderAnthropic:
		// Direct Anthropic API: large (200k-token) context window, but it
		// reviews the diff blind (no repo tools), so keep a healthy cap.
		return 524288 // 512 KiB
	case aiconfig.ProviderOllama, aiconfig.ProviderOpenAICompatible:
		// Local / unknown backends — stay conservative.
		return defaultDiffByteCap
	default:
		return defaultDiffByteCap
	}
}

// SpecialistBudgetPolicy is the reserved seam for per-specialist diff shaping
// (R3.4). The observation is that different specialists want different slices
// of the diff — formatting doesn't need test fixtures; testing needs test
// files most; docs cares about doc files and exported surfaces. This type lets
// a future workstream attach that policy to a budgetDiff call WITHOUT touching
// the budgeter's signature or its callers.
//
// It is deliberately not wired yet: budgetDiff ignores a nil Policy (always
// the case today). Adding per-specialist behaviour later means populating this
// struct and branching on it inside budgetDiff — no call-site churn.
type SpecialistBudgetPolicy struct {
	// Specialist is the lane this policy shapes for (e.g. SpecTesting).
	Specialist string
	// Future fields (keep/drop globs, per-file overrides, priority ordering)
	// go here. Left empty in R3.
}

// BudgetConfig is the resolved input to budgetDiff: the caps and globs to
// enforce plus the optional (currently always nil) per-specialist policy hook.
type BudgetConfig struct {
	// ElisionGlobs are the file globs whose diffs are dropped (manifest only).
	// Empty resolves to repoconfig.DefaultDiffElisionGlobs inside budgetDiff.
	ElisionGlobs []string
	// ByteCap is the whole-diff byte budget. <= 0 resolves to the default.
	ByteCap int
	// PerFileLineCap caps unified-diff lines kept per file. <= 0 resolves to
	// the default.
	PerFileLineCap int
	// Policy is the per-specialist shaping hook (R3.4). Nil today; budgetDiff
	// ignores it. Reserved so per-specialist policies can be added without a
	// signature change.
	Policy *SpecialistBudgetPolicy
}

// newBudgetConfig resolves a BudgetConfig from repo config (globs + overrides)
// and the AI config (per-provider default byte budget).
func newBudgetConfig(rc *repoconfig.Config, cfg *aiconfig.Config) BudgetConfig {
	bc := BudgetConfig{
		ByteCap:        providerDiffByteBudget(cfg),
		PerFileLineCap: defaultDiffPerFileLineCap,
	}
	if rc != nil {
		bc.ElisionGlobs = rc.DiffElisionGlobsOrDefault()
		if rc.DiffByteCap > 0 {
			bc.ByteCap = rc.DiffByteCap
		}
		if rc.DiffPerFileLineCap > 0 {
			bc.PerFileLineCap = rc.DiffPerFileLineCap
		}
	} else {
		bc.ElisionGlobs = repoconfig.DefaultDiffElisionGlobs()
	}
	return bc
}

// ElidedFile records one file dropped from the diff (manifest entry only).
type ElidedFile struct {
	Path   string
	Reason string // "lockfile" | "vendored" | "generated" | "minified asset" | "binary" | "byte cap" | "matched <glob>"
	Lines  int    // diff-line count (additions+deletions), or block line count for binaries
}

// TruncatedFile records one file whose tail was elided (leading lines kept).
type TruncatedFile struct {
	Path         string
	OmittedLines int
}

// BudgetReport describes what budgetDiff did. Truncated is false (and the two
// slices empty) when the diff passed through unchanged.
type BudgetReport struct {
	Truncated     bool
	Elided        []ElidedFile
	Truncations   []TruncatedFile
	OriginalBytes int
	ShapedBytes   int
}

// DisclosureLine is the human-facing one-liner surfaced in both the Progress
// warning and the rendered review body (e.g. "review ran on a truncated diff:
// files go.sum, package-lock.json elided; file huge.go truncated"). Returns ""
// when nothing was shaped.
func (r BudgetReport) DisclosureLine() string {
	if !r.Truncated {
		return ""
	}
	var parts []string
	if len(r.Elided) > 0 {
		names := make([]string, 0, len(r.Elided))
		for _, e := range r.Elided {
			names = append(names, e.Path)
		}
		parts = append(parts, fmt.Sprintf("%s %s elided", filesWord(len(r.Elided)), joinCappedNames(names)))
	}
	if len(r.Truncations) > 0 {
		names := make([]string, 0, len(r.Truncations))
		for _, t := range r.Truncations {
			names = append(names, t.Path)
		}
		parts = append(parts, fmt.Sprintf("%s %s truncated", filesWord(len(r.Truncations)), joinCappedNames(names)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "review ran on a truncated diff: " + strings.Join(parts, "; ")
}

func filesWord(n int) string {
	if n == 1 {
		return "file"
	}
	return "files"
}

func joinCappedNames(names []string) string {
	if len(names) <= maxDisclosureNames {
		return strings.Join(names, ", ")
	}
	shown := names[:maxDisclosureNames]
	return fmt.Sprintf("%s, +%d more", strings.Join(shown, ", "), len(names)-maxDisclosureNames)
}

// budgetDiff shapes diff under cfg's caps/globs and returns the shaped diff
// plus a BudgetReport. It is pure: string in, string + report out, no I/O.
//
// When nothing needs shaping (no elidable/binary file, no file over the
// per-file cap, and the whole diff already fits the byte cap) it returns diff
// UNCHANGED (byte-identical) with report.Truncated == false.
func budgetDiff(diff string, cfg BudgetConfig) (string, BudgetReport) {
	report := BudgetReport{OriginalBytes: len(diff), ShapedBytes: len(diff)}
	if strings.TrimSpace(diff) == "" {
		return diff, report
	}

	globs := cfg.ElisionGlobs
	if len(globs) == 0 {
		globs = repoconfig.DefaultDiffElisionGlobs()
	}
	byteCap := cfg.ByteCap
	if byteCap <= 0 {
		byteCap = defaultDiffByteCap
	}
	perFileCap := cfg.PerFileLineCap
	if perFileCap <= 0 {
		perFileCap = defaultDiffPerFileLineCap
	}
	// cfg.Policy is intentionally unused in R3 — the seam exists but no
	// per-specialist behaviour is wired yet.
	_ = cfg.Policy

	_, blocks := splitDiffIntoFileBlocks(diff)
	if len(blocks) == 0 {
		// Nothing we recognise as a file stanza (empty or preamble-only) —
		// leave verbatim rather than risk mangling an unfamiliar shape.
		return diff, report
	}

	// item is the working record for one file block during shaping.
	type item struct {
		path    string
		text    string // rendered diff text (verbatim or tail-truncated)
		elide   bool
		reason  string
		lines   int // diff-line count for the manifest
		omitted int // lines dropped by per-file / byte-cap truncation
	}

	items := make([]item, 0, len(blocks))
	for _, blk := range blocks {
		fd := parseSingleFileBlock(blk)
		p := fd.Path
		if p == "" {
			p = fd.OldPath
		}
		diffLines := fd.Additions + fd.Deletions
		if reason := elisionReason(fd, p, globs); reason != "" {
			ln := diffLines
			if ln == 0 {
				ln = countLines(blk)
			}
			items = append(items, item{path: p, elide: true, reason: reason, lines: ln})
			continue
		}
		text, omitted := applyPerFileLineCap(blk, perFileCap)
		items = append(items, item{path: p, text: text, lines: diffLines, omitted: omitted})
	}

	// Reserve manifest headroom with a safe over-estimate (assume every block
	// could need a manifest line). Because a byte-cap elision REMOVES a whole
	// block from content and adds only one short manifest line, budgeting
	// content against this reservation guarantees the final output (content +
	// manifest) stays at or below byteCap.
	reserved := 0
	for _, it := range items {
		reserved += len(it.path) + 96
	}
	contentCap := byteCap - reserved
	if contentCap < 0 {
		contentCap = 0
	}

	// Byte-cap pass over kept (rendered) blocks in diff order.
	content := 0
	byteCapHit := false
	for i := range items {
		if items[i].elide {
			continue
		}
		if byteCapHit {
			items[i] = item{path: items[i].path, elide: true, reason: "byte cap", lines: items[i].lines}
			continue
		}
		need := len(items[i].text) + 2 // + "\n\n" separator between blocks
		if content+need <= contentCap {
			content += need
			continue
		}
		// This block overflows the budget. Keep a leading prefix if there is
		// meaningful room; otherwise elide it whole. Either way, everything
		// after it is elided (byte cap reached).
		remaining := contentCap - content
		if remaining >= minKeepBytesForTruncatedFile {
			truncText, omitted := tailTruncateBlockToBytes(items[i].text, remaining)
			items[i].text = truncText
			items[i].omitted += omitted
			content += len(truncText) + 2
		} else {
			items[i] = item{path: items[i].path, elide: true, reason: "byte cap", lines: items[i].lines}
		}
		byteCapHit = true
	}

	// Did anything actually change? If not — and the whole diff already fits —
	// return the ORIGINAL string so small diffs are byte-identical.
	changed := false
	for _, it := range items {
		if it.elide || it.omitted > 0 {
			changed = true
			break
		}
	}
	if !changed && len(diff) <= byteCap {
		return diff, report
	}

	// Assemble: a manifest preamble (which ParseDiff skips, since it precedes
	// the first "diff --git" line) followed by the kept file blocks.
	var elided []ElidedFile
	var truncs []TruncatedFile
	var manifestLines []string
	var kept []string
	for _, it := range items {
		if it.elide {
			elided = append(elided, ElidedFile{Path: it.path, Reason: it.reason, Lines: it.lines})
			manifestLines = append(manifestLines, formatManifestLine(it.path, it.reason, it.lines))
			continue
		}
		if it.omitted > 0 {
			truncs = append(truncs, TruncatedFile{Path: it.path, OmittedLines: it.omitted})
		}
		kept = append(kept, it.text)
	}

	var b strings.Builder
	if len(manifestLines) > 0 || len(truncs) > 0 {
		b.WriteString("[appr-ai-sal] This diff was shaped to fit the review context budget; the review did not see the full diff.\n")
		if len(manifestLines) > 0 {
			b.WriteString("Files elided entirely (not reviewed; listed so you can review them manually if needed):\n")
			for _, m := range manifestLines {
				b.WriteString(m)
				b.WriteString("\n")
			}
		}
		if len(truncs) > 0 {
			b.WriteString("Files truncated (only the leading portion was reviewed):\n")
			for _, t := range truncs {
				fmt.Fprintf(&b, "  ~ %s (%d lines omitted)\n", t.Path, t.OmittedLines)
			}
		}
		b.WriteString("\n")
	}
	for i, k := range kept {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(k)
		if !strings.HasSuffix(k, "\n") {
			b.WriteString("\n")
		}
	}

	shaped := b.String()
	report.Truncated = true
	report.Elided = elided
	report.Truncations = truncs
	report.ShapedBytes = len(shaped)
	return shaped, report
}

// splitDiffIntoFileBlocks splits a unified diff into per-file blocks. Each
// block is the exact substring from a "diff --git " line up to (but not
// including) the next one; text before the first such line is returned as the
// preamble. Splitting on "\n" and rejoining with "\n" is byte-faithful, so a
// block that is kept verbatim reproduces the original bytes exactly.
func splitDiffIntoFileBlocks(diff string) (string, []string) {
	lines := strings.Split(diff, "\n")
	var preamble []string
	var blocks []string
	var cur []string
	started := false
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, ln := range lines {
		if strings.HasPrefix(ln, "diff --git ") {
			flush()
			started = true
			cur = append(cur, ln)
			continue
		}
		if !started {
			preamble = append(preamble, ln)
			continue
		}
		cur = append(cur, ln)
	}
	flush()
	return strings.Join(preamble, "\n"), blocks
}

// parseSingleFileBlock parses one file block into a FileDiff (or the zero value
// when the block has no recognisable stanza).
func parseSingleFileBlock(block string) FileDiff {
	fds := ParseDiff(block)
	if len(fds) == 0 {
		return FileDiff{}
	}
	return fds[0]
}

// elisionReason returns a non-empty category when the file should be dropped
// from the diff (binary content, or a match against one of the elision globs),
// or "" to keep it.
func elisionReason(fd FileDiff, p string, globs []string) string {
	if fd.IsBinary {
		return "binary"
	}
	for _, g := range globs {
		if matchDiffGlob(g, p) {
			return elisionCategory(g)
		}
		if fd.OldPath != "" && fd.OldPath != p && matchDiffGlob(g, fd.OldPath) {
			return elisionCategory(g)
		}
	}
	return ""
}

// matchDiffGlob matches path p against a single glob using gitignore-flavoured
// rules: a trailing "/" is a directory prefix; a pattern containing "/" is
// matched against the full path; a slash-free pattern is matched against the
// basename (so "*_generated*" matches "pkg/foo_generated.go" even though
// path.Match's "*" does not cross a "/").
func matchDiffGlob(glob, p string) bool {
	if p == "" {
		return false
	}
	g := strings.TrimSpace(glob)
	if g == "" {
		return false
	}
	if strings.HasSuffix(g, "/") {
		dir := strings.TrimSuffix(g, "/")
		if dir == "" {
			return false
		}
		return p == dir || strings.HasPrefix(p, dir+"/") || strings.Contains(p, "/"+dir+"/")
	}
	if strings.Contains(g, "/") {
		ok, _ := path.Match(g, p)
		return ok
	}
	ok, _ := path.Match(g, path.Base(p))
	return ok
}

// elisionCategory maps a glob to a short human-readable reason for the manifest
// and disclosure line.
func elisionCategory(glob string) string {
	g := strings.ToLower(glob)
	switch {
	case strings.Contains(g, "lock") || strings.Contains(g, ".sum"):
		return "lockfile"
	case strings.Contains(g, "vendor"):
		return "vendored"
	case strings.Contains(g, "generated"):
		return "generated"
	case strings.Contains(g, "min."):
		return "minified asset"
	default:
		return "matched " + glob
	}
}

// applyPerFileLineCap trims a file block to at most cap unified-diff lines,
// appending a "…N lines omitted" marker when it trims. Only the TAIL is
// dropped, so every kept line (and its `@@` header) retains its real line
// number. Returns the (possibly trimmed) block text and the omitted-line
// count (0 when untouched).
func applyPerFileLineCap(block string, lineCap int) (string, int) {
	if lineCap <= 0 {
		return block, 0
	}
	lines := strings.Split(block, "\n")
	if len(lines) <= lineCap {
		return block, 0
	}
	kept := lines[:lineCap]
	omitted := len(lines) - lineCap
	marker := fmt.Sprintf("… %d lines omitted (appr-ai-sal per-file cap) …", omitted)
	return strings.Join(kept, "\n") + "\n" + marker, omitted
}

// tailTruncateBlockToBytes keeps whole leading lines of block until adding the
// next line (plus a marker reservation) would exceed maxBytes, then appends a
// "…N lines omitted" marker. Leading lines keep their real line numbers.
func tailTruncateBlockToBytes(block string, maxBytes int) (string, int) {
	lines := strings.Split(block, "\n")
	const markerReserve = 64
	var b strings.Builder
	kept := 0
	for _, ln := range lines {
		extra := len(ln) + 1 // + newline
		if b.Len()+extra+markerReserve > maxBytes {
			break
		}
		if kept > 0 {
			b.WriteString("\n")
		}
		b.WriteString(ln)
		kept++
	}
	omitted := len(lines) - kept
	if omitted <= 0 {
		return block, 0
	}
	if kept > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "… %d lines omitted (appr-ai-sal byte cap) …", omitted)
	return b.String(), omitted
}

// formatManifestLine renders one elided-file manifest entry, e.g.
// "  + go.sum (elided: lockfile, 1240 lines)".
func formatManifestLine(p, reason string, lines int) string {
	if lines > 0 {
		return fmt.Sprintf("  + %s (elided: %s, %d lines)", p, reason, lines)
	}
	return fmt.Sprintf("  + %s (elided: %s)", p, reason)
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
