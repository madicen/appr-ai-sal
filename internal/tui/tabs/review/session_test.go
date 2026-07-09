package review

import (
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

// useTempSessionCache points the draft/session cache at a fresh temp dir for one
// test (appdirs resolves the draft-cache subdir as a sibling of
// APPR_AI_SAL_CACHE_DIR), keeping every U2 TUI test hermetic — no writes ever
// touch the developer's real cache.
func useTempSessionCache(t *testing.T) {
	t.Helper()
	t.Setenv("APPR_AI_SAL_CACHE_DIR", t.TempDir())
}

// sessionTestDraft is tabsTestDraft with an explicit Ref so the session keys by
// a realistic owner/repo#N (tabsTestDraft only fills PR, leaving Ref zero).
func sessionTestDraft() *review.Draft {
	d := tabsTestDraft()
	d.Ref = gh.Ref{Owner: "o", Repo: "r", Number: 1}
	return d
}

func cardStateByIdentity(m *Model, id string) (approvalCardState, bool) {
	for _, c := range m.cards {
		if m.cardIdentity(c) == id {
			return c.state, true
		}
	}
	return cardPending, false
}

// Making decisions then persisting writes a resumable session; reopening the
// same PR at the same head SHA rehydrates the Draft + decisions + cursor with no
// LLM re-run (cfg is nil — a re-run would be impossible, which is the point).
func TestSessionPersistAndResumeRoundTrip(t *testing.T) {
	useTempSessionCache(t)

	ro := New(120, 44, true /*dryRun*/, false, false, nil, false)
	// Mirror production: both the original overlay (detail.go) and NewResumed
	// narrow the tab bar to the specialists that actually ran, so a saved tab
	// index lines up on resume.
	ro.SetSpecialists([]string{review.SpecDocs, review.SpecSecurity})
	ro.AdoptDraft(sessionTestDraft())

	docsIdx := ro.agentCardIndices(review.SpecDocs)
	secIdx := ro.agentCardIndices(review.SpecSecurity)
	if len(docsIdx) != 1 || len(secIdx) != 1 {
		t.Fatalf("expected one card per agent; docs=%v sec=%v", docsIdx, secIdx)
	}
	ro.cards[docsIdx[0]].state = cardSkipped
	ro.cards[secIdx[0]].state = cardPosted

	docsID := ro.cardIdentity(ro.cards[docsIdx[0]])
	secID := ro.cardIdentity(ro.cards[secIdx[0]])

	ro.idx = docsIdx[0]
	savedCursor := ro.idx
	savedTab := ro.activeTab
	ro.persistSession()

	dc := review.NewDraftCache()
	s, ok := dc.LoadSession(gh.Ref{Owner: "o", Repo: "r", Number: 1}, "abc")
	if !ok {
		t.Fatalf("LoadSession !ok after persistSession; session was not written")
	}

	rm, _ := NewResumed(120, 44, true, false, false, nil, false, s)

	if st, ok := cardStateByIdentity(rm, docsID); !ok || st != cardSkipped {
		t.Errorf("docs card state after resume = %v (found=%v), want cardSkipped", st, ok)
	}
	if st, ok := cardStateByIdentity(rm, secID); !ok || st != cardPosted {
		t.Errorf("security card state after resume = %v (found=%v), want cardPosted", st, ok)
	}
	if rm.idx != savedCursor {
		t.Errorf("resumed cursor = %d, want %d", rm.idx, savedCursor)
	}
	if rm.activeTab != savedTab {
		t.Errorf("resumed active tab = %d, want %d", rm.activeTab, savedTab)
	}
	// The vibe-coach result rides along in the snapshot: resume reuses it
	// rather than re-running the coach.
	if rm.draft == nil || rm.draft.VibeCoach == nil || rm.draft.VibeCoach.Summary != "verdict" {
		t.Errorf("resumed draft lost its cached vibe-coach result: %+v", rm.draft)
	}
}

// Demo mode must never leave a session file on disk — recordings stay
// self-contained and reproducible.
func TestSessionDemoModeNoPersist(t *testing.T) {
	useTempSessionCache(t)

	ro := New(120, 44, true, false, false, nil, true /*demoMode*/)
	ro.AdoptDraft(sessionTestDraft())
	if idxs := ro.agentCardIndices(review.SpecDocs); len(idxs) == 1 {
		ro.cards[idxs[0]].state = cardSkipped
	}
	ro.persistSession()

	if _, ok := review.NewDraftCache().LoadSession(gh.Ref{Owner: "o", Repo: "r", Number: 1}, "abc"); ok {
		t.Fatalf("demo mode wrote a resume session; want none")
	}
}

// A successful post clears the session so a completed review is never offered
// for resume.
func TestSessionClearedAfterPost(t *testing.T) {
	useTempSessionCache(t)

	ro := New(120, 44, true, false, false, nil, false)
	ro.AdoptDraft(sessionTestDraft())
	if idxs := ro.agentCardIndices(review.SpecDocs); len(idxs) == 1 {
		ro.cards[idxs[0]].state = cardSkipped
	}
	ro.persistSession()

	ref := gh.Ref{Owner: "o", Repo: "r", Number: 1}
	if _, ok := review.NewDraftCache().LoadSession(ref, "abc"); !ok {
		t.Fatalf("precondition: session should exist before clear")
	}
	ro.clearSession()
	if _, ok := review.NewDraftCache().LoadSession(ref, "abc"); ok {
		t.Fatalf("clearSession left a resumable session behind")
	}
}

// Before the pipeline finishes (no adopted draft) there is nothing resumable, so
// persist is a no-op — backward-compatible with today's behavior.
func TestSessionNotPersistedBeforeDone(t *testing.T) {
	useTempSessionCache(t)

	ro := New(120, 44, true, false, false, nil, false)
	// No AdoptDraft: m.done is false, m.draft is nil.
	ro.persistSession()
	if _, ok := review.NewDraftCache().LoadSession(gh.Ref{Owner: "o", Repo: "r", Number: 1}, "abc"); ok {
		t.Fatalf("persisted a session before the draft was adopted")
	}
}
