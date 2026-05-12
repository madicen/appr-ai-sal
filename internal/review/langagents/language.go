package langagents

import (
	"path/filepath"
	"sort"
	"strings"
)

// extensionToLanguage maps lowercased file extensions (including the
// leading dot) to the canonical language name. Multiple extensions can
// alias to the same language — .tsx/.jsx are "typescript", .py is
// "python", and so on.
//
// Keep entries here aligned with Table: every bundled language lives in
// both maps. Non-bundled languages can appear here (so BriefsForDiff
// classifies the PR's files correctly) even when Table has no entry — the
// deterministic gate stays silent for those, but the LLM-generated brief
// (when present) is still injected into the prompt.
var extensionToLanguage = map[string]Language{
	".go":     "go",
	".py":     "python",
	".pyi":    "python",
	".ts":     "typescript",
	".tsx":    "typescript",
	".js":     "typescript",
	".jsx":    "typescript",
	".mjs":    "typescript",
	".cjs":    "typescript",
	".rs":     "rust",
	".tf":     "hcl",
	".hcl":    "hcl",
	".tfvars": "hcl",
	".java":   "java",
	".kt":     "kotlin",
	".kts":    "kotlin",
	".rb":     "ruby",
	".swift":  "swift",
	".c":      "c",
	".h":      "c",
	".cc":     "cpp",
	".cpp":    "cpp",
	".cxx":    "cpp",
	".hpp":    "cpp",
	".cs":     "csharp",
	".sh":     "shell",
	".bash":   "shell",
	".zsh":    "shell",
	".sql":    "sql",
	".yaml":   "yaml",
	".yml":    "yaml",
	".json":   "json",
	".md":     "markdown",
	".markdown": "markdown",
}

// aliasToLanguage normalises user-typed language names to the canonical
// form. Lowercased and trimmed; falls through to "" for unknown names so
// callers can warn.
var aliasToLanguage = map[string]Language{
	"go":         "go",
	"golang":     "go",
	"python":     "python",
	"py":         "python",
	"py3":        "python",
	"typescript": "typescript",
	"ts":         "typescript",
	"tsx":        "typescript",
	"javascript": "typescript",
	"js":         "typescript",
	"jsx":        "typescript",
	"node":       "typescript",
	"rust":       "rust",
	"rs":         "rust",
	"hcl":        "hcl",
	"terraform":  "hcl",
	"tf":         "hcl",
	"java":       "java",
	"kotlin":     "kotlin",
	"kt":         "kotlin",
	"ruby":       "ruby",
	"rb":         "ruby",
	"swift":      "swift",
	"c":          "c",
	"cpp":        "cpp",
	"c++":        "cpp",
	"csharp":     "csharp",
	"cs":         "csharp",
	"shell":      "shell",
	"bash":       "shell",
	"zsh":        "shell",
	"sh":         "shell",
	"sql":        "sql",
	"yaml":       "yaml",
	"yml":        "yaml",
	"json":       "json",
	"markdown":   "markdown",
	"md":         "markdown",
}

// Canonical resolves an extension (".go"), filename ("main.go"), or
// alias ("golang") to its canonical language name ("go"). Returns "" when
// the input doesn't resolve. Empty input also returns "".
func Canonical(s string) Language {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "" {
		return ""
	}
	if strings.HasPrefix(t, ".") {
		return extensionToLanguage[t]
	}
	if l, ok := aliasToLanguage[t]; ok {
		return l
	}
	if ext := filepath.Ext(t); ext != "" {
		if l, ok := extensionToLanguage[ext]; ok {
			return l
		}
	}
	return ""
}

// LanguageForPath returns the canonical language for a file path based on
// its extension, or "" when unknown. Convenience wrapper used by the
// deterministic convention gate.
func LanguageForPath(path string) Language {
	return extensionToLanguage[strings.ToLower(filepath.Ext(path))]
}

// AllKnownLanguages returns the union of every canonical language name
// reachable from extensionToLanguage and aliasToLanguage, in
// alphabetical order. Used by the TUI to render "languages you could
// generate a brief for"; the set is bounded by what Canonical
// recognises today.
func AllKnownLanguages() []Language {
	seen := map[Language]struct{}{}
	for _, l := range extensionToLanguage {
		seen[l] = struct{}{}
	}
	for _, l := range aliasToLanguage {
		seen[l] = struct{}{}
	}
	out := make([]Language, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}
