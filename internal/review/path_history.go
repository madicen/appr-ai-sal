package review

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/review/repocontext"
)

// PathHistoryAggregate distills RecentPRsTouchingPaths into the small set of
// numbers the testing/docs specialists actually care about. Surfaced as a
// markdown bullet block via FormatPathHistoryAggregate so it can be
// concatenated into the static evidence section.
type PathHistoryAggregate struct {
	MatchedPRs       int
	WithTests        int
	WithDocs         int
	WithSourceOnly   int
	SamplePRNumbers  []int
	SampleTestPRs    []int
	SampleDocsPRs    []int
}

type pathHistoryCacheEntry struct {
	V       int                  `json:"v"`
	Updated time.Time            `json:"updated"`
	Limit   int                  `json:"limit"`
	PathKey string               `json:"path_key"`
	Rows    []gh.PRFileTouches   `json:"rows"`
}

// LoadOrFetchPathHistory returns recent merged PRs that touched the same
// directories as paths, caching results under the repo-profile dir so
// subsequent reviews against the same PR don't re-walk gh.
//
// TTL reuses repoconfig.RepoExpertReviewTTLSeconds. limit comes from rc
// (RepoExpertReviewPRs by default). force=true bypasses the cache.
func LoadOrFetchPathHistory(ctx context.Context, rc *repoconfig.Config, owner, repo string, paths []string, force bool) ([]gh.PRFileTouches, error) {
	if rc == nil {
		rc = repoconfig.Default()
	}
	limit := rc.RepoExpertReviewPRs
	if limit < 1 {
		limit = 8
	}
	cacheFile := pathHistoryCachePath(owner, repo, paths, limit)
	if !force {
		if e, ok := readPathHistoryCache(cacheFile); ok && time.Since(e.Updated) < rc.RepoExpertReviewTTL() {
			return e.Rows, nil
		}
	}
	rows, err := gh.RecentPRsTouchingPaths(ctx, owner, repo, paths, limit)
	if err != nil {
		return nil, err
	}
	_ = writePathHistoryCache(cacheFile, pathHistoryCacheEntry{
		V:       1,
		Updated: time.Now().UTC(),
		Limit:   limit,
		PathKey: pathSetKey(paths),
		Rows:    rows,
	})
	return rows, nil
}

// AggregatePathHistory rolls per-PR touches into a small summary. Returns a
// zero-value aggregate (and SamplePRNumbers nil) when rows is empty.
func AggregatePathHistory(rows []gh.PRFileTouches) PathHistoryAggregate {
	a := PathHistoryAggregate{}
	for _, r := range rows {
		a.MatchedPRs++
		switch {
		case r.HasTests() && r.HasDocs():
			a.WithTests++
			a.WithDocs++
		case r.HasTests():
			a.WithTests++
		case r.HasDocs():
			a.WithDocs++
		default:
			if r.SourceFiles > 0 {
				a.WithSourceOnly++
			}
		}
		if len(a.SamplePRNumbers) < 6 {
			a.SamplePRNumbers = append(a.SamplePRNumbers, r.Number)
		}
		if r.HasTests() && len(a.SampleTestPRs) < 4 {
			a.SampleTestPRs = append(a.SampleTestPRs, r.Number)
		}
		if r.HasDocs() && len(a.SampleDocsPRs) < 4 {
			a.SampleDocsPRs = append(a.SampleDocsPRs, r.Number)
		}
	}
	return a
}

// FormatPathHistoryAggregate renders a markdown bullet block. Returns ""
// when no PRs matched. Designed to slot directly under the static-evidence
// section in the testing/docs prompts.
func FormatPathHistoryAggregate(a PathHistoryAggregate) string {
	if a.MatchedPRs == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("_Recent merged PRs that touched the same area(s) as this PR (auto-harvested)._\n\n")
	fmt.Fprintf(&b, "- Matching merged PRs sampled: **%d**.\n", a.MatchedPRs)
	fmt.Fprintf(&b, "- Of those, **%d** also touched a test file; **%d** touched a doc/markdown file.\n",
		a.WithTests, a.WithDocs)
	if a.WithSourceOnly > 0 {
		fmt.Fprintf(&b, "- **%d** touched only source files (no test or doc additions).\n", a.WithSourceOnly)
	}
	if len(a.SampleTestPRs) > 0 {
		fmt.Fprintf(&b, "- Sample PRs that added/updated tests: %s.\n", joinPRNumbers(a.SampleTestPRs))
	}
	if len(a.SampleDocsPRs) > 0 {
		fmt.Fprintf(&b, "- Sample PRs that updated docs: %s.\n", joinPRNumbers(a.SampleDocsPRs))
	}
	return b.String()
}

func joinPRNumbers(nums []int) string {
	if len(nums) == 0 {
		return ""
	}
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = fmt.Sprintf("#%d", n)
	}
	return strings.Join(parts, ", ")
}

func pathSetKey(paths []string) string {
	cp := append([]string(nil), paths...)
	for i, p := range cp {
		cp[i] = filepath.ToSlash(strings.TrimSpace(p))
	}
	sort.Strings(cp)
	h := sha1.New()
	for _, p := range cp {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

func pathHistoryCachePath(owner, repo string, paths []string, limit int) string {
	slug := strings.ReplaceAll(strings.ToLower(owner+"_"+repo), "/", "_")
	key := pathSetKey(paths)
	return filepath.Join(RepoProfilesDir(), fmt.Sprintf("path-history_%s_%s_limit%d.json", slug, key, limit))
}

func readPathHistoryCache(path string) (pathHistoryCacheEntry, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return pathHistoryCacheEntry{}, false
	}
	var e pathHistoryCacheEntry
	if json.Unmarshal(b, &e) != nil || e.V != 1 {
		return pathHistoryCacheEntry{}, false
	}
	return e, true
}

func writePathHistoryCache(path string, e pathHistoryCacheEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// BuildPRReviewEvidence assembles a markdown evidence block for the testing
// and docs specialists at review time: per-PR static evidence (sibling tests,
// doc.go, exported-symbol coverage) plus a path-history aggregate. Returns
// "" when nothing useful was harvested or when rc.IncludeRepoEvidence is
// false.
func BuildPRReviewEvidence(ctx context.Context, rc *repoconfig.Config, pr *gh.PR, diff string, worktree string) string {
	if rc == nil {
		rc = repoconfig.Default()
	}
	if !rc.IncludeRepoEvidence {
		return ""
	}
	if pr == nil || strings.TrimSpace(worktree) == "" {
		return ""
	}
	paths := changedPathsFromDiff(diff)
	staticMD := ""
	if ev, err := repocontext.BuildEvidence(ctx, repocontext.EvidenceOptions{
		Worktree:     worktree,
		LocalRoot:    rc.LocalRootFor(pr.Owner, pr.Repo),
		ChangedPaths: paths,
		MaxBytes:     4096,
	}); err == nil {
		staticMD = repocontext.FormatEvidenceMarkdown(ev, 4096)
	}
	historyMD := ""
	if len(paths) > 0 {
		hctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		rows, err := LoadOrFetchPathHistory(hctx, rc, pr.Owner, pr.Repo, paths, false)
		cancel()
		if err == nil {
			historyMD = FormatPathHistoryAggregate(AggregatePathHistory(rows))
		}
	}
	if strings.TrimSpace(staticMD) == "" && strings.TrimSpace(historyMD) == "" {
		return ""
	}
	var b strings.Builder
	if staticMD != "" {
		b.WriteString(strings.TrimRight(staticMD, "\n"))
		b.WriteString("\n\n")
	}
	if historyMD != "" {
		b.WriteString(strings.TrimRight(historyMD, "\n"))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// changedPathsFromDiff returns the post-image paths of all files in the
// unified diff. Used to scope evidence harvesting to the PR's surface.
func changedPathsFromDiff(diff string) []string {
	files := ParseDiff(diff)
	out := make([]string, 0, len(files))
	seen := map[string]struct{}{}
	for _, f := range files {
		path := strings.TrimSpace(f.Path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

// FormatPRReviewEvidenceSection wraps an evidence body with the conventional
// section heading injected into specialist prompts.
func FormatPRReviewEvidenceSection(evidence string) string {
	body := strings.TrimSpace(evidence)
	if body == "" {
		return ""
	}
	return "\n\n## Repo evidence for this PR (auto-harvested)\n\n" + body + "\n"
}
