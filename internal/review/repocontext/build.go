// Package repocontext gathers bounded, read-only repository convention snippets from disk.
package repocontext

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Options configures filesystem harvesting.
type Options struct {
	// Worktree is the PR checkout root (required).
	Worktree string
	// LocalRoot is an optional local clone used only for convention files missing under Worktree.
	LocalRoot string
	// MaxBytes is the hard cap for returned text (including headers).
	MaxBytes int
	// IncludeManifests, when true, also harvests dependency/build manifests
	// (go.mod, package.json, Dockerfile, root *.tf, CI workflows, etc.) so
	// callers analysing which technologies a repo uses get richer evidence.
	// Off by default to keep the convention bundle tight for the
	// per-specialist briefs that don't need it.
	IncludeManifests bool
}

const perFileReadCap = 256 * 1024

// Convention file paths relative to repo root (checked in order).
var conventionRelPaths = []string{
	"AGENTS.md",
	"CONTRIBUTING.md",
	"CODEOWNERS",
	"README.md",
	".editorconfig",
	"biome.json",
	"ruff.toml",
	"rustfmt.toml",
	".prettierrc",
	".prettierrc.json",
	".prettierrc.yaml",
	".prettierrc.yml",
	"eslint.config.js",
	"eslint.config.mjs",
	".eslintrc.json",
	".eslintrc.yaml",
	".eslintrc.yml",
	".golangci.yml",
	".golangci.yaml",
	"golangci.yml",
}

// manifestRelPaths are dependency/build manifests harvested only when
// Options.IncludeManifests is set. They name the technologies a repo
// depends on far more directly than convention files do.
var manifestRelPaths = []string{
	"go.mod",
	"package.json",
	"requirements.txt",
	"pyproject.toml",
	"Pipfile",
	"Gemfile",
	"pom.xml",
	"build.gradle",
	"build.gradle.kts",
	"Cargo.toml",
	"composer.json",
	"Dockerfile",
	"docker-compose.yml",
	"docker-compose.yaml",
	"Makefile",
}

// deniedPath reports true if rel or any parent segment must not be read.
func deniedPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, p := range strings.Split(rel, "/") {
		switch strings.ToLower(p) {
		case ".git", "node_modules", "vendor", ".venv", "venv", "__pycache__", ".terraform":
			return true
		}
		if strings.HasPrefix(strings.ToLower(p), ".env") {
			return true
		}
	}
	lower := strings.ToLower(rel)
	if strings.Contains(lower, ".pem") || strings.Contains(lower, "id_rsa") || strings.Contains(lower, "id_ed25519") {
		return true
	}
	return false
}

// Build returns a markdown-ish text block of repo conventions, capped at opts.MaxBytes.
func Build(ctx context.Context, opts Options) (string, error) {
	_ = ctx
	if opts.MaxBytes < 512 {
		opts.MaxBytes = 24576
	}
	root := filepath.Clean(opts.Worktree)
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return "", fmt.Errorf("worktree not a directory: %s", opts.Worktree)
	}
	local := strings.TrimSpace(opts.LocalRoot)
	if local != "" {
		local = filepath.Clean(local)
		if st, err := os.Stat(local); err != nil || !st.IsDir() {
			local = ""
		}
	}

	var b strings.Builder
	write := func(title, body string) {
		if strings.TrimSpace(body) == "" {
			return
		}
		if b.Len()+len(title)+len(body)+8 > opts.MaxBytes {
			remain := opts.MaxBytes - b.Len() - 20
			if remain < 80 {
				return
			}
			body = body[:remain] + "\n…(truncated)\n"
		}
		fmt.Fprintf(&b, "### %s\n\n```\n%s\n```\n\n", title, strings.TrimRight(body, "\n"))
	}

	for _, rel := range conventionRelPaths {
		if deniedPath(rel) {
			continue
		}
		text, src := readConventionPair(root, local, rel)
		if text == "" {
			continue
		}
		title := rel
		if src != "" {
			title = fmt.Sprintf("%s (from %s)", rel, src)
		}
		trim := text
		if rel == "README.md" {
			trim = headLines(trim, 100)
		}
		if rel == "CODEOWNERS" {
			trim = headLines(trim, 60)
		}
		write(title, trim)
		if b.Len() >= opts.MaxBytes {
			break
		}
	}

	if opts.IncludeManifests && b.Len() < opts.MaxBytes-200 {
		for _, rel := range manifestPaths(root) {
			if deniedPath(rel) {
				continue
			}
			text, src := readConventionPair(root, local, rel)
			if text == "" {
				continue
			}
			title := rel
			if src != "" {
				title = fmt.Sprintf("%s (from %s)", rel, src)
			}
			write(title, headLines(text, 120))
			if b.Len() >= opts.MaxBytes {
				break
			}
		}
	}

	if b.Len() < opts.MaxBytes-200 {
		if tree := treeSummary(root, opts.MaxBytes-b.Len()-120); tree != "" {
			write("Repository tree (depth 1, file counts by extension)", tree)
		}
	}

	out := strings.TrimSpace(b.String())
	if len(out) > opts.MaxBytes {
		out = out[:opts.MaxBytes] + "\n…(truncated)\n"
	}
	return out, nil
}

func readConventionPair(worktree, local, rel string) (content, sourceLabel string) {
	if !deniedPath(rel) {
		if t := readFileLimited(filepath.Join(worktree, rel), perFileReadCap); t != "" {
			return t, "worktree"
		}
	}
	if local != "" && !deniedPath(rel) {
		if t := readFileLimited(filepath.Join(local, rel), perFileReadCap); t != "" {
			return t, "local clone"
		}
	}
	return "", ""
}

func readFileLimited(path string, max int) string {
	if deniedPath(filepath.Base(path)) {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return ""
	}
	if len(b) > max {
		b = b[:max]
	}
	return string(b)
}

func headLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + "\n…"
}

// manifestPaths returns manifestRelPaths plus dynamically-discovered
// root-level Terraform files and CI workflow definitions under root. The
// dynamic entries are capped so a repo with hundreds of *.tf / workflow
// files can't blow the byte budget before the harvester's own cap kicks in.
func manifestPaths(root string) []string {
	out := append([]string(nil), manifestRelPaths...)

	const maxDynamic = 6
	if ents, err := os.ReadDir(root); err == nil {
		n := 0
		for _, e := range ents {
			if n >= maxDynamic {
				break
			}
			if e.IsDir() {
				continue
			}
			if strings.EqualFold(filepath.Ext(e.Name()), ".tf") {
				out = append(out, e.Name())
				n++
			}
		}
	}

	wfDir := filepath.Join(".github", "workflows")
	if ents, err := os.ReadDir(filepath.Join(root, wfDir)); err == nil {
		n := 0
		for _, e := range ents {
			if n >= maxDynamic {
				break
			}
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext == ".yml" || ext == ".yaml" {
				out = append(out, filepath.Join(wfDir, e.Name()))
				n++
			}
		}
	}
	return out
}

func treeSummary(root string, budget int) string {
	ents, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	extCount := map[string]int{}
	var tops []string
	for _, e := range ents {
		name := e.Name()
		if name == ".git" || name == "node_modules" || name == "vendor" {
			continue
		}
		if e.IsDir() {
			tops = append(tops, name+"/")
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext == "" {
			ext = "(no ext)"
		}
		extCount[ext]++
	}
	sort.Strings(tops)
	var exts []string
	for e := range extCount {
		exts = append(exts, e)
	}
	sort.Strings(exts)
	var b strings.Builder
	b.WriteString("Top-level dirs: " + strings.Join(tops, ", ") + "\n")
	b.WriteString("File counts by extension:\n")
	for _, e := range exts {
		fmt.Fprintf(&b, "  %s: %d\n", e, extCount[e])
	}
	s := b.String()
	if len(s) > budget {
		return s[:budget] + "…"
	}
	return s
}
