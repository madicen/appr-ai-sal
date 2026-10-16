package evals

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/review"
)

// claimRef is one extractable reference from a finding comment.
type claimRef struct {
	Kind string // ident | path | pathline
	Text string // identifier, path, or path:line
}

var (
	backtickIdent = regexp.MustCompile("`([^`\n]{1,128})`")
	pathLineRef   = regexp.MustCompile(`\b([\w./-]+\.\w+):(\d+)\b`)
	pathOnlyRef   = regexp.MustCompile(`\b([\w./-]+\.(?:go|py|ts|tsx|js|jsx|tf|md|json|ya?ml))\b`)
)

// extractClaimRefs pulls backtick identifiers, path:line, and file-path tokens
// from a finding comment. Order is stable but callers treat the set as a bag.
func extractClaimRefs(comment string) []claimRef {
	seen := map[string]bool{}
	var out []claimRef
	add := func(kind, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		key := kind + ":" + text
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, claimRef{Kind: kind, Text: text})
	}
	for _, m := range backtickIdent.FindAllStringSubmatch(comment, -1) {
		tok := m[1]
		// Skip path-looking backticks; those are handled as path refs below
		// when they also appear bare, and as idents when they are simple names.
		if strings.Contains(tok, "/") || strings.Contains(tok, ".") && strings.ContainsAny(tok, "/\\") {
			if strings.Contains(tok, ":") {
				add("pathline", tok)
			} else if looksLikePath(tok) {
				add("path", tok)
			} else {
				add("ident", tok)
			}
			continue
		}
		if looksLikePath(tok) {
			add("path", tok)
			continue
		}
		add("ident", tok)
	}
	for _, m := range pathLineRef.FindAllStringSubmatch(comment, -1) {
		add("pathline", m[1]+":"+m[2])
	}
	for _, m := range pathOnlyRef.FindAllStringSubmatch(comment, -1) {
		add("path", m[1])
	}
	return out
}

func looksLikePath(s string) bool {
	return strings.Contains(s, "/") || pathOnlyRef.MatchString(s)
}

// resolveClaims reports how many findings have ≥1 unresolvable ref (unsupported)
// and how many findings had ≥1 extractable ref (checked). Empty worktree → 0,0.
func resolveClaims(worktree string, findings []review.Finding) (unsupported, checked int) {
	worktree = strings.TrimSpace(worktree)
	if worktree == "" {
		return 0, 0
	}
	for _, f := range findings {
		refs := extractClaimRefs(f.Comment)
		if len(refs) == 0 {
			continue
		}
		checked++
		for _, r := range refs {
			if !claimResolves(worktree, r) {
				unsupported++
				break
			}
		}
	}
	return unsupported, checked
}

func claimResolves(worktree string, r claimRef) bool {
	switch r.Kind {
	case "ident":
		return identInTree(worktree, r.Text)
	case "path":
		return fileExists(worktree, r.Text)
	case "pathline":
		path, line, ok := splitPathLine(r.Text)
		if !ok {
			return false
		}
		return lineInFile(worktree, path, line)
	default:
		return false
	}
}

func fileExists(worktree, rel string) bool {
	rel = filepath.Clean(filepath.FromSlash(rel))
	if rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	info, err := os.Stat(filepath.Join(worktree, rel))
	return err == nil && !info.IsDir()
}

func lineInFile(worktree, rel string, line int) bool {
	if line < 1 || !fileExists(worktree, rel) {
		return false
	}
	b, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return line <= len(lines)
}

func identInTree(worktree, ident string) bool {
	if ident == "" {
		return false
	}
	found := false
	_ = filepath.WalkDir(worktree, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() || found {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		if strings.Contains(string(b), ident) {
			found = true
		}
		return nil
	})
	return found
}

func splitPathLine(s string) (path string, line int, ok bool) {
	i := strings.LastIndex(s, ":")
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil || n < 1 {
		return "", 0, false
	}
	return s[:i], n, true
}
