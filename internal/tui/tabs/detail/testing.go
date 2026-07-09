package detail

import (
	"testing"

	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/keys"
)

func init() { zone.NewGlobal() }

func detailModelWithDiff(t *testing.T) *Model {
	t.Helper()
	host := newTestHost(140, 44)
	diff := "diff --git a/main.go b/main.go\n" +
		"--- a/main.go\n+++ b/main.go\n" +
		"@@ -1,4 +1,4 @@\n" +
		" package main\n" +
		"-func old() int { return 1 }\n" +
		"+func neu() int { return 2 }\n" +
		" // tail\n" +
		" var x = 1\n"
	host.pr = &gh.PR{Owner: "o", Repo: "r", Number: 7, Title: "t"}
	host.diff = review.ParseDiff(diff)
	m := New(host, keys.Default())
	m.OnPRLoaded(host.diff, nil)
	m.focusedPane = paneDiff
	m.RefreshViews()
	return m
}
