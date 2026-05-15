package demo

import (
	"strings"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

// DemoDiff returns a unified diff for the given ref. The fixture PRs each
// have a hand-written diff so the tree pane shows real directory clusters
// (cmd/, internal/tui/, internal/review/) and the diff pane has hunks
// long enough to demo wrapping + line-number alignment.
//
// Falls back to a small generic diff so a synthetic ref pasted by the
// user via the URL input still has something to render.
func DemoDiff(ref gh.Ref) string {
	switch {
	case ref.Owner == "madicen" && ref.Repo == "appr-ai-sal" && ref.Number == 742:
		return demoDiffAppr742
	case ref.Owner == "madicen" && ref.Repo == "appr-ai-sal" && ref.Number == 318:
		return demoDiffAppr318
	case ref.Owner == "madicen" && ref.Repo == "appr-ai-sal" && ref.Number == 109:
		return demoDiffAppr109
	case ref.Owner == "madicen" && ref.Repo == "plumbing-svc" && ref.Number == 56:
		return demoDiffPlumbing56
	case ref.Owner == "madicen" && ref.Repo == "plumbing-svc" && ref.Number == 22:
		return demoDiffPlumbing22
	default:
		return demoDiffGeneric
	}
}

// All diffs below are hand-shaped so they parse cleanly through
// review.ParseDiff: each starts with `diff --git`, has --- / +++
// lines, and at least one `@@` hunk header.
const demoDiffAppr742 = `diff --git a/internal/tui/data/commands.go b/internal/tui/data/commands.go
index 1111111..2222222 100644
--- a/internal/tui/data/commands.go
+++ b/internal/tui/data/commands.go
@@ -72,11 +72,15 @@ func LoadPRDetailCmd(ref gh.Ref) tea.Cmd {

 // StartReviewCmd starts a review.Run goroutine and emits ReviewStartedMsg with
 // the progress channel the caller should poll via WaitForProgressCmd.
-func StartReviewCmd(ref gh.Ref, cfg *aiconfig.Config) tea.Cmd {
+func StartReviewCmd(ref gh.Ref, cfg *aiconfig.Config, demo bool) tea.Cmd {
 	snap := cfg.Clone()
 	return func() tea.Msg {
 		ctx := context.Background()
+		if demo {
+			ch := demopkg.SyntheticReviewProgress(ctx, ref, snap)
+			return ReviewStartedMsg{Ch: ch}
+		}
 		ch, err := review.Run(ctx, ref, snap)
 		if err != nil {
 			return ErrMsg{err}
diff --git a/internal/tui/model/openers.go b/internal/tui/model/openers.go
index 3333333..4444444 100644
--- a/internal/tui/model/openers.go
+++ b/internal/tui/model/openers.go
@@ -41,7 +41,7 @@ func (m *Model) startReviewOverlay(peruse bool) (tea.Cmd, tea.Cmd) {
 		ref := m.currentPR.Ref()
 		cmds = append(cmds,
-			data.StartReviewCmd(ref, m.opts.AIConfig),
+			data.StartReviewCmd(ref, m.opts.AIConfig, m.opts.Demo),
 			m.spinner.Tick,
 		)
 	}
diff --git a/cmd/appr-ai-sal/main.go b/cmd/appr-ai-sal/main.go
index 5555555..6666666 100644
--- a/cmd/appr-ai-sal/main.go
+++ b/cmd/appr-ai-sal/main.go
@@ -47,6 +47,8 @@ func run() error {
 	reviewStrictness := flag.String("review-strictness", "", "Review intensity: critical_only | lenient | balanced | strict (overrides env / config)")
+	demoMode := flag.Bool("demo", false, "run in self-contained demo mode with mock services (for VHS screenshots / GIFs)")
 	flag.Parse()
 	dry := *dryRun
 	if os.Getenv("APPR_AI_SAL_DRY") == "1" {
@@ -68,6 +70,11 @@ func run() error {

 	if t, err := theme.Load(); err == nil && t != nil {
 		theme.Apply(t)
 	}
+	if *demoMode {
+		if err := configureDemoEnv(); err != nil {
+			return fmt.Errorf("demo setup: %w", err)
+		}
+	}

 	// Quick auth sanity check before launching the UI so failures surface
 	// with a readable message rather than an empty list.
diff --git a/internal/demo/review.go b/internal/demo/review.go
new file mode 100644
index 0000000..7777777
--- /dev/null
+++ b/internal/demo/review.go
@@ -0,0 +1,42 @@
+package demo
+
+import (
+	"context"
+	"time"
+
+	"github.com/madicen/appr-ai-sal/internal/aiconfig"
+	"github.com/madicen/appr-ai-sal/internal/gh"
+	"github.com/madicen/appr-ai-sal/internal/review"
+)
+
+// SyntheticReviewProgress emits a scripted sequence of review.Progress
+// events with realistic per-stage delays so the review overlay replays
+// a believable run for VHS recording. Closes the channel with a final
+// "done" Progress carrying a hand-built Draft so the approve/post flow
+// has real cards to interact with.
+func SyntheticReviewProgress(ctx context.Context, ref gh.Ref, cfg *aiconfig.Config) <-chan review.Progress {
+	out := make(chan review.Progress, 16)
+	go runScript(ctx, ref, cfg, out)
+	return out
+}
`

const demoDiffAppr318 = `diff --git a/internal/tui/model/pr_detail.go b/internal/tui/model/pr_detail.go
index aaaaaaa..bbbbbbb 100644
--- a/internal/tui/model/pr_detail.go
+++ b/internal/tui/model/pr_detail.go
@@ -195,6 +195,49 @@ func renderTreePane(rows []treeRow, focused bool, width int) string {
 	return b.String()
 }

+// buildTreeView turns a flat list of treeRows into a hierarchical tree
+// view compatible with the file-tree pane's bubblezone hit map. Each
+// path is split on "/" and merged into a trie; folder rows are emitted
+// before their files (depth-first) so a user can collapse a folder
+// without losing position in the list.
+func (m *Model) buildTreeView() {
+	root := &fileTreeNode{children: map[string]*fileTreeNode{}}
+	for i, r := range m.treeRows {
+		segs := strings.Split(r.Path, "/")
+		node := root
+		for j, seg := range segs {
+			child, ok := node.children[seg]
+			if !ok {
+				child = &fileTreeNode{name: seg, children: map[string]*fileTreeNode{}}
+				node.children[seg] = child
+			}
+			if j == len(segs)-1 {
+				child.fileIdx = i
+				child.isFile = true
+			}
+			node = child
+		}
+	}
+	view := dfsEmit(root, 0, m.collapsedFolders)
+	m.treeViewRows = view
+	m.treeFileToLine = map[string]int{}
+	m.treeLineToFile = map[int]string{}
+	for i, row := range view {
+		if row.isFile {
+			m.treeFileToLine[row.fullPath] = i
+			m.treeLineToFile[i] = row.fullPath
+		}
+	}
+}
+
+// applyScrollToSelectedFile moves the tree viewport's YOffset so the
+// row at m.selectedFilePath becomes visible. No-op when the selected
+// path doesn't appear in the current view (e.g. its containing folder
+// was just collapsed).
+func (m *Model) applyScrollToSelectedFile() {
+	idx, ok := m.treeFileToLine[m.selectedFilePath]
+	if !ok {
+		return
+	}
+	m.treeView.YOffset = idx
+}
diff --git a/internal/tui/model/pr_detail.go b/internal/tui/model/pr_detail.go
index ccccccc..ddddddd 100644
--- a/internal/tui/model/pr_detail.go
+++ b/internal/tui/model/pr_detail.go
@@ -456,6 +456,18 @@ var diffGutterBlank = strings.Repeat(" ", diffGutterWidth)
 var (
 	diffAddBgStyle = lipgloss.NewStyle().Background(lipgloss.Color("#173027"))
 	diffDelBgStyle = lipgloss.NewStyle().Background(lipgloss.Color("#2C1A1F"))
 )
+
+// formatGutter builds the 10-cell ` + "`old│new`" + ` gutter for one diff line and
+// renders it pre-merged with the row background so the bg color paints
+// continuously underneath the dim line numbers.
+func formatGutter(ln review.DiffLine) string {
+	const blankNum = "    "
+	bg := rowBgStyle(ln.Kind)
+	left, right := blankNum, blankNum
+	if ln.OldNo > 0 { left = fmt.Sprintf("%4d", ln.OldNo) }
+	if ln.NewNo > 0 { right = fmt.Sprintf("%4d", ln.NewNo) }
+	return styles.DimStyle.Inherit(bg).Render(left + "\u2502" + right + " ")
+}
diff --git a/internal/tui/model/pr_detail_test.go b/internal/tui/model/pr_detail_test.go
index eeeeeee..fffffff 100644
--- a/internal/tui/model/pr_detail_test.go
+++ b/internal/tui/model/pr_detail_test.go
@@ -200,3 +200,40 @@ func TestRenderDiffPaneNarrowFallsBackToNoGutter(t *testing.T) {
 		t.Fatalf("narrow pane should drop gutter; '│' still present:\n%s", plain)
 	}
 }
+
+// Add/del rows must paint their background tint edge-to-edge.
+func TestRenderHunkLineAddedRowBgPaintsFullWidth(t *testing.T) {
+	withTrueColor(t)
+	ln := review.DiffLine{Kind: review.DiffAdded, NewNo: 5, Text: "func foo() {"}
+	out := renderHunkLine(ln, 60, true)
+	if got := untintedPrintableCells(out); got != 0 {
+		t.Fatalf("added row has %d untinted printable cell(s); raw=%q", got, out)
+	}
+}
`

const demoDiffAppr109 = `diff --git a/internal/review/conventionwitness/witness.go b/internal/review/conventionwitness/witness.go
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/internal/review/conventionwitness/witness.go
@@ -0,0 +1,40 @@
+// Package conventionwitness runs a per-finding classifier between
+// specialists and the repo arbiter. It tags each testing/docs
+// finding as congruent / divergent / unknown vs the PR's static
+// evidence so the arbiter can demote findings that contradict the
+// repo's already-established conventions.
+package conventionwitness
+
+import "context"
+
+// Witness is the per-finding verdict produced by Run.
+type Witness struct {
+	Specialist string
+	Path       string
+	Line       int
+	Verdict    string // "congruent" | "divergent" | "unknown"
+	Reason     string
+}
+
+// CompleteFunc matches review.Complete; injected so tests can stub
+// the model call without spinning up the full provider stack.
+type CompleteFunc func(ctx context.Context, prompt string) (string, error)
diff --git a/internal/review/runner.go b/internal/review/runner.go
index 7777777..8888888 100644
--- a/internal/review/runner.go
+++ b/internal/review/runner.go
@@ -332,7 +332,11 @@ func runSpecialists(ctx context.Context, runCfg *aiconfig.Config, ...) {
 	}

-	// Repo arbiter: filter, demote, override verdict.
+	// Convention witness: classify testing/docs findings against the
+	// PR's static evidence before the arbiter sees them. Demotes the
+	// "noise" rate without losing actionable signal.
+	witnesses := conventionwitness.Run(ctx, runCfg, ...)
+	out <- Progress{Stage: "convention-witness", Detail: fmt.Sprintf("%d witnesses", len(witnesses))}
 	res := repoarbiter.Run(ctx, runCfg, ...)
`

const demoDiffPlumbing56 = `diff --git a/internal/publish/loop.go b/internal/publish/loop.go
index 1aa1aa1..2bb2bb2 100644
--- a/internal/publish/loop.go
+++ b/internal/publish/loop.go
@@ -68,7 +68,30 @@ func (l *Loop) drainOnce(ctx context.Context) error {
-		if err := l.tx.Publish(ctx, msg); err != nil {
-			return fmt.Errorf("publish: %w", err)
-		}
+		if err := l.publishWithBackoff(ctx, msg); err != nil {
+			return fmt.Errorf("publish: %w", err)
+		}
 	}
 	return nil
 }
+
+// publishWithBackoff retries 429s with exponential backoff + jitter,
+// capped at maxBackoff so a long outage doesn't stall the rest of the
+// queue indefinitely.
+func (l *Loop) publishWithBackoff(ctx context.Context, msg Message) error {
+	const maxAttempts = 6
+	const baseDelay = 100 * time.Millisecond
+	const maxBackoff = 30 * time.Second
+	for attempt := 0; attempt < maxAttempts; attempt++ {
+		err := l.tx.Publish(ctx, msg)
+		if err == nil { return nil }
+		if !is429(err) { return err }
+		d := backoffDelay(attempt, baseDelay, maxBackoff)
+		select {
+		case <-ctx.Done():
+			return ctx.Err()
+		case <-time.After(d):
+		}
+	}
+	return fmt.Errorf("publish: exhausted retries (last 429)")
+}
diff --git a/internal/publish/backoff.go b/internal/publish/backoff.go
new file mode 100644
--- /dev/null
+++ b/internal/publish/backoff.go
@@ -0,0 +1,18 @@
+package publish
+
+import (
+	"math/rand"
+	"time"
+)
+
+// backoffDelay returns base*2^attempt with up to ±25% jitter, capped at
+// maxBackoff. Jitter prevents synchronized retries across consumers.
+func backoffDelay(attempt int, base, maxBackoff time.Duration) time.Duration {
+	d := base << attempt
+	if d <= 0 || d > maxBackoff {
+		d = maxBackoff
+	}
+	jitter := time.Duration(rand.Int63n(int64(d / 4)))
+	if rand.Intn(2) == 0 { return d - jitter }
+	return d + jitter
+}
`

const demoDiffPlumbing22 = `diff --git a/docs/api.md b/docs/api.md
index 9aa9aa9..0bb0bb0 100644
--- a/docs/api.md
+++ b/docs/api.md
@@ -42,9 +42,18 @@ Returns the latest message available on the topic.
 ### Archived topics

-Topics flagged ` + "`archived: true`" + ` are read-only. Existing messages are
-retained for an unspecified period.
+Topics flagged ` + "`archived: true`" + ` are read-only — publish operations on
+them return ` + "`409 Conflict`" + `. Existing messages are retained for at
+least the topic's configured ` + "`retention_days`" + `, after which the
+plumbing-svc may garbage-collect them. Clients MUST NOT assume archived
+topics retain messages indefinitely; persistent storage is the
+caller's responsibility.
+
+| Field            | Behaviour after archive                |
+| ---------------- | -------------------------------------- |
+| ` + "`publish`" + `        | Rejected with 409                      |
+| ` + "`get_latest`" + `     | Returned for ` + "`retention_days`" + `              |
+| ` + "`stream_changes`" + ` | Closes the stream after retention pass |
`

// demoDiffGeneric is the fallback rendered when a user pastes an unknown
// owner/repo#N. It's small but covers add/del/context lines so the diff
// pane still demonstrates wrapping + bg painting.
var demoDiffGeneric = strings.TrimLeft(`
diff --git a/example/main.go b/example/main.go
index 0000001..0000002 100644
--- a/example/main.go
+++ b/example/main.go
@@ -1,7 +1,9 @@
 package main

-import "fmt"
+import (
+	"fmt"
+	"os"
+)

 func main() {
-	fmt.Println("hello")
+	fmt.Fprintln(os.Stdout, "hello, world")
 }
`, "\n")
