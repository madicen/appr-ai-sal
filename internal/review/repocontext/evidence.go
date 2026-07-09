package repocontext

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// EvidenceOptions configures per-PR test/doc convention sampling.
type EvidenceOptions struct {
	// Worktree is the PR checkout root (required).
	Worktree string
	// LocalRoot is a fallback clone used only when Worktree is missing files.
	LocalRoot string
	// ChangedPaths lists post-image relative paths of files modified by the PR.
	// When empty, BuildEvidence returns an empty Evidence (no PR scope).
	ChangedPaths []string
	// MaxBytes caps the rendered markdown size in FormatEvidenceMarkdown.
	MaxBytes int
}

// FileEvidence is per-changed-file static evidence about test/doc neighbors.
type FileEvidence struct {
	// Path is the post-image relative path of the changed file.
	Path string
	// Language is a coarse classifier: "go", "python", "ts", "js", "hcl", "rust", "ruby", "java", "kotlin", "swift", "c", "cpp", "csharp", "shell", "sql", "yaml", "json", "markdown", or "other".
	Language string
	// IsSource is true when the file is plausibly a code source file the testing/docs specialists weigh in on.
	IsSource bool
	// IsTest is true when the file's name matches the language's test pattern.
	IsTest bool
	// IsDoc is true when the file is a doc-style file (.md, README, CHANGELOG, docs/*).
	IsDoc bool
	// HasSiblingTest is true when at least one test file lives in the same directory as a source file.
	HasSiblingTest bool
	// SiblingTestPath is one representative neighbor test file path (or "" if none).
	SiblingTestPath string
	// PackageHasDocGo is true (Go only) when a doc.go exists in the file's directory.
	PackageHasDocGo bool
	// DirReadmePath is the path of a README/markdown file in the same directory (or "").
	DirReadmePath string
	// ExportedSymbols counts top-level exported declarations the harvester could detect (Go/Python/TS).
	ExportedSymbols int
	// DocumentedExportedSymbols counts those that have a leading doc comment / docstring.
	DocumentedExportedSymbols int
}

// EvidenceAggregates summarises FileEvidence across the changed file set.
type EvidenceAggregates struct {
	ChangedSourceFiles                    int
	ChangedTestFiles                      int
	ChangedDocFiles                       int
	ChangedSourceFilesWithSiblingTest     int
	ChangedSourceFilesInPackageWithDocGo  int
	TotalExportedSymbolsTouched           int
	TotalDocumentedExportedSymbolsTouched int
}

// Evidence is the full per-PR static convention sample.
type Evidence struct {
	Worktree   string
	Files      []FileEvidence
	Aggregates EvidenceAggregates
	// RepresentativeTestHeader is the first ~20 lines of a representative
	// test file located somewhere near the changed source files. Empty when
	// no nearby test was found.
	RepresentativeTestHeader     string
	RepresentativeTestHeaderPath string
}

// BuildEvidence walks the changed paths, classifies each one, and gathers the
// per-file and aggregate facts used by the testing/docs repo agents and
// specialists. It is read-only and capped — heavy walks are bounded by
// MaxBytes (used at format time) and a fixed per-directory scan budget.
func BuildEvidence(ctx context.Context, opts EvidenceOptions) (*Evidence, error) {
	_ = ctx
	if strings.TrimSpace(opts.Worktree) == "" {
		return nil, fmt.Errorf("evidence: empty worktree")
	}
	root := filepath.Clean(opts.Worktree)
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("evidence: worktree not a directory: %s", opts.Worktree)
	}
	local := strings.TrimSpace(opts.LocalRoot)
	if local != "" {
		local = filepath.Clean(local)
		if st, err := os.Stat(local); err != nil || !st.IsDir() {
			local = ""
		}
	}

	ev := &Evidence{Worktree: root}
	dirCache := map[string]*dirSample{}
	for _, raw := range opts.ChangedPaths {
		rel := filepath.ToSlash(strings.TrimSpace(raw))
		if rel == "" || deniedPath(rel) {
			continue
		}
		fe := classifyChangedFile(root, local, rel, dirCache)
		ev.Files = append(ev.Files, fe)
	}
	ev.Aggregates = aggregate(ev.Files)
	if header, path := pickRepresentativeTest(root, dirCache); header != "" {
		ev.RepresentativeTestHeader = header
		ev.RepresentativeTestHeaderPath = path
	}
	return ev, nil
}

// dirSample is a cached scan of one directory under the worktree.
type dirSample struct {
	dir         string
	sourceFiles []string
	testFiles   []string
	docFiles    []string
	hasDocGo    bool
	readmePath  string
}

const dirEntryScanCap = 400

func sampleDir(root, relDir string, cache map[string]*dirSample) *dirSample {
	key := filepath.ToSlash(relDir)
	if s, ok := cache[key]; ok {
		return s
	}
	s := &dirSample{dir: key}
	cache[key] = s
	abs := filepath.Join(root, relDir)
	ents, err := os.ReadDir(abs)
	if err != nil {
		return s
	}
	count := 0
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		count++
		if count > dirEntryScanCap {
			break
		}
		name := e.Name()
		if deniedPath(filepath.ToSlash(filepath.Join(relDir, name))) {
			continue
		}
		lang := languageForName(name)
		switch {
		case isTestName(name, lang):
			s.testFiles = append(s.testFiles, filepath.ToSlash(filepath.Join(relDir, name)))
		case isDocName(name):
			s.docFiles = append(s.docFiles, filepath.ToSlash(filepath.Join(relDir, name)))
			if isReadmeName(name) && s.readmePath == "" {
				s.readmePath = filepath.ToSlash(filepath.Join(relDir, name))
			}
		case isSourceLanguage(lang):
			s.sourceFiles = append(s.sourceFiles, filepath.ToSlash(filepath.Join(relDir, name)))
			if lang == "go" && strings.EqualFold(name, "doc.go") {
				s.hasDocGo = true
			}
		}
	}
	return s
}

func classifyChangedFile(root, local, rel string, cache map[string]*dirSample) FileEvidence {
	lang := languageForName(filepath.Base(rel))
	fe := FileEvidence{
		Path:     rel,
		Language: lang,
		IsTest:   isTestName(filepath.Base(rel), lang),
		IsDoc:    isDocName(filepath.Base(rel)),
		IsSource: isSourceLanguage(lang) && !isTestName(filepath.Base(rel), lang) && !isDocName(filepath.Base(rel)),
	}
	dir := filepath.ToSlash(filepath.Dir(rel))
	if dir == "." {
		dir = ""
	}
	sample := sampleDir(root, dir, cache)
	// Sibling tests: any test file in the same dir; for Go specifically we
	// also check the filename-paired _test.go convention.
	if len(sample.testFiles) > 0 {
		fe.HasSiblingTest = true
		fe.SiblingTestPath = sample.testFiles[0]
	}
	if lang == "go" && fe.IsSource {
		base := strings.TrimSuffix(filepath.Base(rel), ".go")
		paired := filepath.ToSlash(filepath.Join(dir, base+"_test.go"))
		if _, err := os.Stat(filepath.Join(root, paired)); err == nil {
			fe.HasSiblingTest = true
			fe.SiblingTestPath = paired
		} else if local != "" {
			if _, err := os.Stat(filepath.Join(local, paired)); err == nil {
				fe.HasSiblingTest = true
				fe.SiblingTestPath = paired
			}
		}
	}
	fe.PackageHasDocGo = sample.hasDocGo
	fe.DirReadmePath = sample.readmePath
	if fe.IsSource {
		exported, documented := countExportedSymbolsInFile(filepath.Join(root, rel), lang)
		if exported == 0 && local != "" {
			exported, documented = countExportedSymbolsInFile(filepath.Join(local, rel), lang)
		}
		fe.ExportedSymbols = exported
		fe.DocumentedExportedSymbols = documented
	}
	return fe
}

func aggregate(files []FileEvidence) EvidenceAggregates {
	var a EvidenceAggregates
	for _, f := range files {
		switch {
		case f.IsTest:
			a.ChangedTestFiles++
		case f.IsDoc:
			a.ChangedDocFiles++
		case f.IsSource:
			a.ChangedSourceFiles++
			if f.HasSiblingTest {
				a.ChangedSourceFilesWithSiblingTest++
			}
			if f.PackageHasDocGo {
				a.ChangedSourceFilesInPackageWithDocGo++
			}
			a.TotalExportedSymbolsTouched += f.ExportedSymbols
			a.TotalDocumentedExportedSymbolsTouched += f.DocumentedExportedSymbols
		}
	}
	return a
}

// pickRepresentativeTest tries to surface one nearby test file's header so
// the agent / specialist can see the repo's actual test shape. Iterates the
// dirs we already cached during the per-file pass; returns the first viable
// candidate.
func pickRepresentativeTest(root string, cache map[string]*dirSample) (string, string) {
	dirs := make([]string, 0, len(cache))
	for d := range cache {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		s := cache[d]
		if len(s.testFiles) == 0 {
			continue
		}
		path := s.testFiles[0]
		body := readFileLimited(filepath.Join(root, path), perFileReadCap)
		if body == "" {
			continue
		}
		return headLines(body, 20), path
	}
	return "", ""
}

// FormatEvidenceMarkdown renders ev as a scannable markdown section, capped
// at maxBytes. Returns "" when ev is nil/empty.
func FormatEvidenceMarkdown(ev *Evidence, maxBytes int) string {
	if ev == nil || len(ev.Files) == 0 {
		return ""
	}
	if maxBytes <= 0 {
		maxBytes = 4096
	}
	var b strings.Builder
	a := ev.Aggregates
	fmt.Fprintf(&b, "_Per-PR static evidence (auto-harvested from the worktree)._\n\n")
	fmt.Fprintf(&b, "- Changed source files: **%d** (with sibling test files: **%d**; in packages with `doc.go`: **%d**).\n",
		a.ChangedSourceFiles, a.ChangedSourceFilesWithSiblingTest, a.ChangedSourceFilesInPackageWithDocGo)
	fmt.Fprintf(&b, "- Changed test files: **%d**; changed doc/markdown files: **%d**.\n",
		a.ChangedTestFiles, a.ChangedDocFiles)
	if a.TotalExportedSymbolsTouched > 0 {
		ratio := 0.0
		if a.TotalExportedSymbolsTouched > 0 {
			ratio = float64(a.TotalDocumentedExportedSymbolsTouched) / float64(a.TotalExportedSymbolsTouched)
		}
		fmt.Fprintf(&b, "- Exported declarations in touched source files: **%d** total, **%d** documented (~%d%%).\n",
			a.TotalExportedSymbolsTouched, a.TotalDocumentedExportedSymbolsTouched, int(ratio*100+0.5))
	}
	b.WriteString("\n")

	files := append([]FileEvidence(nil), ev.Files...)
	sort.SliceStable(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if hasInteresting(files) {
		b.WriteString("Per-file convention neighbours:\n\n")
		for _, f := range files {
			line := perFileLine(f)
			if line == "" {
				continue
			}
			b.WriteString(line)
			b.WriteString("\n")
			if b.Len() > maxBytes-200 {
				b.WriteString("…(truncated)\n")
				break
			}
		}
		b.WriteString("\n")
	}

	if strings.TrimSpace(ev.RepresentativeTestHeader) != "" && b.Len() < maxBytes-200 {
		fmt.Fprintf(&b, "Representative existing test (`%s`, head):\n\n```\n%s\n```\n",
			ev.RepresentativeTestHeaderPath, strings.TrimRight(ev.RepresentativeTestHeader, "\n"))
	}

	out := b.String()
	if len(out) > maxBytes {
		out = out[:maxBytes] + "\n…(truncated)\n"
	}
	return out
}

func hasInteresting(files []FileEvidence) bool {
	for _, f := range files {
		if f.IsSource || f.IsTest || f.IsDoc {
			return true
		}
	}
	return false
}

func perFileLine(f FileEvidence) string {
	if !(f.IsSource || f.IsTest || f.IsDoc) {
		return ""
	}
	bits := []string{}
	switch {
	case f.IsTest:
		bits = append(bits, "test file")
	case f.IsDoc:
		bits = append(bits, "doc/markdown")
	case f.IsSource:
		if f.HasSiblingTest {
			bits = append(bits, fmt.Sprintf("sibling test: `%s`", f.SiblingTestPath))
		} else {
			bits = append(bits, "no sibling test in same directory")
		}
		if f.PackageHasDocGo {
			bits = append(bits, "package has `doc.go`")
		}
		if f.DirReadmePath != "" {
			bits = append(bits, fmt.Sprintf("dir README: `%s`", f.DirReadmePath))
		}
		if f.ExportedSymbols > 0 {
			bits = append(bits, fmt.Sprintf("exported decls: %d (documented: %d)", f.ExportedSymbols, f.DocumentedExportedSymbols))
		}
	}
	if len(bits) == 0 {
		return ""
	}
	return fmt.Sprintf("- `%s` (%s) — %s", f.Path, f.Language, strings.Join(bits, "; "))
}

// languageForName returns a coarse language tag derived from the file name.
func languageForName(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".go"):
		return "go"
	case strings.HasSuffix(lower, ".py"):
		return "python"
	case strings.HasSuffix(lower, ".ts"), strings.HasSuffix(lower, ".tsx"):
		return "ts"
	case strings.HasSuffix(lower, ".js"), strings.HasSuffix(lower, ".jsx"), strings.HasSuffix(lower, ".mjs"), strings.HasSuffix(lower, ".cjs"):
		return "js"
	case strings.HasSuffix(lower, ".tf"), strings.HasSuffix(lower, ".hcl"):
		return "hcl"
	case strings.HasSuffix(lower, ".rs"):
		return "rust"
	case strings.HasSuffix(lower, ".rb"):
		return "ruby"
	case strings.HasSuffix(lower, ".java"):
		return "java"
	case strings.HasSuffix(lower, ".kt"), strings.HasSuffix(lower, ".kts"):
		return "kotlin"
	case strings.HasSuffix(lower, ".swift"):
		return "swift"
	case strings.HasSuffix(lower, ".c"), strings.HasSuffix(lower, ".h"):
		return "c"
	case strings.HasSuffix(lower, ".cpp"), strings.HasSuffix(lower, ".cc"), strings.HasSuffix(lower, ".cxx"), strings.HasSuffix(lower, ".hpp"):
		return "cpp"
	case strings.HasSuffix(lower, ".cs"):
		return "csharp"
	case strings.HasSuffix(lower, ".sh"), strings.HasSuffix(lower, ".bash"), strings.HasSuffix(lower, ".zsh"):
		return "shell"
	case strings.HasSuffix(lower, ".sql"):
		return "sql"
	case strings.HasSuffix(lower, ".yml"), strings.HasSuffix(lower, ".yaml"):
		return "yaml"
	case strings.HasSuffix(lower, ".json"):
		return "json"
	case strings.HasSuffix(lower, ".md"), strings.HasSuffix(lower, ".markdown"):
		return "markdown"
	default:
		return "other"
	}
}

func isSourceLanguage(lang string) bool {
	switch lang {
	case "go", "python", "ts", "js", "hcl", "rust", "ruby", "java", "kotlin", "swift", "c", "cpp", "csharp":
		return true
	}
	return false
}

func isTestName(name, lang string) bool {
	lower := strings.ToLower(name)
	switch lang {
	case "go":
		return strings.HasSuffix(lower, "_test.go")
	case "python":
		return strings.HasPrefix(lower, "test_") || strings.HasSuffix(lower, "_test.py")
	case "ts", "js":
		if strings.HasSuffix(lower, ".test.ts") || strings.HasSuffix(lower, ".test.tsx") || strings.HasSuffix(lower, ".test.js") || strings.HasSuffix(lower, ".test.jsx") {
			return true
		}
		if strings.HasSuffix(lower, ".spec.ts") || strings.HasSuffix(lower, ".spec.tsx") || strings.HasSuffix(lower, ".spec.js") || strings.HasSuffix(lower, ".spec.jsx") {
			return true
		}
		return false
	case "rust":
		return strings.Contains(lower, "tests/") || strings.HasSuffix(lower, "_test.rs")
	case "ruby":
		return strings.HasSuffix(lower, "_spec.rb") || strings.HasSuffix(lower, "_test.rb")
	case "java", "kotlin":
		return strings.HasSuffix(lower, "test.java") || strings.HasSuffix(lower, "tests.java") ||
			strings.HasSuffix(lower, "test.kt") || strings.HasSuffix(lower, "tests.kt")
	}
	return false
}

func isDocName(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown") || strings.HasSuffix(lower, ".rst") {
		return true
	}
	switch lower {
	case "readme", "readme.txt", "changelog", "changelog.txt":
		return true
	}
	return false
}

func isReadmeName(name string) bool {
	lower := strings.ToLower(name)
	return lower == "readme.md" || lower == "readme.markdown" || lower == "readme" || lower == "readme.txt"
}

// countExportedSymbolsInFile returns (exported, documented) for the file at
// path. Heuristics are language-specific and intentionally cheap (no AST):
// scan top-level lines and look for the language's "exported" markers, with
// the immediately preceding non-blank line(s) treated as a doc comment.
func countExportedSymbolsInFile(path, lang string) (int, int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	const lineCap = 4000
	const fileLineCap = 5000
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var lines []string
	for sc.Scan() {
		line := sc.Text()
		if len(line) > lineCap {
			line = line[:lineCap]
		}
		lines = append(lines, line)
		if len(lines) >= fileLineCap {
			break
		}
	}
	switch lang {
	case "go":
		return countGoExports(lines)
	case "python":
		return countPythonExports(lines)
	case "ts", "js":
		return countTSExports(lines)
	}
	return 0, 0
}

func countGoExports(lines []string) (int, int) {
	exported := 0
	documented := 0
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(raw, "func ") && !strings.HasPrefix(raw, "type ") &&
			!strings.HasPrefix(raw, "var ") && !strings.HasPrefix(raw, "const ") {
			continue
		}
		ident := goDeclIdentifier(line)
		if ident == "" || !startsWithUpper(ident) {
			continue
		}
		exported++
		if hasGoStyleDocAbove(lines, i, ident) {
			documented++
		}
	}
	return exported, documented
}

func goDeclIdentifier(line string) string {
	switch {
	case strings.HasPrefix(line, "func "):
		rest := strings.TrimPrefix(line, "func ")
		if strings.HasPrefix(rest, "(") {
			if idx := strings.Index(rest, ")"); idx > 0 {
				rest = strings.TrimSpace(rest[idx+1:])
			}
		}
		return firstIdentifier(rest)
	case strings.HasPrefix(line, "type "):
		return firstIdentifier(strings.TrimPrefix(line, "type "))
	case strings.HasPrefix(line, "var "):
		return firstIdentifier(strings.TrimPrefix(line, "var "))
	case strings.HasPrefix(line, "const "):
		return firstIdentifier(strings.TrimPrefix(line, "const "))
	}
	return ""
}

func hasGoStyleDocAbove(lines []string, i int, ident string) bool {
	for j := i - 1; j >= 0; j-- {
		t := strings.TrimSpace(lines[j])
		if t == "" {
			return false
		}
		if !strings.HasPrefix(t, "//") {
			return false
		}
		if strings.HasPrefix(t, "// "+ident) || strings.HasPrefix(t, "//"+ident) {
			return true
		}
		// Walk further up — godoc may span multiple comment lines.
	}
	return false
}

func countPythonExports(lines []string) (int, int) {
	exported := 0
	documented := 0
	for i, raw := range lines {
		t := strings.TrimSpace(raw)
		if !(strings.HasPrefix(raw, "def ") || strings.HasPrefix(raw, "class ") ||
			strings.HasPrefix(raw, "async def ")) {
			continue
		}
		ident := firstIdentifier(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(t, "async def "), "def "), "class "))
		if ident == "" || strings.HasPrefix(ident, "_") {
			continue
		}
		exported++
		if hasPythonDocstring(lines, i) {
			documented++
		}
	}
	return exported, documented
}

func hasPythonDocstring(lines []string, i int) bool {
	for j := i + 1; j < len(lines) && j <= i+3; j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, `"""`) || strings.HasPrefix(t, "'''") || strings.HasPrefix(t, `r"""`) || strings.HasPrefix(t, `r'''`) {
			return true
		}
		return false
	}
	return false
}

func countTSExports(lines []string) (int, int) {
	exported := 0
	documented := 0
	for i, raw := range lines {
		t := strings.TrimSpace(raw)
		if !strings.HasPrefix(t, "export ") {
			continue
		}
		rest := strings.TrimPrefix(t, "export ")
		rest = strings.TrimPrefix(rest, "default ")
		switch {
		case strings.HasPrefix(rest, "function"),
			strings.HasPrefix(rest, "class"),
			strings.HasPrefix(rest, "interface"),
			strings.HasPrefix(rest, "type "),
			strings.HasPrefix(rest, "enum"),
			strings.HasPrefix(rest, "const "),
			strings.HasPrefix(rest, "let "),
			strings.HasPrefix(rest, "var "),
			strings.HasPrefix(rest, "async function"):
			exported++
			if hasJSDocAbove(lines, i) {
				documented++
			}
		}
	}
	return exported, documented
}

func hasJSDocAbove(lines []string, i int) bool {
	if i <= 0 {
		return false
	}
	t := strings.TrimSpace(lines[i-1])
	if strings.HasPrefix(t, "//") {
		return true
	}
	return strings.HasPrefix(t, "*") || strings.HasPrefix(t, "*/") || strings.HasPrefix(t, "/**") || strings.HasPrefix(t, "/*")
}

func firstIdentifier(s string) string {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) {
		c := s[end]
		if !(c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			break
		}
		end++
	}
	return s[:end]
}

func startsWithUpper(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	return c >= 'A' && c <= 'Z'
}

// RepoWideEvidenceOptions configures a bounded repo walk used to evidence
// general test/doc habits at agent-generation time (when there is no PR diff).
type RepoWideEvidenceOptions struct {
	Worktree string
	// MaxDirs caps how many directories the walker will sample. Defaults to 60.
	MaxDirs int
	// MaxFilesPerDir caps how many files per directory the walker will read for
	// exported-symbol counts. Defaults to 6.
	MaxFilesPerDir int
}

// RepoWideEvidence summarises test/doc presence across the repo. It is
// intentionally coarse (sampled, capped) — its purpose is to give the
// testing/docs repo-agent generators a few concrete numbers to ground their
// briefs in, not to be a full coverage report.
type RepoWideEvidence struct {
	DirsSampled                 int
	DirsWithSourceFiles         int
	DirsWithSourceAndTest       int
	DirsWithDocGo               int
	DirsWithReadme              int
	TotalSourceFilesSampled     int
	TotalTestFilesSampled       int
	TotalDocFilesSampled        int
	TotalExportedSymbolsSampled int
	TotalDocumentedSampled      int
}

// BuildRepoWideEvidence walks (depth-first, bounded) the worktree and
// returns aggregate counts. Directories listed in deniedPath are skipped.
// The walk is hard-capped at opts.MaxDirs (default 60) so even very large
// repos return quickly.
func BuildRepoWideEvidence(ctx context.Context, opts RepoWideEvidenceOptions) (*RepoWideEvidence, error) {
	_ = ctx
	if strings.TrimSpace(opts.Worktree) == "" {
		return nil, fmt.Errorf("repo-wide evidence: empty worktree")
	}
	root := filepath.Clean(opts.Worktree)
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("repo-wide evidence: worktree not a directory: %s", opts.Worktree)
	}
	maxDirs := opts.MaxDirs
	if maxDirs <= 0 {
		maxDirs = 60
	}
	maxFiles := opts.MaxFilesPerDir
	if maxFiles <= 0 {
		maxFiles = 6
	}

	ev := &RepoWideEvidence{}
	visited := 0
	var walk func(rel string)
	walk = func(rel string) {
		if visited >= maxDirs {
			return
		}
		if deniedPath(rel) {
			return
		}
		abs := filepath.Join(root, rel)
		ents, err := os.ReadDir(abs)
		if err != nil {
			return
		}
		visited++
		ev.DirsSampled++
		var sources, tests []string
		hasDocGo := false
		hasReadme := false
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			rp := filepath.ToSlash(filepath.Join(rel, name))
			if deniedPath(rp) {
				continue
			}
			lang := languageForName(name)
			switch {
			case isTestName(name, lang):
				tests = append(tests, rp)
				ev.TotalTestFilesSampled++
			case isDocName(name):
				ev.TotalDocFilesSampled++
				if isReadmeName(name) {
					hasReadme = true
				}
			case isSourceLanguage(lang):
				sources = append(sources, rp)
				ev.TotalSourceFilesSampled++
				if lang == "go" && strings.EqualFold(name, "doc.go") {
					hasDocGo = true
				}
			}
		}
		if len(sources) > 0 {
			ev.DirsWithSourceFiles++
			if len(tests) > 0 {
				ev.DirsWithSourceAndTest++
			}
		}
		if hasDocGo {
			ev.DirsWithDocGo++
		}
		if hasReadme {
			ev.DirsWithReadme++
		}
		// Sample a few source files in this dir for export-symbol coverage.
		count := 0
		for _, src := range sources {
			if count >= maxFiles {
				break
			}
			lang := languageForName(filepath.Base(src))
			exported, documented := countExportedSymbolsInFile(filepath.Join(root, src), lang)
			ev.TotalExportedSymbolsSampled += exported
			ev.TotalDocumentedSampled += documented
			count++
		}
		// Recurse into subdirs alphabetically so the sample is stable.
		var subs []string
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if deniedPath(filepath.ToSlash(filepath.Join(rel, name))) {
				continue
			}
			subs = append(subs, name)
		}
		sort.Strings(subs)
		for _, sub := range subs {
			if visited >= maxDirs {
				return
			}
			walk(filepath.ToSlash(filepath.Join(rel, sub)))
		}
	}
	walk("")
	return ev, nil
}

// FormatRepoWideEvidenceMarkdown renders a short bullet block summarising
// the repo-wide static evidence.
func FormatRepoWideEvidenceMarkdown(ev *RepoWideEvidence) string {
	if ev == nil || ev.DirsSampled == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("_Repo-wide static evidence (auto-harvested, sampled)._\n\n")
	fmt.Fprintf(&b, "- Directories sampled: **%d** (with source files: **%d**; source dirs that also contain a test file: **%d**).\n",
		ev.DirsSampled, ev.DirsWithSourceFiles, ev.DirsWithSourceAndTest)
	fmt.Fprintf(&b, "- Total source files sampled: **%d**; total test files: **%d**; total doc/markdown files: **%d**.\n",
		ev.TotalSourceFilesSampled, ev.TotalTestFilesSampled, ev.TotalDocFilesSampled)
	if ev.DirsWithDocGo > 0 || ev.DirsWithReadme > 0 {
		fmt.Fprintf(&b, "- Directories with `doc.go`: **%d**; with a README: **%d**.\n", ev.DirsWithDocGo, ev.DirsWithReadme)
	}
	if ev.TotalExportedSymbolsSampled > 0 {
		ratio := float64(ev.TotalDocumentedSampled) / float64(ev.TotalExportedSymbolsSampled)
		fmt.Fprintf(&b, "- Sampled exported declarations: **%d** total, **%d** documented (~%d%%).\n",
			ev.TotalExportedSymbolsSampled, ev.TotalDocumentedSampled, int(ratio*100+0.5))
	}
	return b.String()
}
