package settings

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

func keyDown() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyDown} }
func keyUp() tea.KeyMsg   { return tea.KeyMsg{Type: tea.KeyUp} }

// TestStrictnessDropdownCyclesDraft: with focus on the strictness dropdown,
// a down/up key cycles the selection and mirrors it onto the draft config
// without needing to open the panel.
func TestStrictnessDropdownCyclesDraft(t *testing.T) {
	m := New(Opts{Cfg: aiconfig.DefaultConfig(), Width: 120, BodyHeight: 120, StartSection: StartReview})
	if m.focus != fieldStrictness {
		t.Fatalf("StartReview should focus strictness, got %d", m.focus)
	}
	idx := m.strictnessDD.SelectedIndex()

	var key tea.KeyMsg
	var want int
	if idx < len(strictnessDDLabels)-1 {
		key, want = keyDown(), idx+1
	} else {
		key, want = keyUp(), idx-1
	}
	updated, _ := m.Update(key)
	m = updated.(*Model)

	if got := m.strictnessDD.SelectedIndex(); got != want {
		t.Fatalf("strictness index: want %d, got %d", want, got)
	}
	if m.draft.ReviewStrictness != strictnessAt(want) {
		t.Fatalf("draft strictness: want %v, got %v", strictnessAt(want), m.draft.ReviewStrictness)
	}
}

// TestProviderDropdownAppliesOnSave: cycling the provider dropdown changes the
// edited profile's provider, which is persisted on commit.
func TestProviderDropdownAppliesOnSave(t *testing.T) {
	m := New(Opts{Cfg: aiconfig.DefaultConfig(), Width: 120, BodyHeight: 120, StartSection: StartReview})

	// Tab focus over to the provider dropdown.
	for i := 0; i < fieldAICount && m.focus != fieldProvider; i++ {
		m.advanceFocus(1)
	}
	if m.focus != fieldProvider {
		t.Fatalf("could not focus provider field, stuck at %d", m.focus)
	}
	start := m.providerDD.SelectedIndex()

	var key tea.KeyMsg
	var want int
	if start < len(providerDDValues)-1 {
		key, want = keyDown(), start+1
	} else {
		key, want = keyUp(), start-1
	}
	updated, _ := m.Update(key)
	m = updated.(*Model)

	if got := m.providerDD.SelectedIndex(); got != want {
		t.Fatalf("provider index: want %d, got %d", want, got)
	}
	m.commitEditorToSelectedProfile()
	if got := m.draft.Profiles[m.selectedProfileIdx].Provider; got != providerDDValues[want] {
		t.Fatalf("committed provider: want %v, got %v", providerDDValues[want], got)
	}
}

// TestStrictnessTriggerClickOpensPanel: clicking the strictness trigger zone
// opens the dropdown panel.
func TestStrictnessTriggerClickOpensPanel(t *testing.T) {
	m := New(Opts{Cfg: aiconfig.DefaultConfig(), Width: 120, BodyHeight: 120, StartSection: StartReview})
	zone.Scan(m.View())

	_ = m.handleMouse(clickFieldZone(t, ZoneStrictnessDD))
	if !m.strictnessDD.Open() {
		t.Fatal("clicking the strictness trigger should open the dropdown panel")
	}
}

// TestProfileDropdownTracksAddDelete: the profile dropdown's option count
// follows the draft profile list as profiles are added and removed.
func TestProfileDropdownTracksAddDelete(t *testing.T) {
	m := New(Opts{Cfg: aiconfig.DefaultConfig(), Width: 120, BodyHeight: 120, StartSection: StartAI})
	before := len(m.draft.Profiles)

	_ = m.addNewProfile()
	if got := len(m.draft.Profiles); got != before+1 {
		t.Fatalf("after add: want %d profiles, got %d", before+1, got)
	}
	// The dropdown was recreated against the new list; selection points at
	// the freshly added profile.
	if m.profileDD.SelectedIndex() != len(m.draft.Profiles)-1 {
		t.Fatalf("profile dropdown should select the new profile, got %d", m.profileDD.SelectedIndex())
	}

	_ = m.deleteSelectedProfile()
	if got := len(m.draft.Profiles); got != before {
		t.Fatalf("after delete: want %d profiles, got %d", before, got)
	}
}
