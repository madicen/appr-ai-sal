package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/review/memory"
)

// RunMemoryCLI implements `appr-ai-sal memory …`: inspect and clear the
// per-repo reviewer-memory store (B1). It is a developer/maintenance
// subcommand, not part of the interactive TUI.
//
//	appr-ai-sal memory list [owner/repo]
//	appr-ai-sal memory clear <owner/repo> [--all | --specialist S --path-glob G --severity V --comment-hash H]
//	appr-ai-sal memory export [owner/repo]
//
// list with no argument lists every repo that has memory; with an owner/repo
// it prints that repo's records. clear removes all of a repo's memory (--all)
// or only the records matching the provided fingerprint selector. export emits
// the repeatedly-skipped patterns as evals must_not_appear JSON scaffolding.
func RunMemoryCLI(_ context.Context, argv []string) error {
	if len(argv) < 1 {
		return memoryUsageErr()
	}
	store := memory.NewStore()
	switch argv[0] {
	case "list":
		return memoryList(store, argv[1:])
	case "clear":
		return memoryClear(store, argv[1:])
	case "export":
		return memoryExport(store, argv[1:])
	default:
		return fmt.Errorf("unknown memory subcommand %q (expected list, clear, or export)", argv[0])
	}
}

func memoryUsageErr() error {
	return fmt.Errorf(`usage:
  appr-ai-sal memory list [owner/repo]
  appr-ai-sal memory clear <owner/repo> [--all | --specialist S --path-glob G --severity V --comment-hash H]
  appr-ai-sal memory export [owner/repo]`)
}

func memoryList(store *memory.Store, args []string) error {
	// No repo → list every repo that has memory.
	if len(args) == 0 {
		repos, err := store.ListRepos()
		if err != nil {
			return err
		}
		if len(repos) == 0 {
			fmt.Println("no reviewer memory stored yet")
			return nil
		}
		fmt.Println("repositories with reviewer memory:")
		for _, r := range repos {
			fmt.Printf("  %s\n", r)
		}
		fmt.Println("\nRun `appr-ai-sal memory list <owner/repo>` to see a repo's records.")
		return nil
	}
	owner, repo, err := parseOwnerRepo(args[0])
	if err != nil {
		return err
	}
	mem, err := store.Load(owner, repo)
	if err != nil {
		return err
	}
	if len(mem.Records) == 0 {
		fmt.Printf("no reviewer memory for %s/%s (file: %s)\n", owner, repo, store.FilePath(owner, repo))
		return nil
	}
	records := append([]memory.Record(nil), mem.Records...)
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Decision != records[j].Decision {
			return records[i].Decision < records[j].Decision
		}
		return records[i].Count > records[j].Count
	})
	fmt.Printf("reviewer memory for %s/%s (%d record(s), file: %s):\n\n", owner, repo, len(records), store.FilePath(owner, repo))
	for _, r := range records {
		fmt.Printf("  %-15s %3d×  specialist=%s path=%s severity=%s comment=%s  (last %s)\n",
			r.Decision, r.Count,
			r.Fingerprint.Specialist,
			displayGlob(r.Fingerprint.PathGlob),
			displaySeverity(r.Fingerprint.Severity),
			displayHash(r.Fingerprint.CommentHash),
			r.Last.Format("2006-01-02"))
	}
	fmt.Println("\nClear one pattern with e.g.:")
	fmt.Printf("  appr-ai-sal memory clear %s/%s --specialist <s> --comment-hash <h>\n", owner, repo)
	return nil
}

func memoryClear(store *memory.Store, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: appr-ai-sal memory clear <owner/repo> [--all | --specialist S --path-glob G --severity V --comment-hash H]")
	}
	owner, repo, err := parseOwnerRepo(args[0])
	if err != nil {
		return err
	}
	all := false
	var mt memory.Matcher
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--all":
			all = true
		case "--specialist", "--path-glob", "--severity", "--comment-hash":
			if i+1 >= len(rest) {
				return fmt.Errorf("flag %s needs a value", rest[i])
			}
			val := rest[i+1]
			i++
			switch rest[i-1] {
			case "--specialist":
				mt.Specialist = val
			case "--path-glob":
				mt.PathGlob = val
			case "--severity":
				mt.Severity = val
			case "--comment-hash":
				mt.CommentHash = val
			}
		default:
			return fmt.Errorf("unknown flag %q", rest[i])
		}
	}
	if all {
		if err := store.Clear(owner, repo); err != nil {
			return err
		}
		fmt.Printf("cleared all reviewer memory for %s/%s\n", owner, repo)
		return nil
	}
	if mt.Empty() {
		return fmt.Errorf("nothing to clear: pass --all to wipe the repo, or a fingerprint selector (--specialist/--path-glob/--severity/--comment-hash)")
	}
	removed, err := store.ClearMatching(owner, repo, mt)
	if err != nil {
		return err
	}
	fmt.Printf("removed %d matching record(s) from %s/%s\n", removed, owner, repo)
	return nil
}

func memoryExport(store *memory.Store, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: appr-ai-sal memory export <owner/repo>")
	}
	owner, repo, err := parseOwnerRepo(args[0])
	if err != nil {
		return err
	}
	mem, err := store.Load(owner, repo)
	if err != nil {
		return err
	}
	neg := mem.ExportNegatives()
	if len(neg) == 0 {
		fmt.Fprintln(os.Stderr, "// no repeatedly-skipped patterns to export")
		fmt.Println("[]")
		return nil
	}
	out, err := json.MarshalIndent(neg, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "// paste the entries below into a corpus case's expectations.json \"must_not_appear\"")
	fmt.Fprintln(os.Stderr, "// Pattern is intentionally blank (raw comments are never stored); fill it in from your review history.")
	fmt.Println(string(out))
	return nil
}

func displayGlob(g string) string {
	if strings.TrimSpace(g) == "" {
		return "*"
	}
	return g
}

func displaySeverity(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(any)"
	}
	return s
}

func displayHash(h string) string {
	if strings.TrimSpace(h) == "" {
		return "(none)"
	}
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
