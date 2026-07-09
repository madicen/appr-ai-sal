package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review/contextexpand"
)

const expandSampleGo = `package sample

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

// newFileDiff builds a unified diff that adds the whole file, so every line is
// an added (new-image) line — a simple way to mark all of a file's functions
// as "changed" for the expander.
func newFileDiff(path, content string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	var b strings.Builder
	b.WriteString("diff --git a/" + path + " b/" + path + "\n")
	b.WriteString("new file mode 100644\n")
	b.WriteString("--- /dev/null\n")
	b.WriteString("+++ b/" + path + "\n")
	b.WriteString("@@ -0,0 +1," + itoa(len(lines)) + " @@\n")
	for _, ln := range lines {
		b.WriteString("+" + ln + "\n")
	}
	return b.String()
}

func writeGo(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestExpandGateClaudeNoExpansion: for the Claude subprocess (RepoTools==true)
// the expander is a no-op — the section is empty and prompts are unchanged.
func TestExpandGateClaudeNoExpansion(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "sample.go", expandSampleGo)
	diff := newFileDiff("sample.go", expandSampleGo)

	cfg := &aiconfig.Config{Provider: aiconfig.ProviderClaude}
	section, res := buildExpandedContextSection(context.Background(), cfg, dir, diff)
	if section != "" || res.HasContent() {
		t.Fatalf("Claude (RepoTools==true) must get no expansion; section=%q items=%d", section, len(res.Items))
	}
}

// TestExpandGateHTTPProviderExpands: for an HTTP provider (RepoTools==false)
// the expander injects enclosing functions + referenced types for a Go change.
func TestExpandGateHTTPProviderExpands(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "sample.go", expandSampleGo)
	diff := newFileDiff("sample.go", expandSampleGo)

	cfg := &aiconfig.Config{Provider: aiconfig.ProviderOllama}
	section, res := buildExpandedContextSection(context.Background(), cfg, dir, diff)
	if !res.HasContent() {
		t.Fatalf("HTTP provider (RepoTools==false) must get expansion; got none")
	}
	if !strings.Contains(section, contextexpand.SectionHeading) {
		t.Errorf("section missing heading:\n%s", section)
	}
	if !strings.Contains(section, "func Build") {
		t.Errorf("section missing enclosing function body:\n%s", section)
	}
	if !strings.Contains(section, "Widget") {
		t.Errorf("section missing referenced type:\n%s", section)
	}
}

// TestExpandGateNonGoDiffEmpty: a diff touching only non-Go files yields no
// expansion (fail-open language coverage).
func TestExpandGateNonGoDiffEmpty(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "README.md", "# hello\n")
	diff := newFileDiff("README.md", "# hello\nmore\n")
	cfg := &aiconfig.Config{Provider: aiconfig.ProviderOllama}
	section, res := buildExpandedContextSection(context.Background(), cfg, dir, diff)
	if section != "" || res.HasContent() {
		t.Fatalf("non-Go diff must yield no expansion; section=%q", section)
	}
}

// TestChangedGoLineRangesOnlyGoAdditions: only added lines of Go files are
// collected; deletions and non-Go files are ignored.
func TestChangedGoLineRangesOnlyGoAdditions(t *testing.T) {
	diff := "" +
		"diff --git a/a.go b/a.go\n" +
		"--- a/a.go\n+++ b/a.go\n" +
		"@@ -1,2 +1,3 @@\n" +
		" package a\n" +
		"-var old = 1\n" +
		"+var neu = 2\n" +
		"+var extra = 3\n" +
		"diff --git a/b.txt b/b.txt\n" +
		"--- a/b.txt\n+++ b/b.txt\n" +
		"@@ -0,0 +1,1 @@\n" +
		"+hello\n"
	got := changedGoLineRanges(diff)
	if len(got) != 1 || got[0].Path != "a.go" {
		t.Fatalf("expected only a.go, got %+v", got)
	}
	if len(got[0].ChangedLines) != 2 {
		t.Fatalf("expected 2 added Go lines, got %v", got[0].ChangedLines)
	}
}

// TestBuildReviewUserPromptExpandedSectionPlacement: the expanded section is
// injected before the diff, and an empty expandedSection leaves the prompt
// without the heading (byte-identical guarantee for Claude / empty expansion).
func TestBuildReviewUserPromptExpandedSectionPlacement(t *testing.T) {
	pr := &gh.PR{Number: 1, Title: "x", Repository: "o/r", BaseRef: "main", HeadRef: "feat"}

	empty := buildReviewUserPrompt(pr, "DIFFBODY", aiconfig.ReviewBalanced, "", "", "", "", "", "", "")
	if strings.Contains(empty, contextexpand.SectionHeading) {
		t.Errorf("empty expandedSection must not add the heading")
	}

	section := contextexpand.WrapSection("EXPANDED_MARKER body")
	withExp := buildReviewUserPrompt(pr, "DIFFBODY", aiconfig.ReviewBalanced, "", "", "", "", "", "", section)
	idxExp := strings.Index(withExp, "EXPANDED_MARKER")
	idxDiff := strings.Index(withExp, "DIFFBODY")
	if idxExp < 0 || idxDiff < 0 {
		t.Fatalf("missing markers: exp=%d diff=%d", idxExp, idxDiff)
	}
	if idxExp > idxDiff {
		t.Errorf("expanded context must appear before the diff: exp=%d diff=%d", idxExp, idxDiff)
	}
}
