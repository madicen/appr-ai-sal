package settings

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/tui/util/dropdown"
)

// Dropdown kinds on the Review & AI tab. ddNone means no dropdown.
const (
	ddNone = iota
	ddStrictness
	ddProfile
	ddProvider
	ddPreset
	ddModel
)

// providerDDValues maps the provider dropdown's option index to the
// aiconfig.Provider it selects; providerDDLabels are the visible strings.
var (
	providerDDValues = []aiconfig.Provider{
		aiconfig.ProviderClaude,
		aiconfig.ProviderAnthropic,
		aiconfig.ProviderGemini,
		aiconfig.ProviderOllama,
		aiconfig.ProviderOpenAICompatible,
		aiconfig.ProviderAzure,
	}
	providerDDLabels = []string{"claude", "anthropic", "gemini", "ollama", "openai_compatible", "azure"}

	// strictnessDDLabels index order matches strictnessAt / strictnessIndex.
	strictnessDDLabels = []string{"critical-only", "lenient", "balanced", "strict"}
)

// presetDDLabels are the preset picker options: a leading "(custom)" no-op
// entry followed by each built-in provider preset. Applying a non-custom entry
// fills the provider + base URL (+ Azure api-version). presetDDIndexToPreset
// converts the option index to a preset index into aiconfig.ProviderPresets.
func presetDDLabels() []string {
	presets := aiconfig.ProviderPresets()
	labels := make([]string, 0, len(presets)+1)
	labels = append(labels, "(custom)")
	for _, p := range presets {
		labels = append(labels, p.Name)
	}
	return labels
}

// providerDDIndex returns the dropdown index for a provider (0 when unknown).
func providerDDIndex(p aiconfig.Provider) int {
	for i, v := range providerDDValues {
		if v == p {
			return i
		}
	}
	return 0
}

// strictnessHint is the one-line description shown under the strictness
// dropdown for the option at index i.
func strictnessHint(i int) string {
	switch i {
	case 0:
		return "only merge-blocking (critical) findings"
	case 1:
		return "error + critical only"
	case 3:
		return "info-level nits included"
	default:
		return "warning and above (default)"
	}
}

// initDropdowns builds the three Review & AI dropdowns through the shared
// dropdown.Host, wiring each one's OnSelect to mirror its selection onto the
// draft (strictness / profile) or leaving it read-at-commit (provider). It is
// called once from New; refreshProfileDropdown keeps the profile list current.
func (m *Model) initDropdowns(selProv aiconfig.Provider) {
	m.strictnessDD = dropdown.New("strictness")
	m.strictnessDD.OnSelect = func(idx int) tea.Cmd {
		m.draft.ReviewStrictness = strictnessAt(idx)
		return nil
	}
	m.strictnessDD.Rebuild(strictnessDDLabels, strictnessIndex(m.draft.ReviewStrictness))

	// The provider value lives on the dropdown and is read by
	// commitEditorToSelectedProfile, so it needs no OnSelect mirror.
	m.providerDD = dropdown.New("provider")
	m.providerDD.Rebuild(providerDDLabels, providerDDIndex(selProv))

	m.profileDD = dropdown.New("profile")
	m.profileDD.OnSelect = func(idx int) tea.Cmd {
		m.selectProfileByIndex(idx)
		return nil
	}
	m.refreshProfileDropdown()

	// Preset picker: applying a non-"(custom)" entry fills provider + base URL
	// (+ Azure api-version) onto the edited profile. Manual entry still works.
	m.presetDD = dropdown.New("preset")
	m.presetDD.OnSelect = func(idx int) tea.Cmd {
		m.applyPreset(idx)
		return nil
	}
	m.presetDD.Rebuild(presetDDLabels(), 0)
}

// applyPreset copies preset option idx (0 = "(custom)", a no-op) onto the
// selected profile and reloads the editor. It commits in-progress edits first
// so nothing is lost.
func (m *Model) applyPreset(idx int) {
	if idx <= 0 { // 0 = "(custom)"
		return
	}
	presets := aiconfig.ProviderPresets()
	pi := idx - 1
	if pi < 0 || pi >= len(presets) {
		return
	}
	preset := presets[pi]
	m.commitEditorToSelectedProfile()
	if m.selectedProfileIdx < 0 || m.selectedProfileIdx >= len(m.draft.Profiles) {
		return
	}
	prof := m.draft.Profiles[m.selectedProfileIdx]
	prof.Provider = preset.Provider
	prof.BaseURL = preset.BaseURL
	if preset.Provider == aiconfig.ProviderAzure {
		prof.AzureAPIVersion = preset.APIVersion
	}
	m.draft.Profiles[m.selectedProfileIdx] = prof
	m.loadEditorFromSelectedProfile()
}

// profileDDLabels builds the profile dropdown options from the draft profile
// list, marking the active profile so the trigger conveys both the edited and
// the active selection.
func (m *Model) profileDDLabels() []string {
	labels := make([]string, len(m.draft.Profiles))
	for i, p := range m.draft.Profiles {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			name = "(unnamed)"
		}
		if strings.EqualFold(strings.TrimSpace(p.Name), strings.TrimSpace(m.draft.ActiveProfile)) {
			name += " (active)"
		}
		labels[i] = name
	}
	return labels
}

// refreshProfileDropdown rebuilds the profile dropdown so its labels (names,
// active marker) and selection track the draft. The Host skips the rebuild
// while the panel is open (no structural change can be in flight then).
func (m *Model) refreshProfileDropdown() {
	if m.profileDD.Open() {
		return
	}
	idx := m.selectedProfileIdx
	if idx < 0 || idx >= len(m.draft.Profiles) {
		idx = 0
	}
	m.profileDD.Rebuild(m.profileDDLabels(), idx)
	m.syncDropdownFocus()
}

// dropdownForKind returns the Host backing a dropdown kind (nil for ddNone).
func (m *Model) dropdownForKind(kind int) *dropdown.Host {
	switch kind {
	case ddStrictness:
		return m.strictnessDD
	case ddProfile:
		return m.profileDD
	case ddProvider:
		return m.providerDD
	case ddPreset:
		return m.presetDD
	case ddModel:
		return m.modelDD
	}
	return nil
}

// openDropdownKind reports which Review & AI dropdown panel is open (ddNone
// when none). At most one is open at a time.
func (m *Model) openDropdownKind() int {
	switch {
	case m.strictnessDD.Open():
		return ddStrictness
	case m.profileDD.Open():
		return ddProfile
	case m.providerDD.Open():
		return ddProvider
	case m.presetDD.Open():
		return ddPreset
	case m.modelDD.Open():
		return ddModel
	}
	return ddNone
}

// anyDropdownOpen reports whether a Review & AI dropdown panel is open.
func (m *Model) anyDropdownOpen() bool { return m.openDropdownKind() != ddNone }

// focusedDropdownKind maps the current keyboard focus to a dropdown kind.
func (m *Model) focusedDropdownKind() int {
	switch m.focus {
	case fieldStrictness:
		return ddStrictness
	case fieldProfilePicker:
		return ddProfile
	case fieldPreset:
		return ddPreset
	case fieldProvider:
		return ddProvider
	case fieldModelList:
		return ddModel
	default:
		return ddNone
	}
}

// fieldForDropdown returns the focus field constant for a dropdown kind.
func fieldForDropdown(kind int) int {
	switch kind {
	case ddStrictness:
		return fieldStrictness
	case ddProfile:
		return fieldProfilePicker
	case ddPreset:
		return fieldPreset
	case ddProvider:
		return fieldProvider
	case ddModel:
		return fieldModelList
	default:
		return fieldStrictness
	}
}

// syncDropdownFocus sets the focused-arrow indicator on whichever dropdown
// currently owns keyboard focus (only on the Review & AI tab).
func (m *Model) syncDropdownFocus() {
	fk := ddNone
	if m.panelTab == 0 {
		fk = m.focusedDropdownKind()
	}
	m.strictnessDD.SetFocused(fk == ddStrictness)
	m.profileDD.SetFocused(fk == ddProfile)
	m.providerDD.SetFocused(fk == ddProvider)
	m.presetDD.SetFocused(fk == ddPreset)
	m.modelDD.SetFocused(fk == ddModel)
}

// forwardToDropdown routes msg to the dropdown of kind. The Host translates
// mouse coordinates into settings-body-local space (see ContentTop) and
// mirrors any selection change onto the draft via its OnSelect callback.
func (m *Model) forwardToDropdown(kind int, msg tea.Msg) tea.Cmd {
	return m.dropdownForKind(kind).Forward(msg)
}

// handleDropdownResult applies an ItemChosenMsg / ItemCanceledMsg to the open
// dropdown (closing it and recording the choice).
func (m *Model) handleDropdownResult(msg tea.Msg) tea.Cmd {
	kind := m.openDropdownKind()
	if kind == ddNone {
		return nil
	}
	return m.forwardToDropdown(kind, msg)
}

// selectProfileByIndex commits in-progress edits, switches the edited profile
// to idx, and reloads the editor fields (including the provider dropdown).
func (m *Model) selectProfileByIndex(idx int) {
	if idx < 0 || idx >= len(m.draft.Profiles) || idx == m.selectedProfileIdx {
		return
	}
	m.commitEditorToSelectedProfile()
	m.selectedProfileIdx = idx
	m.loadEditorFromSelectedProfile()
}

// editedProvider returns the provider currently selected in the dropdown.
func (m *Model) editedProvider() aiconfig.Provider {
	i := m.providerDD.SelectedIndex()
	if i < 0 || i >= len(providerDDValues) {
		return aiconfig.ProviderClaude
	}
	return providerDDValues[i]
}
