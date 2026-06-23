package repoagents

import (
	"testing"

	ta "github.com/madicen/appr-ai-sal/internal/review/techagents"
)

func seedCandidates(m *Model) {
	m.candidates = []ta.Candidate{
		{Tech: "kafka", Label: "Kafka", Seed: "events", Rationale: "kafka-go"},
		{Tech: "terraform", Label: "Terraform", Seed: "infra", Rationale: "main.tf"},
	}
	m.candidateApproved = map[string]bool{}
}

// newTallModel returns a test model whose viewport is tall enough that the
// whole Tech experts section (which renders below the agent list) is visible
// to bubblezone's scanner — otherwise off-screen click zones never register.
func newTallModel(t *testing.T, repos []string) *Model {
	t.Helper()
	m := newTestModel(t, repos)
	m.Resize(160, 200)
	return m
}

func TestMouseClickSuggestMarksBusy(t *testing.T) {
	m := newTallModel(t, []string{"a/b"})
	renderAndScan(m)
	_ = m.handleMouse(clickCenter(t, ZoneSuggestTech))
	if !m.suggestBusy {
		t.Fatalf("clicking Suggest should set suggestBusy true")
	}
}

func TestMouseApproveCandidateTogglesState(t *testing.T) {
	m := newTallModel(t, []string{"a/b"})
	seedCandidates(m)
	renderAndScan(m)

	_ = m.handleMouse(clickCenter(t, zoneCandApprove("kafka")))
	if !m.candidateApproved["kafka"] {
		t.Fatalf("approve click should mark kafka approved")
	}
	if m.approvedCandidateCount() != 1 {
		t.Fatalf("approved count should be 1, got %d", m.approvedCandidateCount())
	}

	// Deny it again.
	renderAndScan(m)
	_ = m.handleMouse(clickCenter(t, zoneCandDeny("kafka")))
	if m.candidateApproved["kafka"] {
		t.Fatalf("deny click should clear kafka approval")
	}
}

func TestMouseGenerateApprovedDispatchesAndClears(t *testing.T) {
	m := newTallModel(t, []string{"a/b"})
	seedCandidates(m)
	m.candidateApproved["kafka"] = true
	renderAndScan(m)

	cmd := m.handleMouse(clickCenter(t, ZoneGenApproved))
	if cmd == nil {
		t.Fatalf("Generate approved should return a command")
	}
	if !m.busy[techBusyKey("a", "b", "kafka")] {
		t.Fatalf("approved candidate should be marked busy")
	}
	if m.busy[techBusyKey("a", "b", "terraform")] {
		t.Fatalf("non-approved candidate should not be generated")
	}
	if len(m.candidates) != 0 {
		t.Fatalf("candidate panel should be cleared after generate, got %d", len(m.candidates))
	}
}

func TestGenerateApprovedNoneApprovedSetsError(t *testing.T) {
	m := newTestModel(t, []string{"a/b"})
	seedCandidates(m)
	if cmd := m.generateApprovedCmd(); cmd != nil {
		t.Fatalf("expected nil cmd when nothing approved")
	}
	if m.err == nil {
		t.Fatalf("expected an error when generating with nothing approved")
	}
	// Candidates remain so the user can still approve some.
	if len(m.candidates) != 2 {
		t.Fatalf("candidates should be preserved when generate is rejected")
	}
}

func TestMouseDismissClearsCandidates(t *testing.T) {
	m := newTallModel(t, []string{"a/b"})
	seedCandidates(m)
	renderAndScan(m)
	_ = m.handleMouse(clickCenter(t, ZoneDismissSuggest))
	if len(m.candidates) != 0 {
		t.Fatalf("dismiss should clear candidates")
	}
}

func TestSuggestDoneIgnoredForStaleRepo(t *testing.T) {
	m := newTestModel(t, []string{"a/b", "c/d"})
	m.suggestBusy = true
	// Result for a repo that is not the current selection should be dropped.
	_, _ = m.Update(techSuggestDoneMsg{
		Owner:      "c",
		Repo:       "d",
		Candidates: []ta.Candidate{{Tech: "kafka", Label: "Kafka"}},
	})
	if len(m.candidates) != 0 {
		t.Fatalf("stale suggestion result should be ignored")
	}
	if m.suggestBusy {
		t.Fatalf("suggestBusy should be cleared even for stale results")
	}
}

func TestSuggestDoneStoresCandidates(t *testing.T) {
	m := newTestModel(t, []string{"a/b"})
	m.suggestBusy = true
	_, _ = m.Update(techSuggestDoneMsg{
		Owner:      "a",
		Repo:       "b",
		Candidates: []ta.Candidate{{Tech: "kafka", Label: "Kafka"}},
	})
	if len(m.candidates) != 1 {
		t.Fatalf("expected 1 stored candidate, got %d", len(m.candidates))
	}
	if m.suggestBusy {
		t.Fatalf("suggestBusy should be cleared after done")
	}
}

func TestSwitchingRepoResetsSuggestions(t *testing.T) {
	m := newTestModel(t, []string{"a/b", "c/d"})
	seedCandidates(m)
	m.moveNextRepo()
	if len(m.candidates) != 0 {
		t.Fatalf("switching repos should clear candidates")
	}
}
