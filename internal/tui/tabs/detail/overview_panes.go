package detail

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
)

// renderChecksPane renders the centre pane for the Checks overview. It
// shows the rolled-up status, then the per-run breakdown — failing runs
// expand inline to surface their summary + the first few annotations so
// the reviewer can grok the failure without leaving the TUI.
func renderChecksPane(report *gh.ChecksReport, loading bool, fetchErr error, width int) string {
	width = max(8, width)
	var b strings.Builder
	b.WriteString(styles.BoldStyle.Render("Checks") + "\n\n")
	if loading {
		b.WriteString(styles.DimStyle.Render("loading status checks from GitHub…"))
		return b.String()
	}
	if fetchErr != nil {
		b.WriteString(styles.ErrStyle.Render("failed to load checks: ") + fetchErr.Error())
		return b.String()
	}
	if report == nil || (report.RollupState == "" && len(report.Runs) == 0) {
		b.WriteString(styles.DimStyle.Render("(no status checks have run on this PR)"))
		return b.String()
	}

	rollup := strings.ToUpper(strings.TrimSpace(report.RollupState))
	switch rollup {
	case "SUCCESS":
		b.WriteString(styles.OkStyle.Render("All checks passed"))
	case "FAILURE":
		b.WriteString(styles.ErrStyle.Render("One or more checks failed"))
	case "ERROR":
		b.WriteString(styles.ErrStyle.Render("One or more checks errored"))
	case "PENDING":
		b.WriteString(styles.WarnStyle.Render("Checks are still running"))
	case "":
		b.WriteString(styles.DimStyle.Render("Rollup state unavailable"))
	default:
		b.WriteString(styles.DimStyle.Render(rollup))
	}
	if report.HeadSHA != "" {
		short := report.HeadSHA
		if len(short) > 7 {
			short = short[:7]
		}
		b.WriteString("  " + styles.DimStyle.Render("· head "+short))
	}
	b.WriteString("\n\n")

	if len(report.Runs) == 0 {
		b.WriteString(styles.DimStyle.Render("(no individual run details available)"))
		return b.String()
	}
	for i, r := range report.Runs {
		b.WriteString(renderCheckRun(r, width))
		if i < len(report.Runs)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderCheckRun renders one CheckRun row plus (for failing runs) a small
// expansion with the run summary + first ~5 annotations.
func renderCheckRun(r gh.CheckRun, width int) string {
	state := strings.ToUpper(strings.TrimSpace(coalesceStr(r.Conclusion, r.State)))
	var glyph, name string
	switch state {
	case "SUCCESS":
		glyph = styles.OkStyle.Render("✓")
		name = r.Name
	case "FAILURE":
		glyph = styles.ErrStyle.Render("✗")
		name = styles.ErrStyle.Render(r.Name)
	case "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STALE":
		glyph = styles.ErrStyle.Render("!")
		name = styles.ErrStyle.Render(r.Name)
	case "PENDING", "QUEUED", "IN_PROGRESS", "EXPECTED":
		glyph = styles.WarnStyle.Render("…")
		name = styles.WarnStyle.Render(r.Name)
	case "NEUTRAL", "SKIPPED":
		glyph = styles.DimStyle.Render("-")
		name = styles.DimStyle.Render(r.Name)
	case "":
		// Empty conclusion + non-COMPLETED status = still in flight.
		if !strings.EqualFold(r.Status, "COMPLETED") && r.Status != "" {
			glyph = styles.WarnStyle.Render("…")
			name = styles.WarnStyle.Render(r.Name)
		} else {
			glyph = styles.DimStyle.Render("?")
			name = styles.DimStyle.Render(r.Name)
		}
	default:
		glyph = styles.DimStyle.Render("·")
		name = r.Name
	}

	var line strings.Builder
	line.WriteString(glyph + "  " + name)
	if r.App != "" {
		line.WriteString("  " + styles.DimStyle.Render("· "+r.App))
	}
	if d := runDuration(r); d != "" {
		line.WriteString("  " + styles.DimStyle.Render("· "+d))
	}
	if r.DetailsURL != "" {
		line.WriteString("  " + styles.DimStyle.Render("· "+r.DetailsURL))
	}
	out := ansi.Truncate(line.String(), width, "…") + "\n"

	// Failing runs get an inline expansion so the reviewer can see what
	// went wrong without clicking through to GitHub.
	if state == "FAILURE" || state == "ERROR" || state == "TIMED_OUT" || state == "CANCELLED" || state == "ACTION_REQUIRED" {
		body := strings.TrimSpace(r.Title)
		if body != "" {
			out += util.IndentLines(body, "    ", width-4) + "\n"
		}
		if sum := strings.TrimSpace(r.Summary); sum != "" {
			out += util.IndentLines(sum, "    ", width-4) + "\n"
		}
		if len(r.Annotations) > 0 {
			limit := len(r.Annotations)
			if limit > 5 {
				limit = 5
			}
			for i := 0; i < limit; i++ {
				a := r.Annotations[i]
				header := styles.DimStyle.Render(fmt.Sprintf("    %s:%d", a.Path, a.Line))
				out += header + "\n"
				out += util.IndentLines(strings.TrimSpace(a.Message), "      ", width-6) + "\n"
			}
			if len(r.Annotations) > limit {
				out += styles.DimStyle.Render(fmt.Sprintf("    … %d more annotation(s)\n", len(r.Annotations)-limit))
			}
		}
	}
	return out
}

func runDuration(r gh.CheckRun) string {
	if r.StartedAt.IsZero() {
		return ""
	}
	end := r.CompletedAt
	if end.IsZero() {
		end = time.Now()
	}
	d := end.Sub(r.StartedAt)
	if d < time.Second {
		return ""
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// renderDiscussionPane renders the centre pane for the Discussion overview.
// Each event is shown with `@author kind · since` header and the body
// rendered through Glamour so markdown / code fences / lists pass through
// formatted instead of raw.
func renderDiscussionPane(events []gh.DiscussionEvent, loading bool, fetchErr error, width int) string {
	width = max(8, width)
	var b strings.Builder
	b.WriteString(styles.BoldStyle.Render("Discussion") + "\n\n")
	if loading {
		b.WriteString(styles.DimStyle.Render("loading conversation from GitHub…"))
		return b.String()
	}
	if fetchErr != nil {
		b.WriteString(styles.ErrStyle.Render("failed to load discussion: ") + fetchErr.Error())
		return b.String()
	}
	if len(events) == 0 {
		b.WriteString(styles.DimStyle.Render("(no comments yet — this PR's Conversation tab is empty)"))
		return b.String()
	}
	for i, e := range events {
		b.WriteString(renderDiscussionEvent(e, width))
		if i < len(events)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func renderDiscussionEvent(e gh.DiscussionEvent, width int) string {
	width = max(8, width)
	author := "@" + e.Author
	if strings.TrimSpace(e.Author) == "" {
		author = "@unknown"
	}
	var verb string
	switch e.Kind {
	case gh.DiscussionReview:
		switch strings.ToUpper(strings.TrimSpace(e.Verdict)) {
		case "APPROVED":
			verb = styles.OkStyle.Render("approved")
		case "CHANGES_REQUESTED":
			verb = styles.ErrStyle.Render("requested changes")
		case "DISMISSED":
			verb = styles.DimStyle.Render("review dismissed")
		default:
			verb = styles.DimStyle.Render("commented on the review")
		}
	default:
		verb = styles.DimStyle.Render("commented")
	}
	when := styles.DimStyle.Render("· " + humanSince(e.When))
	header := styles.BoldStyle.Render(author) + " " + verb + " " + when
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return header + "\n"
	}
	rendered := util.RenderMarkdownIndented(body, width-2, 0)
	return header + "\n" + util.IndentLines(rendered, "  ", width-2) + "\n"
}

func coalesceStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
