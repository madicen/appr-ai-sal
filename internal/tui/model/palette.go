package model

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	overlay "github.com/madicen/bubble-overlay"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/tui/commands"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/overlays"
	"github.com/madicen/appr-ai-sal/internal/tui/tabs/settings"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
)

// commandContext snapshots the state a command's Enabled predicate reads to
// decide whether it should appear in the palette right now.
func (m *Model) commandContext() commands.Context {
	ctx := commands.Context{HasPR: m.currentPR != nil, HasDraft: m.draft != nil}
	switch m.mode {
	case modeList:
		ctx.Mode = "list"
	case modeDetail:
		ctx.Mode = "detail"
	case modeSettings:
		ctx.Mode = "settings"
	case modeRepoAgents:
		ctx.Mode = "repoagents"
	case modeLangAgents:
		ctx.Mode = "langagents"
	}
	_, ctx.HasSelection = m.list.SelectedItem().(prItem)
	return ctx
}

// openHelpOverlay pushes the full-screen `?` help overlay, derived from the
// central keymap's sections so it always mirrors the live bindings.
func (m *Model) openHelpOverlay() tea.Cmd {
	hm := overlays.NewHelpOverlay(m.keys.Sections(), max(40, m.width-6), max(10, m.height-6))
	return tea.Batch(
		m.overlayStack.Push(hm, overlay.DefaultOverlayConfig()),
		func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
	)
}

// openCommandPalette pushes the ctrl+k fuzzy command palette over the
// currently-enabled subset of the registry. The palette is modal (owns key
// input while open) and, on selection, runs the command's wired action.
func (m *Model) openCommandPalette() tea.Cmd {
	enabled := m.palette.Enabled(m.commandContext())
	pm := overlays.NewPaletteOverlay(enabled, max(40, m.width-6), max(10, m.height-6))
	cfg := overlay.DefaultOverlayConfig()
	cfg.CloseOnClickOutside = true
	return tea.Batch(
		m.overlayStack.Push(pm, cfg),
		func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
	)
}

// buildCommandRegistry wires every palette command to an existing model
// handler — commands never reimplement actions, they return the same
// tea.Cmd the key binding does. Enabled predicates gate each command on the
// active screen (and, where relevant, whether a PR / draft is loaded).
//
// Registration seam: later Phase 5 groups append their commands here (item 2
// edit `e`, item 5 triage sort/filter, item 10 queue) without touching core
// routing.
func (m *Model) buildCommandRegistry() *commands.Registry {
	r := commands.New()
	km := m.keys

	inList := func(c commands.Context) bool { return c.Mode == "list" }
	inDetail := func(c commands.Context) bool { return c.Mode == "detail" }
	listOrDetail := func(c commands.Context) bool { return c.Mode == "list" || c.Mode == "detail" }

	// wrapModel adapts a handler returning (tea.Model, tea.Cmd) to a
	// command Run (the model mutation persists via the *Model receiver).
	wrapModel := func(fn func() (tea.Model, tea.Cmd)) func() tea.Cmd {
		return func() tea.Cmd { _, cmd := fn(); return cmd }
	}
	act := func(fn func()) func() tea.Cmd {
		return func() tea.Cmd { fn(); return nil }
	}

	// Global.
	r.Register(commands.Command{
		ID: "help.show", Title: "Show keyboard shortcuts", Category: "Global",
		Binding: km.Help, Run: func() tea.Cmd { return m.openHelpOverlay() },
	})
	r.Register(commands.Command{
		ID: "app.quit", Title: "Quit appr-ai-sal", Category: "Global",
		Binding: km.Quit, Run: func() tea.Cmd { util.FlushMouse(); return tea.Quit },
	})

	// List.
	r.Register(commands.Command{
		ID: "list.refresh", Title: "Refresh PR list", Category: "Review queue",
		Binding: km.ListRefresh, Enabled: inList,
		Run: func() tea.Cmd { return m.refreshPRListCmd() },
	})
	r.Register(commands.Command{
		ID: "list.filter", Title: "Cycle review-queue filter", Category: "Review queue",
		Binding: km.ListFilter, Enabled: inList,
		Run: func() tea.Cmd { return m.cycleFilterCmd() },
	})
	r.Register(commands.Command{
		ID: "list.search", Title: "Search PRs", Category: "Review queue",
		Binding: km.ListSearch, Enabled: inList,
		Run: func() tea.Cmd { return m.focusSearchInput() },
	})
	r.Register(commands.Command{
		ID: "list.url", Title: "Open PR by URL", Category: "Review queue",
		Binding: km.ListURL, Enabled: inList,
		Run: func() tea.Cmd { return m.focusURLInput() },
	})
	r.Register(commands.Command{
		ID: "list.open", Title: "Open highlighted PR", Category: "Review queue",
		Binding: km.ListOpen,
		Enabled: func(c commands.Context) bool { return c.Mode == "list" && c.HasSelection },
		Run: func() tea.Cmd {
			it, ok := m.list.SelectedItem().(prItem)
			if !ok {
				return nil
			}
			ref := gh.Ref{Owner: it.pr.Owner, Repo: it.pr.Repo, Number: it.pr.Number}
			return data.LoadPRDetailCmd(ref, m.opts.Demo)
		},
	})

	// Detail.
	r.Register(commands.Command{
		ID: "detail.review", Title: "Start AI review", Category: "PR detail",
		Binding: km.DetailReview,
		Enabled: func(c commands.Context) bool { return c.Mode == "detail" && c.HasPR },
		Run:     wrapModel(m.startReviewOverlay),
	})
	r.Register(commands.Command{
		ID: "detail.toggle-controls", Title: "Toggle review controls pane", Category: "PR detail",
		Binding: km.DetailToggleControls, Enabled: inDetail,
		Run: act(m.detailToggleControls),
	})
	r.Register(commands.Command{
		ID: "detail.diff-only", Title: "Toggle full-width diff", Category: "PR detail",
		Binding: km.DetailDiffOnly, Enabled: inDetail,
		Run: act(m.detailToggleDiffOnly),
	})
	r.Register(commands.Command{
		ID: "detail.description", Title: "Toggle description / diff", Category: "PR detail",
		Binding: km.DetailDescription, Enabled: inDetail,
		Run: func() tea.Cmd { return m.detailToggleDescription() },
	})
	r.Register(commands.Command{
		ID: "detail.reopen-approval", Title: "Reopen approval overlay", Category: "PR detail",
		Binding: km.DetailReopenApproval,
		Enabled: func(c commands.Context) bool { return c.Mode == "detail" && c.HasDraft },
		Run:     wrapModel(m.reopenApprovalIfPossible),
	})
	r.Register(commands.Command{
		ID: "detail.bulk-post", Title: "Bulk-post review draft", Category: "PR detail",
		Binding: km.DetailBulk,
		Enabled: func(c commands.Context) bool { return c.Mode == "detail" && c.HasDraft },
		Run:     func() tea.Cmd { return m.detailBulkConfirmCmd() },
	})
	r.Register(commands.Command{
		ID: "detail.tech-experts", Title: "Open tech experts for this PR", Category: "PR detail",
		Binding: km.DetailTech, Enabled: inDetail,
		Run: func() tea.Cmd { return m.openRepoAgentsForCurrentPR(false) },
	})
	r.Register(commands.Command{
		ID: "detail.back", Title: "Back to PR list", Category: "PR detail",
		Binding: km.DetailBack, Enabled: inDetail,
		Run: act(m.detailBackToList),
	})

	// Shared navigation (list + detail).
	r.Register(commands.Command{
		ID: "open.settings-ai", Title: "Open settings (AI profile)", Category: "Navigation",
		Binding: km.SettingsAI, Enabled: listOrDetail,
		Run: func() tea.Cmd { return m.openSettings(settings.StartAI) },
	})
	r.Register(commands.Command{
		ID: "open.settings-review", Title: "Open settings (review)", Category: "Navigation",
		Binding: km.SettingsReview, Enabled: listOrDetail,
		Run: func() tea.Cmd { return m.openSettings(settings.StartReview) },
	})
	r.Register(commands.Command{
		ID: "open.repo-context", Title: "Open settings (repo context)", Category: "Navigation",
		Binding: km.RepoCtx, Enabled: listOrDetail,
		Run: func() tea.Cmd { return m.openSettings(settings.StartRepoContext) },
	})
	r.Register(commands.Command{
		ID: "open.repo-agents", Title: "Open repo agents", Category: "Navigation",
		Binding: km.RepoAgents, Enabled: listOrDetail,
		Run: func() tea.Cmd {
			if m.mode == modeDetail {
				return m.openRepoAgentsForCurrentPR(false)
			}
			return m.openRepoAgents("", false)
		},
	})
	r.Register(commands.Command{
		ID: "open.lang-agents", Title: "Open language experts", Category: "Navigation",
		Binding: km.LangAgents, Enabled: listOrDetail,
		Run: func() tea.Cmd { return m.openLangAgents() },
	})
	r.Register(commands.Command{
		ID: "open.build-agents", Title: "Build / refresh repo agents", Category: "Navigation",
		Binding: km.BuildAgents, Enabled: listOrDetail,
		Run: func() tea.Cmd {
			if m.mode == modeDetail {
				return m.openRepoAgentsForCurrentPR(true)
			}
			if it, ok := m.list.SelectedItem().(prItem); ok {
				return m.openRepoAgents(it.pr.Owner+"/"+it.pr.Repo, true)
			}
			return m.openRepoAgents("", false)
		},
	})
	r.Register(commands.Command{
		ID: "open.browser", Title: "Open PR in browser", Category: "Navigation",
		Binding: km.Browser,
		Enabled: func(c commands.Context) bool {
			return (c.Mode == "detail" && c.HasPR) || (c.Mode == "list" && c.HasSelection)
		},
		Run: func() tea.Cmd {
			if m.mode == modeDetail {
				return m.detailOpenBrowserCmd()
			}
			if it, ok := m.list.SelectedItem().(prItem); ok {
				if u := strings.TrimSpace(it.pr.URL); u != "" {
					return util.OpenInBrowserCmd(u)
				}
			}
			return nil
		},
	})

	return r
}
