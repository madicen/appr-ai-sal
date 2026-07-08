// Package keys is the single source of truth for every key binding the
// root TUI's list and detail screens (and the global overlays) respond to.
//
// Historically the bindings were scattered raw-string `case` arms across
// model/list.go and model/detail.go, and the bottom status bar carried a
// second, hand-maintained list of "f filter" / "/ search" hint strings that
// could (and did) drift out of sync with the handlers. Phase 5 item 1
// consolidates all of that here: each binding is a bubbles/key.Binding with
// its keys AND its help text, so:
//
//   - the key handlers match against these bindings (key.Matches), and
//   - the status bar + the `?` help overlay + the command palette all read
//     their labels from the SAME Binding.Help(), so a hint can never drift
//     from the key that triggers it.
//
// This package is a leaf: it imports only bubbles/key so both model/tabs
// and overlays can depend on it without import cycles.
//
// Registration seams for later Phase 5 groups: to add a new binding, add a
// field to Map, populate it in Default(), and (if it should appear) add it
// to the relevant Sections() group and to the model's status/handler sites.
// Item 2 (edit, `e`), item 5 (triage sort/filter), item 8 (thread browse),
// item 9 (clipboard) and item 10 (queue) each just add fields here.
package keys

import "github.com/charmbracelet/bubbles/key"

// Map holds every binding, grouped by the context it applies in. A single
// shared Default() value is stored on the root model; keeping it a value
// (not globals) leaves a clean seam for a future user-remap feature.
type Map struct {
	// Global — available from the root-native list and detail screens.
	Help    key.Binding // ? — toggle the full-screen help overlay
	Palette key.Binding // ctrl+k — open the fuzzy command palette
	Quit    key.Binding // ctrl+c — always quits

	// List screen.
	ListNav         key.Binding // ↑/↓ (list widget owns the actual scroll)
	ListClick       key.Binding // mouse gesture (display only)
	ListOpenClick   key.Binding // double-click gesture (display only)
	ListOpen        key.Binding // enter — open the highlighted PR
	ListCycleFocus  key.Binding // tab — cycle list/search/url focus
	ListCycleFocusB key.Binding // shift+tab — cycle focus backward
	ListClearSearch key.Binding // esc — clear the inline search (display only)
	ListSearch      key.Binding // / — focus the search field
	ListURL         key.Binding // u — focus the URL field
	ListFilter      key.Binding // f — cycle the review-queue filter
	ListRefresh     key.Binding // R — refetch the PR list
	ListQuit        key.Binding // q — quit

	// Detail screen.
	DetailBack           key.Binding // esc/q — back to the list
	DetailCyclePane      key.Binding // tab — cycle tree/diff/controls
	DetailCyclePaneB     key.Binding // shift+tab — cycle panes backward
	DetailNav            key.Binding // j/k — move the tree cursor (display)
	DetailNavDown        key.Binding // j/down
	DetailNavUp          key.Binding // k/up
	DetailFold           key.Binding // space — fold the current folder row
	DetailEnter          key.Binding // enter — fold the current folder row
	DetailReview         key.Binding // r — start an AI review
	DetailToggleControls key.Binding // c — show/hide the controls pane
	DetailReopenApproval key.Binding // a — reopen the approval overlay
	DetailDescription    key.Binding // g — toggle description/diff
	DetailDiffOnly       key.Binding // d — toggle full-width diff
	DetailBulk           key.Binding // P — bulk-post the draft
	DetailTech           key.Binding // ctrl+t — tech experts for this PR
	DetailHalfDown       key.Binding // ctrl+d — half page down (diff)
	DetailHalfUp         key.Binding // ctrl+u — half page up (diff)

	// Shared navigation-to-tab bindings (identical keys in list + detail;
	// the handlers pick behaviour appropriate to the screen).
	SettingsAI     key.Binding // o — settings (AI profile)
	SettingsReview key.Binding // , / ctrl+@ — settings (review)
	RepoCtx        key.Binding // ctrl+g — settings (repo context)
	RepoAgents     key.Binding // ctrl+r — repo agents tab
	LangAgents     key.Binding // ctrl+l — language experts tab
	BuildAgents    key.Binding // ctrl+b — build/refresh repo agents
	Browser        key.Binding // O — open the PR in the browser

	// Review overlay — documentation-only. The review tab owns its own
	// phase-dependent handling; these are listed so the `?` overlay is a
	// complete reference. Do NOT match against them from root routing.
	ReviewPost      key.Binding // y — post the focused finding
	ReviewSkip      key.Binding // n/s — skip the focused finding
	ReviewResurface key.Binding // x — resurface a suppressed finding
	ReviewChallenge key.Binding // c — challenge the finding (B4)
	ReviewNext      key.Binding // →/l — next finding
	ReviewPrev      key.Binding // ←/h — previous finding
	ReviewRefresh   key.Binding // r — refresh the PR (head drift)
	ReviewFileLevel key.Binding // F — post as a file-level comment
	ReviewTabs      key.Binding // tab/[ ] — switch overlay tabs
	ReviewClose     key.Binding // esc/q — close the overlay
}

// Default returns the built-in keymap. Keys and help text mirror the
// pre-migration handlers and status-bar strings exactly (see the model
// package) so behaviour and hints are preserved.
func Default() Map {
	b := func(help, desc string, keys ...string) key.Binding {
		return key.NewBinding(key.WithKeys(keys...), key.WithHelp(help, desc))
	}
	// disp is a display-only binding (no functional keys) used for gesture
	// hints like "click" that have no keystroke equivalent.
	disp := func(help, desc string) key.Binding {
		return key.NewBinding(key.WithHelp(help, desc), key.WithDisabled())
	}
	return Map{
		// Global.
		Help:    b("?", "help", "?"),
		Palette: b("ctrl+k", "commands", "ctrl+k"),
		Quit:    b("ctrl+c", "quit", "ctrl+c"),

		// List.
		ListNav:         disp("↑/↓", ""),
		ListClick:       disp("click", ""),
		ListOpenClick:   disp("double-click", "open"),
		ListOpen:        b("enter", "", "enter"),
		ListCycleFocus:  b("tab", "fields", "tab"),
		ListCycleFocusB: b("shift+tab", "fields", "shift+tab"),
		ListClearSearch: disp("esc", "clear"),
		ListSearch:      b("/", "search", "/"),
		ListURL:         b("u", "URL", "u"),
		ListFilter:      b("f", "filter", "f"),
		ListRefresh:     b("R", "refresh", "R"),
		ListQuit:        b("q", "quit", "q"),

		// Detail.
		DetailBack:           b("esc", "back", "esc", "q"),
		DetailCyclePane:      b("tab", "pane", "tab"),
		DetailCyclePaneB:     b("shift+tab", "pane", "shift+tab"),
		DetailNav:            disp("j/k", "nav"),
		DetailNavDown:        b("j", "down", "j", "down"),
		DetailNavUp:          b("k", "up", "k", "up"),
		DetailFold:           b("space", "fold", " "),
		DetailEnter:          b("enter", "fold", "enter"),
		DetailReview:         b("r", "review", "r"),
		DetailToggleControls: b("c", "toggle controls", "c"),
		DetailReopenApproval: b("a", "reopen approval", "a"),
		DetailDescription:    b("g", "description", "g"),
		DetailDiffOnly:       b("d", "diff-only", "d"),
		DetailBulk:           b("P", "bulk", "P"),
		DetailTech:           b("ctrl+t", "tech experts", "ctrl+t"),
		DetailHalfDown:       b("ctrl+d", "half page down", "ctrl+d"),
		DetailHalfUp:         b("ctrl+u", "half page up", "ctrl+u"),

		// Shared navigation.
		SettingsAI:     b("o/,", "settings", "o"),
		SettingsReview: b(",", "review settings", ",", "ctrl+@"),
		RepoCtx:        b("ctrl+g", "repo ctx", "ctrl+g"),
		RepoAgents:     b("ctrl+r", "repo agents", "ctrl+r"),
		LangAgents:     b("ctrl+l", "lang experts", "ctrl+l"),
		BuildAgents:    b("ctrl+b", "build agents", "ctrl+b"),
		Browser:        b("O", "browser", "O"),

		// Review overlay (reference only).
		ReviewPost:      b("y", "post finding", "y"),
		ReviewSkip:      b("n/s", "skip finding", "n", "s"),
		ReviewResurface: b("x", "resurface suppressed", "x"),
		ReviewChallenge: b("c", "challenge finding", "c"),
		ReviewNext:      b("→/l", "next finding", "right", "l"),
		ReviewPrev:      b("←/h", "prev finding", "left", "h"),
		ReviewRefresh:   b("r", "refresh PR", "r"),
		ReviewFileLevel: b("F", "post file-level", "F"),
		ReviewTabs:      b("tab/[ ]", "switch tabs", "tab", "[", "]"),
		ReviewClose:     b("esc", "close", "esc", "q"),
	}
}

// SegText renders a binding as a compact status-bar / palette segment:
// "key desc" (e.g. "f filter") or just "key" when the binding carries no
// description (e.g. "↑/↓"). This is the one place status-hint text is
// produced from a binding so the two can never drift.
func SegText(bnd key.Binding) string {
	h := bnd.Help()
	if h.Desc == "" {
		return h.Key
	}
	return h.Key + " " + h.Desc
}

// Section is one titled group in the `?` help overlay.
type Section struct {
	Title    string
	Bindings []key.Binding
}

// Sections returns the ordered, titled binding groups the help overlay
// renders. New Phase 5 bindings should be appended to the appropriate
// group here so they show up in help automatically.
func (m Map) Sections() []Section {
	return []Section{
		{Title: "Global", Bindings: []key.Binding{
			m.Help, m.Palette, m.Quit,
		}},
		{Title: "Review queue (list)", Bindings: []key.Binding{
			m.ListNav, m.ListOpen, m.ListOpenClick, m.ListSearch, m.ListURL,
			m.ListFilter, m.ListCycleFocus, m.ListClearSearch, m.ListRefresh,
			m.Browser, m.SettingsAI, m.SettingsReview, m.RepoCtx, m.RepoAgents,
			m.LangAgents, m.BuildAgents, m.ListQuit,
		}},
		{Title: "PR detail", Bindings: []key.Binding{
			m.DetailNav, m.DetailCyclePane, m.DetailFold, m.DetailReview,
			m.DetailToggleControls, m.DetailReopenApproval, m.DetailDescription,
			m.DetailDiffOnly, m.DetailBulk, m.DetailHalfDown, m.DetailHalfUp,
			m.Browser, m.SettingsAI, m.RepoCtx, m.RepoAgents, m.LangAgents,
			m.DetailTech, m.BuildAgents, m.DetailBack,
		}},
		{Title: "Review overlay", Bindings: []key.Binding{
			m.ReviewPost, m.ReviewSkip, m.ReviewResurface, m.ReviewChallenge,
			m.ReviewNext, m.ReviewPrev, m.ReviewRefresh, m.ReviewFileLevel,
			m.ReviewTabs, m.ReviewClose,
		}},
	}
}
