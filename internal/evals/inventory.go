package evals

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// evalTreatmentEnv selects an eval-only treatment arm. Empty = control.
const evalTreatmentEnv = "APPR_AI_SAL_EVAL_TREATMENT"

var (
	goIdent    = regexp.MustCompile(`(?m)^(?:func\s+(?:\([^)]+\)\s+)?|type\s+|const\s+|var\s+)([A-Za-z_][\w]*)`)
	pyIdent    = regexp.MustCompile(`(?m)^(?:def|class)\s+([A-Za-z_][\w]*)`)
	tsIdent    = regexp.MustCompile(`(?m)^(?:export\s+)?(?:async\s+)?(?:function|class|const|let|type|interface)\s+([A-Za-z_][\w]*)`)
	tfIdent    = regexp.MustCompile(`(?m)^(?:resource|data|module|variable|output)\s+"([^"]+)"`)
	backtickID = regexp.MustCompile("`" + `([A-Za-z_][\w.]{0,64})` + "`")
)

// applyEvalTreatment mutates pre-run inputs for the selected eval treatment.
// inventory-v1 appends a worktree path/identifier inventory to RepoContext.
// claim-strip-v1 is applied post-run (see applyPostEvalTreatment).
func applyEvalTreatment(worktree string, repoContext string) string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(evalTreatmentEnv))) {
	case "inventory-v1":
		inv := buildWorktreeInventory(worktree)
		if inv == "" {
			return repoContext
		}
		if strings.TrimSpace(repoContext) == "" {
			return inv
		}
		return strings.TrimSpace(repoContext) + "\n\n" + inv
	default:
		return repoContext
	}
}

// buildWorktreeInventory lists relative paths and declaration-like identifiers
// under worktree. Empty worktree → "".
func buildWorktreeInventory(worktree string) string {
	worktree = strings.TrimSpace(worktree)
	if worktree == "" {
		return ""
	}
	var files []string
	idents := map[string]string{} // ident -> first path
	_ = filepath.WalkDir(worktree, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(worktree, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, ".") {
			return nil
		}
		files = append(files, rel)
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, id := range extractDeclIdents(rel, string(b)) {
			if _, ok := idents[id]; !ok {
				idents[id] = rel
			}
		}
		return nil
	})
	if len(files) == 0 {
		return ""
	}
	sort.Strings(files)
	var b strings.Builder
	b.WriteString("## Worktree inventory (treatment: inventory-v1)\n\n")
	b.WriteString("Paths and declared identifiers in the review worktree. Prefer these;\n")
	b.WriteString("do not invent paths or identifiers absent from this list or the diff.\n\n")
	b.WriteString("### Files\n")
	for _, f := range files {
		b.WriteString("- `")
		b.WriteString(f)
		b.WriteString("`\n")
	}
	if len(idents) > 0 {
		keys := make([]string, 0, len(idents))
		for id := range idents {
			keys = append(keys, id)
		}
		sort.Strings(keys)
		b.WriteString("\n### Identifiers\n")
		for _, id := range keys {
			b.WriteString("- `")
			b.WriteString(id)
			b.WriteString("` (")
			b.WriteString(idents[id])
			b.WriteString(")\n")
		}
	}
	return b.String()
}

func extractDeclIdents(path, src string) []string {
	var re *regexp.Regexp
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".go.txt":
		re = goIdent
	case ".py":
		re = pyIdent
	case ".ts", ".tsx", ".js", ".jsx":
		re = tsIdent
	case ".tf":
		re = tfIdent
	default:
		// Still pull a few backtick-looking tokens from docs/markdown fixtures.
		re = backtickID
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		id := m[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
