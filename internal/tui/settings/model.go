package settings

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
)

// StartSection selects which group is focused when the pane opens.
type StartSection int

const (
	StartReview StartSection = iota
	StartAI
	// StartRepoContext opens settings on the Repo context tab.
	StartRepoContext
)

const (
	fieldStrictness = iota
	fieldProvider
	fieldBaseURL
	fieldModel
	fieldAPIKey
	fieldTimeout
)

// Repo context tab focus order (must match advanceRepoFocus / renderRepoPanel).
const (
	repoFieldRoots = iota
	repoFieldMaxBytes
	repoFieldTTL
	repoFieldPRHistLimit
	repoFieldIncludePR
	repoFieldCultureSum
	repoFieldCtxVsChange
	repoFieldExpertPanel
	repoFieldParallelSpecs
	repoFieldParallelExperts
	repoFieldExpertPRs
	repoFieldExpertMaxB
	repoFieldExpertTTL
	repoFieldCount
)

// Opts configures the settings pane.
type Opts struct {
	Cfg          *aiconfig.Config
	Width        int
	BodyHeight   int
	StartSection StartSection
}

// Model is the full-screen settings form (keyboard + mouse).
type Model struct {
	width      int
	bodyH      int
	contentW   int
	fieldWidth int

	draft *aiconfig.Config
	focus int

	strictIdx int

	provider textinput.Model
	baseURL  textinput.Model
	model    textinput.Model
	apiKey   textinput.Model
	timeout  textinput.Model

	// Repo context tab (structured form; persisted as repo-context.json).
	repoRoots       textarea.Model
	repoMaxBytes    textinput.Model
	repoTTL         textinput.Model
	repoPRHistLimit textinput.Model
	repoExpertPRs   textinput.Model
	repoExpertMaxB  textinput.Model
	repoExpertTTL   textinput.Model
	repoIncludePR   bool
	repoCultureSum  bool
	repoCtxVsChange bool
	repoExpertPanel     bool
	repoParallelSpecs   bool
	repoParallelExperts bool
	repoFocus           int

	// panelTab 0 = Review strictness + AI fields; 1 = repo context form.
	panelTab int

	vp viewport.Model
}

var saveKeys = key.NewBinding(key.WithKeys("ctrl+s"))

// New builds a settings model from current config (cloned).
func New(o Opts) *Model {
	if o.Cfg == nil {
		o.Cfg = aiconfig.DefaultConfig()
	}
	draft := o.Cfg.Clone()
	if draft.TimeoutSec <= 0 {
		draft.TimeoutSec = 300
	}

	w := o.Width
	if w <= 0 {
		w = 80
	}
	bodyH := o.BodyHeight
	if bodyH <= 0 {
		bodyH = 20
	}
	fieldW := max(20, w-4)

	mk := func(placeholder string, echo textinput.EchoMode) textinput.Model {
		ti := textinput.New()
		ti.Placeholder = placeholder
		ti.CharLimit = 2048
		ti.Width = fieldW
		ti.EchoMode = echo
		return ti
	}

	m := &Model{
		width:      w,
		bodyH:      bodyH,
		contentW:   max(1, w),
		fieldWidth: fieldW,
		draft:      draft,
		strictIdx:  strictnessIndex(draft.ReviewStrictness),
		provider:   mk("claude | gemini | ollama | openai_compatible", textinput.EchoNormal),
		baseURL:    mk("optional; Ollama default if empty", textinput.EchoNormal),
		model:      mk("model id", textinput.EchoNormal),
		apiKey:     mk("optional for Ollama", textinput.EchoPassword),
		timeout:    mk("seconds (default 300)", textinput.EchoNormal),
	}
	m.provider.SetValue(string(draft.Provider))
	m.baseURL.SetValue(draft.BaseURL)
	m.model.SetValue(draft.Model)
	if draft.APIKey != "" {
		m.apiKey.SetValue(draft.APIKey)
	}
	m.timeout.SetValue(strconv.Itoa(draft.TimeoutSec))

	rr := textarea.New()
	rr.ShowLineNumbers = false
	rr.Prompt = ""
	rr.CharLimit = 65536
	rr.SetWidth(m.fieldWidth)
	rr.SetHeight(min(8, max(4, bodyH/4)))
	rr.Blur()
	m.repoRoots = rr

	mkInt := func(ph string) textinput.Model {
		t := textinput.New()
		t.Placeholder = ph
		t.CharLimit = 12
		t.Width = fieldW
		t.EchoMode = textinput.EchoNormal
		return t
	}
	m.repoMaxBytes = mkInt("default 24576")
	m.repoTTL = mkInt("seconds; default 86400")
	m.repoPRHistLimit = mkInt("default 30")
	m.repoExpertPRs = mkInt("default 8")
	m.repoExpertMaxB = mkInt("default 12000")
	m.repoExpertTTL = mkInt("seconds; default 21600")
	m.initRepoFieldsFromDisk()

	m.vp = viewport.New(m.contentW, bodyH)
	m.vp.MouseWheelEnabled = true

	switch o.StartSection {
	case StartAI:
		m.focus = fieldProvider
		m.provider.Focus()
	case StartRepoContext:
		m.panelTab = 1
		m.focusRepoField(repoFieldRoots)
	default:
		m.focus = fieldStrictness
		m.blurInputs()
	}
	return m
}

func (m *Model) initRepoFieldsFromDisk() {
	c, err := repoconfig.Load()
	if err != nil || c == nil {
		c = repoconfig.Default()
	}
	m.repoRoots.SetValue(repoconfig.FormatRepoRootsLines(c.RepoRoots))
	m.repoMaxBytes.SetValue(strconv.Itoa(c.MaxBytes))
	m.repoTTL.SetValue(strconv.Itoa(c.TTLSeconds))
	m.repoPRHistLimit.SetValue(strconv.Itoa(c.PRHistoryLimit))
	m.repoExpertPRs.SetValue(strconv.Itoa(c.RepoExpertReviewPRs))
	m.repoExpertMaxB.SetValue(strconv.Itoa(c.RepoExpertMaxBytes))
	m.repoExpertTTL.SetValue(strconv.Itoa(c.RepoExpertReviewTTLSeconds))
	m.repoIncludePR = c.IncludePRHistory
	m.repoCultureSum = c.RepoCultureSummarize
	m.repoCtxVsChange = c.ContextVersusChangeSummary
	m.repoExpertPanel = c.RepoExpertPanel
	m.repoParallelSpecs = c.ParallelSpecialists
	m.repoParallelExperts = c.ParallelRepoExperts
}

func strictnessIndex(rs aiconfig.ReviewStrictness) int {
	switch rs {
	case aiconfig.ReviewCriticalOnly:
		return 0
	case aiconfig.ReviewLenient:
		return 1
	case aiconfig.ReviewStrict:
		return 3
	default:
		return 2
	}
}

func strictnessAt(i int) aiconfig.ReviewStrictness {
	switch i {
	case 0:
		return aiconfig.ReviewCriticalOnly
	case 1:
		return aiconfig.ReviewLenient
	case 3:
		return aiconfig.ReviewStrict
	default:
		return aiconfig.ReviewBalanced
	}
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
	m.fieldWidth = max(20, m.width-4)
	m.provider.Width = m.fieldWidth
	m.baseURL.Width = m.fieldWidth
	m.model.Width = m.fieldWidth
	m.apiKey.Width = m.fieldWidth
	m.timeout.Width = m.fieldWidth
	m.repoRoots.SetWidth(m.fieldWidth)
	m.repoRoots.SetHeight(min(10, max(4, m.bodyH/3)))
	m.repoMaxBytes.Width = m.fieldWidth
	m.repoTTL.Width = m.fieldWidth
	m.repoPRHistLimit.Width = m.fieldWidth
	m.repoExpertPRs.Width = m.fieldWidth
	m.repoExpertMaxB.Width = m.fieldWidth
	m.repoExpertTTL.Width = m.fieldWidth
	m.vp.Width = m.contentW
	m.vp.Height = m.bodyH
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	if m.panelTab == 1 {
		if m.repoFocus == repoFieldRoots {
			return textarea.Blink
		}
		return textinput.Blink
	}
	return textinput.Blink
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.panelTab == 1 {
			switch {
			case key.Matches(msg, saveKeys):
				return m, m.submitRepoSave()
			case msg.String() == "esc":
				return m, func() tea.Msg { return DoneMsg{Cancelled: true} }
			case msg.String() == "[":
				m.setPanelTab(0)
				return m, textinput.Blink
			case msg.String() == "]":
				m.setPanelTab(1)
				return m, textarea.Blink
			case msg.String() == "tab" || msg.String() == "ctrl+i":
				m.advanceRepoFocus(1)
				return m, m.repoBlinkCmd()
			case msg.String() == "shift+tab":
				m.advanceRepoFocus(-1)
				return m, m.repoBlinkCmd()
			}
			if m.isRepoBoolFocus() {
				switch msg.String() {
				case " ", "enter", "y", "Y", "n", "N":
					m.toggleRepoBoolAtFocus()
					return m, nil
				}
			}
			var cmd tea.Cmd
			switch m.repoFocus {
			case repoFieldRoots:
				m.repoRoots, cmd = m.repoRoots.Update(msg)
			case repoFieldMaxBytes:
				m.repoMaxBytes, cmd = m.repoMaxBytes.Update(msg)
			case repoFieldTTL:
				m.repoTTL, cmd = m.repoTTL.Update(msg)
			case repoFieldPRHistLimit:
				m.repoPRHistLimit, cmd = m.repoPRHistLimit.Update(msg)
			case repoFieldExpertPRs:
				m.repoExpertPRs, cmd = m.repoExpertPRs.Update(msg)
			case repoFieldExpertMaxB:
				m.repoExpertMaxB, cmd = m.repoExpertMaxB.Update(msg)
			case repoFieldExpertTTL:
				m.repoExpertTTL, cmd = m.repoExpertTTL.Update(msg)
			}
			return m, cmd
		}

		switch {
		case key.Matches(msg, saveKeys):
			return m, m.submitSave()
		case msg.String() == "esc":
			return m, func() tea.Msg { return DoneMsg{Cancelled: true} }
		case msg.String() == "[":
			m.setPanelTab(0)
			return m, textinput.Blink
		case msg.String() == "]":
			m.setPanelTab(1)
			return m, m.repoBlinkCmd()
		case msg.String() == "tab" || msg.String() == "ctrl+i":
			m.advanceFocus(1)
			return m, textinput.Blink
		case msg.String() == "shift+tab":
			m.advanceFocus(-1)
			return m, textinput.Blink
		}
		if m.focus == fieldStrictness {
			switch msg.String() {
			case "up", "k":
				m.strictIdx = (m.strictIdx + 3) % 4
				m.draft.ReviewStrictness = strictnessAt(m.strictIdx)
				return m, nil
			case "down", "j":
				m.strictIdx = (m.strictIdx + 1) % 4
				m.draft.ReviewStrictness = strictnessAt(m.strictIdx)
				return m, nil
			case "1":
				m.strictIdx, m.draft.ReviewStrictness = 0, aiconfig.ReviewCriticalOnly
				return m, nil
			case "2":
				m.strictIdx, m.draft.ReviewStrictness = 1, aiconfig.ReviewLenient
				return m, nil
			case "3":
				m.strictIdx, m.draft.ReviewStrictness = 2, aiconfig.ReviewBalanced
				return m, nil
			case "4":
				m.strictIdx, m.draft.ReviewStrictness = 3, aiconfig.ReviewStrict
				return m, nil
			}
		}
		var cmd tea.Cmd
		switch m.focus {
		case fieldProvider:
			m.provider, cmd = m.provider.Update(msg)
		case fieldBaseURL:
			m.baseURL, cmd = m.baseURL.Update(msg)
		case fieldModel:
			m.model, cmd = m.model.Update(msg)
		case fieldAPIKey:
			m.apiKey, cmd = m.apiKey.Update(msg)
		case fieldTimeout:
			m.timeout, cmd = m.timeout.Update(msg)
		}
		return m, cmd

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

	default:
		return m, nil
	}
}

func (m *Model) advanceFocus(delta int) {
	m.blurInputs()
	m.focus = (m.focus + delta + 6) % 6
	if m.focus != fieldStrictness {
		m.focusedInput().Focus()
	}
}

func (m *Model) setPanelTab(tab int) {
	if tab < 0 {
		tab = 0
	}
	if tab > 1 {
		tab = 1
	}
	if m.panelTab == tab {
		return
	}
	m.panelTab = tab
	m.blurInputs()
	m.blurRepoInputs()
	if m.panelTab == 1 {
		if m.repoFocus < 0 || m.repoFocus >= repoFieldCount {
			m.repoFocus = 0
		}
		m.focusRepoField(m.repoFocus)
	} else {
		m.focus = fieldStrictness
	}
}

func (m *Model) blurInputs() {
	m.provider.Blur()
	m.baseURL.Blur()
	m.model.Blur()
	m.apiKey.Blur()
	m.timeout.Blur()
	m.blurRepoInputs()
}

func (m *Model) blurRepoInputs() {
	m.repoRoots.Blur()
	m.repoMaxBytes.Blur()
	m.repoTTL.Blur()
	m.repoPRHistLimit.Blur()
	m.repoExpertPRs.Blur()
	m.repoExpertMaxB.Blur()
	m.repoExpertTTL.Blur()
}

func (m *Model) focusRepoField(i int) {
	m.blurRepoInputs()
	if i < 0 {
		i = 0
	}
	if i >= repoFieldCount {
		i = repoFieldCount - 1
	}
	m.repoFocus = i
	switch i {
	case repoFieldRoots:
		m.repoRoots.Focus()
	case repoFieldMaxBytes:
		m.repoMaxBytes.Focus()
	case repoFieldTTL:
		m.repoTTL.Focus()
	case repoFieldPRHistLimit:
		m.repoPRHistLimit.Focus()
	case repoFieldExpertPRs:
		m.repoExpertPRs.Focus()
	case repoFieldExpertMaxB:
		m.repoExpertMaxB.Focus()
	case repoFieldExpertTTL:
		m.repoExpertTTL.Focus()
	}
}

func (m *Model) advanceRepoFocus(delta int) {
	m.repoFocus = (m.repoFocus + delta + repoFieldCount) % repoFieldCount
	m.focusRepoField(m.repoFocus)
}

func (m *Model) isRepoBoolFocus() bool {
	switch m.repoFocus {
	case repoFieldIncludePR, repoFieldCultureSum, repoFieldCtxVsChange, repoFieldExpertPanel,
		repoFieldParallelSpecs, repoFieldParallelExperts:
		return true
	default:
		return false
	}
}

func (m *Model) toggleRepoBoolAtFocus() {
	switch m.repoFocus {
	case repoFieldIncludePR:
		m.repoIncludePR = !m.repoIncludePR
	case repoFieldCultureSum:
		m.repoCultureSum = !m.repoCultureSum
	case repoFieldCtxVsChange:
		m.repoCtxVsChange = !m.repoCtxVsChange
	case repoFieldExpertPanel:
		m.repoExpertPanel = !m.repoExpertPanel
	case repoFieldParallelSpecs:
		m.repoParallelSpecs = !m.repoParallelSpecs
	case repoFieldParallelExperts:
		m.repoParallelExperts = !m.repoParallelExperts
	}
}

func (m *Model) repoBlinkCmd() tea.Cmd {
	if m.repoFocus == repoFieldRoots {
		return textarea.Blink
	}
	return textinput.Blink
}

func (m *Model) focusedInput() *textinput.Model {
	switch m.focus {
	case fieldProvider:
		return &m.provider
	case fieldBaseURL:
		return &m.baseURL
	case fieldModel:
		return &m.model
	case fieldAPIKey:
		return &m.apiKey
	default:
		return &m.timeout
	}
}

func (m *Model) submitSave() tea.Cmd {
	return func() tea.Msg {
		p, err := aiconfig.ParseProvider(strings.TrimSpace(m.provider.Value()))
		if err != nil {
			return DoneMsg{Err: err}
		}
		ts := strings.TrimSpace(m.timeout.Value())
		timeoutSec := 300
		if ts != "" {
			n, err := strconv.Atoi(ts)
			if err != nil || n <= 0 {
				return DoneMsg{Err: fmt.Errorf("invalid timeout: must be a positive integer")}
			}
			timeoutSec = n
		}
		rs := m.draft.ReviewStrictness
		if _, err := aiconfig.ParseReviewStrictness(string(rs)); err != nil {
			rs = strictnessAt(m.strictIdx)
		}
		cfg := &aiconfig.Config{
			Provider:         p,
			ReviewStrictness: rs,
			BaseURL:          strings.TrimSpace(m.baseURL.Value()),
			Model:            strings.TrimSpace(m.model.Value()),
			APIKey:           m.apiKey.Value(),
			TimeoutSec:       timeoutSec,
		}
		if err := aiconfig.Save(cfg, ""); err != nil {
			return DoneMsg{Err: err}
		}
		return DoneMsg{Cfg: cfg.Clone()}
	}
}

func (m *Model) submitRepoSave() tea.Cmd {
	return func() tea.Msg {
		roots, err := repoconfig.ParseRepoRootsLines(m.repoRoots.Value())
		if err != nil {
			return DoneMsg{Err: err}
		}
		parseInt := func(name, s string, minVal int) (int, error) {
			s = strings.TrimSpace(s)
			if s == "" {
				return 0, fmt.Errorf("%s is empty", name)
			}
			n, err := strconv.Atoi(s)
			if err != nil {
				return 0, fmt.Errorf("%s: not an integer", name)
			}
			if n < minVal {
				return 0, fmt.Errorf("%s: must be >= %d", name, minVal)
			}
			return n, nil
		}
		maxB, err := parseInt("Max context bytes", m.repoMaxBytes.Value(), 2048)
		if err != nil {
			return DoneMsg{Err: err}
		}
		ttl, err := parseInt("Context cache TTL (seconds)", m.repoTTL.Value(), 60)
		if err != nil {
			return DoneMsg{Err: err}
		}
		prLim, err := parseInt("Merged PR list limit", m.repoPRHistLimit.Value(), 1)
		if err != nil {
			return DoneMsg{Err: err}
		}
		exPR, err := parseInt("Repo expert history PR sample", m.repoExpertPRs.Value(), 1)
		if err != nil {
			return DoneMsg{Err: err}
		}
		exMax, err := parseInt("Repo expert history max bytes", m.repoExpertMaxB.Value(), 1024)
		if err != nil {
			return DoneMsg{Err: err}
		}
		exTTL, err := parseInt("Repo expert history cache TTL (seconds)", m.repoExpertTTL.Value(), 60)
		if err != nil {
			return DoneMsg{Err: err}
		}
		cfg := repoconfig.Config{
			RepoRoots:                  roots,
			MaxBytes:                   maxB,
			TTLSeconds:                 ttl,
			IncludePRHistory:           m.repoIncludePR,
			PRHistoryLimit:             prLim,
			RepoCultureSummarize:       m.repoCultureSum,
			ContextVersusChangeSummary: m.repoCtxVsChange,
			RepoExpertPanel:            m.repoExpertPanel,
			ParallelSpecialists:        m.repoParallelSpecs,
			ParallelRepoExperts:        m.repoParallelExperts,
			RepoExpertReviewPRs:        exPR,
			RepoExpertMaxBytes:         exMax,
			RepoExpertReviewTTLSeconds: exTTL,
		}
		cfg.Normalize()
		if err := repoconfig.Save(&cfg, ""); err != nil {
			return DoneMsg{Err: err}
		}
		return DoneMsg{RepoSaved: true}
	}
}

func (m *Model) renderTabStrip() string {
	t0 := " Review & AI "
	t1 := " Repo context "
	var left, right string
	if m.panelTab == 0 {
		left = okStyle.Render(t0)
		right = dimStyle.Render(t1)
	} else {
		left = dimStyle.Render(t0)
		right = okStyle.Render(t1)
	}
	row := zone.Mark(ZoneSettingsTabReview, left) + "  " + zone.Mark(ZoneSettingsTabRepoCtx, right)
	return row + "\n" + dimStyle.Render("click tabs or [ ] to switch") + "\n"
}

func (m *Model) renderStrictnessRows() string {
	var b strings.Builder
	b.WriteString(boldStyle.Render("Review strictness") + "\n")
	b.WriteString(dimStyle.Render("click row · ↑/↓ or j/k · 1–4") + "\n\n")

	rows := []struct {
		zoneID string
		idx    int
		title  string
		desc   string
	}{
		{ZoneStrictCriticalOnly, 0, "critical-only", "only merge-blocking (critical) findings"},
		{ZoneStrictLenient, 1, "lenient", "error + critical only"},
		{ZoneStrictBalanced, 2, "balanced", "warning and above (default)"},
		{ZoneStrictStrict, 3, "strict", "info-level nits included"},
	}
	for _, r := range rows {
		mark := " "
		if m.strictIdx == r.idx {
			mark = "●"
		}
		line := fmt.Sprintf("%s  %-10s  %s", mark, r.title, dimStyle.Render(r.desc))
		row := lipgloss.NewStyle().Padding(0, 0).Render(line)
		b.WriteString(zone.Mark(r.zoneID, row))
		b.WriteString("\n")
	}
	return b.String()
}

func (m *Model) buildForm() string {
	var b strings.Builder
	b.WriteString(boldStyle.Render("Settings") + "\n\n")
	b.WriteString(m.renderTabStrip())
	b.WriteString("\n")
	if m.panelTab == 0 {
		b.WriteString(dimStyle.Render("tab / shift+tab fields · ctrl+s save · esc cancel · [ ] repo tab") + "\n\n")
		b.WriteString(m.renderStrictnessRows())
		b.WriteString("\n")
		b.WriteString(boldStyle.Render("AI") + "\n\n")
		b.WriteString("provider\n")
		b.WriteString(m.provider.View() + "\n\n")
		b.WriteString("base URL\n")
		b.WriteString(m.baseURL.View() + "\n\n")
		b.WriteString("model\n")
		b.WriteString(m.model.View() + "\n\n")
		b.WriteString("API key (masked)\n")
		b.WriteString(m.apiKey.View() + "\n\n")
		b.WriteString("timeout (sec)\n")
		b.WriteString(m.timeout.View() + "\n\n")
		b.WriteString(dimStyle.Render("Config file: "+aiconfig.DefaultPath()) + "\n\n")
	} else {
		b.WriteString(dimStyle.Render("tab / shift+tab fields · space on toggles · ctrl+s save · esc cancel · [ ] review tab") + "\n\n")
		b.WriteString(m.renderRepoPanel())
		b.WriteString("\n")
	}
	b.WriteString(zone.Mark(ZoneSettingsSave, okStyle.Render(" Save ")) + "  ")
	b.WriteString(zone.Mark(ZoneSettingsCancel, errStyle.Render(" Cancel ")))
	return b.String()
}

// View renders the scrollable settings body (no header/status — root adds those).
func (m *Model) View() string {
	// Rebuild each frame so textinput cursor blink and zones stay aligned with layout.
	m.vp.SetContent(m.buildForm())
	return lipgloss.NewStyle().Width(m.width).MaxWidth(m.width).Height(m.bodyH).Render(m.vp.View())
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

func (m *Model) renderRepoPanel() string {
	var b strings.Builder
	b.WriteString(dimStyle.Render("Saved to "+repoconfig.DefaultPath()) + "\n\n")

	writeHdr := func(title string) {
		b.WriteString(boldStyle.Render(title) + "\n")
	}
	writeFocus := func(field int) string {
		if m.repoFocus == field {
			return "> "
		}
		return "  "
	}

	writeHdr("Local repository clones (repo_roots)")
	b.WriteString(dimStyle.Render("One line per mapping: owner/repo=/absolute/path (e.g. acme/widget=/Users/me/src/widget). Lines starting with # are ignored.") + "\n")
	b.WriteString(writeFocus(repoFieldRoots))
	b.WriteString(m.repoRoots.View())
	b.WriteString("\n\n")

	writeHdr("Context bundle")
	b.WriteString(writeFocus(repoFieldMaxBytes))
	b.WriteString("max_bytes (injected context cap)\n")
	b.WriteString(m.repoMaxBytes.View() + "\n")
	b.WriteString(writeFocus(repoFieldTTL))
	b.WriteString("ttl_seconds (on-disk context bundle cache)\n")
	b.WriteString(m.repoTTL.View() + "\n\n")

	writeHdr("Merged PR culture")
	b.WriteString(writeFocus(repoFieldIncludePR))
	b.WriteString(m.renderRepoToggle("Include merged PR titles in bundle", m.repoIncludePR, ZoneRepoToggleIncludePR) + "\n")
	b.WriteString(writeFocus(repoFieldPRHistLimit))
	b.WriteString("pr_history_limit\n")
	b.WriteString(m.repoPRHistLimit.View() + "\n")
	b.WriteString(writeFocus(repoFieldCultureSum))
	b.WriteString(m.renderRepoToggle("Summarize merged-PR culture (extra AI call)", m.repoCultureSum, ZoneRepoToggleCulture) + "\n\n")

	writeHdr("Review helpers")
	b.WriteString(writeFocus(repoFieldCtxVsChange))
	b.WriteString(m.renderRepoToggle("Context vs change summary (parallel AI pass)", m.repoCtxVsChange, ZoneRepoToggleCtxVs) + "\n\n")

	writeHdr("Repo arbiter (final pass before vibe-coach)")
	b.WriteString(writeFocus(repoFieldExpertPanel))
	b.WriteString(m.renderRepoToggle("Run repo arbiter (suppresses noisy findings, may override verdict)", m.repoExpertPanel, ZoneRepoToggleExpert) + "\n")
	b.WriteString(writeFocus(repoFieldParallelSpecs))
	b.WriteString(m.renderRepoToggle("Run specialists in parallel (faster; may hit rate limits)", m.repoParallelSpecs, ZoneRepoToggleParallelSpecs) + "\n")
	b.WriteString(writeFocus(repoFieldParallelExperts))
	b.WriteString(m.renderRepoToggle("[deprecated] parallel repo experts (no effect — repo agents replaced this)", m.repoParallelExperts, ZoneRepoToggleParallelExperts) + "\n")
	b.WriteString(dimStyle.Render("Repo-agent generation reuses these review-history digest knobs:") + "\n")
	b.WriteString(writeFocus(repoFieldExpertPRs))
	b.WriteString("repo_expert_review_prs (merged PRs sampled for review-body digest)\n")
	b.WriteString(m.repoExpertPRs.View() + "\n")
	b.WriteString(writeFocus(repoFieldExpertMaxB))
	b.WriteString("repo_expert_max_bytes (digest size cap)\n")
	b.WriteString(m.repoExpertMaxB.View() + "\n")
	b.WriteString(writeFocus(repoFieldExpertTTL))
	b.WriteString("repo_expert_review_ttl_seconds (digest disk cache)\n")
	b.WriteString(m.repoExpertTTL.View() + "\n")

	return b.String()
}

func (m *Model) renderRepoToggle(label string, on bool, zoneID string) string {
	var left, right string
	if on {
		left = okStyle.Render("ON")
		right = dimStyle.Render("off")
	} else {
		left = dimStyle.Render("on")
		right = okStyle.Render("OFF")
	}
	return label + "  " + zone.Mark(zoneID, "["+left+" / "+right+"]")
}
