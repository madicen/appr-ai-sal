package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/applog"
)

// MergedPRDigestRow is one merged pull request used for repository culture digests.
type MergedPRDigestRow struct {
	Number        int
	Title         string
	URL           string
	BodyFirstLine string
	UpdatedAt     time.Time
}

// PRFileTouches summarises the file kinds touched by one merged PR. Used by
// RecentPRsTouchingPaths to evidence whether prior PRs in the same area
// added tests / docs, so the testing/docs specialists can calibrate.
type PRFileTouches struct {
	Number       int
	Title        string
	URL          string
	UpdatedAt    time.Time
	SourceFiles  int
	TestFiles    int
	DocFiles     int
	MatchedPaths []string
}

// HasTests reports whether the PR touched at least one test file.
func (p PRFileTouches) HasTests() bool { return p.TestFiles > 0 }

// HasDocs reports whether the PR touched at least one doc/markdown file.
func (p PRFileTouches) HasDocs() bool { return p.DocFiles > 0 }

// ListMergedPRs returns up to limit recently updated merged PRs for owner/repo.
func ListMergedPRs(ctx context.Context, owner, repo string, limit int) ([]MergedPRDigestRow, error) {
	if limit < 1 {
		limit = 30
	}
	repoPath := owner + "/" + repo
	args := []string{
		"pr", "list",
		"--repo", repoPath,
		"--state", "merged",
		"--limit", strconv.Itoa(limit),
		"--json", "number,title,url,body,updatedAt",
	}
	out, err := runJSON(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("list merged PRs: %w", err)
	}
	var raw []struct {
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		URL       string    `json:"url"`
		Body      string    `json:"body"`
		UpdatedAt time.Time `json:"updatedAt"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse merged PR list: %w", err)
	}
	rows := make([]MergedPRDigestRow, 0, len(raw))
	for _, r := range raw {
		rows = append(rows, MergedPRDigestRow{
			Number:        r.Number,
			Title:         strings.TrimSpace(r.Title),
			URL:           strings.TrimSpace(r.URL),
			BodyFirstLine: firstMeaningfulLine(r.Body),
			UpdatedAt:     r.UpdatedAt,
		})
	}
	return rows, nil
}

func firstMeaningfulLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		if len(s) > 240 {
			return s[:240] + "…"
		}
		return s
	}
	return ""
}

// PRFiles returns the file list for a single PR (path + additions/deletions),
// retrieved from `gh pr view --json files`. The caller is responsible for
// classifying which of those paths matter to the request (filename heuristics).
func PRFiles(ctx context.Context, owner, repo string, prNumber int) ([]PRFile, error) {
	args := []string{
		"pr", "view", strconv.Itoa(prNumber),
		"--repo", owner + "/" + repo,
		"--json", "files",
	}
	out, err := runJSON(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("list files for #%d: %w", prNumber, err)
	}
	var raw struct {
		Files []PRFile `json:"files"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse files for #%d: %w", prNumber, err)
	}
	return raw.Files, nil
}

// PRFile is one entry from `gh pr view --json files`.
type PRFile struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// RecentPRsTouchingPaths returns up to limit merged PRs that touched at
// least one file under the same directory as any path in paths. Each result
// includes a per-kind count (source/test/doc) so the caller can build
// aggregates like "of N matching PRs, M added a test file."
//
// When paths is nil or empty, every recent merged PR is included (no path
// filter); callers use this to evidence repo-wide habits at agent-generation
// time when there is no PR diff to anchor against.
//
// R6.4: this previously pulled a candidate window of merged PRs (1 `gh pr
// list`) and then looped PR-by-PR fetching file lists (up to ~60 sequential
// `gh pr view --json files` execs). It now fetches the candidate PRs AND their
// changed files in ONE batched GraphQL query, doing the classification
// client-side — O(1) round trips instead of O(candidate). Callers still cache
// aggressively (the review pipeline wraps this with the repo-profile cache).
func RecentPRsTouchingPaths(ctx context.Context, owner, repo string, paths []string, limit int) ([]PRFileTouches, error) {
	if limit < 1 {
		limit = 8
	}
	matchAll := len(paths) == 0
	candidate := limit * 3
	if candidate > 60 {
		candidate = 60
	}
	if matchAll {
		candidate = limit
	}
	data, err := graphQLQuery[mergedPRFilesData](ctx, graphqlMergedPRFilesQuery, map[string]any{
		"owner": owner,
		"name":  repo,
		"first": candidate,
	})
	if err != nil {
		return nil, err
	}
	dirs := pathParentDirs(paths)
	var out []PRFileTouches
	for _, n := range data.Repository.PullRequests.Nodes {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		m := MergedPRDigestRow{
			Number: n.Number,
			Title:  strings.TrimSpace(n.Title),
			URL:    strings.TrimSpace(n.URL),
		}
		m.UpdatedAt, _ = time.Parse(time.RFC3339, n.UpdatedAt)
		files := make([]PRFile, 0, len(n.Files.Nodes))
		for _, f := range n.Files.Nodes {
			files = append(files, PRFile{Path: f.Path, Additions: f.Additions, Deletions: f.Deletions})
		}
		if n.Files.PageInfo.HasNextPage {
			applog.Warn("merged PR files truncated (classification uses first 100)",
				"repo", owner+"/"+repo, "pr", n.Number, "total", n.Files.TotalCount)
		}
		row, ok := classifyPRTouches(m, files, dirs)
		if !ok && !matchAll {
			continue
		}
		if matchAll {
			row = forceClassify(m, files)
		}
		out = append(out, row)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// mergedPRFilesData mirrors the `data` object of graphqlMergedPRFilesQuery: a
// window of recently-updated merged PRs, each with its changed-file list.
type mergedPRFilesData struct {
	Repository struct {
		PullRequests struct {
			Nodes []struct {
				Number    int    `json:"number"`
				Title     string `json:"title"`
				URL       string `json:"url"`
				UpdatedAt string `json:"updatedAt"`
				Files     struct {
					PageInfo   pageInfo `json:"pageInfo"`
					TotalCount int      `json:"totalCount"`
					Nodes      []struct {
						Path      string `json:"path"`
						Additions int    `json:"additions"`
						Deletions int    `json:"deletions"`
					} `json:"nodes"`
				} `json:"files"`
			} `json:"nodes"`
		} `json:"pullRequests"`
	} `json:"repository"`
}

const graphqlMergedPRFilesQuery = `query($owner: String!, $name: String!, $first: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequests(states: [MERGED], first: $first, orderBy: {field: UPDATED_AT, direction: DESC}) {
      nodes {
        number
        title
        url
        updatedAt
        files(first: 100) {
          pageInfo { hasNextPage }
          totalCount
          nodes {
            path
            additions
            deletions
          }
        }
      }
    }
  }
}`

// forceClassify builds a PRFileTouches that includes counts for every file in
// the PR (no path filter). Used by the matchAll branch of
// RecentPRsTouchingPaths.
func forceClassify(m MergedPRDigestRow, files []PRFile) PRFileTouches {
	row := PRFileTouches{
		Number:    m.Number,
		Title:     m.Title,
		URL:       m.URL,
		UpdatedAt: m.UpdatedAt,
	}
	for _, f := range files {
		path := strings.TrimSpace(f.Path)
		if path == "" {
			continue
		}
		if len(row.MatchedPaths) < 8 {
			row.MatchedPaths = append(row.MatchedPaths, path)
		}
		base := filepath.Base(path)
		switch {
		case isPRTestFileName(base):
			row.TestFiles++
		case isPRDocFileName(path):
			row.DocFiles++
		case isPRSourceFileName(base):
			row.SourceFiles++
		}
	}
	return row
}

// pathParentDirs returns the set of directories worth comparing against,
// derived from each input path. For a path "internal/review/foo.go" we
// include "internal/review" so a merged PR touching "internal/review/bar.go"
// is treated as relevant. The repo root is included only when an input path
// has no directory (e.g. "README.md" at root).
func pathParentDirs(paths []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(filepath.ToSlash(p)))
		if dir == "." || dir == "" {
			out[""] = struct{}{}
			continue
		}
		out[dir] = struct{}{}
	}
	return out
}

func classifyPRTouches(m MergedPRDigestRow, files []PRFile, dirs map[string]struct{}) (PRFileTouches, bool) {
	row := PRFileTouches{
		Number:    m.Number,
		Title:     m.Title,
		URL:       m.URL,
		UpdatedAt: m.UpdatedAt,
	}
	matched := false
	for _, f := range files {
		path := strings.TrimSpace(f.Path)
		if path == "" {
			continue
		}
		fileDir := filepath.ToSlash(filepath.Dir(filepath.ToSlash(path)))
		if fileDir == "." {
			fileDir = ""
		}
		if !pathDirMatches(fileDir, dirs) && !isRootDocFile(path) {
			continue
		}
		matched = true
		if len(row.MatchedPaths) < 8 {
			row.MatchedPaths = append(row.MatchedPaths, path)
		}
		base := filepath.Base(path)
		switch {
		case isPRTestFileName(base):
			row.TestFiles++
		case isPRDocFileName(path):
			row.DocFiles++
		case isPRSourceFileName(base):
			row.SourceFiles++
		}
	}
	if !matched {
		return row, false
	}
	return row, true
}

func pathDirMatches(fileDir string, want map[string]struct{}) bool {
	for w := range want {
		if w == fileDir {
			return true
		}
		if w != "" && (strings.HasPrefix(fileDir, w+"/") || strings.HasPrefix(w, fileDir+"/")) {
			return true
		}
	}
	return false
}

// isRootDocFile catches top-of-repo doc updates (README/CHANGELOG/docs/) that
// are always relevant for "did similar PRs update docs?" evidence.
func isRootDocFile(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	if strings.HasPrefix(lower, "docs/") {
		return true
	}
	switch lower {
	case "readme.md", "readme", "readme.txt", "changelog.md", "changelog", "changelog.txt":
		return true
	}
	return false
}

// isPRTestFileName mirrors the per-language test heuristics used by the
// static evidence harvester (kept simple to avoid pulling repocontext into
// gh).
func isPRTestFileName(base string) bool {
	lower := strings.ToLower(base)
	switch {
	case strings.HasSuffix(lower, "_test.go"):
		return true
	case strings.HasPrefix(lower, "test_") && strings.HasSuffix(lower, ".py"):
		return true
	case strings.HasSuffix(lower, "_test.py"):
		return true
	case strings.HasSuffix(lower, ".test.ts"), strings.HasSuffix(lower, ".test.tsx"),
		strings.HasSuffix(lower, ".test.js"), strings.HasSuffix(lower, ".test.jsx"):
		return true
	case strings.HasSuffix(lower, ".spec.ts"), strings.HasSuffix(lower, ".spec.tsx"),
		strings.HasSuffix(lower, ".spec.js"), strings.HasSuffix(lower, ".spec.jsx"):
		return true
	case strings.HasSuffix(lower, "_spec.rb"), strings.HasSuffix(lower, "_test.rb"):
		return true
	case strings.HasSuffix(lower, "test.java"), strings.HasSuffix(lower, "tests.java"),
		strings.HasSuffix(lower, "test.kt"), strings.HasSuffix(lower, "tests.kt"):
		return true
	}
	return false
}

func isPRDocFileName(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	if strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown") || strings.HasSuffix(lower, ".rst") {
		return true
	}
	if strings.HasPrefix(lower, "docs/") {
		return true
	}
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "readme", "readme.txt", "changelog", "changelog.txt":
		return true
	}
	return false
}

func isPRSourceFileName(base string) bool {
	lower := strings.ToLower(base)
	for _, ext := range []string{
		".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
		".tf", ".hcl", ".rs", ".rb", ".java", ".kt", ".kts",
		".swift", ".c", ".h", ".cpp", ".cc", ".cxx", ".hpp", ".cs",
		".sh", ".bash", ".zsh", ".sql",
	} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
