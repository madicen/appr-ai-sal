package repocontext

import (
	"os"
	"path/filepath"
	"strings"
)

// LintConfigs records which linter/formatter configuration files a repo
// checks in. It exists so the Q5 static-analysis pre-pass can decide whether
// to run a config-driven tool (golangci-lint, eslint, ruff) without
// re-implementing the detection: the same convention-file knowledge that
// drives Build (conventionRelPaths) is reused here, keeping "does this repo
// configure tool X" in one package.
type LintConfigs struct {
	// Golangci is true when a .golangci.{yml,yaml} (or golangci.yml) exists.
	Golangci bool
	// ESLint is true when any eslint config (flat or legacy) exists.
	ESLint bool
	// Ruff is true when ruff.toml exists or pyproject.toml declares a
	// [tool.ruff] table.
	Ruff bool
	// Prettier is true when a .prettierrc(.json|.yaml|.yml) exists.
	Prettier bool
	// Biome is true when biome.json exists.
	Biome bool
}

// golangciRelPaths / eslintRelPaths etc. are the subsets of conventionRelPaths
// that name a specific tool's config, plus a couple of variants Build does not
// harvest but that still mean "this tool is configured".
var (
	golangciRelPaths = []string{".golangci.yml", ".golangci.yaml", "golangci.yml", "golangci.yaml", ".golangci.toml", ".golangci.json"}
	eslintRelPaths   = []string{"eslint.config.js", "eslint.config.mjs", "eslint.config.cjs", ".eslintrc.json", ".eslintrc.yaml", ".eslintrc.yml", ".eslintrc.js", ".eslintrc.cjs", ".eslintrc"}
	prettierRelPaths = []string{".prettierrc", ".prettierrc.json", ".prettierrc.yaml", ".prettierrc.yml", ".prettierrc.js", "prettier.config.js"}
	ruffRelPaths     = []string{"ruff.toml", ".ruff.toml"}
	biomeRelPaths    = []string{"biome.json", "biome.jsonc"}
)

// DetectLintConfigs reports which linter/formatter configs exist under the
// worktree (falling back to localRoot for any file missing there, mirroring
// readConventionPair). It is read-only, does not read file contents beyond a
// cheap [tool.ruff] presence check in pyproject.toml, and never errors — an
// unreadable repo simply yields the zero value (nothing configured), which the
// pre-pass treats as "skip config-driven tools" (fail-open).
func DetectLintConfigs(worktree, localRoot string) LintConfigs {
	worktree = strings.TrimSpace(worktree)
	localRoot = strings.TrimSpace(localRoot)
	exists := func(rel string) bool {
		if worktree != "" && fileExists(filepath.Join(worktree, filepath.FromSlash(rel))) {
			return true
		}
		if localRoot != "" && fileExists(filepath.Join(localRoot, filepath.FromSlash(rel))) {
			return true
		}
		return false
	}
	anyExists := func(rels []string) bool {
		for _, rel := range rels {
			if exists(rel) {
				return true
			}
		}
		return false
	}
	cfg := LintConfigs{
		Golangci: anyExists(golangciRelPaths),
		ESLint:   anyExists(eslintRelPaths),
		Prettier: anyExists(prettierRelPaths),
		Biome:    anyExists(biomeRelPaths),
		Ruff:     anyExists(ruffRelPaths),
	}
	if !cfg.Ruff {
		cfg.Ruff = pyprojectDeclaresRuff(worktree) || pyprojectDeclaresRuff(localRoot)
	}
	return cfg
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// pyprojectDeclaresRuff reports whether root/pyproject.toml contains a
// [tool.ruff] table (or a "tool.ruff" dotted key), the idiomatic way a Python
// project configures ruff without a standalone ruff.toml.
func pyprojectDeclaresRuff(root string) bool {
	if root == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(root, "pyproject.toml"))
	if err != nil {
		return false
	}
	text := string(b)
	return strings.Contains(text, "[tool.ruff") || strings.Contains(text, "tool.ruff.") || strings.Contains(text, "\"tool.ruff\"")
}
