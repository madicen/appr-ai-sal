package staticpass

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/review/repocontext"
)

// defaultTools is the real adapter set, gated by the detected lint configs.
// Config-driven linters (golangci-lint, eslint, ruff) are only included when
// the repo actually configures them, so the pre-pass never runs an opinionated
// linter the repo did not opt into.
func defaultTools(lint repocontext.LintConfigs) []Tool {
	return []Tool{
		gofmtTool{},
		goVetTool{},
		golangciTool{configured: lint.Golangci},
		ruffTool{configured: lint.Ruff},
		eslintTool{configured: lint.ESLint},
		terraformValidateTool{},
	}
}

// lookPath is indirected so tests can stub availability without touching PATH.
var lookPath = exec.LookPath

func binaryAvailable(name string) bool {
	_, err := lookPath(name)
	return err == nil
}

// runCmd executes name+args in dir, capturing stdout and stderr separately. It
// returns them as strings plus the run error (non-nil for a non-zero exit).
// Callers decide whether a non-zero exit is fatal — most static tools exit
// non-zero precisely when they found issues, which is not an error to us.
func runCmd(ctx context.Context, dir, name string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	// Keep the pre-pass strictly offline (the environment forbids network):
	// only go vet could otherwise try to reach the module proxy.
	cmd.Env = append(cmd.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	return out.String(), errb.String(), err
}

func filesWithExt(files []string, exts ...string) []string {
	want := map[string]bool{}
	for _, e := range exts {
		want[strings.ToLower(e)] = true
	}
	var out []string
	for _, f := range files {
		if want[strings.ToLower(filepath.Ext(f))] {
			out = append(out, filepath.ToSlash(f))
		}
	}
	return out
}

// uniqueDirs returns the unique parent directories (repo-relative, "." for
// root) of the given files, sorted for determinism.
func uniqueDirs(files []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		d := filepath.ToSlash(filepath.Dir(f))
		if d == "." || d == "" {
			d = "."
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// ---- gofmt -----------------------------------------------------------------

// gofmtTool runs `gofmt -l` over the changed Go files. gofmt ships with the Go
// toolchain, so it is the one tool guaranteed available in a Go dev/CI
// environment — which is why the Q5 acceptance test grounds on it.
type gofmtTool struct{}

func (gofmtTool) Name() string    { return "gofmt" }
func (gofmtTool) Formatter() bool { return true }
func (gofmtTool) Available() bool { return binaryAvailable("gofmt") }

func (gofmtTool) Run(ctx context.Context, worktree string, changedFiles []string) ([]Annotation, []string, error) {
	goFiles := filesWithExt(changedFiles, ".go")
	if len(goFiles) == 0 {
		return nil, nil, nil
	}
	stdout, _, err := runCmd(ctx, worktree, "gofmt", append([]string{"-l"}, goFiles...)...)
	anns := parseGofmtList(stdout)
	// checked = every Go file we asked gofmt about; the ones NOT listed are
	// clean, which FormatterCleanFiles derives from (checked − annotated).
	return anns, goFiles, err
}

// parseGofmtList turns `gofmt -l` stdout (one unformatted file path per line)
// into annotations.
func parseGofmtList(stdout string) []Annotation {
	var out []Annotation
	for _, line := range strings.Split(stdout, "\n") {
		p := filepath.ToSlash(strings.TrimSpace(line))
		if p == "" {
			continue
		}
		out = append(out, Annotation{
			Tool:    "gofmt",
			Path:    p,
			Line:    0,
			Level:   LevelWarning,
			Message: "file is not gofmt-formatted (run `gofmt -w`)",
		})
	}
	return out
}

// ---- go vet ----------------------------------------------------------------

type goVetTool struct{}

func (goVetTool) Name() string    { return "go vet" }
func (goVetTool) Formatter() bool { return false }
func (goVetTool) Available() bool { return binaryAvailable("go") }

func (goVetTool) Run(ctx context.Context, worktree string, changedFiles []string) ([]Annotation, []string, error) {
	goFiles := filesWithExt(changedFiles, ".go")
	if len(goFiles) == 0 {
		return nil, nil, nil
	}
	// Scope vet to the packages that actually changed, as ./dir patterns,
	// so we don't pay to vet the whole module.
	var patterns []string
	for _, d := range uniqueDirs(goFiles) {
		if d == "." {
			patterns = append(patterns, ".")
		} else {
			patterns = append(patterns, "./"+d)
		}
	}
	_, stderr, err := runCmd(ctx, worktree, "go", append([]string{"vet"}, patterns...)...)
	return parseGoVet(stderr), goFiles, err
}

var goVetLineRe = regexp.MustCompile(`^(?:\.[\\/])?(.+?\.go):(\d+):(?:\d+:)?\s+(.*)$`)

// parseGoVet extracts `file.go:line:col: message` diagnostics from go vet's
// stderr, skipping the build/setup noise vet interleaves.
func parseGoVet(stderr string) []Annotation {
	var out []Annotation
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		m := goVetLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ln, _ := strconv.Atoi(m[2])
		out = append(out, Annotation{
			Tool:    "go vet",
			Path:    filepath.ToSlash(m[1]),
			Line:    ln,
			Level:   LevelError,
			Message: strings.TrimSpace(m[3]),
		})
	}
	return out
}

// ---- golangci-lint ---------------------------------------------------------

type golangciTool struct{ configured bool }

func (golangciTool) Name() string      { return "golangci-lint" }
func (golangciTool) Formatter() bool   { return false }
func (t golangciTool) Available() bool { return t.configured && binaryAvailable("golangci-lint") }

func (t golangciTool) Run(ctx context.Context, worktree string, changedFiles []string) ([]Annotation, []string, error) {
	goFiles := filesWithExt(changedFiles, ".go")
	if len(goFiles) == 0 {
		return nil, nil, nil
	}
	stdout, _, _ := runCmd(ctx, worktree, "golangci-lint", "run", "--out-format=json", "./...")
	anns, err := parseGolangciJSON([]byte(stdout))
	return anns, goFiles, err
}

// parseGolangciJSON parses golangci-lint's JSON report ({"Issues":[...]}).
func parseGolangciJSON(b []byte) ([]Annotation, error) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil, nil
	}
	var doc struct {
		Issues []struct {
			FromLinter string `json:"FromLinter"`
			Text       string `json:"Text"`
			Severity   string `json:"Severity"`
			Pos        struct {
				Filename string `json:"Filename"`
				Line     int    `json:"Line"`
			} `json:"Pos"`
		} `json:"Issues"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	var out []Annotation
	for _, is := range doc.Issues {
		msg := strings.TrimSpace(is.Text)
		if is.FromLinter != "" {
			msg = msg + " (" + is.FromLinter + ")"
		}
		out = append(out, Annotation{
			Tool:    "golangci-lint",
			Path:    filepath.ToSlash(is.Pos.Filename),
			Line:    is.Pos.Line,
			Level:   normalizeLevel(is.Severity, LevelWarning),
			Message: msg,
		})
	}
	return out, nil
}

// ---- ruff ------------------------------------------------------------------

type ruffTool struct{ configured bool }

func (ruffTool) Name() string      { return "ruff" }
func (ruffTool) Formatter() bool   { return false }
func (t ruffTool) Available() bool { return t.configured && binaryAvailable("ruff") }

func (t ruffTool) Run(ctx context.Context, worktree string, changedFiles []string) ([]Annotation, []string, error) {
	pyFiles := filesWithExt(changedFiles, ".py", ".pyi")
	if len(pyFiles) == 0 {
		return nil, nil, nil
	}
	stdout, _, _ := runCmd(ctx, worktree, "ruff", append([]string{"check", "--output-format=json"}, pyFiles...)...)
	anns, err := parseRuffJSON([]byte(stdout))
	return anns, pyFiles, err
}

// parseRuffJSON parses `ruff check --output-format=json` output (a JSON array).
func parseRuffJSON(b []byte) ([]Annotation, error) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil, nil
	}
	var items []struct {
		Code     string `json:"code"`
		Message  string `json:"message"`
		Filename string `json:"filename"`
		Location struct {
			Row int `json:"row"`
		} `json:"location"`
	}
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, err
	}
	var out []Annotation
	for _, it := range items {
		msg := strings.TrimSpace(it.Message)
		if it.Code != "" {
			msg = it.Code + ": " + msg
		}
		out = append(out, Annotation{
			Tool:    "ruff",
			Path:    filepath.ToSlash(it.Filename),
			Line:    it.Location.Row,
			Level:   LevelWarning,
			Message: msg,
		})
	}
	return out, nil
}

// ---- eslint ----------------------------------------------------------------

type eslintTool struct{ configured bool }

func (eslintTool) Name() string      { return "eslint" }
func (eslintTool) Formatter() bool   { return false }
func (t eslintTool) Available() bool { return t.configured && binaryAvailable("eslint") }

func (t eslintTool) Run(ctx context.Context, worktree string, changedFiles []string) ([]Annotation, []string, error) {
	jsFiles := filesWithExt(changedFiles, ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx")
	if len(jsFiles) == 0 {
		return nil, nil, nil
	}
	stdout, _, _ := runCmd(ctx, worktree, "eslint", append([]string{"-f", "json"}, jsFiles...)...)
	anns, err := parseEslintJSON([]byte(stdout))
	return anns, jsFiles, err
}

// parseEslintJSON parses `eslint -f json` output (array of file result objects).
func parseEslintJSON(b []byte) ([]Annotation, error) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil, nil
	}
	var files []struct {
		FilePath string `json:"filePath"`
		Messages []struct {
			Line     int    `json:"line"`
			Message  string `json:"message"`
			Severity int    `json:"severity"`
			RuleID   string `json:"ruleId"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(b, &files); err != nil {
		return nil, err
	}
	var out []Annotation
	for _, f := range files {
		for _, m := range f.Messages {
			msg := strings.TrimSpace(m.Message)
			if m.RuleID != "" {
				msg = msg + " (" + m.RuleID + ")"
			}
			lvl := LevelWarning
			if m.Severity >= 2 {
				lvl = LevelError
			}
			out = append(out, Annotation{
				Tool:    "eslint",
				Path:    filepath.ToSlash(f.FilePath),
				Line:    m.Line,
				Level:   lvl,
				Message: msg,
			})
		}
	}
	return out, nil
}

// ---- terraform validate ----------------------------------------------------

type terraformValidateTool struct{}

func (terraformValidateTool) Name() string    { return "terraform validate" }
func (terraformValidateTool) Formatter() bool { return false }
func (terraformValidateTool) Available() bool { return binaryAvailable("terraform") }

func (terraformValidateTool) Run(ctx context.Context, worktree string, changedFiles []string) ([]Annotation, []string, error) {
	tfFiles := filesWithExt(changedFiles, ".tf")
	if len(tfFiles) == 0 {
		return nil, nil, nil
	}
	stdout, _, _ := runCmd(ctx, worktree, "terraform", "validate", "-json")
	anns, err := parseTerraformValidateJSON([]byte(stdout))
	return anns, tfFiles, err
}

// parseTerraformValidateJSON parses `terraform validate -json` output.
func parseTerraformValidateJSON(b []byte) ([]Annotation, error) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil, nil
	}
	var doc struct {
		Diagnostics []struct {
			Severity string `json:"severity"`
			Summary  string `json:"summary"`
			Detail   string `json:"detail"`
			Range    *struct {
				Filename string `json:"filename"`
				Start    struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"range"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	var out []Annotation
	for _, d := range doc.Diagnostics {
		msg := strings.TrimSpace(d.Summary)
		if d.Detail != "" {
			msg = msg + ": " + strings.TrimSpace(d.Detail)
		}
		path := ""
		line := 0
		if d.Range != nil {
			path = filepath.ToSlash(d.Range.Filename)
			line = d.Range.Start.Line
		}
		out = append(out, Annotation{
			Tool:    "terraform validate",
			Path:    path,
			Line:    line,
			Level:   normalizeLevel(d.Severity, LevelError),
			Message: msg,
		})
	}
	return out, nil
}

// normalizeLevel maps tool-specific severity strings to the shared Level enum,
// defaulting to def when unrecognised.
func normalizeLevel(s string, def Level) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "error", "failure", "fatal", "high", "critical":
		return LevelError
	case "warning", "warn", "medium":
		return LevelWarning
	case "notice", "info", "information", "low", "hint":
		return LevelNotice
	}
	return def
}
