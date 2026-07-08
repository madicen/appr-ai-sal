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
	bubblepicker "github.com/madicen/bubble-color-picker"
	bubbledropdown "github.com/madicen/bubble-dropdown"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/theme"
	"github.com/madicen/appr-ai-sal/internal/tui/state"
	"github.com/madicen/appr-ai-sal/internal/tui/util/dropdown"
)

// StartSection selects which group is focused when the pane opens.
type StartSection int

const (
	StartReview StartSection = iota
	StartAI
	// StartRepoContext opens settings on the Repo context tab.
	StartRepoContext
	// StartTheme opens settings on the Theme tab.
	StartTheme
)

const (
	fieldStrictness = iota
	fieldProfilePicker
	fieldProfileName
	fieldPreset
	fieldProvider
	fieldBaseURL
	fieldModel
	fieldModelList
	fieldAPIKey
	fieldTimeout
	fieldAICount
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
	repoFieldParallelPRAgents
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

	// Review & AI tab dropdowns, each via the shared dropdown.Host.
	// strictnessDD and providerDD have static options; profileDD is
	// recreated when the profile list changes (the component has no runtime
	// SetOptions).
	strictnessDD *dropdown.Host
	profileDD    *dropdown.Host
	providerDD   *dropdown.Host
	// presetDD fills provider + base URL from a built-in preset; modelDD is a
	// picker built after a successful ListModels fetch (Phase 6 items 2 & 3).
	presetDD *dropdown.Host
	modelDD  *dropdown.Host

	// modelOptions holds the model ids parallel to modelDD's labels so a
	// selection can fill the model input. modelsLoading / modelsErr drive the
	// fetch button's status line (fail-open to manual entry).
	modelOptions  []string
	modelsLoading bool
	modelsErr     string

	// Recorded content-line indices of each dropdown trigger, populated
	// while buildForm assembles the body so SetBounds can be applied after
	// the viewport scroll offset is known. col is shared (triggers render
	// at column 0).
	ddStrictRow   int
	ddProfileRow  int
	ddProviderRow int
	ddPresetRow   int
	ddModelRow    int

	// contentTop is the absolute terminal row where the settings body
	// begins (the chrome header height). Mouse events are translated by
	// this offset before reaching an open dropdown's geometric hit-test.
	contentTop int

	// Profile editor state. selectedProfileIdx is the index of the
	// profile in draft.Profiles whose fields are currently shown in the
	// editor (provider/baseURL/model/apiKey/timeout textinputs). It moves
	// independently of draft.ActiveProfile (the user can edit a non-active
	// profile and "Set active" later).
	selectedProfileIdx int

	profileName textinput.Model
	baseURL     textinput.Model
	model       textinput.Model
	apiKey      textinput.Model
	timeout     textinput.Model

	// Repo context tab (structured form; persisted as repo-context.json).
	repoRoots            textarea.Model
	repoMaxBytes         textinput.Model
	repoTTL              textinput.Model
	repoPRHistLimit      textinput.Model
	repoExpertPRs        textinput.Model
	repoExpertMaxB       textinput.Model
	repoExpertTTL        textinput.Model
	repoIncludePR        bool
	repoCultureSum       bool
	repoCtxVsChange      bool
	repoExpertPanel      bool
	repoParallelSpecs    bool
	repoParallelPRAgents bool
	repoParallelExperts  bool
	repoFocus            int

	// panelTab 0 = Review strictness + AI fields; 1 = repo context form;
	// 2 = theme palette.
	panelTab int

	vp viewport.Model

	theme *themePanel
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
		width:       w,
		bodyH:       bodyH,
		contentW:    max(1, w),
		fieldWidth:  fieldW,
		draft:       draft,
		profileName: mk("profile name (e.g. sonnet, fast, ollama)", textinput.EchoNormal),
		baseURL:     mk("optional; Ollama default if empty", textinput.EchoNormal),
		model:       mk("model id", textinput.EchoNormal),
		apiKey:      mk("optional for Ollama", textinput.EchoPassword),
		timeout:     mk("seconds (default 300)", textinput.EchoNormal),
	}
	m.selectedProfileIdx = m.indexOfActiveProfile()

	// Build dropdowns before loading the editor so loadEditorFromSelected
	// Profile can sync the provider dropdown's index.
	selProv := aiconfig.ProviderClaude
	if m.selectedProfileIdx >= 0 && m.selectedProfileIdx < len(draft.Profiles) {
		selProv = draft.Profiles[m.selectedProfileIdx].Provider
	}
	m.initDropdowns(selProv)

	m.loadEditorFromSelectedProfile()

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

	m.theme = newThemePanel()

	switch o.StartSection {
	case StartAI:
		m.focus = fieldProfilePicker
		m.blurInputs()
	case StartRepoContext:
		m.panelTab = 1
		m.focusRepoField(repoFieldRoots)
	case StartTheme:
		m.panelTab = 2
		m.blurInputs()
	default:
		m.focus = fieldStrictness
		m.blurInputs()
	}
	m.syncDropdownFocus()
	return m
}

// indexOfActiveProfile returns the index of the active profile in
// draft.Profiles, falling back to 0 when not found / empty.
func (m *Model) indexOfActiveProfile() int {
	if m == nil || m.draft == nil || len(m.draft.Profiles) == 0 {
		return 0
	}
	for i, p := range m.draft.Profiles {
		if strings.EqualFold(strings.TrimSpace(p.Name), strings.TrimSpace(m.draft.ActiveProfile)) {
			return i
		}
	}
	return 0
}

// loadEditorFromSelectedProfile copies the selected profile's fields into
// the textinput models so the editor reflects the row the cursor is on.
func (m *Model) loadEditorFromSelectedProfile() {
	if m == nil || m.draft == nil {
		return
	}
	// A fetched model list belongs to the previously edited profile's
	// provider/base URL; drop it when the editor loads a different profile.
	m.clearModelList()
	if m.selectedProfileIdx < 0 || m.selectedProfileIdx >= len(m.draft.Profiles) {
		m.selectedProfileIdx = 0
	}
	if len(m.draft.Profiles) == 0 {
		// Should not happen after Load() / DefaultConfig(); guard anyway.
		m.profileName.SetValue("")
		if m.providerDD != nil {
			m.providerDD.SetSelectedIndex(0)
		}
		m.baseURL.SetValue("")
		m.model.SetValue("")
		m.apiKey.SetValue("")
		m.timeout.SetValue("300")
		return
	}
	p := m.draft.Profiles[m.selectedProfileIdx]
	m.profileName.SetValue(p.Name)
	if m.providerDD != nil {
		m.providerDD.SetSelectedIndex(providerDDIndex(p.Provider))
	}
	m.baseURL.SetValue(p.BaseURL)
	m.model.SetValue(p.Model)
	if p.APIKey != "" {
		m.apiKey.SetValue(p.APIKey)
	} else {
		m.apiKey.SetValue("")
	}
	t := p.TimeoutSec
	if t <= 0 {
		t = 300
	}
	m.timeout.SetValue(strconv.Itoa(t))
}

// commitEditorToSelectedProfile copies the textinputs into the selected
// profile slot. Called before profile-list mutations (Add / Delete / move
// selection / SetActive / submitSave) so in-progress edits aren't lost.
func (m *Model) commitEditorToSelectedProfile() {
	if m == nil || m.draft == nil {
		return
	}
	if m.selectedProfileIdx < 0 || m.selectedProfileIdx >= len(m.draft.Profiles) {
		return
	}
	prof := m.draft.Profiles[m.selectedProfileIdx]
	prof.Name = strings.TrimSpace(m.profileName.Value())
	if prof.Name == "" {
		prof.Name = m.draft.Profiles[m.selectedProfileIdx].Name
	}
	prof.Provider = m.editedProvider()
	prof.BaseURL = strings.TrimSpace(m.baseURL.Value())
	prof.Model = strings.TrimSpace(m.model.Value())
	prof.APIKey = m.apiKey.Value()
	if ts := strings.TrimSpace(m.timeout.Value()); ts != "" {
		if n, err := strconv.Atoi(ts); err == nil && n > 0 {
			prof.TimeoutSec = n
		}
	}
	wasActive := strings.EqualFold(m.draft.ActiveProfile, m.draft.Profiles[m.selectedProfileIdx].Name)
	m.draft.Profiles[m.selectedProfileIdx] = prof
	if wasActive {
		m.draft.ActiveProfile = prof.Name
	}
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
	m.repoParallelPRAgents = c.ParallelPRAgents
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
	m.profileName.Width = m.fieldWidth
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
	// Color-picker results are emitted as commands by the picker library
	// and surface here on a later tick. They must be handled before normal
	// key/mouse routing so the active swatch can close cleanly.
	switch typed := msg.(type) {
	case modelsListedMsg:
		m.handleModelsListed(typed)
		return m, nil
	case bubblepicker.ColorChangedMsg:
		if m.theme != nil {
			m.theme.applyChosenColor(typed.Color)
			if idx := m.theme.openSwatchIndex(); idx >= 0 {
				updated, cmd := m.theme.swatches[idx].swatch.Update(typed)
				m.theme.swatches[idx].swatch = updated
				return m, cmd
			}
		}
		return m, nil
	case bubblepicker.ColorCanceledMsg:
		if m.theme != nil {
			if idx := m.theme.openSwatchIndex(); idx >= 0 {
				updated, cmd := m.theme.swatches[idx].swatch.Update(typed)
				m.theme.swatches[idx].swatch = updated
				return m, cmd
			}
		}
		return m, nil
	}

	// Dropdown result messages (emitted as commands on a later tick) close
	// the open dropdown and apply the choice to the draft.
	switch msg.(type) {
	case bubbledropdown.ItemChosenMsg, bubbledropdown.ItemCanceledMsg:
		return m, m.handleDropdownResult(msg)
	}

	// When a Review & AI dropdown panel is open, route every key/mouse event
	// to it (it owns its own list navigation and geometric hit-testing).
	if m.panelTab == 0 && m.anyDropdownOpen() {
		switch msg.(type) {
		case tea.KeyMsg, tea.MouseMsg:
			return m, m.forwardToDropdown(m.openDropdownKind(), msg)
		case zone.MsgZoneInBounds:
			// Swallow stray zone-release dispatches so background zones
			// (tab strip, Save) don't fire while the panel is open.
			return m, nil
		}
	}

	// When a swatch modal is open, route every key/mouse event to it so the
	// picker can drive its own focus, hue/grid hit-testing, and key bindings.
	if m.panelTab == 2 && m.theme != nil {
		if idx := m.theme.openSwatchIndex(); idx >= 0 {
			switch msg.(type) {
			case tea.KeyMsg, tea.MouseMsg:
				updated, cmd := m.theme.swatches[idx].swatch.Update(msg)
				m.theme.swatches[idx].swatch = updated
				return m, cmd
			}
		}
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.panelTab == 2 {
			return m.updateThemeKey(msg)
		}
		if m.panelTab == 1 {
			switch {
			case key.Matches(msg, saveKeys):
				return m, m.submitRepoSave()
			case msg.String() == "esc":
				return m, func() tea.Msg {
					return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Cancelled: true}}
				}
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
			return m, func() tea.Msg {
				return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Cancelled: true}}
			}
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
		// When focus is on a dropdown field, forward navigation/open keys to
		// it. The profile dropdown keeps the add/delete shortcuts so the
		// list can still be managed from the keyboard.
		if dk := m.focusedDropdownKind(); dk != ddNone {
			if dk == ddProfile {
				switch msg.String() {
				case "n":
					return m, m.addNewProfile()
				case "d":
					return m, m.deleteSelectedProfile()
				}
			}
			// The model-list field doubles as a fetch trigger until a list has
			// been fetched: enter/space starts the async ListModels call.
			if dk == ddModel && !m.modelDD.Built() {
				switch msg.String() {
				case "enter", " ":
					return m, m.fetchModelsCmd()
				}
				return m, nil
			}
			return m, m.forwardToDropdown(dk, msg)
		}
		var cmd tea.Cmd
		if ti := m.focusedInput(); ti != nil {
			updated, c := ti.Update(msg)
			*ti = updated
			cmd = c
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
	m.focus = (m.focus + delta + fieldAICount) % fieldAICount
	if ti := m.focusedInput(); ti != nil {
		ti.Focus()
	}
	m.syncDropdownFocus()
}

func (m *Model) setPanelTab(tab int) {
	const tabCount = 3
	if tab < 0 {
		tab = 0
	}
	if tab >= tabCount {
		tab = tabCount - 1
	}
	if m.panelTab == tab {
		return
	}
	m.panelTab = tab
	m.blurInputs()
	m.blurRepoInputs()
	switch m.panelTab {
	case 1:
		if m.repoFocus < 0 || m.repoFocus >= repoFieldCount {
			m.repoFocus = 0
		}
		m.focusRepoField(m.repoFocus)
	case 2:
		// Theme tab: focus is owned by the themePanel; nothing to do here
		// beyond blurring the form inputs above.
	default:
		m.focus = fieldStrictness
	}
	m.syncDropdownFocus()
}

// updateThemeKey handles key events while the Theme tab is focused (and no
// swatch modal is open — that case is short-circuited in Update above).
func (m *Model) updateThemeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, saveKeys):
		return m, m.submitThemeSave()
	}
	switch msg.String() {
	case "esc":
		return m, func() tea.Msg {
			return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Cancelled: true}}
		}
	case "[":
		m.setPanelTab(m.panelTab - 1)
		return m, textinput.Blink
	case "]":
		m.setPanelTab(m.panelTab + 1)
		return m, textinput.Blink
	case "tab", "ctrl+i", "down", "j":
		if m.theme != nil {
			m.theme.advanceFocus(1)
		}
		return m, nil
	case "shift+tab", "up", "k":
		if m.theme != nil {
			m.theme.advanceFocus(-1)
		}
		return m, nil
	case "enter", " ":
		if m.theme != nil {
			return m, m.theme.openFocused()
		}
		return m, nil
	case "r":
		if m.theme != nil {
			m.theme.resetAll()
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) blurInputs() {
	m.profileName.Blur()
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
		repoFieldParallelSpecs, repoFieldParallelPRAgents, repoFieldParallelExperts:
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
	case repoFieldParallelPRAgents:
		m.repoParallelPRAgents = !m.repoParallelPRAgents
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

// focusAIField focuses a specific AI-profile text field (the click
// equivalent of tabbing to it). field must be one of the fieldProfile*
// / fieldProvider / … text-field constants.
func (m *Model) focusAIField(field int) {
	m.blurInputs()
	m.focus = field
	if ti := m.focusedInput(); ti != nil {
		ti.Focus()
	}
}

func (m *Model) focusedInput() *textinput.Model {
	switch m.focus {
	case fieldProfileName:
		return &m.profileName
	case fieldBaseURL:
		return &m.baseURL
	case fieldModel:
		return &m.model
	case fieldAPIKey:
		return &m.apiKey
	case fieldTimeout:
		return &m.timeout
	default:
		return nil
	}
}

// activateSelectedProfile sets the selected row as the active profile.
func (m *Model) activateSelectedProfile() tea.Cmd {
	if m == nil || m.draft == nil {
		return nil
	}
	if m.selectedProfileIdx < 0 || m.selectedProfileIdx >= len(m.draft.Profiles) {
		return nil
	}
	m.commitEditorToSelectedProfile()
	m.draft.ActiveProfile = m.draft.Profiles[m.selectedProfileIdx].Name
	// Mirror onto top-level fields so callers reading cfg.Provider see the
	// active profile while still in the settings overlay.
	m.draft.Provider = m.draft.Profiles[m.selectedProfileIdx].Provider
	m.draft.BaseURL = m.draft.Profiles[m.selectedProfileIdx].BaseURL
	m.draft.Model = m.draft.Profiles[m.selectedProfileIdx].Model
	m.draft.APIKey = m.draft.Profiles[m.selectedProfileIdx].APIKey
	m.draft.TimeoutSec = m.draft.Profiles[m.selectedProfileIdx].TimeoutSec
	m.refreshProfileDropdown()
	return nil
}

// addNewProfile appends a fresh profile with a unique placeholder name and
// focuses it for editing.
func (m *Model) addNewProfile() tea.Cmd {
	if m == nil || m.draft == nil {
		return nil
	}
	m.commitEditorToSelectedProfile()
	base := "profile"
	name := base
	i := 2
	for {
		clash := false
		for _, p := range m.draft.Profiles {
			if strings.EqualFold(strings.TrimSpace(p.Name), name) {
				clash = true
				break
			}
		}
		if !clash {
			break
		}
		name = base + "-" + strconv.Itoa(i)
		i++
	}
	m.draft.Profiles = append(m.draft.Profiles, aiconfig.Profile{
		Name:       name,
		Provider:   aiconfig.ProviderClaude,
		TimeoutSec: 300,
	})
	m.selectedProfileIdx = len(m.draft.Profiles) - 1
	m.loadEditorFromSelectedProfile()
	m.refreshProfileDropdown()
	m.focus = fieldProfileName
	m.blurInputs()
	m.profileName.Focus()
	m.syncDropdownFocus()
	return textinput.Blink
}

// deleteSelectedProfile removes the selected profile (no-op when only one
// remains; ActiveProfile shifts when the deleted entry was active).
func (m *Model) deleteSelectedProfile() tea.Cmd {
	if m == nil || m.draft == nil {
		return nil
	}
	if len(m.draft.Profiles) <= 1 {
		return nil
	}
	if m.selectedProfileIdx < 0 || m.selectedProfileIdx >= len(m.draft.Profiles) {
		return nil
	}
	deleted := m.draft.Profiles[m.selectedProfileIdx].Name
	m.draft.Profiles = append(m.draft.Profiles[:m.selectedProfileIdx], m.draft.Profiles[m.selectedProfileIdx+1:]...)
	if strings.EqualFold(strings.TrimSpace(m.draft.ActiveProfile), strings.TrimSpace(deleted)) {
		m.draft.ActiveProfile = m.draft.Profiles[0].Name
	}
	if m.selectedProfileIdx >= len(m.draft.Profiles) {
		m.selectedProfileIdx = len(m.draft.Profiles) - 1
	}
	m.loadEditorFromSelectedProfile()
	m.refreshProfileDropdown()
	return nil
}

func (m *Model) submitSave() tea.Cmd {
	return func() tea.Msg {
		m.commitEditorToSelectedProfile()
		// Validate every profile (provider + timeout) before persisting so
		// the on-disk file isn't left half-valid on a typo.
		for i, p := range m.draft.Profiles {
			parsed, err := aiconfig.ParseProvider(strings.TrimSpace(string(p.Provider)))
			if err != nil {
				return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Err: fmt.Errorf("profile %q: %w", p.Name, err)}}
			}
			m.draft.Profiles[i].Provider = parsed
			if strings.TrimSpace(p.Name) == "" {
				return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Err: fmt.Errorf("profile at row %d has empty name", i+1)}}
			}
			if p.TimeoutSec <= 0 {
				m.draft.Profiles[i].TimeoutSec = 300
			}
		}
		rs := m.draft.ReviewStrictness
		if _, err := aiconfig.ParseReviewStrictness(string(rs)); err != nil {
			rs = strictnessAt(m.strictnessDD.SelectedIndex())
		}
		m.draft.ReviewStrictness = rs
		// R8: surface provider-specific configuration problems here — on
		// save — instead of at first inference. Validate the active profile
		// (the one a review will actually use) and block the save with a
		// clear message when it is not runnable (missing base URL / key, or
		// the claude CLI not on PATH).
		if err := m.draft.Active().ValidateForProvider(); err != nil {
			return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Err: err}}
		}
		if err := aiconfig.Save(m.draft, ""); err != nil {
			return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Err: err}}
		}
		// Re-load so the returned config has the active profile mirrored
		// onto top-level fields the way callers expect.
		fresh, err := aiconfig.Load()
		if err != nil {
			return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Err: err}}
		}
		return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Cfg: fresh}}
	}
}

func (m *Model) submitRepoSave() tea.Cmd {
	return func() tea.Msg {
		roots, err := repoconfig.ParseRepoRootsLines(m.repoRoots.Value())
		if err != nil {
			return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Err: err}}
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
			return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Err: err}}
		}
		ttl, err := parseInt("Context cache TTL (seconds)", m.repoTTL.Value(), 60)
		if err != nil {
			return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Err: err}}
		}
		prLim, err := parseInt("Merged PR list limit", m.repoPRHistLimit.Value(), 1)
		if err != nil {
			return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Err: err}}
		}
		exPR, err := parseInt("Repo expert history PR sample", m.repoExpertPRs.Value(), 1)
		if err != nil {
			return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Err: err}}
		}
		exMax, err := parseInt("Repo expert history max bytes", m.repoExpertMaxB.Value(), 1024)
		if err != nil {
			return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Err: err}}
		}
		exTTL, err := parseInt("Repo expert history cache TTL (seconds)", m.repoExpertTTL.Value(), 60)
		if err != nil {
			return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Err: err}}
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
			ParallelPRAgents:           m.repoParallelPRAgents,
			ParallelRepoExperts:        m.repoParallelExperts,
			RepoExpertReviewPRs:        exPR,
			RepoExpertMaxBytes:         exMax,
			RepoExpertReviewTTLSeconds: exTTL,
		}
		cfg.Normalize()
		if err := repoconfig.Save(&cfg, ""); err != nil {
			return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Err: err}}
		}
		return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack}}
	}
}

func (m *Model) renderTabStrip() string {
	t0 := " Review & AI "
	t1 := " Repo context "
	t2 := " Theme "
	style := func(label string, active bool) string {
		if active {
			return okStyle.Render(label)
		}
		return dimStyle.Render(label)
	}
	row := zone.Mark(ZoneSettingsTabReview, style(t0, m.panelTab == 0)) + "  " +
		zone.Mark(ZoneSettingsTabRepoCtx, style(t1, m.panelTab == 1)) + "  " +
		zone.Mark(ZoneSettingsTabTheme, style(t2, m.panelTab == 2))
	return row + "\n" + dimStyle.Render("click tabs or [ ] to switch") + "\n"
}

// fieldLabel renders a label, bolded when the field is currently focused.
func (m *Model) fieldLabel(label string, field int) string {
	if m.focus == field {
		return boldStyle.Render(label)
	}
	return label
}

func (m *Model) buildForm() string {
	var b strings.Builder
	b.WriteString(boldStyle.Render("Settings") + "\n\n")
	b.WriteString(m.renderTabStrip())
	b.WriteString("\n")
	if m.panelTab == 0 {
		b.WriteString(dimStyle.Render("tab / shift+tab fields · enter open menu · ctrl+s save · esc cancel · [ ] repo tab") + "\n\n")

		// Review strictness dropdown.
		b.WriteString(m.fieldLabel("Review strictness", fieldStrictness) + "\n")
		m.ddStrictRow = strings.Count(b.String(), "\n")
		b.WriteString(zone.Mark(ZoneStrictnessDD, m.strictnessDD.TriggerView()) + "\n")
		b.WriteString(dimStyle.Render(strictnessHint(m.strictnessDD.SelectedIndex())) + "\n\n")

		// AI profile dropdown + action buttons.
		b.WriteString(m.fieldLabel("AI profile", fieldProfilePicker) + "\n")
		m.ddProfileRow = strings.Count(b.String(), "\n")
		b.WriteString(zone.Mark(ZoneProfileDD, m.profileDD.TriggerView()) + "\n")
		b.WriteString(zone.Mark(ZoneProfileSetActive, okStyle.Render(" Set active ")) + "  ")
		b.WriteString(zone.Mark(ZoneProfileAdd, okStyle.Render(" + Add ")) + "  ")
		b.WriteString(zone.Mark(ZoneProfileDelete, errStyle.Render(" Delete ")) + "\n\n")

		b.WriteString(boldStyle.Render("Edit profile") + "\n")
		b.WriteString(dimStyle.Render("changes apply to the selected profile; click 'Set active' to use it for reviews") + "\n\n")
		b.WriteString(zone.Mark(ZoneAIFieldName, m.fieldLabel("name", fieldProfileName)+"\n"+m.profileName.View()) + "\n\n")

		// Provider preset picker (fills provider + base URL from a known endpoint).
		b.WriteString(m.fieldLabel("preset (fills provider + base URL)", fieldPreset) + "\n")
		m.ddPresetRow = strings.Count(b.String(), "\n")
		b.WriteString(zone.Mark(ZonePresetDD, m.presetDD.TriggerView()) + "\n")
		b.WriteString(dimStyle.Render(m.presetHint()) + "\n\n")

		// Provider dropdown.
		b.WriteString(m.fieldLabel("provider", fieldProvider) + "\n")
		m.ddProviderRow = strings.Count(b.String(), "\n")
		b.WriteString(zone.Mark(ZoneProviderDD, m.providerDD.TriggerView()) + "\n\n")

		b.WriteString(zone.Mark(ZoneAIFieldBaseURL, m.fieldLabel("base URL", fieldBaseURL)+"\n"+m.baseURL.View()) + "\n\n")
		b.WriteString(zone.Mark(ZoneAIFieldModel, m.fieldLabel("model", fieldModel)+"\n"+m.model.View()) + "\n\n")

		// Model picker (Phase 6 item 3): before a fetch, a button; after a
		// successful fetch, a dropdown whose selection fills the model field.
		m.buildModelPicker(&b)

		b.WriteString(zone.Mark(ZoneAIFieldAPIKey, m.fieldLabel("API key (masked)", fieldAPIKey)+"\n"+m.apiKey.View()) + "\n\n")
		b.WriteString(zone.Mark(ZoneAIFieldTimeout, m.fieldLabel("timeout (sec)", fieldTimeout)+"\n"+m.timeout.View()) + "\n\n")
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

// presetHint returns the one-line description of the currently selected preset
// (blank for "(custom)"), shown under the preset trigger.
func (m *Model) presetHint() string {
	i := m.presetDD.SelectedIndex()
	if i <= 0 {
		return "keep the fields below as-is, or pick a known provider endpoint"
	}
	presets := aiconfig.ProviderPresets()
	if i-1 < len(presets) {
		return presets[i-1].Notes
	}
	return ""
}

// buildModelPicker writes the model-listing UI into b: a fetch button until a
// list has been retrieved, then a picker dropdown. It records ddModelRow (the
// trigger's content-line index in the whole form) so an open panel composites
// at the right row.
func (m *Model) buildModelPicker(b *strings.Builder) {
	b.WriteString(m.fieldLabel("model list", fieldModelList) + "\n")
	if m.modelDD.Built() {
		m.ddModelRow = strings.Count(b.String(), "\n")
		b.WriteString(zone.Mark(ZoneModelDD, m.modelDD.TriggerView()) + "\n")
		b.WriteString(dimStyle.Render("pick to fill the model field above") + "\n\n")
		return
	}
	status := "enter / click to fetch models for this provider"
	if m.modelsLoading {
		status = "fetching models…"
	} else if m.modelsErr != "" {
		status = "fetch failed: " + m.modelsErr + " — type the model above"
	}
	b.WriteString(zone.Mark(ZoneModelFetch, okStyle.Render(" ⟳ Fetch models ")) + "\n")
	b.WriteString(dimStyle.Render(status) + "\n\n")
}

// composeDropdownOverlays composites any open dropdown panel onto body. Each
// Host sizes/positions its own trigger bounds in settings-body-local
// coordinates (the trigger's on-screen row = its recorded content line index
// minus the viewport scroll offset).
func (m *Model) composeDropdownOverlays(body string) string {
	off := m.vp.YOffset
	body = m.strictnessDD.Composite(body, m.ddStrictRow-off, 0, m.width, m.bodyH)
	body = m.profileDD.Composite(body, m.ddProfileRow-off, 0, m.width, m.bodyH)
	body = m.providerDD.Composite(body, m.ddProviderRow-off, 0, m.width, m.bodyH)
	body = m.presetDD.Composite(body, m.ddPresetRow-off, 0, m.width, m.bodyH)
	body = m.modelDD.Composite(body, m.ddModelRow-off, 0, m.width, m.bodyH)
	return body
}

// View renders the scrollable settings body (no header/status — root adds those).
func (m *Model) View() string {
	// The Theme tab bypasses the viewport so each swatch's row index lines
	// up with the cell where its colour cell is drawn — bubble-color-picker
	// uses those coordinates to centre the modal overlay.
	if m.panelTab == 2 && m.theme != nil {
		panel := m.buildThemeView()
		panel = m.theme.applyOverlays(panel, m.width, m.bodyH)
		return lipgloss.NewStyle().Width(m.width).MaxWidth(m.width).Height(m.bodyH).Render(panel)
	}
	if m.panelTab == 0 {
		// Keep the profile dropdown's labels/selection fresh (no-op while
		// its panel is open, which is the only time labels can't change).
		m.refreshProfileDropdown()
	}
	// Rebuild each frame so textinput cursor blink and zones stay aligned with layout.
	m.vp.SetContent(m.buildForm())
	body := lipgloss.NewStyle().Width(m.width).MaxWidth(m.width).Height(m.bodyH).Render(m.vp.View())
	if m.panelTab == 0 {
		body = m.composeDropdownOverlays(body)
	}
	return body
}

// SetContentOrigin records the absolute terminal row where the settings body
// is drawn (the chrome header height). The root calls this so an open
// dropdown's geometric mouse hit-testing lines up with the rendered panel.
func (m *Model) SetContentOrigin(top int) {
	if top < 0 {
		top = 0
	}
	m.contentTop = top
	m.strictnessDD.ContentTop = top
	m.profileDD.ContentTop = top
	m.providerDD.ContentTop = top
	m.presetDD.ContentTop = top
	if m.modelDD != nil {
		m.modelDD.ContentTop = top
	}
}

// buildThemeView assembles the Theme tab body (header + tab strip + panel).
// The line offset of the tab strip is reflected in each swatch's recorded
// row by themePanel.renderPanel, so SetBounds matches the rendered cell.
func (m *Model) buildThemeView() string {
	var b strings.Builder
	b.WriteString(boldStyle.Render("Settings") + "\n\n")
	b.WriteString(m.renderTabStrip())
	b.WriteString("\n")
	// Hand the rest of the layout to the theme panel; the row indices it
	// records are panel-local, but the parent settings view above is fixed
	// height so we add that offset before SetBounds is consulted.
	headerLines := lipgloss.Height(b.String())
	body := m.theme.renderPanel(m.width)
	// Adjust each swatch row by the prelude height so SetBounds reflects
	// the swatch's actual line within m.View().
	for _, sw := range m.theme.swatches {
		sw.row += headerLines
		sw.swatch.SetBounds(sw.row, sw.col, 0, 0)
	}
	b.WriteString(body)
	return b.String()
}

// submitThemeSave persists the draft theme and applies it live so any open
// review or list view re-renders with the new colours.
func (m *Model) submitThemeSave() tea.Cmd {
	return func() tea.Msg {
		if m.theme == nil {
			return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack}}
		}
		if err := theme.Save(m.theme.draft, ""); err != nil {
			return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Err: err}}
		}
		theme.Apply(m.theme.draft)
		return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack}}
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
	b.WriteString(zone.Mark(ZoneRepoFieldRoots, m.repoRoots.View()))
	b.WriteString("\n\n")

	writeHdr("Context bundle")
	b.WriteString(writeFocus(repoFieldMaxBytes))
	b.WriteString("max_bytes (injected context cap)\n")
	b.WriteString(zone.Mark(ZoneRepoFieldMaxBytes, m.repoMaxBytes.View()) + "\n")
	b.WriteString(writeFocus(repoFieldTTL))
	b.WriteString("ttl_seconds (on-disk context bundle cache)\n")
	b.WriteString(zone.Mark(ZoneRepoFieldTTL, m.repoTTL.View()) + "\n\n")

	writeHdr("Merged PR culture")
	b.WriteString(writeFocus(repoFieldIncludePR))
	b.WriteString(m.renderRepoToggle("Include merged PR titles in bundle", m.repoIncludePR, ZoneRepoToggleIncludePR) + "\n")
	b.WriteString(writeFocus(repoFieldPRHistLimit))
	b.WriteString("pr_history_limit\n")
	b.WriteString(zone.Mark(ZoneRepoFieldPRHistLimit, m.repoPRHistLimit.View()) + "\n")
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
	b.WriteString(writeFocus(repoFieldParallelPRAgents))
	b.WriteString(m.renderRepoToggle("Run PR agents in parallel with the specialists (faster; may hit rate limits)", m.repoParallelPRAgents, ZoneRepoToggleParallelPRAgents) + "\n")
	b.WriteString(writeFocus(repoFieldParallelExperts))
	b.WriteString(m.renderRepoToggle("[deprecated] parallel repo experts (no effect — repo agents replaced this)", m.repoParallelExperts, ZoneRepoToggleParallelExperts) + "\n")
	b.WriteString(dimStyle.Render("Repo-agent generation reuses these review-history digest knobs:") + "\n")
	b.WriteString(writeFocus(repoFieldExpertPRs))
	b.WriteString("repo_expert_review_prs (merged PRs sampled for review-body digest)\n")
	b.WriteString(zone.Mark(ZoneRepoFieldExpertPRs, m.repoExpertPRs.View()) + "\n")
	b.WriteString(writeFocus(repoFieldExpertMaxB))
	b.WriteString("repo_expert_max_bytes (digest size cap)\n")
	b.WriteString(zone.Mark(ZoneRepoFieldExpertMaxB, m.repoExpertMaxB.View()) + "\n")
	b.WriteString(writeFocus(repoFieldExpertTTL))
	b.WriteString("repo_expert_review_ttl_seconds (digest disk cache)\n")
	b.WriteString(zone.Mark(ZoneRepoFieldExpertTTL, m.repoExpertTTL.View()) + "\n")

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
