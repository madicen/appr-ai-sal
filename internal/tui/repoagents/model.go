// Package repoagents is the TUI tab for managing per-repo, per-specialist
// "repo agents". Users can browse repos, regenerate agent briefs (calls the
// configured LLM), edit briefs by hand, or delete them.
//
// Persistence is delegated to internal/review/repoagents; this package owns
// only the UI layer (model, mouse zones, messages, styles).
package repoagents

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	ra "github.com/madicen/appr-ai-sal/internal/review/repoagents"
)

// Opts configures a fresh Model.
type Opts struct {
	AICfg        *aiconfig.Config
	RC           *repoconfig.Config
	Width        int
	BodyHeight   int
	Complete     ra.CompleteFunc       // required: typically review.Complete
	History      ra.HistoryFetcher     // optional: typically gh.BuildReviewHistoryDigest
	PathHistory  ra.PathHistoryFetcher // optional: feeds the testing/docs generator with path-history evidence
	InitialRepos []string              // owner/repo discovered from the loaded PR list (lowercased)
	// FocusRepo, when non-empty, selects this owner/repo at startup so the
	// tab opens on a specific repository (typically the one for the PR the
	// user is currently viewing). Falls back silently if the repo isn't in
	// InitialRepos / on disk.
	FocusRepo string
	// AutoRegenAll, when true, dispatches a "regenerate all" for FocusRepo
	// immediately after Init() so opening the tab from a "Build/refresh
	// repo agents" action goes straight from key press to running LLM jobs
	// without an extra click.
	AutoRegenAll bool
}

// Model is the repo-agents tab.
type Model struct {
	width    int
	bodyH    int
	contentW int

	aiCfg       *aiconfig.Config
	rc          *repoconfig.Config
	complete    ra.CompleteFunc
	history     ra.HistoryFetcher
	pathHistory ra.PathHistoryFetcher

	repos   []string // lowercased owner/repo
	repoIdx int

	// addingRepo true while the user is typing into the inline owner/repo input.
	addingRepo bool
	addInput   textinput.Model

	// per-repo cache of loaded RepoAgents.
	agents map[string]*ra.RepoAgents

	// busy["owner/repo|specialist"] true while a Regenerate command is in
	// flight; disables the row's chips and shows a spinner-style label.
	busy map[string]bool

	// editing state. When true, the right pane swaps to a textarea for the
	// selected (repoIdx, editSpecialist) brief.
	editing        bool
	editSpecialist string
	editArea       textarea.Model

	statusMsg string
	err       error

	// pendingAutoRegen is set from Opts.AutoRegenAll and consumed by Init so
	// opening the tab via "Build/refresh repo agents" immediately dispatches
	// regenerate-all for the focused repo.
	pendingAutoRegen string

	vp viewport.Model
}

var (
	closeKeys = key.NewBinding(key.WithKeys("esc"))
	saveKeys  = key.NewBinding(key.WithKeys("ctrl+s"))
)

// New builds a fresh repo-agents Model. Caller is responsible for sending
// Init() back into Bubble Tea so initial loads kick off.
func New(o Opts) *Model {
	if o.AICfg == nil {
		o.AICfg = aiconfig.DefaultConfig()
	}
	if o.RC == nil {
		o.RC = repoconfig.Default()
	}
	w := o.Width
	if w <= 0 {
		w = 80
	}
	bodyH := o.BodyHeight
	if bodyH <= 0 {
		bodyH = 20
	}

	ti := textinput.New()
	ti.Placeholder = "owner/repo"
	ti.CharLimit = 200
	ti.Width = max(20, w-12)

	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 65536
	ta.SetWidth(max(20, w-4))
	ta.SetHeight(min(20, max(6, bodyH/2)))
	ta.Blur()

	vp := viewport.New(max(1, w), bodyH)
	vp.MouseWheelEnabled = true

	m := &Model{
		width:       w,
		bodyH:       bodyH,
		contentW:    max(1, w),
		aiCfg:       o.AICfg,
		rc:          o.RC,
		complete:    o.Complete,
		history:     o.History,
		pathHistory: o.PathHistory,
		addInput:    ti,
		editArea:    ta,
		agents:      map[string]*ra.RepoAgents{},
		busy:        map[string]bool{},
		repos:       sanitizeRepos(o.InitialRepos),
		vp:          vp,
	}
	// Honour FocusRepo by adding it to the seed list (so it always shows up
	// even when no PRs in the list use it) and selecting it as the active
	// row. AutoRegenAll is consumed in Init below.
	if focus := normalizeRepoKey(o.FocusRepo); focus != "" {
		m.repos = sanitizeRepos(append(m.repos, focus))
		for i, r := range m.repos {
			if r == focus {
				m.repoIdx = i
				break
			}
		}
		if o.AutoRegenAll {
			m.pendingAutoRegen = focus
		}
	}
	return m
}

// normalizeRepoKey lowercases and trims an owner/repo string. Returns "" if
// the result is empty or doesn't contain "/".
func normalizeRepoKey(s string) string {
	k := strings.ToLower(strings.TrimSpace(s))
	if !strings.Contains(k, "/") {
		return ""
	}
	return k
}

func sanitizeRepos(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, r := range in {
		k := strings.ToLower(strings.TrimSpace(r))
		if k == "" || !strings.Contains(k, "/") {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Init kicks off the initial repo discovery + first repo's agent load.
//
// When Opts.AutoRegenAll was set with a FocusRepo we also dispatch a
// regenerate-all for that repo so opening the tab via "Build/refresh repo
// agents" runs the LLM jobs immediately — no extra click required.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{loadReposCmd(m.repos), m.maybeLoadAgentsCmd()}
	if m.pendingAutoRegen != "" && m.currentRepoKey() == m.pendingAutoRegen {
		if regen := m.regenerateAllForCurrentRepo(); regen != nil {
			cmds = append(cmds, regen)
			m.statusMsg = "regenerating all agents for " + m.pendingAutoRegen + " …"
		}
		m.pendingAutoRegen = ""
	}
	return tea.Batch(cmds...)
}

// SelectRepo sets the active repo in the tab. Returns true when the repo was
// found in the list (or could be added), false otherwise. Used by the root
// model to retarget an already-open tab when the user presses the "Build /
// refresh repo agents" chip on a different PR.
func (m *Model) SelectRepo(repoKey string) bool {
	k := normalizeRepoKey(repoKey)
	if k == "" {
		return false
	}
	for i, r := range m.repos {
		if r == k {
			m.repoIdx = i
			return true
		}
	}
	m.repos = sanitizeRepos(append(m.repos, k))
	for i, r := range m.repos {
		if r == k {
			m.repoIdx = i
			return true
		}
	}
	return false
}

// RegenerateAllForCurrentRepo is the public entry point for triggering a
// regenerate-all on the currently-selected repo (e.g. after SelectRepo).
// Returns nil when no repo is selected or the LLM completion isn't
// configured.
func (m *Model) RegenerateAllForCurrentRepo() tea.Cmd {
	cmd := m.regenerateAllForCurrentRepo()
	if cmd != nil {
		m.statusMsg = "regenerating all agents for " + m.currentRepoKey() + " …"
	}
	return cmd
}

// Resize updates layout for a new terminal size.
func (m *Model) Resize(width, bodyHeight int) {
	if width > 0 {
		m.width = width
	}
	if bodyHeight > 0 {
		m.bodyH = bodyHeight
	}
	m.contentW = max(1, m.width)
	m.addInput.Width = max(20, m.width-12)
	m.editArea.SetWidth(max(20, m.width-4))
	m.editArea.SetHeight(min(20, max(6, m.bodyH/2)))
	m.vp.Width = m.contentW
	m.vp.Height = m.bodyH
}

// View renders the scrollable body.
func (m *Model) View() string {
	m.vp.SetContent(m.buildContent())
	return lipgloss.NewStyle().Width(m.width).MaxWidth(m.width).Height(m.bodyH).Render(m.vp.View())
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		if cmd := m.handleMouse(msg); cmd != nil {
			return m, cmd
		}
		if tea.MouseEvent(msg).IsWheel() {
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}
		return m, nil

	case reposLoadedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		merged := append([]string{}, m.repos...)
		for _, r := range msg.Repos {
			merged = append(merged, r)
		}
		m.repos = sanitizeRepos(merged)
		if m.repoIdx >= len(m.repos) {
			m.repoIdx = 0
		}
		return m, m.maybeLoadAgentsCmd()

	case agentsLoadedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		m.agents[msg.Owner+"/"+msg.Repo] = msg.RA
		return m, nil

	case regenStartedMsg:
		m.busy[busyKey(msg.Owner, msg.Repo, msg.Specialist)] = true
		m.statusMsg = fmt.Sprintf("regenerating %s/%s · %s …", msg.Owner, msg.Repo, msg.Specialist)
		return m, nil

	case regenDoneMsg:
		delete(m.busy, busyKey(msg.Owner, msg.Repo, msg.Specialist))
		key := msg.Owner + "/" + msg.Repo
		if msg.Err != nil {
			m.err = fmt.Errorf("%s · %s: %w", key, msg.Specialist, msg.Err)
			m.statusMsg = ""
			return m, nil
		}
		if msg.Agent != nil {
			cur, ok := m.agents[key]
			if !ok || cur == nil {
				cur = &ra.RepoAgents{Owner: msg.Owner, Repo: msg.Repo, Agents: map[string]ra.Agent{}}
			}
			cur.Set(msg.Specialist, *msg.Agent)
			m.agents[key] = cur
		}
		m.statusMsg = fmt.Sprintf("saved %s/%s · %s (%s)", msg.Owner, msg.Repo, msg.Specialist, time.Now().Format("15:04:05"))
		return m, nil

	case deletedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		key := msg.Owner + "/" + msg.Repo
		if cur, ok := m.agents[key]; ok && cur != nil && cur.Agents != nil {
			delete(cur.Agents, strings.ToLower(strings.TrimSpace(msg.Specialist)))
		}
		m.statusMsg = fmt.Sprintf("deleted %s/%s · %s", msg.Owner, msg.Repo, msg.Specialist)
		return m, nil

	case savedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		m.editing = false
		m.editArea.Blur()
		m.statusMsg = fmt.Sprintf("saved %s/%s · %s (manual)", msg.Owner, msg.Repo, msg.Specialist)
		// Re-load agents from disk so we see the persisted entry.
		owner, repo := splitRepoKey(msg.Owner + "/" + msg.Repo)
		return m, loadAgentsCmd(owner, repo)

	case repoRemovedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		key := msg.Owner + "/" + msg.Repo
		delete(m.agents, key)
		out := make([]string, 0, len(m.repos))
		for _, r := range m.repos {
			if r != key {
				out = append(out, r)
			}
		}
		m.repos = out
		if m.repoIdx >= len(m.repos) {
			m.repoIdx = max(0, len(m.repos)-1)
		}
		m.statusMsg = "removed repo " + key
		return m, m.maybeLoadAgentsCmd()
	}

	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.editing {
		switch {
		case key.Matches(msg, saveKeys):
			return m, m.commitEdit()
		case msg.String() == "esc":
			m.editing = false
			m.editArea.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.editArea, cmd = m.editArea.Update(msg)
		return m, cmd
	}
	if m.addingRepo {
		switch msg.String() {
		case "enter":
			return m, m.commitAddRepo()
		case "esc":
			m.addingRepo = false
			m.addInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.addInput, cmd = m.addInput.Update(msg)
		return m, cmd
	}
	switch {
	case key.Matches(msg, closeKeys):
		return m, func() tea.Msg { return DoneMsg{Cancelled: true} }
	}
	switch msg.String() {
	case "left", "h":
		m.movePrevRepo()
		return m, m.maybeLoadAgentsCmd()
	case "right", "l":
		m.moveNextRepo()
		return m, m.maybeLoadAgentsCmd()
	case "a":
		m.openAddRepo()
		return m, textinput.Blink
	case "A":
		return m, m.regenerateAllForCurrentRepo()
	}
	return m, nil
}

func (m *Model) movePrevRepo() {
	if len(m.repos) == 0 {
		return
	}
	m.repoIdx = (m.repoIdx - 1 + len(m.repos)) % len(m.repos)
}

func (m *Model) moveNextRepo() {
	if len(m.repos) == 0 {
		return
	}
	m.repoIdx = (m.repoIdx + 1) % len(m.repos)
}

func (m *Model) openAddRepo() {
	m.addingRepo = true
	m.addInput.SetValue("")
	m.addInput.Focus()
}

func (m *Model) commitAddRepo() tea.Cmd {
	v := strings.TrimSpace(strings.ToLower(m.addInput.Value()))
	m.addingRepo = false
	m.addInput.Blur()
	if v == "" || !strings.Contains(v, "/") {
		m.err = fmt.Errorf("invalid owner/repo %q", v)
		return nil
	}
	for i, r := range m.repos {
		if r == v {
			m.repoIdx = i
			m.statusMsg = "selected " + v
			return m.maybeLoadAgentsCmd()
		}
	}
	m.repos = sanitizeRepos(append(m.repos, v))
	for i, r := range m.repos {
		if r == v {
			m.repoIdx = i
			break
		}
	}
	m.statusMsg = "added " + v
	return m.maybeLoadAgentsCmd()
}

func (m *Model) commitEdit() tea.Cmd {
	body := strings.TrimRight(m.editArea.Value(), "\n")
	if strings.TrimSpace(body) == "" {
		m.err = fmt.Errorf("cannot save empty brief; use Delete instead")
		return nil
	}
	owner, repo := splitRepoKey(m.currentRepoKey())
	if owner == "" || repo == "" {
		m.err = fmt.Errorf("no repo selected")
		return nil
	}
	spec := m.editSpecialist
	return saveManualCmd(owner, repo, spec, body)
}

func (m *Model) currentRepoKey() string {
	if m.repoIdx < 0 || m.repoIdx >= len(m.repos) {
		return ""
	}
	return m.repos[m.repoIdx]
}

func (m *Model) currentAgents() *ra.RepoAgents {
	k := m.currentRepoKey()
	if k == "" {
		return nil
	}
	return m.agents[k]
}

func (m *Model) maybeLoadAgentsCmd() tea.Cmd {
	k := m.currentRepoKey()
	if k == "" {
		return nil
	}
	if _, ok := m.agents[k]; ok {
		return nil
	}
	owner, repo := splitRepoKey(k)
	return loadAgentsCmd(owner, repo)
}

func splitRepoKey(k string) (owner, repo string) {
	parts := strings.SplitN(strings.TrimSpace(k), "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

func busyKey(owner, repo, specialist string) string {
	return owner + "/" + repo + "|" + strings.ToLower(strings.TrimSpace(specialist))
}

func (m *Model) startRegenerate(specialist string) tea.Cmd {
	owner, repo := splitRepoKey(m.currentRepoKey())
	if owner == "" || repo == "" {
		m.err = fmt.Errorf("no repo selected")
		return nil
	}
	if !ra.IsKnownSpecialist(specialist) {
		m.err = fmt.Errorf("unknown specialist %q", specialist)
		return nil
	}
	if m.complete == nil {
		m.err = fmt.Errorf("LLM completion is not configured")
		return nil
	}
	m.busy[busyKey(owner, repo, specialist)] = true
	m.err = nil
	return tea.Batch(
		func() tea.Msg { return regenStartedMsg{Owner: owner, Repo: repo, Specialist: specialist} },
		regenerateCmd(m.complete, m.history, m.pathHistory, m.aiCfg, m.rc, owner, repo, specialist),
	)
}

func (m *Model) regenerateAllForCurrentRepo() tea.Cmd {
	owner, repo := splitRepoKey(m.currentRepoKey())
	if owner == "" || repo == "" {
		return nil
	}
	if m.complete == nil {
		m.err = fmt.Errorf("LLM completion is not configured")
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(ra.Specialists)*2)
	for _, s := range ra.Specialists {
		spec := s
		m.busy[busyKey(owner, repo, spec)] = true
		cmds = append(cmds,
			func() tea.Msg { return regenStartedMsg{Owner: owner, Repo: repo, Specialist: spec} },
			regenerateCmd(m.complete, m.history, m.pathHistory, m.aiCfg, m.rc, owner, repo, spec),
		)
	}
	return tea.Batch(cmds...)
}

func (m *Model) startEdit(specialist string) {
	owner, repo := splitRepoKey(m.currentRepoKey())
	if owner == "" || repo == "" {
		return
	}
	cur := m.currentAgents()
	body := ""
	if cur != nil {
		body = cur.ContextFor(specialist)
	}
	m.editing = true
	m.editSpecialist = specialist
	m.editArea.SetValue(body)
	m.editArea.Focus()
	_ = owner
	_ = repo
}

func (m *Model) startDelete(specialist string) tea.Cmd {
	owner, repo := splitRepoKey(m.currentRepoKey())
	if owner == "" || repo == "" {
		return nil
	}
	return deleteAgentCmd(owner, repo, specialist)
}

// Init helpers ───────────────────────────────────────────────────────────────

// loadReposCmd discovers all owner/repo keys present in the on-disk
// repo-profiles directory and merges them with the seed list.
func loadReposCmd(seed []string) tea.Cmd {
	return func() tea.Msg {
		disk, err := ra.ListRepos()
		if err != nil {
			return reposLoadedMsg{Repos: seed, Err: err}
		}
		merged := append([]string{}, seed...)
		merged = append(merged, disk...)
		return reposLoadedMsg{Repos: merged}
	}
}

func loadAgentsCmd(owner, repo string) tea.Cmd {
	return func() tea.Msg {
		got, err := ra.Load(owner, repo)
		return agentsLoadedMsg{Owner: owner, Repo: repo, RA: got, Err: err}
	}
}

func regenerateCmd(complete ra.CompleteFunc, history ra.HistoryFetcher, pathHistory ra.PathHistoryFetcher, aiCfg *aiconfig.Config, rc *repoconfig.Config, owner, repo, specialist string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		agent, err := ra.Generate(ctx, ra.GenerateOpts{
			AICfg:       aiCfg,
			RC:          rc,
			Owner:       owner,
			Repo:        repo,
			Specialist:  specialist,
			Complete:    complete,
			History:     history,
			PathHistory: pathHistory,
		})
		if err != nil {
			return regenDoneMsg{Owner: owner, Repo: repo, Specialist: specialist, Err: err}
		}
		if err := ra.SaveAgent(owner, repo, *agent); err != nil {
			return regenDoneMsg{Owner: owner, Repo: repo, Specialist: specialist, Err: fmt.Errorf("save: %w", err)}
		}
		return regenDoneMsg{Owner: owner, Repo: repo, Specialist: specialist, Agent: agent}
	}
}

func deleteAgentCmd(owner, repo, specialist string) tea.Cmd {
	return func() tea.Msg {
		err := ra.Delete(owner, repo, specialist)
		return deletedMsg{Owner: owner, Repo: repo, Specialist: specialist, Err: err}
	}
}

func saveManualCmd(owner, repo, specialist, body string) tea.Cmd {
	return func() tea.Msg {
		agent := ra.Agent{
			Specialist:  specialist,
			Context:     body,
			GeneratedAt: time.Now().UTC(),
			Manual:      true,
		}
		if err := ra.SaveAgent(owner, repo, agent); err != nil {
			return savedMsg{Owner: owner, Repo: repo, Specialist: specialist, Err: err}
		}
		return savedMsg{Owner: owner, Repo: repo, Specialist: specialist}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// View ──────────────────────────────────────────────────────────────────────

func (m *Model) buildContent() string {
	var b strings.Builder
	b.WriteString(boldStyle.Render("Repo Agents") + "  ")
	b.WriteString(dimStyle.Render("· per-specialist briefs that get injected into reviews for this repo") + "\n\n")

	b.WriteString(m.renderRepoSelector())
	b.WriteString("\n")
	b.WriteString(m.renderAgentList())
	b.WriteString("\n")
	b.WriteString(m.renderFooter())

	if m.statusMsg != "" {
		b.WriteString("\n\n")
		b.WriteString(dimStyle.Render(m.statusMsg))
	}
	if m.err != nil {
		b.WriteString("\n\n")
		b.WriteString(errStyle.Render("error: " + m.err.Error()))
	}
	return b.String()
}

func (m *Model) renderRepoSelector() string {
	var b strings.Builder
	b.WriteString(boldStyle.Render("Repository") + "\n")
	if len(m.repos) == 0 {
		b.WriteString(dimStyle.Render("no repos yet — paste an owner/repo to start"))
		b.WriteString("\n")
	} else {
		cur := m.currentRepoKey()
		nav := zone.Mark(ZonePrevRepo, chipStyle.Render(" ← prev ")) + " " +
			boldStyle.Render(cur) + " " +
			dimStyle.Render(fmt.Sprintf("(%d/%d)", m.repoIdx+1, len(m.repos))) + " " +
			zone.Mark(ZoneNextRepo, chipStyle.Render(" next → "))
		b.WriteString(nav + "\n")
	}
	if m.addingRepo {
		b.WriteString(m.addInput.View())
		b.WriteString("  ")
		b.WriteString(zone.Mark(ZoneAddRepoSave, okStyle.Render(" Add ")))
		b.WriteString("  ")
		b.WriteString(zone.Mark(ZoneAddRepoCancel, errStyle.Render(" Cancel ")))
	} else {
		b.WriteString(zone.Mark(ZoneAddRepoOpen, chipStyle.Render(" + Add repo ")))
		if m.currentRepoKey() != "" {
			b.WriteString("  ")
			b.WriteString(zone.Mark(ZoneRemoveRepo, chipDanger.Render(" Remove repo ")))
		}
	}
	b.WriteString("\n")
	return b.String()
}

func (m *Model) renderAgentList() string {
	if m.editing {
		return m.renderEditPane()
	}

	var b strings.Builder
	b.WriteString(boldStyle.Render("Agents") + "  ")
	b.WriteString(dimStyle.Render("· one brief per specialist") + "\n\n")

	cur := m.currentAgents()
	if m.currentRepoKey() == "" {
		b.WriteString(dimStyle.Render("Select a repo above to manage its agents."))
		b.WriteString("\n")
		return b.String()
	}

	for _, name := range ra.Specialists {
		b.WriteString(m.renderAgentRow(name, cur))
		b.WriteString("\n")
	}
	return b.String()
}

func (m *Model) renderAgentRow(specialist string, cur *ra.RepoAgents) string {
	rule := sectionRule.Render(strings.Repeat("─", max(8, m.contentW-2)))
	var b strings.Builder
	b.WriteString(rule + "\n")
	owner, repo := splitRepoKey(m.currentRepoKey())
	bk := busyKey(owner, repo, specialist)

	header := boldStyle.Render(specialist)
	status := dimStyle.Render("missing")
	preview := ""
	if cur != nil {
		if a, ok := cur.Get(specialist); ok {
			when := ""
			if !a.GeneratedAt.IsZero() {
				when = " · " + a.GeneratedAt.Local().Format("2006-01-02 15:04")
			}
			labels := []string{}
			if a.Manual {
				labels = append(labels, warnStyle.Render("manual"))
			} else {
				labels = append(labels, okStyle.Render("generated"))
			}
			if a.Provider != "" {
				labels = append(labels, dimStyle.Render(a.Provider))
			}
			if a.Model != "" {
				labels = append(labels, dimStyle.Render(a.Model))
			}
			status = strings.Join(labels, " · ") + dimStyle.Render(when)
			preview = trimPreview(a.Context, 320)
		}
	}
	if m.busy[bk] {
		status = chipBusy.Render(" regenerating … ")
	}

	b.WriteString(zone.Mark(zoneAgentRow(specialist), header+"  "+status))
	b.WriteString("\n")
	if preview != "" {
		b.WriteString(dimStyle.Render(preview))
		b.WriteString("\n")
	}
	chips := []string{}
	regenLabel := " Regenerate "
	if m.busy[bk] {
		regenLabel = " (running) "
		chips = append(chips, chipBusy.Render(regenLabel))
	} else {
		chips = append(chips, zone.Mark(zoneAgentRegen(specialist), chipPrimary.Render(regenLabel)))
	}
	chips = append(chips, zone.Mark(zoneAgentEdit(specialist), chipStyle.Render(" Edit ")))
	if cur != nil {
		if _, ok := cur.Get(specialist); ok {
			chips = append(chips, zone.Mark(zoneAgentDelete(specialist), chipDanger.Render(" Delete ")))
		}
	}
	b.WriteString(strings.Join(chips, "  "))
	b.WriteString("\n")
	return b.String()
}

func (m *Model) renderEditPane() string {
	var b strings.Builder
	b.WriteString(boldStyle.Render("Editing brief: " + m.editSpecialist))
	b.WriteString("  ")
	b.WriteString(dimStyle.Render("· ctrl+s save · esc cancel"))
	b.WriteString("\n\n")
	b.WriteString(m.editArea.View())
	b.WriteString("\n\n")
	b.WriteString(zone.Mark(ZoneEditSave, okStyle.Render(" Save ")))
	b.WriteString("  ")
	b.WriteString(zone.Mark(ZoneEditCancel, errStyle.Render(" Cancel ")))
	b.WriteString("\n")
	return b.String()
}

func (m *Model) renderFooter() string {
	var b strings.Builder
	if !m.editing && m.currentRepoKey() != "" {
		b.WriteString(zone.Mark(ZoneRegenAll, chipPrimary.Render(" Regenerate all ")))
		b.WriteString("  ")
	}
	b.WriteString(zone.Mark(ZoneClose, chipStyle.Render(" Close ")))
	return b.String()
}

func trimPreview(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
