package review

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Bounds for the tech-convention sibling sampler. Even a large IaC monorepo
// returns quickly: we read at most techEvidenceMaxFiles sibling files, look
// at no more than techEvidenceMaxFindings findings, and emit no more than
// techEvidenceMaxTokens tokens per finding.
const (
	techEvidenceMaxFiles    = 80
	techEvidenceMaxFindings = 12
	techEvidenceMaxTokens   = 6
	techEvidenceMaxDirs     = 400
	techEvidencePerFileRead = 256 * 1024
)

// hclReferenceRe pulls Terraform/HCL reference expressions (var.x, local.y,
// module.z.attr, data.a.b) out of a finding comment even when they are not
// wrapped in backticks. These are the strongest "does the repo actually use
// this?" signals: a finding that tells the author to add `var.common_tags`
// is only congruent with the repo's habit if sibling files use that exact
// reference.
var hclReferenceRe = regexp.MustCompile(`\b(?:var|local|module|data)\.[A-Za-z0-9_.]+`)

// BuildTechConventionEvidence harvests repo-grounding evidence for tech
// specialist findings so the convention witness can judge whether a finding
// asks the author to do something the rest of the repo actually does.
//
// The signal is deliberately simple and bounded: for each token a finding
// references (an HCL reference expression like `var.common_tags`, or a
// backtick-quoted identifier like `tags`), it counts how many sibling files
// of the SAME extension — sampled from the changed file's directory subtree
// — already contain that token. A finding that says "add `tags =
// var.common_tags` for repo compliance" is congruent with the repo's habit
// only if sibling `.tf` files actually use `var.common_tags`; when the
// count is zero, the witness has hard evidence the cited convention is not a
// repo norm and the arbiter can demote or suppress it.
//
// Returns "" when there are no tech findings carrying usable tokens, or when
// no sibling files could be sampled (the witness then falls back to
// `unknown` for those findings, which is the correct "no opinion" posture).
func BuildTechConventionEvidence(worktree string, findings []Finding) string {
	worktree = strings.TrimSpace(worktree)
	if worktree == "" || len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	cache := map[string][]string{} // ext|searchRoot → sampled sibling paths
	rendered := 0
	for _, f := range findings {
		if rendered >= techEvidenceMaxFindings {
			break
		}
		path := strings.TrimSpace(f.Path)
		if path == "" || f.Line <= 0 {
			continue
		}
		tokens := extractTechTokens(f.Comment)
		if len(tokens) == 0 {
			continue
		}
		searchRoot, siblings := sampleSiblingsCached(worktree, path, cache)
		if len(siblings) == 0 {
			continue
		}
		if rendered == 0 {
			b.WriteString("_Tech convention evidence (auto-harvested sibling sampling)._\n\n")
		}
		rendered++
		side := f.Side
		if side == "" {
			side = "RIGHT"
		}
		fmt.Fprintf(&b, "- Finding `%s:%d` side=%s (tech): sampled %d sibling `%s` file(s) near `%s`.\n",
			path, f.Line, side, len(siblings), fileExt(path), searchRoot)
		for _, tok := range tokens {
			present := countFilesContaining(worktree, siblings, tok)
			fmt.Fprintf(&b, "  - token `%s`: present in %d of %d sampled file(s).\n", tok, present, len(siblings))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// appendTechConventionEvidence folds the harvested tech evidence into the
// shared per-PR evidence pack passed to the convention witness. The shared
// pack (testing/docs static facts) and the tech section are separated by a
// blank line so the witness reads them as distinct blocks. When there is no
// tech evidence the original pack is returned unchanged.
func appendTechConventionEvidence(evidence, worktree string, techFindings []Finding) string {
	tech := BuildTechConventionEvidence(worktree, techFindings)
	if strings.TrimSpace(tech) == "" {
		return evidence
	}
	if strings.TrimSpace(evidence) == "" {
		return tech
	}
	return strings.TrimRight(evidence, "\n") + "\n\n" + tech
}

// extractTechTokens returns the distinct, ranked set of tokens worth
// counting across sibling files for one tech finding. HCL reference
// expressions (var.x, local.y, …) rank first because they are the most
// specific evidence; backtick identifiers fill the remainder. The result is
// capped at techEvidenceMaxTokens to keep the evidence pack small.
func extractTechTokens(comment string) []string {
	body := strings.TrimSpace(comment)
	if body == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var refs, idents []string
	for _, m := range hclReferenceRe.FindAllString(body, -1) {
		m = strings.TrimRight(m, ".")
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		refs = append(refs, m)
	}
	for _, id := range extractBacktickIdentifiers(body) {
		if _, ok := seen[id]; ok {
			continue
		}
		// Skip bare numbers and very short tokens — they match everywhere
		// and produce no usable signal.
		if len(id) < 3 || isAllDigits(id) {
			continue
		}
		seen[id] = struct{}{}
		idents = append(idents, id)
	}
	out := append(refs, idents...)
	if len(out) > techEvidenceMaxTokens {
		out = out[:techEvidenceMaxTokens]
	}
	return out
}

// sampleSiblingsCached returns the search root (relative to the worktree)
// and a sampled set of sibling files sharing relPath's extension, caching
// the result per (extension, search root) so multiple findings in the same
// module don't re-walk the tree.
func sampleSiblingsCached(worktree, relPath string, cache map[string][]string) (string, []string) {
	searchRoot := siblingSearchRoot(relPath)
	key := fileExt(relPath) + "|" + searchRoot
	if s, ok := cache[key]; ok {
		return searchRoot, s
	}
	s := sampleSiblingFiles(worktree, searchRoot, relPath, techEvidenceMaxFiles)
	cache[key] = s
	return searchRoot, s
}

// siblingSearchRoot picks the directory to sample siblings from: the parent
// of the changed file's own directory when one exists (so a bucket module's
// peers under `.../s3/` are in scope), otherwise the file's directory.
func siblingSearchRoot(relPath string) string {
	dir := filepath.ToSlash(filepath.Dir(relPath))
	if dir == "." || dir == "" {
		return ""
	}
	parent := filepath.ToSlash(filepath.Dir(dir))
	if parent == "." || parent == "" {
		return dir
	}
	return parent
}

// sampleSiblingFiles walks searchRoot (bounded) and returns up to max
// relative paths of files with the same extension as relPath, excluding the
// finding's own file and denied directories.
func sampleSiblingFiles(worktree, searchRoot, relPath string, max int) []string {
	ext := strings.ToLower(fileExt(relPath))
	if ext == "" {
		return nil
	}
	absRoot := filepath.Join(worktree, filepath.FromSlash(searchRoot))
	self := filepath.ToSlash(relPath)
	var out []string
	dirs := 0
	_ = filepath.WalkDir(absRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip unreadable entries, keep walking
		}
		if len(out) >= max || dirs > techEvidenceMaxDirs {
			return filepath.SkipAll
		}
		rel, rerr := filepath.Rel(worktree, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			dirs++
			if isDeniedSampleDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == self || strings.ToLower(fileExt(rel)) != ext {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out
}

func isDeniedSampleDir(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".terraform", "node_modules", "vendor", ".venv", "venv", "__pycache__":
		return true
	}
	return false
}

// countFilesContaining returns how many of the sampled sibling files contain
// token as a substring. Reads are capped per file.
func countFilesContaining(worktree string, siblings []string, token string) int {
	n := 0
	for _, rel := range siblings {
		b := readFileCapped(filepath.Join(worktree, filepath.FromSlash(rel)), techEvidencePerFileRead)
		if b != "" && strings.Contains(b, token) {
			n++
		}
	}
	return n
}

func readFileCapped(path string, max int) string {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return ""
	}
	if len(b) > max {
		b = b[:max]
	}
	return string(b)
}

func fileExt(path string) string {
	return filepath.Ext(filepath.Base(path))
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
