package review

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
)

// RunRepoContextCLI implements `appr-ai-sal repo-context …` subcommands.
func RunRepoContextCLI(ctx context.Context, argv []string) error {
	if len(argv) < 1 {
		return fmt.Errorf(`usage: appr-ai-sal repo-context refresh <owner/repo>
       appr-ai-sal repo-context refresh --all-mapped`)
	}
	if argv[0] != "refresh" {
		return fmt.Errorf("unknown repo-context subcommand %q (expected refresh)", argv[0])
	}
	rc, err := repoconfig.Load()
	if err != nil {
		return err
	}
	aiCfg, err := aiconfig.Load()
	if err != nil {
		return err
	}
	if len(argv) < 2 {
		return fmt.Errorf(`usage: appr-ai-sal repo-context refresh <owner/repo>
       appr-ai-sal repo-context refresh --all-mapped`)
	}
	if argv[1] == "--all-mapped" {
		if len(rc.RepoRoots) == 0 {
			return fmt.Errorf("repo_roots is empty; add mappings in %s", repoconfig.DefaultPath())
		}
		for key := range rc.RepoRoots {
			owner, repo, ok := splitOwnerRepoKey(key)
			if !ok {
				return fmt.Errorf("invalid repo_roots key %q (want owner/repo)", key)
			}
			if err := refreshRepoContextOne(ctx, aiCfg, rc, owner, repo); err != nil {
				return fmt.Errorf("%s/%s: %w", owner, repo, err)
			}
			fmt.Printf("refreshed repo-context cache for %s/%s\n", owner, repo)
		}
		return nil
	}
	owner, repo, err := parseOwnerRepo(argv[1])
	if err != nil {
		return err
	}
	if err := refreshRepoContextOne(ctx, aiCfg, rc, owner, repo); err != nil {
		return err
	}
	fmt.Printf("refreshed repo-context cache for %s/%s (profiles under %s)\n", owner, repo, RepoProfilesDir())
	return nil
}

func refreshRepoContextOne(ctx context.Context, aiCfg *aiconfig.Config, rc *repoconfig.Config, owner, repo string) error {
	_ = os.MkdirAll(RepoProfilesDir(), 0o755)
	pr := &gh.PR{
		Owner:      owner,
		Repo:       repo,
		Repository: owner + "/" + repo,
		BaseRef:    "",
	}
	wt := rc.LocalRootFor(owner, repo)
	if wt == "" {
		tmp, err := os.MkdirTemp("", "appr-ai-sal-repocontext-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		wt = tmp
	}
	_, err := ComposeRepositoryContextBlock(ctx, aiCfg, rc, pr, wt, true)
	return err
}

func parseOwnerRepo(s string) (owner, repo string, err error) {
	s = strings.TrimSpace(s)
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid owner/repo %q (expected owner/repo)", s)
	}
	return parts[0], parts[1], nil
}

func splitOwnerRepoKey(key string) (owner, repo string, ok bool) {
	key = strings.TrimSpace(strings.ToLower(key))
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
