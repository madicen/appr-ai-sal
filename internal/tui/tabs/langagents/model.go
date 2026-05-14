// Package langagents is the TUI tab for managing language-convention
// briefs ("language agents"). All briefs are LLM-generated into a
// user-global cache; users add, refresh, or delete them at will. The
// tab shows currently-cached briefs alongside the wider set of
// languages the binary knows about (so the user can generate a brief
// for any of them with a single keystroke).
//
// Persistence is delegated to internal/review/langagents; this package
// owns only the UI layer.
package langagents

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	la "github.com/madicen/appr-ai-sal/internal/review/langagents"
	"github.com/madicen/appr-ai-sal/internal/tui/state"
)

// Opts configures a fresh Model.
type Opts struct {
	AICfg      *aiconfig.Config
	Width      int
	BodyHeight int
	// Complete runs LLM inference. The root TUI passes review.Complete
	// here; the indirection keeps this package free of any review
	// dependency.
	Complete la.CompleteFunc
	// PRLanguages, when non-empty, scopes the tab to ONLY those
	// languages — the rationale being "you opened this from a PR, so
	// only the languages that PR exercises are relevant right now."
	// Each language is rendered with its current status (cached /
	// stale / missing) and the same g/r/d actions; rows for languages
	// outside the scope are not displayed.
	//
	// When PRLanguages is empty (typically: opened from the PR list
	// without a specific PR in scope), the tab falls back to showing
	// only languages the user has already cached, so they can manage
	// their existing briefs. Adding new languages from that view is
	// intentionally not surfaced — the user is expected to drill into
	// a PR first.
	PRLanguages []la.Language
	// PRLabel is a short human label for the scope (e.g. "PR #1234")
	// rendered in the tab header when PRLanguages is non-empty. Empty
	// is fine; the header falls back to a generic label.
	PRLabel string
}

// Model is the language-agents tab.
type Model struct {
	width    int
	bodyH    int
	contentW int

	aiCfg    *aiconfig.Config
	complete la.CompleteFunc

	// scope is the PR-derived language set. When non-nil, the tab
	// renders rows ONLY for these languages (scoped mode). When nil,
	// the tab renders rows for every cached language (unscoped mode).
	// Empty slice and nil are treated differently here on purpose: an
	// empty scope means "the PR touches zero languages we recognise"
	// — we still want to render a scoped header in that case so the
	// user sees we noticed the PR, just with no rows.
	scope    []la.Language
	prLabel  string

	// rows is the displayable row set, rebuilt whenever scope or
	// cache change. Status (cached / stale / missing) is derived
	// from cache at render time.
	rows []row
	idx  int

	cache *la.LangAgents

	// busy[language] is true while a regenerate/generate command is in
	// flight; disables the row's actions and shows a spinner-ish label.
	busy map[la.Language]bool

	statusMsg string
	err       error
}

// row represents one displayable language line.
type row struct {
	Language la.Language
}

// New constructs a Model. When opts.PRLanguages is non-nil the tab
// opens in scoped mode (only those languages, even an empty list).
// When nil it opens in unscoped mode (only cached languages once the
// cache loads).
func New(opts Opts) tea.Model {
	m := &Model{
		width:    opts.Width,
		bodyH:    opts.BodyHeight,
		contentW: opts.Width,
		aiCfg:    opts.AICfg,
		complete: opts.Complete,
		busy:     make(map[la.Language]bool),
		prLabel:  strings.TrimSpace(opts.PRLabel),
	}
	if opts.PRLanguages != nil {
		// Canonicalise + dedupe at construction time so callers can
		// pass whatever shape is convenient (paths, aliases, mixed
		// case) and the model holds a clean set internally.
		seen := map[la.Language]struct{}{}
		scope := make([]la.Language, 0, len(opts.PRLanguages))
		for _, l := range opts.PRLanguages {
			c := la.Canonical(l)
			if c == "" {
				continue
			}
			if _, dup := seen[c]; dup {
				continue
			}
			seen[c] = struct{}{}
			scope = append(scope, c)
		}
		m.scope = scope
	}
	m.rebuildRows()
	return m
}

// Resize updates the model's width/height. Called by the root TUI on
// resize. Renamed from SetSize for parity with settings.Resize and
// repoagents.Resize so root can call all three through the same shape.
func (m *Model) Resize(width, body int) {
	m.width = width
	m.bodyH = body
	m.contentW = width
}

// Init kicks off the initial cache load.
func (m *Model) Init() tea.Cmd {
	return loadCacheCmd()
}

// Update handles all incoming messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Resize(msg.Width, msg.Height)
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case cacheLoadedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		m.cache = msg.Cache
		m.rebuildRows()
		return m, nil
	case regenStartedMsg:
		m.busy[msg.Language] = true
		m.statusMsg = la.LabelFor(msg.Language) + ": generating…"
		return m, nil
	case regenDoneMsg:
		delete(m.busy, msg.Language)
		if msg.Err != nil {
			m.err = fmt.Errorf("regenerate %s: %w", la.LabelFor(msg.Language), msg.Err)
			return m, nil
		}
		m.statusMsg = la.LabelFor(msg.Language) + ": generated"
		return m, loadCacheCmd()
	case deleteDoneMsg:
		if msg.Err != nil {
			m.err = fmt.Errorf("delete %s: %w", la.LabelFor(msg.Language), msg.Err)
			return m, nil
		}
		m.statusMsg = la.LabelFor(msg.Language) + ": deleted"
		return m, loadCacheCmd()
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.err = nil
	switch msg.String() {
	case "esc", "q":
		return m, state.NavigateTarget{Kind: state.NavBack, Cancelled: true}.Cmd()
	case "up", "k":
		if m.idx > 0 {
			m.idx--
		}
		return m, nil
	case "down", "j":
		if m.idx < len(m.rows)-1 {
			m.idx++
		}
		return m, nil
	case "g", "r":
		return m.actionGenerate()
	case "d", "delete":
		return m.actionDelete()
	}
	return m, nil
}

func (m *Model) actionGenerate() (tea.Model, tea.Cmd) {
	if m.idx < 0 || m.idx >= len(m.rows) {
		return m, nil
	}
	r := m.rows[m.idx]
	if m.busy[r.Language] {
		return m, nil
	}
	if m.complete == nil || m.aiCfg == nil {
		m.err = fmt.Errorf("generate %s: no LLM backend wired", la.LabelFor(r.Language))
		return m, nil
	}
	refLang, refBody := m.referenceBriefFor(r.Language)
	return m, tea.Batch(
		func() tea.Msg { return regenStartedMsg{Language: r.Language} },
		regenerateCmd(m.aiCfg, m.complete, r.Language, refLang, refBody),
	)
}

// referenceBriefFor picks an existing cached brief to pass to Generate
// as a shape reference. Returns ("", "") when no cached brief is
// suitable (first-run case or generating the only cached brief). We
// deliberately skip self-references (generating Go shouldn't seed off
// the existing Go brief — that biases regenerations toward themselves).
func (m *Model) referenceBriefFor(target la.Language) (la.Language, string) {
	if m.cache == nil {
		return "", ""
	}
	for _, l := range m.cache.SortedLanguages() {
		if l == target {
			continue
		}
		body := strings.TrimSpace(m.cache.ContextFor(l))
		if body != "" {
			return l, body
		}
	}
	return "", ""
}

func (m *Model) actionDelete() (tea.Model, tea.Cmd) {
	if m.idx < 0 || m.idx >= len(m.rows) {
		return m, nil
	}
	r := m.rows[m.idx]
	if m.busy[r.Language] {
		return m, nil
	}
	if m.cache == nil {
		return m, nil
	}
	if _, ok := m.cache.Get(r.Language); !ok {
		return m, nil
	}
	return m, deleteCmd(r.Language)
}

// View renders the tab.
func (m *Model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(m.title()))
	b.WriteString("\n")
	b.WriteString(hintStyle.Render(m.subtitle()))
	b.WriteString("\n\n")

	if len(m.rows) == 0 {
		b.WriteString(statusStyle.Render(m.emptyMessage()))
		b.WriteString("\n")
	} else {
		for i, r := range m.rows {
			b.WriteString(m.renderRow(r, i == m.idx))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	if m.statusMsg != "" {
		b.WriteString(statusStyle.Render(m.statusMsg))
		b.WriteString("\n")
	}
	if m.err != nil {
		b.WriteString(errStyle.Render(m.err.Error()))
		b.WriteString("\n")
	}
	b.WriteString(hintStyle.Render("↑/↓ select · g/r generate or refresh · d delete · esc close"))
	return b.String()
}

func (m *Model) title() string {
	if m.scope == nil {
		return "Language experts · cached"
	}
	if m.prLabel != "" {
		return "Language experts · " + m.prLabel
	}
	return "Language experts · PR scope"
}

func (m *Model) subtitle() string {
	if m.scope == nil {
		return "Briefs you have already generated. Open this tab from a PR to scope to that PR's languages and discover new ones."
	}
	return "Languages this PR touches. Generate or refresh a brief per language; only these are shown because the tab is scoped to the current PR."
}

func (m *Model) emptyMessage() string {
	if m.scope == nil {
		if m.cache == nil {
			return "Loading cached language briefs…"
		}
		return "No cached language briefs yet. Open a PR and press ctrl+l to scope the tab to that PR's languages, then press g to generate."
	}
	return "This PR doesn't touch any languages we recognise."
}

func (m *Model) renderRow(r row, selected bool) string {
	style := rowStyle
	if selected {
		style = rowSelectedStyle
	}
	label := la.LabelFor(r.Language)
	chip := m.chipForRow(r)
	meta := m.metaForRow(r)
	line := lipgloss.JoinHorizontal(lipgloss.Top,
		fmt.Sprintf("%-16s", label),
		chip,
		meta,
	)
	return style.Render(line)
}

func (m *Model) chipForRow(r row) string {
	if m.busy[r.Language] {
		return chipBusy.Render("[generating]")
	}
	freshness := la.ComputeLanguage(r.Language, m.cache, time.Now(), la.DefaultStaleAfter)
	switch freshness {
	case la.FreshnessFresh:
		return chipCached.Render("[generated]")
	case la.FreshnessStale:
		return chipStale.Render("[generated · stale]")
	case la.FreshnessMissing:
		return chipMissing.Render("[no brief]")
	}
	return statusStyle.Render("")
}

func (m *Model) metaForRow(r row) string {
	if m.cache == nil {
		return ""
	}
	a, ok := m.cache.Get(r.Language)
	if !ok {
		return ""
	}
	if a.GeneratedAt.IsZero() {
		return "  (timestamp missing)"
	}
	age := time.Since(a.GeneratedAt).Round(time.Hour)
	prov := strings.TrimSpace(a.Provider)
	if prov == "" {
		prov = "?"
	}
	model := strings.TrimSpace(a.Model)
	if model == "" {
		model = "?"
	}
	return fmt.Sprintf("  %s · %s · %s ago", prov, model, humanDuration(age))
}

// rebuildRows recomputes m.rows from the current scope and cache.
// Called from New (initial render) and from cacheLoadedMsg (after
// regenerate/delete refreshes the cache). Selection rules:
//
//   - Scoped (m.scope != nil): render exactly the scoped languages,
//     in the order they were provided. PR-touched languages without
//     a cached brief still appear so the user can generate one.
//   - Unscoped (m.scope == nil): render only languages that have a
//     cached brief. New languages are added by opening the tab from
//     a PR (which seeds scope).
//
// Preserves the selected index where possible; clamps to the new row
// range when the row count shrinks.
func (m *Model) rebuildRows() {
	prevLang := la.Language("")
	if m.idx >= 0 && m.idx < len(m.rows) {
		prevLang = m.rows[m.idx].Language
	}
	rows := make([]row, 0, 16)
	if m.scope != nil {
		for _, l := range m.scope {
			rows = append(rows, row{Language: l})
		}
	} else if m.cache != nil {
		for _, l := range m.cache.SortedLanguages() {
			rows = append(rows, row{Language: l})
		}
	}
	m.rows = rows
	m.idx = 0
	for i, r := range rows {
		if r.Language == prevLang {
			m.idx = i
			break
		}
	}
	if m.idx >= len(m.rows) {
		m.idx = len(m.rows) - 1
	}
	if m.idx < 0 {
		m.idx = 0
	}
}

// humanDuration renders d in a short human form (1d 4h, 3h, 12m).
// Hours-only resolution is fine for "language brief age" — we don't
// need minute precision.
func humanDuration(d time.Duration) string {
	if d < time.Hour {
		return "<1h"
	}
	hours := int(d / time.Hour)
	if hours < 24 {
		return fmt.Sprintf("%dh", hours)
	}
	days := hours / 24
	rem := hours - days*24
	if rem == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd %dh", days, rem)
}

// --- commands ---

func loadCacheCmd() tea.Cmd {
	return func() tea.Msg {
		cache, err := la.LoadCache()
		return cacheLoadedMsg{Cache: cache, Err: err}
	}
}

func regenerateCmd(cfg *aiconfig.Config, complete la.CompleteFunc, lang, refLang la.Language, refBody string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		a, err := la.Generate(ctx, la.GenerateOpts{
			AICfg:             cfg,
			Language:          lang,
			Complete:          complete,
			ReferenceLanguage: refLang,
			ReferenceBrief:    refBody,
		})
		if err != nil {
			return regenDoneMsg{Language: lang, Err: err}
		}
		if err := la.SaveAgent(*a); err != nil {
			return regenDoneMsg{Language: lang, Err: fmt.Errorf("save: %w", err)}
		}
		return regenDoneMsg{Language: lang, Agent: a}
	}
}

func deleteCmd(lang la.Language) tea.Cmd {
	return func() tea.Msg {
		err := la.DeleteAgent(lang)
		return deleteDoneMsg{Language: lang, Err: err}
	}
}
