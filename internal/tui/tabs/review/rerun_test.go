package review

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/review"
)

func testOverlayWithFailedDesign(t *testing.T) *Model {
	t.Helper()
	cfg := aiconfig.DefaultConfig()
	ro := New(80, 40, true, false, false, cfg, true)
	d := &review.Draft{
		Diff: "",
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecFormatting, Findings: nil},
			{Specialist: review.SpecDesign, Err: errors.New("boom"), Outcome: review.OutcomeFailed},
		},
		Rerun: &review.RerunContext{
			PreArbiterSpecialists: []review.SpecialistResult{
				{Specialist: review.SpecFormatting},
				{Specialist: review.SpecDesign, Err: errors.New("boom"), Outcome: review.OutcomeFailed},
			},
			RepoExpertPanel: true,
		},
		VibeCoach: &review.VibeCoachResult{Summary: "ok", Prompts: nil},
	}
	_ = ro.AdoptDraft(d)
	// Stay on overview for r/R tests.
	_ = ro.focusTab(0)
	// Point cursor at design.
	for i := range ro.agents {
		if ro.agents[i].name == review.SpecDesign {
			ro.agents[i].phase = oaErr
			ro.agents[i].err = errors.New("boom")
			ro.cursor = i
			break
		}
	}
	return ro
}

func TestKeyRTriggersRerunOnFailedAgent(t *testing.T) {
	ro := testOverlayWithFailedDesign(t)
	if !ro.canRerunNamed(review.SpecDesign) {
		t.Fatal("design should be re-runnable")
	}
	_, cmd := ro.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd == nil {
		t.Fatal("r on failed design should return a re-run cmd")
	}
	if !ro.rerunInFlight {
		t.Fatal("rerunInFlight should be set")
	}
}

func TestKeyRNoopOnCleanAgent(t *testing.T) {
	ro := testOverlayWithFailedDesign(t)
	for i := range ro.agents {
		if ro.agents[i].name == review.SpecFormatting {
			ro.agents[i].phase = oaDone
			ro.cursor = i
			break
		}
	}
	_, cmd := ro.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd != nil {
		t.Fatal("r on clean formatting must be a no-op")
	}
	if ro.rerunInFlight {
		t.Fatal("must not start re-run")
	}
}

func TestSummaryBannerWhenSynthesisStale(t *testing.T) {
	ro := testOverlayWithFailedDesign(t)
	ro.draft.MarkSynthesisStale()
	_ = ro.focusTab(ro.summaryTabIndex())
	body := ro.renderSummaryBody()
	if !strings.Contains(body, "Arbiter/vibe out of date") {
		t.Fatalf("summary should show stale banner, got:\n%s", body)
	}
}

func TestOverviewShowsRerunAllFailedZone(t *testing.T) {
	ro := testOverlayWithFailedDesign(t)
	body := ro.renderRunningBody()
	if !strings.Contains(body, "Re-run all failed") {
		t.Fatalf("overview should offer batch re-run, got:\n%s", body)
	}
	if RerunAllFailedZoneName() == "" {
		t.Fatal("zone name helper")
	}
}
