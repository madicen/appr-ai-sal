package model

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/review"
	langagentsstore "github.com/madicen/appr-ai-sal/internal/review/langagents"
	repoagentsstore "github.com/madicen/appr-ai-sal/internal/review/repoagents"
	langagentstui "github.com/madicen/appr-ai-sal/internal/tui/tabs/langagents"
	repoagentstui "github.com/madicen/appr-ai-sal/internal/tui/tabs/repoagents"
	"github.com/madicen/appr-ai-sal/internal/tui/tabs/settings"
)

func (m *Model) openSettings(start settings.StartSection) tea.Cmd {
	m.settingsPrevMode = m.mode
	m.mode = modeSettings
	m.settings = settings.New(settings.Opts{
		Cfg:          m.opts.AIConfig,
		Width:        m.width,
		BodyHeight:   m.chromeBodyHeight(),
		StartSection: start,
	})
	return m.settings.Init()
}

// openRepoAgents seeds the repo-agents tab with repos derived from the
// currently-loaded PR list (plus any saved on disk) and switches into the
// new mode. Pass review.Complete and gh.BuildReviewHistoryDigest as the LLM
// hooks; the subpackage only depends on those callables, not on the rest of
// the review package.
//
// focusRepo (lowercased owner/repo) is selected at startup when non-empty
// — typically the repo for the PR the user is viewing, so ctrl+r from
// detail lands on the right row instead of the alphabetical first one.
//
// When autoRegen is true and focusRepo is set, "Regenerate all" fires
// immediately. That's the path bound to ctrl+b ("build agents") so the user
// gets straight from key press to running LLM jobs.
func (m *Model) openRepoAgents(focusRepo string, autoRegen bool) tea.Cmd {
	m.repoAgentsPrevMode = m.mode
	m.mode = modeRepoAgents
	// We're about to let the user edit / regenerate; invalidate eagerly so
	// the freshness chip reflects the new state the moment they return.
	m.invalidateRepoAgentsFreshness()
	rc, _ := repoconfig.Load()
	if rc == nil {
		rc = repoconfig.Default()
	}
	repoconfig.ApplyParallelExecutionEnv(rc)

	seeds := m.repoSeedsFromList(rc)
	if m.currentPR != nil {
		seeds = append(seeds, m.currentPR.Owner+"/"+m.currentPR.Repo)
	}

	pathHistoryFetcher := func(ctx context.Context, owner, repo string) (string, error) {
		rows, err := review.LoadOrFetchPathHistory(ctx, rc, owner, repo, nil, false)
		if err != nil {
			return "", err
		}
		return review.FormatPathHistoryAggregate(review.AggregatePathHistory(rows)), nil
	}

	m.repoAgents = repoagentstui.New(repoagentstui.Opts{
		AICfg:        m.opts.AIConfig,
		RC:           rc,
		Width:        m.width,
		BodyHeight:   m.chromeBodyHeight(),
		Complete:     repoagentsstore.CompleteFunc(review.Complete),
		History:      repoagentsstore.HistoryFetcher(gh.BuildReviewHistoryDigest),
		PathHistory:  repoagentsstore.PathHistoryFetcher(pathHistoryFetcher),
		InitialRepos: seeds,
		FocusRepo:    strings.ToLower(strings.TrimSpace(focusRepo)),
		AutoRegenAll: autoRegen,
	})
	return m.repoAgents.Init()
}

// openLangAgents opens the language-experts tab. From detail mode (a
// PR is loaded with a parsed diff) the tab is scoped to ONLY the
// languages that PR touches, so generation flows are anchored to "I
// need a brief because this PR uses it." From list mode the tab opens
// unscoped, showing cached briefs only; the user is expected to drill
// into a PR to discover and generate new languages.
//
// Language briefs themselves are user-global — generating Swift from
// PR #1234 makes Swift available to every subsequent review across
// every repo.
func (m *Model) openLangAgents() tea.Cmd {
	m.langAgentsPrevMode = m.mode
	m.mode = modeLangAgents
	opts := langagentstui.Opts{
		AICfg:      m.opts.AIConfig,
		Width:      m.width,
		BodyHeight: m.chromeBodyHeight(),
		Complete:   langagentsstore.CompleteFunc(review.Complete),
	}
	if m.langAgentsPrevMode == modeDetail && len(m.parsedDiff) > 0 {
		// Use a non-nil slice (even when empty) to opt into scoped
		// rendering — the tab's header tells the user we noticed the
		// PR even when no rows match.
		opts.PRLanguages = languagesForFileDiffs(m.parsedDiff)
		if m.currentPR != nil {
			opts.PRLabel = fmt.Sprintf("%s#%d", m.currentPR.Repository, m.currentPR.Number)
		}
	}
	m.langAgents = langagentstui.New(opts).(*langagentstui.Model)
	return m.langAgents.Init()
}

// languagesForFileDiffs returns the canonical language names touched
// by a parsed diff, sorted by descending touch count (sum of added +
// deleted lines per language). Used to scope the language-experts
// tab to the dominant-first language set the PR exercises.
func languagesForFileDiffs(files []review.FileDiff) []langagentsstore.Language {
	if len(files) == 0 {
		return []langagentsstore.Language{}
	}
	touches := map[langagentsstore.Language]int{}
	for _, f := range files {
		c := langagentsstore.LanguageForPath(f.Path)
		if c == "" {
			continue
		}
		touches[c] += f.Additions + f.Deletions
	}
	out := make([]langagentsstore.Language, 0, len(touches))
	for l := range touches {
		out = append(out, l)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if touches[out[i]] != touches[out[j]] {
			return touches[out[i]] > touches[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// openRepoAgentsForCurrentPR is the convenience wrapper bound to the
// "Build/refresh repo agents" action available on the PR detail view. It
// pre-focuses the tab on the PR's owner/repo and (when autoRegen is true)
// kicks off "Regenerate all" so a single key press takes the user from
// detail to "all 5 specialist agents are being rebuilt for this repo".
func (m *Model) openRepoAgentsForCurrentPR(autoRegen bool) tea.Cmd {
	focus := ""
	if m.currentPR != nil {
		focus = m.currentPR.Owner + "/" + m.currentPR.Repo
	}
	return m.openRepoAgents(focus, autoRegen)
}

// repoAgentsFreshness returns a TTL-cached freshness reading for the given
// owner/repo so the chip and status hint can colour themselves cheaply on
// every render. Empty owner or repo returns FreshnessUnknown so the caller
// renders neutrally.
