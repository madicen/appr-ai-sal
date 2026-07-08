package contextexpand

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleGo = `package sample

type Widget struct {
	Name string
	Size int
}

func Build(w Widget) string {
	return helper(w.Name)
}

func helper(s string) string {
	return "x" + s
}
`

// lineOf returns the 1-based line number of the first line in src that contains
// sub, or 0.
func lineOf(src, sub string) int {
	for i, ln := range strings.Split(src, "\n") {
		if strings.Contains(ln, sub) {
			return i + 1
		}
	}
	return 0
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func itemsByKind(res Result, k ItemKind) []Item {
	var out []Item
	for _, it := range res.Items {
		if it.Kind == k {
			out = append(out, it)
		}
	}
	return out
}

func hasSymbol(items []Item, sym string) bool {
	for _, it := range items {
		if it.Symbol == sym {
			return true
		}
	}
	return false
}

// TestExpandEnclosingFunctionTypeAndCallee is the hermetic AST baseline: a
// change inside Build should surface Build's full body, the Widget type it
// references, and the helper it calls — with NO external tool.
func TestExpandEnclosingFunctionTypeAndCallee(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sample.go", sampleGo)
	changedLine := lineOf(sampleGo, "return helper(w.Name)")
	if changedLine == 0 {
		t.Fatal("could not locate changed line")
	}

	res := Expand(context.Background(), Options{
		Worktree:         dir,
		Changed:          []ChangedFile{{Path: "sample.go", ChangedLines: []int{changedLine}}},
		DisableEnrichers: true,
	})

	encl := itemsByKind(res, KindEnclosingFunc)
	if !hasSymbol(encl, "Build") {
		t.Fatalf("expected enclosing function Build, got items %+v", res.Items)
	}
	// The enclosing item must carry the FULL body, not just the changed hunk.
	for _, it := range encl {
		if it.Symbol == "Build" && !strings.Contains(it.Code, "func Build(w Widget) string") {
			t.Errorf("enclosing Build missing its signature/body:\n%s", it.Code)
		}
	}
	if !hasSymbol(itemsByKind(res, KindTypeDef), "Widget") {
		t.Errorf("expected referenced type Widget in items %+v", res.Items)
	}
	if !hasSymbol(itemsByKind(res, KindCallee), "helper") {
		t.Errorf("expected callee helper in items %+v", res.Items)
	}
}

// TestExpandTypeDefIncludesFields ensures the whole type definition (not just
// the name) is captured so the model sees the fields it must reason about.
func TestExpandTypeDefIncludesFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sample.go", sampleGo)
	changedLine := lineOf(sampleGo, "return helper(w.Name)")

	res := Expand(context.Background(), Options{
		Worktree:         dir,
		Changed:          []ChangedFile{{Path: "sample.go", ChangedLines: []int{changedLine}}},
		DisableEnrichers: true,
	})
	types := itemsByKind(res, KindTypeDef)
	found := false
	for _, it := range types {
		if it.Symbol == "Widget" {
			found = true
			if !strings.Contains(it.Code, "Name string") || !strings.Contains(it.Code, "Size int") {
				t.Errorf("Widget type def missing fields:\n%s", it.Code)
			}
		}
	}
	if !found {
		t.Fatal("Widget type def not found")
	}
}

// TestExpandNonGoFileSkipped: a non-Go changed file contributes nothing and
// never errors (language coverage / fail-open).
func TestExpandNonGoFileSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "notes.txt", "hello world\nsome text\n")
	res := Expand(context.Background(), Options{
		Worktree:         dir,
		Changed:          []ChangedFile{{Path: "notes.txt", ChangedLines: []int{1, 2}}},
		DisableEnrichers: true,
	})
	if res.HasContent() {
		t.Fatalf("non-Go file must yield no items, got %+v", res.Items)
	}
}

// TestExpandUnparseableGoFailsOpen: a syntactically broken Go file must not
// crash — the package simply indexes nothing.
func TestExpandUnparseableGoFailsOpen(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "broken.go", "package sample\nfunc Oops( { this is not go\n")
	res := Expand(context.Background(), Options{
		Worktree:         dir,
		Changed:          []ChangedFile{{Path: "broken.go", ChangedLines: []int{2}}},
		DisableEnrichers: true,
	})
	if res.HasContent() {
		t.Fatalf("unparseable file must yield no items, got %+v", res.Items)
	}
}

// TestExpandEmptyInputs: no worktree / no changed files → empty, no panic.
func TestExpandEmptyInputs(t *testing.T) {
	if Expand(context.Background(), Options{}).HasContent() {
		t.Error("zero Options should yield no content")
	}
	if Expand(nil, Options{Worktree: "/nope", Changed: nil}).HasContent() {
		t.Error("no changed files should yield no content")
	}
}

// TestBudgetTruncationAndOmission: a tiny byte budget forces truncation /
// omission and the result discloses it.
func TestBudgetTruncationAndOmission(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sample.go", sampleGo)
	changedLine := lineOf(sampleGo, "return helper(w.Name)")

	res := Expand(context.Background(), Options{
		Worktree:         dir,
		Changed:          []ChangedFile{{Path: "sample.go", ChangedLines: []int{changedLine}}},
		DisableEnrichers: true,
		ByteBudget:       40, // far smaller than any single item
		PerItemBytes:     30,
	})
	if !res.Truncated {
		t.Errorf("expected Truncated=true under a tiny budget")
	}
	// Total kept bytes must respect the budget.
	total := 0
	for _, it := range res.Items {
		total += len(it.Code) + 2
	}
	if total > 40 {
		t.Errorf("kept bytes %d exceed budget 40", total)
	}
}

// TestEnricherInjectedCallers: with an injected cross-ref finder pointing at a
// caller in another file, a KindCaller item is surfaced (the gopls/ctags path,
// exercised WITHOUT either binary via the injectable seam).
func TestEnricherInjectedCallers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sample.go", sampleGo)
	callerGo := `package sample

func CallSite() string {
	return Build(Widget{Name: "n"})
}
`
	writeFile(t, dir, "caller.go", callerGo)
	changedLine := lineOf(sampleGo, "return helper(w.Name)")
	callerLine := lineOf(callerGo, "return Build(Widget")

	injected := func(ctx context.Context, worktree string, sym symbolRef) CrossRefResult {
		if sym.Name != "Build" {
			return CrossRefResult{}
		}
		return CrossRefResult{
			Tool:      "gopls",
			Locations: []Location{{Path: "caller.go", Line: callerLine}},
		}
	}

	res := Expand(context.Background(), Options{
		Worktree: dir,
		Changed:  []ChangedFile{{Path: "sample.go", ChangedLines: []int{changedLine}}},
		crossRef: injected,
	})
	callers := itemsByKind(res, KindCaller)
	if !hasSymbol(callers, "CallSite") {
		t.Fatalf("expected caller CallSite via injected enricher, got %+v", res.Items)
	}
	found := false
	for _, e := range res.EnrichersUsed {
		if e == "gopls" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected EnrichersUsed to record gopls, got %v", res.EnrichersUsed)
	}
}

// TestFormatSectionEmptyIsNoOp: an empty result renders to "" so the prompt
// gains no section (byte-identical guarantee).
func TestFormatSectionEmptyIsNoOp(t *testing.T) {
	if FormatSection(Result{}) != "" {
		t.Error("empty Result must render to empty string")
	}
	if WrapSection(FormatSection(Result{})) != "" {
		t.Error("WrapSection of empty must be empty")
	}
}

// TestFormatSectionRendersReadOnlyFraming: a non-empty section must be clearly
// labelled read-only and carry the gathered code.
func TestFormatSectionRendersReadOnlyFraming(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sample.go", sampleGo)
	changedLine := lineOf(sampleGo, "return helper(w.Name)")
	res := Expand(context.Background(), Options{
		Worktree:         dir,
		Changed:          []ChangedFile{{Path: "sample.go", ChangedLines: []int{changedLine}}},
		DisableEnrichers: true,
	})
	body := FormatSection(res)
	if body == "" {
		t.Fatal("expected a non-empty section")
	}
	if !strings.Contains(body, "read-only") && !strings.Contains(body, "NOT part of the change") {
		t.Errorf("section must be framed as read-only:\n%s", body)
	}
	if !strings.Contains(body, "func Build") {
		t.Errorf("section must contain the gathered function body:\n%s", body)
	}
	wrapped := WrapSection(body)
	if !strings.Contains(wrapped, SectionHeading) {
		t.Errorf("wrapped section must carry the heading")
	}
}
