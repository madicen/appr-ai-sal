package contextexpand

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// enrichers.go holds the OPTIONAL cross-reference enrichers. They add cross-file
// callers/callees the hermetic AST baseline cannot cheaply see. Both are
// behind an Available() check (a binary on PATH), bounded by a timeout, and
// fully fail-open — a missing tool, a slow tool, or unparseable output
// contributes nothing. Their OUTPUT PARSING is factored into pure functions
// (parseGoplsReferences, parseCtags) tested with canned data, so the package's
// tests never need gopls or ctags installed.

// Location is a resolved file:line reference in the worktree.
type Location struct {
	// Path is repo-relative, forward-slashed (relative to the worktree).
	Path string
	// Line is the 1-indexed line number.
	Line int
}

// CrossRefResult is what one cross-reference lookup produced: the tool that
// answered (for telemetry) and the locations it found.
type CrossRefResult struct {
	Tool      string
	Locations []Location
}

// crossRefFunc finds cross-file references to a changed symbol. It is a package
// var seam so tests inject canned results without needing gopls/ctags.
type crossRefFunc func(ctx context.Context, worktree string, sym symbolRef) CrossRefResult

// lookPath is indirected so tests can stub tool availability without touching
// PATH.
var lookPath = exec.LookPath

// defaultCrossReferences tries gopls (references) first, then ctags. The first
// tool that is available AND returns at least one location wins. Fully
// fail-open: returns a zero CrossRefResult when neither tool helps.
func defaultCrossReferences(ctx context.Context, worktree string, sym symbolRef) CrossRefResult {
	if r := goplsCrossReferences(ctx, worktree, sym); len(r.Locations) > 0 {
		return r
	}
	if r := ctagsCrossReferences(ctx, worktree, sym); len(r.Locations) > 0 {
		return r
	}
	return CrossRefResult{}
}

// goplsCrossReferences runs `gopls references file:line:col` for the symbol and
// parses the referencing locations. Returns an empty result when gopls is
// unavailable or the call fails.
func goplsCrossReferences(ctx context.Context, worktree string, sym symbolRef) CrossRefResult {
	if _, err := lookPath("gopls"); err != nil {
		return CrossRefResult{}
	}
	abs := filepath.Join(worktree, filepath.FromSlash(sym.Path))
	pos := fmt.Sprintf("%s:%d:%d", abs, sym.Line, sym.Col)
	cmd := exec.CommandContext(ctx, "gopls", "references", pos)
	cmd.Dir = worktree
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return CrossRefResult{}
	}
	locs := parseGoplsReferences(out.String(), worktree)
	if len(locs) == 0 {
		return CrossRefResult{}
	}
	return CrossRefResult{Tool: "gopls", Locations: locs}
}

// parseGoplsReferences parses `gopls references` output. Each line is a
// location of the form `path:line:col` or `path:line:col-endcol`, where path
// is absolute. Paths are relativised against worktree (forward-slashed) and
// out-of-worktree hits are dropped. Pure: no I/O, so it is unit-tested with
// canned output.
func parseGoplsReferences(out, worktree string) []Location {
	var locs []Location
	seen := map[Location]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		path, lineNo, ok := splitPathLineCol(line)
		if !ok {
			continue
		}
		rel := relativiseWithin(worktree, path)
		if rel == "" {
			continue
		}
		loc := Location{Path: rel, Line: lineNo}
		if seen[loc] {
			continue
		}
		seen[loc] = true
		locs = append(locs, loc)
	}
	return locs
}

// splitPathLineCol parses `path:line:col[-...]` into path + line. It scans from
// the right so a Windows drive letter or a path containing colons is handled:
// the last two colon-separated fields are col and line.
func splitPathLineCol(s string) (path string, line int, ok bool) {
	// Trim a trailing `-endcol` / `:endcol` range and any trailing text.
	if i := strings.IndexByte(s, ' '); i >= 0 {
		s = s[:i]
	}
	last := strings.LastIndexByte(s, ':')
	if last <= 0 {
		return "", 0, false
	}
	rest := s[:last]
	// col field is s[last+1:]; strip any `-endcol`.
	prev := strings.LastIndexByte(rest, ':')
	if prev <= 0 {
		return "", 0, false
	}
	lineStr := rest[prev+1:]
	n, err := strconv.Atoi(lineStr)
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return rest[:prev], n, true
}

// relativiseWithin returns abs relative to worktree (forward-slashed) when abs
// is inside worktree, else "". A path that is already relative is returned
// forward-slashed as-is.
func relativiseWithin(worktree, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		return filepath.ToSlash(p)
	}
	rel, err := filepath.Rel(worktree, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel)
}

// ctagsCrossReferences generates a tag index for the worktree and returns the
// definition location(s) of the symbol's name. ctags indexes DEFINITIONS, so
// this resolves cross-package callee definitions the AST baseline (same
// package only) misses. Returns empty when ctags is unavailable / fails.
func ctagsCrossReferences(ctx context.Context, worktree string, sym symbolRef) CrossRefResult {
	if _, err := lookPath("ctags"); err != nil {
		return CrossRefResult{}
	}
	// -f - → write tags to stdout; --fields=+n → include the line number.
	cmd := exec.CommandContext(ctx, "ctags", "-f", "-", "--fields=+n", "-R", ".")
	cmd.Dir = worktree
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return CrossRefResult{}
	}
	tags := parseCtags(out.String())
	locs := tags[sym.Name]
	if len(locs) == 0 {
		return CrossRefResult{}
	}
	return CrossRefResult{Tool: "ctags", Locations: locs}
}

// parseCtags parses a tags file (universal-ctags / exuberant-ctags format)
// into name → definition locations. Each non-comment line is
// `name<TAB>file<TAB>address[;" extension-fields]`. The line number is taken
// from a `line:N` extension field when present, else from a numeric address.
// Pure: unit-tested with canned tag lines (ctags not required).
func parseCtags(out string) map[string][]Location {
	res := map[string][]Location{}
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, "!_TAG_") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		file := filepath.ToSlash(strings.TrimSpace(fields[1]))
		if name == "" || file == "" {
			continue
		}
		lineNo := ctagsLineNumber(fields[2:])
		if lineNo <= 0 {
			continue
		}
		loc := Location{Path: strings.TrimPrefix(file, "./"), Line: lineNo}
		res[name] = append(res[name], loc)
	}
	return res
}

// ctagsLineNumber extracts the line number from a tag's address + extension
// fields. It prefers an explicit `line:N` field; otherwise it accepts a purely
// numeric address (the `{n}` line-number address form).
func ctagsLineNumber(rest []string) int {
	for _, f := range rest {
		f = strings.TrimSpace(f)
		if strings.HasPrefix(f, "line:") {
			if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(f, "line:"))); err == nil {
				return n
			}
		}
		// Look inside a combined address field like `12;"` too.
		if i := strings.Index(f, "line:"); i >= 0 {
			tail := f[i+len("line:"):]
			tail = strings.TrimSpace(strings.TrimRight(tail, "\";"))
			if n, err := strconv.Atoi(firstInt(tail)); err == nil && n > 0 {
				return n
			}
		}
	}
	// Numeric address form: the address field is just `12;"` or `12`.
	if len(rest) > 0 {
		addr := strings.TrimRight(strings.TrimSpace(rest[0]), "\";")
		if n, err := strconv.Atoi(addr); err == nil {
			return n
		}
	}
	return 0
}

// firstInt returns the leading run of digits in s (empty when none).
func firstInt(s string) string {
	for i, r := range s {
		if r < '0' || r > '9' {
			return s[:i]
		}
	}
	return s
}
