package settings

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
	bubbledropdown "github.com/madicen/bubble-dropdown"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// Dropdown kinds on the Review & AI tab. ddNone means no dropdown.
const (
	ddNone = iota
	ddStrictness
	ddProfile
	ddProvider
)

// providerDDValues maps the provider dropdown's option index to the
// aiconfig.Provider it selects; providerDDLabels are the visible strings.
var (
	providerDDValues = []aiconfig.Provider{
		aiconfig.ProviderClaude,
		aiconfig.ProviderGemini,
		aiconfig.ProviderOllama,
		aiconfig.ProviderOpenAICompatible,
	}
	providerDDLabels = []string{"claude", "gemini", "ollama", "openai_compatible"}

	// strictnessDDLabels index order matches strictnessAt / strictnessIndex.
	strictnessDDLabels = []string{"critical-only", "lenient", "balanced", "strict"}
)

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

func newSettingsDropdown(labels []string, idx int, placeholder string) *bubbledropdown.Dropdown {
	d := bubbledropdown.New(
		bubbledropdown.WithOptions(labels),
		bubbledropdown.WithInitialIndex(idx),
		bubbledropdown.WithPlaceholder(placeholder),
	)
	// Match the existing bubble-color-picker integration: use the global
	// bubblezone manager so the trigger is hit-tested via the root zone.Scan.
	d.SetZoneManager(zone.DefaultManager)
	return d
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

// newProfileDropdown builds a profile dropdown reflecting the current draft.
// The component has no runtime SetOptions, so the dropdown is recreated
// whenever the option set changes (see refreshProfileDropdown).
func (m *Model) newProfileDropdown() *bubbledropdown.Dropdown {
	idx := m.selectedProfileIdx
	if idx < 0 || idx >= len(m.draft.Profiles) {
		idx = 0
	}
	return newSettingsDropdown(m.profileDDLabels(), idx, "profile")
}

// refreshProfileDropdown rebuilds the profile dropdown so its labels (names,
// active marker) and selection track the draft. Only runs while the panel is
// closed; an open panel implies no structural change can be in flight.
func (m *Model) refreshProfileDropdown() {
	if m.profileDD != nil && m.profileDD.Open() {
		return
	}
	m.profileDD = m.newProfileDropdown()
	m.syncDropdownFocus()
}

// openDropdownKind reports which Review & AI dropdown panel is open (ddNone
// when none). At most one is open at a time.
func (m *Model) openDropdownKind() int {
	switch {
	case m.strictnessDD != nil && m.strictnessDD.Open():
		return ddStrictness
	case m.profileDD != nil && m.profileDD.Open():
		return ddProfile
	case m.providerDD != nil && m.providerDD.Open():
		return ddProvider
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
	case fieldProvider:
		return ddProvider
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
	case ddProvider:
		return fieldProvider
	default:
		return fieldStrictness
	}
}

// dropdownPtr returns the address of the dropdown field for kind so Update
// results can be written back (Dropdown.Update returns a fresh pointer).
func (m *Model) dropdownPtr(kind int) **bubbledropdown.Dropdown {
	switch kind {
	case ddStrictness:
		return &m.strictnessDD
	case ddProfile:
		return &m.profileDD
	case ddProvider:
		return &m.providerDD
	default:
		return nil
	}
}

// syncDropdownFocus sets the focused-arrow indicator on whichever dropdown
// currently owns keyboard focus (only on the Review & AI tab).
func (m *Model) syncDropdownFocus() {
	fk := ddNone
	if m.panelTab == 0 {
		fk = m.focusedDropdownKind()
	}
	if m.strictnessDD != nil {
		m.strictnessDD.SetFocused(fk == ddStrictness)
	}
	if m.profileDD != nil {
		m.profileDD.SetFocused(fk == ddProfile)
	}
	if m.providerDD != nil {
		m.providerDD.SetFocused(fk == ddProvider)
	}
}

// forwardToDropdown routes msg to the dropdown of kind, translating mouse
// coordinates from absolute screen space into the settings-body-local space
// the open panel uses for geometric hit-testing, then applies the resulting
// selection to the draft. Returns any tea.Cmd the dropdown emits.
func (m *Model) forwardToDropdown(kind int, msg tea.Msg) tea.Cmd {
	pp := m.dropdownPtr(kind)
	if pp == nil || *pp == nil {
		return nil
	}
	if mm, ok := msg.(tea.MouseMsg); ok {
		mm.Y -= m.contentTop
		msg = mm
	}
	updated, cmd := (*pp).Update(msg)
	*pp = updated
	m.applyDropdownSelection(kind)
	return cmd
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

// applyDropdownSelection mirrors a dropdown's current selection onto the
// draft config: strictness updates the draft directly, provider is stored on
// the dropdown (read at commit time), and profile switches the edited profile.
func (m *Model) applyDropdownSelection(kind int) {
	switch kind {
	case ddStrictness:
		if m.strictnessDD != nil {
			m.draft.ReviewStrictness = strictnessAt(m.strictnessDD.SelectedIndex())
		}
	case ddProvider:
		// The provider value lives on the dropdown and is read by
		// commitEditorToSelectedProfile; nothing else to mirror here.
	case ddProfile:
		if m.profileDD != nil {
			m.selectProfileByIndex(m.profileDD.SelectedIndex())
		}
	}
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
	if m.providerDD == nil {
		return aiconfig.ProviderClaude
	}
	i := m.providerDD.SelectedIndex()
	if i < 0 || i >= len(providerDDValues) {
		return aiconfig.ProviderClaude
	}
	return providerDDValues[i]
}
