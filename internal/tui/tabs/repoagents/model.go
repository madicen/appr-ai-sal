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
	bubbledropdown "github.com/madicen/bubble-dropdown"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	ra "github.com/madicen/appr-ai-sal/internal/review/repoagents"
	ta "github.com/madicen/appr-ai-sal/internal/review/techagents"
	"github.com/madicen/appr-ai-sal/internal/tui/state"
	"github.com/madicen/appr-ai-sal/internal/tui/util/async"
	"github.com/madicen/appr-ai-sal/internal/tui/util/dropdown"
)

// Opts configures a fresh Model.
type Opts struct {
	AICfg        *aiconfig.Config
	RC           *repoconfig.Config
	Width        int
	BodyHeight   int
	Complete     ai.CompleteFunc       // required: typically review.Complete
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

// editKind discriminates between editing a per-specialist repo-agent brief
// and a per-technology tech-expert brief in the shared textarea.
type editKind int

const (
	editKindNone editKind = iota
	editKindSpecialist
	editKindTech
)

// Model is the repo-agents tab.
type Model struct {
	width    int
	bodyH    int
	contentW int

	aiCfg       *aiconfig.Config
	rc          *repoconfig.Config
	complete    ai.CompleteFunc
	history     ra.HistoryFetcher
	pathHistory ra.PathHistoryFetcher

	repos   []string // lowercased owner/repo
	repoIdx int

	// Active-repository dropdown, via the shared dropdown.Host (recreated
	// when the repo list or selection changes; the component has no runtime
	// SetOptions).
	repoDD *dropdown.Host
	// ddRepoRow is the content-line index of the dropdown trigger, recorded
	// while buildContent runs so View can apply scroll-adjusted bounds.
	ddRepoRow int
	// contentTop is the absolute terminal row where the tab body begins
	// (chrome header height); mouse events are translated by it before
	// reaching an open dropdown's geometric hit-test.
	contentTop int

	// addingRepo true while the user is typing into the inline owner/repo input.
	addingRepo bool
	addInput   textinput.Model

	// per-repo cache of loaded RepoAgents.
	agents map[string]*ra.RepoAgents

	// per-repo cache of loaded TechAgents (per-repo "technology expert"
	// briefs). Lazy-loaded the first time a repo is selected.
	techs map[string]*ta.TechAgents

	// busy tracks the "owner/repo|specialist" (and "tech:"-prefixed) keys
	// whose Regenerate command is in flight; a running key disables the
	// row's chips and shows a spinner-style label. The shared async.Tracker
	// keeps the gating uniform across both kinds of agent.
	busy async.Tracker[string]

	// editing state. When true, the right pane swaps to a textarea for the
	// selected brief. editKind discriminates between repo-agent specialist
	// briefs and tech-expert briefs (textarea is shared since only one edit
	// can be in flight at a time).
	editing        bool
	editKind       editKind
	editSpecialist string
	editTech       string
	editArea       textarea.Model

	// addingTech true while the user is filling in the new-tech form.
	// techNameInput is the canonical/display name (e.g. "Kestra"); techSeedInput
	// is the short user-supplied description that primes the LLM.
	addingTech    bool
	techNameInput textinput.Model
	techSeedInput textinput.Model
	// techSeedFocus toggles which of the two add-tech inputs has focus
	// (false = name, true = seed). Tab cycles between them.
	techSeedFocus bool

	// Suggest-technologies flow. suggestBusy is true while the suggester
	// LLM call is in flight; candidates holds the proposed technologies for
	// the current repo (cleared when the repo changes); candidateApproved
	// tracks the user's per-candidate decision keyed by canonical tech
	// (true = approved, absent/false = not approved). Generating approved
	// candidates reuses the per-tech regenerate pipeline.
	suggestBusy       bool
	candidates        []ta.Candidate
	candidateApproved map[string]bool

	statusMsg string
	err       error

	// pendingAutoRegen is set from Opts.AutoRegenAll and consumed by Init so
	// opening the tab via "Build/refresh repo agents" immediately dispatches
	// regenerate-all for the focused repo.
	pendingAutoRegen string

	// focusRepo is the lowercased owner/repo the tab was asked to land on
	// when opened (Opts.FocusRepo). We persist it past New so the async
	// reposLoadedMsg handler can re-apply the selection after merging /
	// re-sorting the repo list — without it, repoIdx is a stale numeric
	// pointer into a list whose alphabetical order may have shifted, and
	// the user ends up on whatever happens to land at that index (often
	// the alphabetically first repo). Cleared after the first apply so
	// later manual SelectRepo calls aren't second-guessed by reloads.
	focusRepo string

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

	techNameTI := textinput.New()
	techNameTI.Placeholder = "kestra"
	techNameTI.CharLimit = 80
	techNameTI.Width = max(20, w-12)

	techSeedTI := textinput.New()
	techSeedTI.Placeholder = "Kestra workflow engine; YAML-based; plugin model"
	techSeedTI.CharLimit = 400
	techSeedTI.Width = max(20, w-12)

	taArea := textarea.New()
	taArea.ShowLineNumbers = false
	taArea.Prompt = ""
	taArea.CharLimit = 65536
	taArea.SetWidth(max(20, w-4))
	taArea.SetHeight(min(20, max(6, bodyH/2)))
	taArea.Blur()

	vp := viewport.New(max(1, w), bodyH)
	vp.MouseWheelEnabled = true

	m := &Model{
		width:             w,
		bodyH:             bodyH,
		contentW:          max(1, w),
		aiCfg:             o.AICfg,
		rc:                o.RC,
		complete:          o.Complete,
		history:           o.History,
		pathHistory:       o.PathHistory,
		addInput:          ti,
		techNameInput:     techNameTI,
		techSeedInput:     techSeedTI,
		editArea:          taArea,
		agents:            map[string]*ra.RepoAgents{},
		techs:             map[string]*ta.TechAgents{},
		candidateApproved: map[string]bool{},
		repos:             sanitizeRepos(o.InitialRepos),
		vp:                vp,
	}
	// Honour FocusRepo by adding it to the seed list (so it always shows up
	// even when no PRs in the list use it) and selecting it as the active
	// row. AutoRegenAll is consumed in Init below. We also stash the key on
	// the model so reposLoadedMsg can re-apply the selection after the
	// async disk merge resorts the list (otherwise repoIdx points at a
	// different repo after the sort).
	if focus := normalizeRepoKey(o.FocusRepo); focus != "" {
		m.repos = sanitizeRepos(append(m.repos, focus))
		m.focusRepo = focus
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

// CurrentRepoKey returns the lowercased owner/repo currently selected in the
// tab, or "" when the tab has no repos. Public so callers (and tests) can
// observe which repo the tab landed on after opening — useful for asserting
// that FocusRepo survived the async reposLoadedMsg merge.
func (m *Model) CurrentRepoKey() string {
	return m.currentRepoKey()
}

// Status returns the user-visible status line (e.g. "regenerating all
// agents for owner/repo …"). Tests use this to confirm whether opening
// the tab via AutoRegenAll triggered a build vs. landed in pure
// navigation mode (empty status).
func (m *Model) Status() string {
	return m.statusMsg
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
			if m.repoIdx != i {
				m.resetSuggestions()
			}
			m.repoIdx = i
			return true
		}
	}
	m.repos = sanitizeRepos(append(m.repos, k))
	for i, r := range m.repos {
		if r == k {
			m.resetSuggestions()
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
	m.techNameInput.Width = max(20, m.width-12)
	m.techSeedInput.Width = max(20, m.width-12)
	m.editArea.SetWidth(max(20, m.width-4))
	m.editArea.SetHeight(min(20, max(6, m.bodyH/2)))
	m.vp.Width = m.contentW
	m.vp.Height = m.bodyH
}

// View renders the tab. Scrollable content lives in the viewport; the
// footer (Close / Regenerate all chips) is rendered as a sticky line
// outside the viewport so it stays clickable no matter how long the
// agent + tech list grows.
func (m *Model) View() string {
	footer := m.renderFooter()
	footerH := lipgloss.Height(footer)
	vpH := max(1, m.bodyH-footerH-1)
	m.vp.Height = vpH
	// Keep the repo dropdown's options/selection fresh (no-op while open).
	m.refreshRepoDropdown()
	m.vp.SetContent(m.buildContent())
	body := m.vp.View()
	// Composite the panel onto the scrollable body only; clamping to the
	// viewport height keeps it from bleeding into the sticky footer. The
	// trigger row is scroll-adjusted (content line index minus YOffset).
	body = m.repoDD.Composite(body, m.ddRepoRow-m.vp.YOffset, 0, m.width, vpH)
	return lipgloss.NewStyle().
		Width(m.width).
		MaxWidth(m.width).
		Height(m.bodyH).
		Render(lipgloss.JoinVertical(lipgloss.Left, body, "", footer))
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Dropdown result messages (emitted as commands on a later tick) close
	// the open dropdown and apply the choice.
	switch msg.(type) {
	case bubbledropdown.ItemChosenMsg, bubbledropdown.ItemCanceledMsg:
		if m.repoDropdownOpen() {
			return m, m.forwardToRepoDropdown(msg)
		}
		return m, nil
	}
	// When the repo dropdown panel is open, route all key/mouse input to it.
	if m.repoDropdownOpen() {
		switch msg.(type) {
		case tea.KeyMsg, tea.MouseMsg:
			return m, m.forwardToRepoDropdown(msg)
		}
	}

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
		// Re-apply the initial focus, if any. sanitizeRepos re-sorts the
		// merged list alphabetically, so the numeric repoIdx that was
		// good at New time often points at a different repo after the
		// disk merge. Re-finding by key restores the caller's intent
		// (e.g. "open the tab on the current PR's repo"). We only do
		// this once — subsequent reposLoadedMsg events (e.g. from a
		// user-initiated add) should respect whatever the user is
		// currently looking at.
		if m.focusRepo != "" {
			for i, r := range m.repos {
				if r == m.focusRepo {
					m.repoIdx = i
					break
				}
			}
			m.focusRepo = ""
		}
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
		m.busy.Start(busyKey(msg.Key.Owner, msg.Key.Repo, msg.Key.Specialist))
		m.statusMsg = fmt.Sprintf("regenerating %s/%s · %s …", msg.Key.Owner, msg.Key.Repo, msg.Key.Specialist)
		return m, nil

	case regenDoneMsg:
		m.busy.Clear(busyKey(msg.Key.Owner, msg.Key.Repo, msg.Key.Specialist))
		key := msg.Key.Owner + "/" + msg.Key.Repo
		if msg.Err != nil {
			m.err = fmt.Errorf("%s · %s: %w", key, msg.Key.Specialist, msg.Err)
			m.statusMsg = ""
			return m, nil
		}
		if msg.Val != nil {
			cur, ok := m.agents[key]
			if !ok || cur == nil {
				cur = &ra.RepoAgents{Owner: msg.Key.Owner, Repo: msg.Key.Repo, Agents: map[string]ra.Agent{}}
			}
			cur.Set(msg.Key.Specialist, *msg.Val)
			m.agents[key] = cur
		}
		m.statusMsg = fmt.Sprintf("saved %s/%s · %s (%s)", msg.Key.Owner, msg.Key.Repo, msg.Key.Specialist, time.Now().Format("15:04:05"))
		return m, nil

	case deletedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		key := msg.Key.Owner + "/" + msg.Key.Repo
		if cur, ok := m.agents[key]; ok && cur != nil && cur.Agents != nil {
			delete(cur.Agents, strings.ToLower(strings.TrimSpace(msg.Key.Specialist)))
		}
		m.statusMsg = fmt.Sprintf("deleted %s/%s · %s", msg.Key.Owner, msg.Key.Repo, msg.Key.Specialist)
		return m, nil

	case savedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		m.editing = false
		m.editKind = editKindNone
		m.editArea.Blur()
		m.statusMsg = fmt.Sprintf("saved %s/%s · %s (manual)", msg.Key.Owner, msg.Key.Repo, msg.Key.Specialist)
		// Re-load agents from disk so we see the persisted entry.
		return m, loadAgentsCmd(msg.Key.Owner, msg.Key.Repo)

	case repoRemovedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		key := msg.Key.Owner + "/" + msg.Key.Repo
		delete(m.agents, key)
		delete(m.techs, key)
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

	case techsLoadedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		m.techs[msg.Owner+"/"+msg.Repo] = msg.TA
		return m, nil

	case techRegenStartedMsg:
		m.busy.Start(techBusyKey(msg.Key.Owner, msg.Key.Repo, msg.Key.Tech))
		m.statusMsg = fmt.Sprintf("regenerating %s/%s · tech %s …", msg.Key.Owner, msg.Key.Repo, msg.Key.Tech)
		return m, nil

	case techRegenDoneMsg:
		m.busy.Clear(techBusyKey(msg.Key.Owner, msg.Key.Repo, msg.Key.Tech))
		key := msg.Key.Owner + "/" + msg.Key.Repo
		if msg.Err != nil {
			m.err = fmt.Errorf("%s · tech %s: %w", key, msg.Key.Tech, msg.Err)
			m.statusMsg = ""
			return m, nil
		}
		if msg.Val != nil {
			cur, ok := m.techs[key]
			if !ok || cur == nil {
				cur = &ta.TechAgents{Owner: msg.Key.Owner, Repo: msg.Key.Repo, Agents: map[string]ta.Agent{}}
			}
			cur.Set(msg.Val.Tech, *msg.Val)
			m.techs[key] = cur
		}
		m.statusMsg = fmt.Sprintf("saved %s/%s · tech %s (%s)", msg.Key.Owner, msg.Key.Repo, msg.Key.Tech, time.Now().Format("15:04:05"))
		return m, nil

	case techDeletedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		key := msg.Key.Owner + "/" + msg.Key.Repo
		if cur, ok := m.techs[key]; ok && cur != nil {
			cur.Delete(msg.Key.Tech)
		}
		m.statusMsg = fmt.Sprintf("deleted %s/%s · tech %s", msg.Key.Owner, msg.Key.Repo, msg.Key.Tech)
		return m, nil

	case techSavedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		m.editing = false
		m.editKind = editKindNone
		m.editArea.Blur()
		m.statusMsg = fmt.Sprintf("saved %s/%s · tech %s (manual)", msg.Key.Owner, msg.Key.Repo, msg.Key.Tech)
		return m, loadTechsCmd(msg.Key.Owner, msg.Key.Repo)

	case techSuggestStartedMsg:
		m.suggestBusy = true
		m.statusMsg = fmt.Sprintf("analyzing %s/%s for technologies …", msg.Key.Owner, msg.Key.Repo)
		return m, nil

	case techSuggestDoneMsg:
		m.suggestBusy = false
		// Ignore results that arrived after the user moved to another repo.
		if msg.Key.Owner+"/"+msg.Key.Repo != m.currentRepoKey() {
			return m, nil
		}
		if msg.Err != nil {
			m.err = msg.Err
			m.statusMsg = ""
			return m, nil
		}
		m.candidates = msg.Val
		m.candidateApproved = map[string]bool{}
		if len(msg.Val) == 0 {
			m.statusMsg = fmt.Sprintf("no new technologies suggested for %s/%s", msg.Key.Owner, msg.Key.Repo)
		} else {
			m.statusMsg = fmt.Sprintf("suggested %d technologies for %s/%s — approve the ones to generate", len(msg.Val), msg.Key.Owner, msg.Key.Repo)
		}
		return m, nil
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
			m.editKind = editKindNone
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
	if m.addingTech {
		switch msg.String() {
		case "enter":
			return m, m.commitAddTech()
		case "esc":
			m.cancelAddTech()
			return m, nil
		case "tab", "shift+tab":
			if m.techSeedFocus {
				m.focusTechName()
			} else {
				m.focusTechSeed()
			}
			return m, textinput.Blink
		}
		var cmd tea.Cmd
		if m.techSeedFocus {
			m.techSeedInput, cmd = m.techSeedInput.Update(msg)
		} else {
			m.techNameInput, cmd = m.techNameInput.Update(msg)
		}
		return m, cmd
	}
	switch {
	case key.Matches(msg, closeKeys):
		return m, state.NavigateTarget{Kind: state.NavBack, Cancelled: true}.Cmd()
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
	case "t":
		m.openAddTech()
		return m, textinput.Blink
	case "s":
		return m, m.startSuggestTechs()
	case "g":
		if len(m.candidates) > 0 {
			return m, m.generateApprovedCmd()
		}
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
	m.resetSuggestions()
}

func (m *Model) moveNextRepo() {
	if len(m.repos) == 0 {
		return
	}
	m.repoIdx = (m.repoIdx + 1) % len(m.repos)
	m.resetSuggestions()
}

// resetSuggestions clears any in-progress suggestion state. Candidates are
// repo-specific, so switching repos must drop a stale list.
func (m *Model) resetSuggestions() {
	m.suggestBusy = false
	m.candidates = nil
	m.candidateApproved = map[string]bool{}
}

func (m *Model) openAddRepo() {
	m.addingRepo = true
	m.addInput.SetValue("")
	m.addInput.Focus()
}

func (m *Model) openAddTech() {
	m.addingTech = true
	m.techNameInput.SetValue("")
	m.techSeedInput.SetValue("")
	m.techSeedFocus = false
	m.techNameInput.Focus()
	m.techSeedInput.Blur()
}

func (m *Model) cancelAddTech() {
	m.addingTech = false
	m.techNameInput.Blur()
	m.techSeedInput.Blur()
}

// focusTechName / focusTechSeed move focus between the two add-tech
// fields. Shared by the tab key and the click-to-focus zones.
func (m *Model) focusTechName() {
	m.techSeedFocus = false
	m.techSeedInput.Blur()
	m.techNameInput.Focus()
}

func (m *Model) focusTechSeed() {
	m.techSeedFocus = true
	m.techNameInput.Blur()
	m.techSeedInput.Focus()
}

func (m *Model) commitAddTech() tea.Cmd {
	owner, repo := splitRepoKey(m.currentRepoKey())
	if owner == "" || repo == "" {
		m.err = fmt.Errorf("no repo selected")
		return nil
	}
	rawName := strings.TrimSpace(m.techNameInput.Value())
	seed := strings.TrimSpace(m.techSeedInput.Value())
	if rawName == "" {
		m.err = fmt.Errorf("tech name is required")
		return nil
	}
	canonical := ta.CanonicalTech(rawName)
	if canonical == "" {
		m.err = fmt.Errorf("invalid tech name %q (use letters/numbers)", rawName)
		return nil
	}
	if m.complete == nil {
		m.err = fmt.Errorf("LLM completion is not configured")
		return nil
	}
	m.cancelAddTech()
	m.busy.Start(techBusyKey(owner, repo, canonical))
	m.err = nil
	m.statusMsg = fmt.Sprintf("generating %s/%s · tech %s …", owner, repo, canonical)
	return tea.Batch(
		func() tea.Msg { return techRegenStartedMsg{Key: techKey{owner, repo, canonical}} },
		regenerateTechCmd(m.complete, nil, m.aiCfg, m.rc, owner, repo, canonical, rawName, seed),
	)
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
	switch m.editKind {
	case editKindTech:
		return saveManualTechCmd(owner, repo, m.editTech, body)
	default:
		return saveManualCmd(owner, repo, m.editSpecialist, body)
	}
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

func (m *Model) currentTechs() *ta.TechAgents {
	k := m.currentRepoKey()
	if k == "" {
		return nil
	}
	return m.techs[k]
}

func (m *Model) maybeLoadAgentsCmd() tea.Cmd {
	k := m.currentRepoKey()
	if k == "" {
		return nil
	}
	owner, repo := splitRepoKey(k)
	cmds := []tea.Cmd{}
	if _, ok := m.agents[k]; !ok {
		cmds = append(cmds, loadAgentsCmd(owner, repo))
	}
	if _, ok := m.techs[k]; !ok {
		cmds = append(cmds, loadTechsCmd(owner, repo))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
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
	m.busy.Start(busyKey(owner, repo, specialist))
	m.err = nil
	return tea.Batch(
		func() tea.Msg { return regenStartedMsg{Key: specKey{owner, repo, specialist}} },
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
		m.busy.Start(busyKey(owner, repo, spec))
		cmds = append(cmds,
			func() tea.Msg { return regenStartedMsg{Key: specKey{owner, repo, spec}} },
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
	m.editKind = editKindSpecialist
	m.editSpecialist = specialist
	m.editTech = ""
	m.editArea.SetValue(body)
	m.editArea.Focus()
	_ = owner
	_ = repo
}

// startEditTech opens the brief editor for a per-tech expert. Reuses the
// same textarea as the per-specialist editor — only one edit can be in
// flight at a time.
func (m *Model) startEditTech(tech string) {
	owner, repo := splitRepoKey(m.currentRepoKey())
	if owner == "" || repo == "" {
		return
	}
	cur := m.currentTechs()
	body := ""
	if cur != nil {
		body = cur.ContextFor(tech)
	}
	m.editing = true
	m.editKind = editKindTech
	m.editSpecialist = ""
	m.editTech = tech
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

func regenerateCmd(complete ai.CompleteFunc, history ra.HistoryFetcher, pathHistory ra.PathHistoryFetcher, aiCfg *aiconfig.Config, rc *repoconfig.Config, owner, repo, specialist string) tea.Cmd {
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
			return regenDoneMsg{Key: specKey{owner, repo, specialist}, Err: err}
		}
		if err := ra.SaveAgent(owner, repo, *agent); err != nil {
			return regenDoneMsg{Key: specKey{owner, repo, specialist}, Err: fmt.Errorf("save: %w", err)}
		}
		return regenDoneMsg{Key: specKey{owner, repo, specialist}, Val: agent}
	}
}

func deleteAgentCmd(owner, repo, specialist string) tea.Cmd {
	return func() tea.Msg {
		err := ra.Delete(owner, repo, specialist)
		return deletedMsg{Key: specKey{owner, repo, specialist}, Err: err}
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
			return savedMsg{Key: specKey{owner, repo, specialist}, Err: err}
		}
		return savedMsg{Key: specKey{owner, repo, specialist}}
	}
}

// Tech-experts command helpers ──────────────────────────────────────────────

func loadTechsCmd(owner, repo string) tea.Cmd {
	return func() tea.Msg {
		got, err := ta.Load(owner, repo)
		return techsLoadedMsg{Owner: owner, Repo: repo, TA: got, Err: err}
	}
}

func techBusyKey(owner, repo, tech string) string {
	return owner + "/" + repo + "|tech:" + ta.CanonicalTech(tech)
}

func regenerateTechCmd(complete ai.CompleteFunc, history ta.HistoryFetcher, aiCfg *aiconfig.Config, rc *repoconfig.Config, owner, repo, tech, label, seed string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		agent, err := ta.Generate(ctx, ta.GenerateOpts{
			AICfg:    aiCfg,
			RC:       rc,
			Owner:    owner,
			Repo:     repo,
			Tech:     tech,
			Label:    label,
			Seed:     seed,
			Complete: complete,
			History:  history,
		})
		if err != nil {
			return techRegenDoneMsg{Key: techKey{owner, repo, ta.CanonicalTech(tech)}, Err: err}
		}
		if err := ta.SaveAgent(owner, repo, *agent); err != nil {
			return techRegenDoneMsg{Key: techKey{owner, repo, ta.CanonicalTech(tech)}, Err: fmt.Errorf("save: %w", err)}
		}
		return techRegenDoneMsg{Key: techKey{owner, repo, ta.CanonicalTech(tech)}, Val: agent}
	}
}

func deleteTechCmd(owner, repo, tech string) tea.Cmd {
	return func() tea.Msg {
		err := ta.Delete(owner, repo, tech)
		return techDeletedMsg{Key: techKey{owner, repo, ta.CanonicalTech(tech)}, Err: err}
	}
}

func saveManualTechCmd(owner, repo, tech, body string) tea.Cmd {
	return func() tea.Msg {
		// Preserve the existing label/seed when the user is editing an
		// existing brief manually; fall back to the canonical tech key.
		existing, _ := ta.Load(owner, repo)
		label := strings.TrimSpace(tech)
		seed := ""
		if existing != nil {
			if a, ok := existing.Get(tech); ok {
				if a.Label != "" {
					label = a.Label
				}
				seed = a.Seed
			}
		}
		agent := ta.Agent{
			Tech:        ta.CanonicalTech(tech),
			Label:       label,
			Seed:        seed,
			Context:     body,
			GeneratedAt: time.Now().UTC(),
			Manual:      true,
		}
		if err := ta.SaveAgent(owner, repo, agent); err != nil {
			return techSavedMsg{Key: techKey{owner, repo, ta.CanonicalTech(tech)}, Err: err}
		}
		return techSavedMsg{Key: techKey{owner, repo, ta.CanonicalTech(tech)}}
	}
}

func (m *Model) startRegenerateTech(tech string) tea.Cmd {
	owner, repo := splitRepoKey(m.currentRepoKey())
	if owner == "" || repo == "" {
		m.err = fmt.Errorf("no repo selected")
		return nil
	}
	if m.complete == nil {
		m.err = fmt.Errorf("LLM completion is not configured")
		return nil
	}
	canonical := ta.CanonicalTech(tech)
	if canonical == "" {
		m.err = fmt.Errorf("invalid tech name %q", tech)
		return nil
	}
	cur := m.currentTechs()
	label := canonical
	seed := ""
	if cur != nil {
		if a, ok := cur.Get(canonical); ok {
			if a.Label != "" {
				label = a.Label
			}
			seed = a.Seed
		}
	}
	m.busy.Start(techBusyKey(owner, repo, canonical))
	m.err = nil
	return tea.Batch(
		func() tea.Msg { return techRegenStartedMsg{Key: techKey{owner, repo, canonical}} },
		regenerateTechCmd(m.complete, nil, m.aiCfg, m.rc, owner, repo, canonical, label, seed),
	)
}

func (m *Model) startDeleteTech(tech string) tea.Cmd {
	owner, repo := splitRepoKey(m.currentRepoKey())
	if owner == "" || repo == "" {
		return nil
	}
	return deleteTechCmd(owner, repo, tech)
}

// Suggest-technologies flow ──────────────────────────────────────────────────

// startSuggestTechs dispatches an LLM pass that proposes technologies for the
// current repo. Already-configured techs are excluded so the user only sees
// new candidates. Returns nil (with m.err set) when prerequisites are missing.
func (m *Model) startSuggestTechs() tea.Cmd {
	owner, repo := splitRepoKey(m.currentRepoKey())
	if owner == "" || repo == "" {
		m.err = fmt.Errorf("no repo selected")
		return nil
	}
	if m.complete == nil {
		m.err = fmt.Errorf("LLM completion is not configured")
		return nil
	}
	if m.suggestBusy {
		return nil
	}
	existing := []string{}
	if cur := m.currentTechs(); cur != nil {
		existing = cur.SortedTechs()
	}
	m.err = nil
	m.candidates = nil
	m.candidateApproved = map[string]bool{}
	m.suggestBusy = true
	m.statusMsg = fmt.Sprintf("analyzing %s/%s for technologies …", owner, repo)
	return tea.Batch(
		func() tea.Msg { return techSuggestStartedMsg{Key: repoKey{owner, repo}} },
		suggestTechsCmd(m.complete, m.aiCfg, m.rc, owner, repo, existing),
	)
}

func suggestTechsCmd(complete ai.CompleteFunc, aiCfg *aiconfig.Config, rc *repoconfig.Config, owner, repo string, existing []string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cands, err := ta.Suggest(ctx, ta.SuggestOpts{
			AICfg:    aiCfg,
			RC:       rc,
			Owner:    owner,
			Repo:     repo,
			Complete: complete,
			Existing: existing,
		})
		return techSuggestDoneMsg{Key: repoKey{owner, repo}, Val: cands, Err: err}
	}
}

// setCandidateApproval records the user's approve/deny decision for a
// candidate keyed by canonical tech.
func (m *Model) setCandidateApproval(tech string, approved bool) {
	c := ta.CanonicalTech(tech)
	if c == "" {
		return
	}
	if m.candidateApproved == nil {
		m.candidateApproved = map[string]bool{}
	}
	m.candidateApproved[c] = approved
}

// approvedCandidateCount returns how many current candidates are approved.
func (m *Model) approvedCandidateCount() int {
	n := 0
	for _, c := range m.candidates {
		if m.candidateApproved[ta.CanonicalTech(c.Tech)] {
			n++
		}
	}
	return n
}

// dismissSuggestions clears the candidate panel without generating anything.
func (m *Model) dismissSuggestions() {
	m.resetSuggestions()
	m.statusMsg = "dismissed suggestions"
}

// generateApprovedCmd kicks off brief generation for every approved
// candidate, reusing the per-tech regenerate pipeline (and its done/save
// handling). Clears the candidate panel after dispatch.
func (m *Model) generateApprovedCmd() tea.Cmd {
	owner, repo := splitRepoKey(m.currentRepoKey())
	if owner == "" || repo == "" {
		m.err = fmt.Errorf("no repo selected")
		return nil
	}
	if m.complete == nil {
		m.err = fmt.Errorf("LLM completion is not configured")
		return nil
	}
	approved := make([]ta.Candidate, 0, len(m.candidates))
	for _, c := range m.candidates {
		if m.candidateApproved[ta.CanonicalTech(c.Tech)] {
			approved = append(approved, c)
		}
	}
	if len(approved) == 0 {
		m.err = fmt.Errorf("no candidates approved; click Approve on the technologies to generate")
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(approved)*2)
	for _, c := range approved {
		canonical := ta.CanonicalTech(c.Tech)
		label := strings.TrimSpace(c.Label)
		if label == "" {
			label = canonical
		}
		seed := c.Seed
		m.busy.Start(techBusyKey(owner, repo, canonical))
		cmds = append(cmds,
			func() tea.Msg { return techRegenStartedMsg{Key: techKey{owner, repo, canonical}} },
			regenerateTechCmd(m.complete, nil, m.aiCfg, m.rc, owner, repo, canonical, label, seed),
		)
	}
	m.err = nil
	m.statusMsg = fmt.Sprintf("generating %d approved technologies for %s/%s …", len(approved), owner, repo)
	m.resetSuggestions()
	return tea.Batch(cmds...)
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

	b.WriteString(m.renderRepoSelector(strings.Count(b.String(), "\n")))
	b.WriteString("\n")
	if m.editing {
		b.WriteString(m.renderEditPane())
		b.WriteString("\n")
	} else {
		b.WriteString(m.renderAgentList())
		b.WriteString("\n")
		b.WriteString(m.renderTechList())
	}

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

func (m *Model) renderRepoSelector(base int) string {
	var b strings.Builder
	b.WriteString(boldStyle.Render("Repository") + "\n")
	if len(m.repos) == 0 || m.repoDD == nil {
		b.WriteString(dimStyle.Render("no repos yet — paste an owner/repo to start"))
		b.WriteString("\n")
	} else {
		// Record the trigger's content-line index so View can position the
		// overlay panel after the viewport scroll offset is known.
		m.ddRepoRow = base + strings.Count(b.String(), "\n")
		b.WriteString(zone.Mark(ZoneRepoDD, m.repoDD.TriggerView()) + " ")
		b.WriteString(dimStyle.Render(fmt.Sprintf("(%d/%d)", m.repoIdx+1, len(m.repos))) + " ")
		b.WriteString(dimStyle.Render("←/→ to cycle") + "\n")
	}
	if m.addingRepo {
		b.WriteString(zone.Mark(ZoneAddRepoField, m.addInput.View()))
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
	if m.busy.Running(bk) {
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
	if m.busy.Running(bk) {
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

func (m *Model) renderTechList() string {
	var b strings.Builder
	b.WriteString(boldStyle.Render("Tech experts") + "  ")
	b.WriteString(dimStyle.Render("· one brief per technology, shared across all specialists for this repo") + "\n\n")

	if m.currentRepoKey() == "" {
		b.WriteString(dimStyle.Render("Select a repo above to manage its tech experts."))
		b.WriteString("\n")
		return b.String()
	}

	cur := m.currentTechs()
	keys := []string{}
	if cur != nil {
		keys = cur.SortedTechs()
	}
	if len(keys) == 0 {
		b.WriteString(dimStyle.Render("No tech experts yet — press t (or click + Add tech) to add one."))
		b.WriteString("\n")
	} else {
		for _, k := range keys {
			b.WriteString(m.renderTechRow(k, cur))
			b.WriteString("\n")
		}
	}

	if m.suggestBusy {
		b.WriteString(sectionRule.Render(strings.Repeat("─", max(8, m.contentW-2))))
		b.WriteString("\n")
		b.WriteString(chipBusy.Render(" analyzing repo for technologies … "))
		b.WriteString("\n")
	} else if len(m.candidates) > 0 {
		b.WriteString(m.renderCandidatePanel())
	}

	if m.addingTech {
		b.WriteString(sectionRule.Render(strings.Repeat("─", max(8, m.contentW-2))))
		b.WriteString("\n")
		b.WriteString(boldStyle.Render("New tech expert") + "  ")
		b.WriteString(dimStyle.Render("· tab to switch fields · enter to generate · esc to cancel") + "\n")
		b.WriteString(dimStyle.Render("Name") + "  ")
		b.WriteString(zone.Mark(ZoneAddTechName, m.techNameInput.View()))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("Seed") + "  ")
		b.WriteString(zone.Mark(ZoneAddTechSeed, m.techSeedInput.View()))
		b.WriteString("\n\n")
		b.WriteString(zone.Mark(ZoneAddTechSave, okStyle.Render(" Generate ")))
		b.WriteString("  ")
		b.WriteString(zone.Mark(ZoneAddTechCancel, errStyle.Render(" Cancel ")))
		b.WriteString("\n")
	} else {
		b.WriteString(zone.Mark(ZoneAddTechOpen, chipStyle.Render(" + Add tech ")))
		if !m.suggestBusy && len(m.candidates) == 0 {
			b.WriteString("  ")
			b.WriteString(zone.Mark(ZoneSuggestTech, chipPrimary.Render(" Suggest technologies ")))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderCandidatePanel renders the suggested-technology approve/deny list
// plus the Generate-approved / Dismiss footer.
func (m *Model) renderCandidatePanel() string {
	var b strings.Builder
	b.WriteString(sectionRule.Render(strings.Repeat("─", max(8, m.contentW-2))))
	b.WriteString("\n")
	b.WriteString(boldStyle.Render("Suggested technologies") + "  ")
	b.WriteString(dimStyle.Render("· approve the ones to generate, then Generate approved") + "\n\n")

	for _, c := range m.candidates {
		canonical := ta.CanonicalTech(c.Tech)
		approved := m.candidateApproved[canonical]

		header := boldStyle.Render(c.Label)
		if !strings.EqualFold(c.Label, canonical) {
			header += " " + dimStyle.Render("("+canonical+")")
		}
		if approved {
			header += "  " + okStyle.Render("approved")
		} else {
			header += "  " + dimStyle.Render("not approved")
		}
		b.WriteString(zone.Mark(zoneTechRow("cand:"+canonical), header))
		b.WriteString("\n")
		if s := strings.TrimSpace(c.Seed); s != "" {
			b.WriteString(dimStyle.Render(trimPreview(s, 240)))
			b.WriteString("\n")
		}
		if r := strings.TrimSpace(c.Rationale); r != "" {
			b.WriteString(dimStyle.Render("Evidence: " + trimPreview(r, 160)))
			b.WriteString("\n")
		}
		approveChip := chipStyle.Render(" Approve ")
		if approved {
			approveChip = chipPrimary.Render(" Approved ✓ ")
		}
		b.WriteString(zone.Mark(zoneCandApprove(canonical), approveChip))
		b.WriteString("  ")
		b.WriteString(zone.Mark(zoneCandDeny(canonical), chipStyle.Render(" Deny ")))
		b.WriteString("\n")
		b.WriteString(sectionRule.Render(strings.Repeat("─", max(8, m.contentW-2))))
		b.WriteString("\n")
	}

	n := m.approvedCandidateCount()
	genLabel := fmt.Sprintf(" Generate approved (%d) ", n)
	if n == 0 {
		b.WriteString(chipBusy.Render(genLabel))
	} else {
		b.WriteString(zone.Mark(ZoneGenApproved, okStyle.Render(genLabel)))
	}
	b.WriteString("  ")
	b.WriteString(zone.Mark(ZoneDismissSuggest, errStyle.Render(" Dismiss ")))
	b.WriteString("\n")
	return b.String()
}

func (m *Model) renderTechRow(tech string, cur *ta.TechAgents) string {
	rule := sectionRule.Render(strings.Repeat("─", max(8, m.contentW-2)))
	var b strings.Builder
	b.WriteString(rule + "\n")
	owner, repo := splitRepoKey(m.currentRepoKey())
	bk := techBusyKey(owner, repo, tech)

	label := tech
	if cur != nil {
		label = cur.LabelFor(tech)
	}
	header := boldStyle.Render(label)
	if label != tech {
		header += " " + dimStyle.Render("("+tech+")")
	}

	status := dimStyle.Render("missing")
	preview := ""
	seed := ""
	if cur != nil {
		if a, ok := cur.Get(tech); ok {
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
			seed = strings.TrimSpace(a.Seed)
		}
	}
	if m.busy.Running(bk) {
		status = chipBusy.Render(" regenerating … ")
	}

	b.WriteString(zone.Mark(zoneTechRow(tech), header+"  "+status))
	b.WriteString("\n")
	if seed != "" {
		b.WriteString(dimStyle.Render("Seed: " + trimPreview(seed, 200)))
		b.WriteString("\n")
	}
	if preview != "" {
		b.WriteString(dimStyle.Render(preview))
		b.WriteString("\n")
	}
	chips := []string{}
	regenLabel := " Regenerate "
	if m.busy.Running(bk) {
		regenLabel = " (running) "
		chips = append(chips, chipBusy.Render(regenLabel))
	} else {
		chips = append(chips, zone.Mark(zoneTechRegen(tech), chipPrimary.Render(regenLabel)))
	}
	chips = append(chips, zone.Mark(zoneTechEditBrief(tech), chipStyle.Render(" Edit brief ")))
	if cur != nil {
		if _, ok := cur.Get(tech); ok {
			chips = append(chips, zone.Mark(zoneTechDelete(tech), chipDanger.Render(" Delete ")))
		}
	}
	b.WriteString(strings.Join(chips, "  "))
	b.WriteString("\n")
	return b.String()
}

func (m *Model) renderEditPane() string {
	var b strings.Builder
	header := "Editing brief: "
	switch m.editKind {
	case editKindTech:
		header += "tech " + m.editTech
	default:
		header += m.editSpecialist
	}
	b.WriteString(boldStyle.Render(header))
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
