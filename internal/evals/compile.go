package evals

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/review"
)

// compileStatus is the outcome of trying to apply + compile one suggestion.
type compileStatus string

const (
	compileOK         compileStatus = "ok"
	compileFail       compileStatus = "fail"
	compileSkipTool   compileStatus = "skip_tool"
	compileSkipNoFile compileStatus = "skip_no_file"
)

type compileResult struct {
	Status compileStatus
	Err    error
}

// checkSuggestionCompile applies f.Suggestion onto a temp copy of the worktree
// file and runs the language's parse/compile check. Missing tools soft-skip.
func checkSuggestionCompile(worktree string, f review.Finding) compileResult {
	worktree = strings.TrimSpace(worktree)
	path := strings.TrimSpace(f.Path)
	sug := strings.TrimSpace(f.Suggestion)
	if worktree == "" || path == "" || sug == "" || f.Line < 1 {
		return compileResult{Status: compileSkipNoFile}
	}
	src := filepath.Join(worktree, filepath.FromSlash(path))
	body, err := os.ReadFile(src)
	if err != nil {
		return compileResult{Status: compileSkipNoFile, Err: err}
	}
	start, end := f.Line, f.Line
	if f.StartLine > 0 && f.StartLine < f.Line {
		start = f.StartLine
	}
	applied, err := applySuggestion(string(body), start, end, sug)
	if err != nil {
		return compileResult{Status: compileFail, Err: err}
	}

	tmp, err := os.MkdirTemp("", "evals-compile-")
	if err != nil {
		return compileResult{Status: compileFail, Err: err}
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	dst := filepath.Join(tmp, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return compileResult{Status: compileFail, Err: err}
	}
	if err := os.WriteFile(dst, []byte(applied), 0o644); err != nil {
		return compileResult{Status: compileFail, Err: err}
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return runCompileCmd("gofmt", []string{"-e", dst}, nil)
	case ".py":
		return runCompileCmd("python3", []string{"-m", "py_compile", dst}, nil)
	case ".ts", ".tsx":
		if _, err := exec.LookPath("tsc"); err != nil {
			return compileResult{Status: compileSkipTool, Err: err}
		}
		// --noEmit on a single file; allow default lib resolution to fail soft
		// by also writing a tiny tsconfig when absent.
		cfg := filepath.Join(tmp, "tsconfig.json")
		_ = os.WriteFile(cfg, []byte(`{"compilerOptions":{"strict":true,"noEmit":true},"files":[]}`+"\n"), 0o644)
		return runCompileCmd("tsc", []string{"--strict", "--noEmit", dst}, nil)
	case ".tf":
		if _, err := exec.LookPath("terraform"); err != nil {
			return compileResult{Status: compileSkipTool, Err: err}
		}
		mod := filepath.Dir(dst)
		// init may fail without providers; still try validate
		_ = runCompileCmd("terraform", []string{"init", "-backend=false", "-input=false"}, &mod)
		return runCompileCmd("terraform", []string{"validate"}, &mod)
	default:
		return compileResult{Status: compileSkipNoFile}
	}
}

func runCompileCmd(bin string, args []string, dir *string) compileResult {
	if _, err := exec.LookPath(bin); err != nil {
		return compileResult{Status: compileSkipTool, Err: err}
	}
	cmd := exec.Command(bin, args...)
	if dir != nil {
		cmd.Dir = *dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return compileResult{Status: compileFail, Err: fmt.Errorf("%s: %w\n%s", bin, err, out)}
	}
	return compileResult{Status: compileOK}
}

// applySuggestion replaces lines start..end (1-based inclusive) with suggestion.
func applySuggestion(content string, start, end int, suggestion string) (string, error) {
	if start < 1 || end < start {
		return "", fmt.Errorf("invalid line range %d-%d", start, end)
	}
	keepNL := strings.HasSuffix(content, "\n")
	lines := strings.Split(content, "\n")
	if keepNL && len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if end > len(lines) {
		return "", fmt.Errorf("end line %d past EOF (%d)", end, len(lines))
	}
	sugLines := strings.Split(suggestion, "\n")
	out := append([]string{}, lines[:start-1]...)
	out = append(out, sugLines...)
	out = append(out, lines[end:]...)
	joined := strings.Join(out, "\n")
	if keepNL {
		joined += "\n"
	}
	return joined, nil
}

// scoreCompiles tallies surviving suggestions' compile outcomes.
func scoreCompiles(worktree string, findings []review.Finding) (ok, attempted, skipped int) {
	for _, f := range findings {
		if !suggestionSurvived(f) {
			continue
		}
		res := checkSuggestionCompile(worktree, f)
		switch res.Status {
		case compileOK:
			ok++
			attempted++
		case compileFail:
			attempted++
		case compileSkipTool, compileSkipNoFile:
			skipped++
		}
	}
	return ok, attempted, skipped
}
