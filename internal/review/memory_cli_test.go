package review

import (
	"context"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/review/memory"
)

func TestRunMemoryCLIListAndClear(t *testing.T) {
	t.Setenv("APPR_AI_SAL_CACHE_DIR", t.TempDir())
	store := memory.NewStore()
	fpA := memory.NewFingerprint("formatting", "a/x.go", "nit about spacing", "info")
	fpB := memory.NewFingerprint("design", "b/y.go", "questionable abstraction", "warning")
	if err := store.Record("acme", "widget",
		memory.Entry{Fingerprint: fpA, Decision: memory.DecisionSkipped},
		memory.Entry{Fingerprint: fpB, Decision: memory.DecisionSkipped},
	); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	// list (all) and list (repo) must not error.
	if err := RunMemoryCLI(ctx, []string{"list"}); err != nil {
		t.Fatalf("memory list: %v", err)
	}
	if err := RunMemoryCLI(ctx, []string{"list", "acme/widget"}); err != nil {
		t.Fatalf("memory list repo: %v", err)
	}
	// export must not error.
	if err := RunMemoryCLI(ctx, []string{"export", "acme/widget"}); err != nil {
		t.Fatalf("memory export: %v", err)
	}

	// clear by fingerprint selector removes only the matching record.
	if err := RunMemoryCLI(ctx, []string{"clear", "acme/widget", "--specialist", "formatting"}); err != nil {
		t.Fatalf("memory clear selector: %v", err)
	}
	mem, _ := store.Load("acme", "widget")
	if len(mem.Records) != 1 || mem.Records[0].Fingerprint != fpB {
		t.Fatalf("selector clear should leave only the design record, got %+v", mem.Records)
	}

	// clear --all wipes the repo.
	if err := RunMemoryCLI(ctx, []string{"clear", "acme/widget", "--all"}); err != nil {
		t.Fatalf("memory clear --all: %v", err)
	}
	mem, _ = store.Load("acme", "widget")
	if len(mem.Records) != 0 {
		t.Fatalf("clear --all should empty the store, got %+v", mem.Records)
	}
}

func TestRunMemoryCLIErrors(t *testing.T) {
	t.Setenv("APPR_AI_SAL_CACHE_DIR", t.TempDir())
	ctx := context.Background()
	if err := RunMemoryCLI(ctx, nil); err == nil {
		t.Fatal("no args must error with usage")
	}
	if err := RunMemoryCLI(ctx, []string{"bogus"}); err == nil {
		t.Fatal("unknown subcommand must error")
	}
	// clear without a selector or --all must refuse (never silently wipe).
	if err := RunMemoryCLI(ctx, []string{"clear", "acme/widget"}); err == nil {
		t.Fatal("clear without --all or selector must error")
	}
}
