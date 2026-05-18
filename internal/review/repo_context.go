package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/review/repocontext"
)

const repoContextUserHeading = "## Repository context (auto-generated, do not quote verbatim in findings unless relevant)"

// repoContextAuthorityPreamble rides with every non-empty repo context block
// and tells the model how to treat it: authoritative for what's idiomatic in
// this specific repo, calibrating for severity, and never to be contradicted
// in a finding. Mirrors the language brief's framing in
// langagents/brief.go:124-126 so the same "do not file findings that
// contradict this" rule applies across language, technology, and repo briefs.
const repoContextAuthorityPreamble = "The brief below is a per-(repo, specialist) summary of how this repository writes the kind of code your specialty cares about. Treat it as authoritative for repo-specific conventions: do not file findings that contradict the conventions stated here, and let it calibrate the severity of borderline findings. The unified diff remains the authority for what changed in this PR."

// FormatRepoContextSection wraps a pre-built repository context blob for injection into user prompts.
func FormatRepoContextSection(block string) string {
	block = strings.TrimSpace(block)
	if block == "" {
		return ""
	}
	return fmt.Sprintf("\n\n%s\n\n%s\n\n%s\n\n", repoContextUserHeading, repoContextAuthorityPreamble, block)
}

// ComposeRepositoryContextBlock gathers capped convention text from disk and optional merged-PR digest.
// forceProfileRefresh bypasses the on-disk TTL for the merged-PR section (used by the repo-context CLI).
func ComposeRepositoryContextBlock(ctx context.Context, aiCfg *aiconfig.Config, rc *repoconfig.Config, pr *gh.PR, worktree string, forceProfileRefresh bool) (string, error) {
	if pr == nil || strings.TrimSpace(worktree) == "" {
		return "", nil
	}
	if rc == nil {
		rc = repoconfig.Default()
	}
	fsCap := rc.MaxBytes
	if rc.IncludePRHistory {
		fsCap = minInt(rc.MaxBytes*2/3, rc.MaxBytes-512)
		if fsCap < 1024 {
			fsCap = 1024
		}
	}
	fsPart, err := repocontext.Build(ctx, repocontext.Options{
		Worktree:  worktree,
		LocalRoot: rc.LocalRootFor(pr.Owner, pr.Repo),
		MaxBytes:  fsCap,
	})
	if err != nil {
		return "", err
	}
	var parts []string
	if strings.TrimSpace(fsPart) != "" {
		parts = append(parts, strings.TrimSpace(fsPart))
	}
	if rc.IncludePRHistory {
		md, err := prHistoryMarkdown(ctx, aiCfg, rc, pr, forceProfileRefresh, worktree)
		if err != nil {
			parts = append(parts, "### Recent merged PRs (culture signal)\n\n(unavailable: "+err.Error()+")")
		} else if strings.TrimSpace(md) != "" {
			parts = append(parts, "### Recent merged PRs (culture signal)\n\n"+strings.TrimSpace(md))
		}
	}
	out := strings.Join(parts, "\n\n")
	if len(out) > rc.MaxBytes {
		out = out[:rc.MaxBytes] + "\n…(truncated)\n"
	}
	return out, nil
}

type prHistoryCacheEntry struct {
	V       int       `json:"v"`
	Updated time.Time `json:"updated"`
	BaseRef string    `json:"base_ref"`
	ListMD  string    `json:"list_md"`
	Summary string    `json:"summary,omitempty"`
}

func prHistoryCachePath(owner, repo, baseRef string, limit int) string {
	slug := strings.ReplaceAll(strings.ToLower(owner+"_"+repo), "/", "_")
	baseKey := strings.TrimSpace(strings.ToLower(baseRef))
	if baseKey == "" {
		baseKey = "default"
	}
	baseKey = strings.ReplaceAll(baseKey, "/", "_")
	return filepath.Join(RepoProfilesDir(), fmt.Sprintf("merged-prs_%s_%s_limit%d.json", slug, baseKey, limit))
}

func formatMergedPRRowsAsMarkdown(rows []gh.MergedPRDigestRow) string {
	var b strings.Builder
	for _, r := range rows {
		line := fmt.Sprintf("- #%d %s <%s>", r.Number, r.Title, r.URL)
		if r.BodyFirstLine != "" {
			line += " — " + r.BodyFirstLine
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func readPRHistoryCache(path string) (prHistoryCacheEntry, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return prHistoryCacheEntry{}, false
	}
	var e prHistoryCacheEntry
	if json.Unmarshal(b, &e) != nil || e.V != 1 {
		return prHistoryCacheEntry{}, false
	}
	return e, true
}

func writePRHistoryCache(path string, e prHistoryCacheEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func prHistoryMarkdown(ctx context.Context, aiCfg *aiconfig.Config, rc *repoconfig.Config, pr *gh.PR, force bool, worktree string) (string, error) {
	limit := rc.PRHistoryLimit
	path := prHistoryCachePath(pr.Owner, pr.Repo, pr.BaseRef, limit)

	if !force {
		ent, ok := readPRHistoryCache(path)
		if ok && time.Since(ent.Updated) < rc.TTL() {
			switch {
			case !rc.RepoCultureSummarize:
				return ent.ListMD, nil
			case strings.TrimSpace(ent.Summary) != "":
				return ent.Summary, nil
			default:
				if aiCfg == nil {
					return ent.ListMD, nil
				}
				s, err := summarizeMergedPRCulture(ctx, aiCfg, ent.ListMD, worktree)
				if err != nil || strings.TrimSpace(s) == "" {
					return ent.ListMD, nil
				}
				ent.Summary = s
				_ = writePRHistoryCache(path, ent)
				return s, nil
			}
		}
	}

	rows, err := gh.ListMergedPRs(ctx, pr.Owner, pr.Repo, limit)
	if err != nil {
		return "", err
	}
	listMD := formatMergedPRRowsAsMarkdown(rows)
	ent := prHistoryCacheEntry{
		V:       1,
		Updated: time.Now().UTC(),
		BaseRef: pr.BaseRef,
		ListMD:  listMD,
	}
	if rc.RepoCultureSummarize && aiCfg != nil {
		if s, err := summarizeMergedPRCulture(ctx, aiCfg, listMD, worktree); err == nil {
			ent.Summary = strings.TrimSpace(s)
		}
	}
	_ = writePRHistoryCache(path, ent)
	if rc.RepoCultureSummarize && strings.TrimSpace(ent.Summary) != "" {
		return ent.Summary, nil
	}
	return ent.ListMD, nil
}

// SummarizeContextVersusChange asks the configured AI to explain how the composed
// repository context relates to this PR's diff (runs in parallel with specialists when enabled).
func SummarizeContextVersusChange(ctx context.Context, aiCfg *aiconfig.Config, pr *gh.PR, diff, repoBlock, worktree string) (string, error) {
	if aiCfg == nil || pr == nil {
		return "", nil
	}
	ctx2, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	system := `You relate two inputs: (A) an auto-generated repository context bundle (conventions, doc snippets, optional merged-PR culture) and (B) the unified diff for ONE pull request.

Write concise markdown for the human reviewer: bullets or short sections. Cover:
- What the context supports about norms, ownership, or habits in this repo (only from (A)).
- How that lens applies to THIS change—paths touched, consistency, things to verify.
If (A) is empty or minimal, say so and focus on what the diff shows.

Ground claims in the inputs only—do not invent policies. No JSON. About 200–400 words.`

	user := buildContextVersusChangeUser(pr, diff, repoBlock)
	sysAug, userAug := augmentPromptsForProvider(aiCfg.Provider, system, user, true)

	out, err := Complete(ctx2, aiCfg, sysAug, userAug, worktree)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(truncate(out, 6000)), nil
}

func buildContextVersusChangeUser(pr *gh.PR, diff, repoBlock string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "PR: %s#%d\nTitle: %s\nAuthor: %s\nBase: %s → Head: %s\n\n",
		pr.Repository, pr.Number, pr.Title, pr.Author, pr.BaseRef, pr.HeadRef)
	rb := strings.TrimSpace(repoBlock)
	if rb == "" {
		b.WriteString("### Repository context bundle\n\n_(empty — none was composed for this run.)_\n\n")
	} else {
		b.WriteString("### Repository context bundle\n\n")
		b.WriteString(truncate(rb, 14000))
		b.WriteString("\n\n")
	}
	b.WriteString("### Unified diff for this PR\n\n```diff\n")
	b.WriteString(truncate(diff, 65000))
	b.WriteString("\n```\n")
	return b.String()
}

func summarizeMergedPRCulture(ctx context.Context, aiCfg *aiconfig.Config, listMD, worktree string) (string, error) {
	listMD = strings.TrimSpace(listMD)
	if listMD == "" || aiCfg == nil {
		return "", nil
	}
	sctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	system := "You distill engineering habits from merged pull request titles. Reply with a short markdown bullet list only (8–15 lines). No preamble, no JSON, no code fences."
	user := "Recently merged PRs for this repository (titles and one-line blurbs):\n\n" + truncate(listMD, 12000)
	out, err := Complete(sctx, aiCfg, system, user, worktree)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(truncate(out, 4000)), nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
