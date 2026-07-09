package review

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/agentstore"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/applog"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review/conventionwitness"
)

// session_cache.go implements U2 (draft persistence & resume): it persists a
// reviewer's IN-PROGRESS approval session — the completed Draft PLUS the user's
// per-card decisions (post / skip / resurface), demoted opt-ins, and cursor —
// so quitting mid-approval no longer discards a multi-minute, multi-dollar run.
//
// Relationship to B2 (draft_cache.go):
//   - B2 caches a LEAN Draft subset (Diff + per-specialist findings) keyed by
//     (owner/repo#N, headSHA) so the NEXT review with NEW commits can re-review
//     incrementally. It is written by the runner at pipeline completion.
//   - U2 stores a self-contained session snapshot in a SIBLING file next to the
//     B2 document, sharing the exact same (owner/repo#N, headSHA) key:
//         <cache>/draft-cache/<owner>__<repo>__<N>__<sha>.session.json
//     It is written by the TUI as the reviewer makes decisions, and read on
//     reopening the PR to offer a resume. Keeping it in a sibling file (rather
//     than embedding it in the B2 doc) means:
//       * the B2 doc stays byte-identical → B2 incremental is untouched;
//       * a decision toggle rewrites only the small session doc, not the whole
//         draft cache;
//       * the two phases coexist under one DraftCache and one SHA key.
//
// Fail-open, exactly like B2: a missing / unreadable / unparseable /
// version-mismatched session is treated as "no session" — the overlay behaves
// exactly as it did before U2 (no resume offered).

// sessionCacheVersion is the on-disk schema version for the session document.
// Load discards any document whose Version does not match so an old/new session
// is silently ignored (→ no resume) rather than mis-read.
const sessionCacheVersion = 1

// sessionFileSuffix is appended (in place of ".json") to the shared B2 filename
// stem so a session doc sits beside its draft doc under the same SHA key.
const sessionFileSuffix = ".session.json"

// CardDecision is one approval card's persisted state. The Key is the card's
// stable identity (built by the TUI from the finding + card kind) so decisions
// re-attach to the right card after the Draft is rehydrated, even though cards
// are rebuilt deterministically from the same snapshot. EditedBody is reserved
// for edit-before-post (Phase 5.2) and is empty today.
type CardDecision struct {
	Key        string `json:"key"`
	Decision   string `json:"decision"`
	Resurfaced bool   `json:"resurfaced,omitempty"`
	EditedBody string `json:"edited_body,omitempty"`
}

// SessionDraft is the fully serializable form of a review.Draft. Draft itself
// marks several runtime fields json:"-" (they must not leak into the U1 headless
// JSON output), so a resume snapshot cannot round-trip through Draft directly;
// this mirror carries every field the approval overlay needs to rehydrate the
// exact same cards, summary body, and posting payload without re-running the
// LLM pipeline. Map-typed skip/opt-in sets are stored as sorted-independent
// string slices; RepoArbiter's unexported key sets are rebuilt deterministically
// on load via RebuildArbiterKeySets.
type SessionDraft struct {
	Ref                        gh.Ref                      `json:"ref"`
	PR                         *gh.PR                      `json:"pr,omitempty"`
	Diff                       string                      `json:"diff"`
	Strictness                 aiconfig.ReviewStrictness   `json:"strictness,omitempty"`
	Specialists                []SpecialistResult          `json:"specialists,omitempty"`
	VibeCoach                  *VibeCoachResult            `json:"vibe_coach,omitempty"`
	RepositoryContext          string                      `json:"repository_context,omitempty"`
	ContextVersusChangeSummary string                      `json:"context_vs_change_summary,omitempty"`
	RepoArbiter                *RepoArbiterResult          `json:"repo_arbiter,omitempty"`
	ConventionWitness          []conventionwitness.Witness `json:"convention_witness,omitempty"`
	DemotedHidden              []FlatFinding               `json:"demoted_hidden,omitempty"`
	// UserSkipPostKeys is deliberately NOT persisted: on restore the decision
	// layer (per-card state) is the source of truth for skips, and AdoptDraft
	// filters UserSkipPostKeys out of the card list — so pre-populating it would
	// make skipped findings vanish instead of showing as skipped cards. The
	// TUI rebuilds it from card states via syncUserSkipsToDraft after applying
	// the decision layer.
	UserPostDemotedKeys []string                  `json:"user_post_demoted_keys,omitempty"`
	DiffBudget          *BudgetReport             `json:"diff_budget,omitempty"`
	MemorySuppressed    []MemorySuppressedFinding `json:"memory_suppressed,omitempty"`
	PRIntent            *PRIntent                 `json:"pr_intent,omitempty"`
	PriorReview         *CachedDraft              `json:"prior_review,omitempty"`
}

// SessionState is the persisted resume document: a Draft snapshot plus the
// reviewer's decision layer and cursor. Version-stamped and keyed by
// (Ref, HeadSHA) so it only ever rehydrates onto the same code it was captured
// against.
type SessionState struct {
	Version   int            `json:"version"`
	SavedAt   time.Time      `json:"saved_at"`
	HeadSHA   string         `json:"head_sha"`
	Ref       gh.Ref         `json:"ref"`
	Draft     *SessionDraft  `json:"draft"`
	Cursor    int            `json:"cursor"`
	ActiveTab int            `json:"active_tab"`
	Decisions []CardDecision `json:"decisions,omitempty"`
	// CoachHash is the TUI's skipSetHash of the skip set the persisted
	// VibeCoach result was computed against. Restored so the overlay reuses
	// the cached vibe-coach summary (no LLM re-run) when the reviewer re-enters
	// the summary with the same skip set. Empty when no vibe-coach was cached.
	CoachHash string `json:"coach_hash,omitempty"`
}

// keysToSlice returns the map keys as a slice (order-independent; the consumer
// rebuilds a set). Nil in → nil out so an omitempty field stays absent.
func keysToSlice(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// sliceToSet rebuilds a set from a slice. Nil in → nil out.
func sliceToSet(s []string) map[string]struct{} {
	if len(s) == 0 {
		return nil
	}
	m := make(map[string]struct{}, len(s))
	for _, k := range s {
		m[k] = struct{}{}
	}
	return m
}

// SessionSnapshot builds the serializable SessionDraft mirror of d. Returns nil
// for a nil draft.
func (d *Draft) SessionSnapshot() *SessionDraft {
	if d == nil {
		return nil
	}
	return &SessionDraft{
		Ref:                        d.Ref,
		PR:                         d.PR,
		Diff:                       d.Diff,
		Strictness:                 d.Strictness,
		Specialists:                d.Specialists,
		VibeCoach:                  d.VibeCoach,
		RepositoryContext:          d.RepositoryContext,
		ContextVersusChangeSummary: d.ContextVersusChangeSummary,
		RepoArbiter:                d.RepoArbiter,
		ConventionWitness:          d.ConventionWitness,
		DemotedHidden:              d.DemotedHidden,
		UserPostDemotedKeys:        keysToSlice(d.UserPostDemotedKeys),
		DiffBudget:                 d.DiffBudget,
		MemorySuppressed:           d.MemorySuppressed,
		PRIntent:                   d.PRIntent,
		PriorReview:                d.PriorReview,
	}
}

// Restore rebuilds a live *Draft from the snapshot, including the RepoArbiter's
// unexported suppress/demote key sets (reconstructed deterministically from the
// exported Suppressed/Demoted refs — no LLM call). Returns nil for a nil
// snapshot.
func (sd *SessionDraft) Restore() *Draft {
	if sd == nil {
		return nil
	}
	d := &Draft{
		Ref:                        sd.Ref,
		PR:                         sd.PR,
		Diff:                       sd.Diff,
		Strictness:                 sd.Strictness,
		Specialists:                sd.Specialists,
		VibeCoach:                  sd.VibeCoach,
		RepositoryContext:          sd.RepositoryContext,
		ContextVersusChangeSummary: sd.ContextVersusChangeSummary,
		RepoArbiter:                sd.RepoArbiter,
		ConventionWitness:          sd.ConventionWitness,
		DemotedHidden:              sd.DemotedHidden,
		UserPostDemotedKeys:        sliceToSet(sd.UserPostDemotedKeys),
		DiffBudget:                 sd.DiffBudget,
		MemorySuppressed:           sd.MemorySuppressed,
		PRIntent:                   sd.PRIntent,
		PriorReview:                sd.PriorReview,
	}
	if d.RepoArbiter != nil && d.RepoArbiter.Err == nil {
		RebuildArbiterKeySets(d.RepoArbiter)
	}
	return d
}

// RebuildArbiterKeySets reconstructs the RepoArbiterResult's unexported
// suppressKeySet / demoteKeySet from its exported Suppressed / Demoted slices.
// FinalizeRepoArbiter normally populates them (with side effects that mutate the
// draft), but a rehydrated arbiter has already been finalized: its Suppressed /
// Demoted lists are the KEPT, applied refs, and the draft's findings already
// carry the demoted severities. This function therefore only derives the lookup
// sets — it never re-mutates findings, so it is safe and idempotent on a
// snapshot. It mirrors FinalizeRepoArbiter's keying (side defaults to RIGHT;
// PR-wide refs collapse to the (specialist,"",0,side) key).
func RebuildArbiterKeySets(ar *RepoArbiterResult) {
	if ar == nil {
		return
	}
	ar.suppressKeySet = make(map[string]struct{}, len(ar.Suppressed))
	for _, sup := range ar.Suppressed {
		side := sup.Side
		if side == "" {
			side = "RIGHT"
		}
		if isGeneralRef(sup.Path, sup.Line) {
			ar.suppressKeySet[suppressionKey(sup.Specialist, "", 0, side)] = struct{}{}
			continue
		}
		ar.suppressKeySet[suppressionKey(sup.Specialist, sup.Path, sup.Line, side)] = struct{}{}
	}
	ar.demoteKeySet = make(map[string]Severity, len(ar.Demoted))
	for _, dem := range ar.Demoted {
		side := dem.Side
		if side == "" {
			side = "RIGHT"
		}
		var k string
		if isGeneralRef(dem.Path, dem.Line) {
			k = suppressionKey(dem.Specialist, "", 0, side)
		} else {
			k = suppressionKey(dem.Specialist, dem.Path, dem.Line, side)
		}
		ar.demoteKeySet[k] = dem.From
	}
}

// ---------------------------------------------------------------------------
// DraftCache session I/O (sibling of the B2 draft document)
// ---------------------------------------------------------------------------

// sessionFileNameFor returns the "<owner>__<repo>__<N>__<sha>.session.json"
// document name for one (PR, head SHA) pair — the B2 stem with the session
// suffix in place of ".json".
func (c *DraftCache) sessionFileNameFor(ref gh.Ref, sha string) string {
	return c.prefixFor(ref) + sanitizeSHA(sha) + sessionFileSuffix
}

// sessionPathFor returns the absolute session-document path for one
// (PR, head SHA) pair.
func (c *DraftCache) sessionPathFor(ref gh.Ref, sha string) string {
	return filepath.Join(c.dir, c.sessionFileNameFor(ref, sha))
}

// SaveSession atomically writes s as the resume session for its (Ref, HeadSHA).
// It is a no-op (nil error) when there is nothing to key on (nil state or empty
// SHA). The write is atomic (temp + rename via agentstore.WriteJSONAtomic).
func (c *DraftCache) SaveSession(s *SessionState) error {
	if s == nil || strings.TrimSpace(s.HeadSHA) == "" {
		return nil
	}
	s.Version = sessionCacheVersion
	if s.SavedAt.IsZero() {
		s.SavedAt = time.Now().UTC()
	}
	return agentstore.WriteJSONAtomic(c.sessionPathFor(s.Ref, s.HeadSHA), s)
}

// LoadSession reads the resume session for exactly (ref, headSHA). It returns
// (nil, false) for any reason the document cannot be trusted — absent,
// unreadable, unparseable, version mismatch, or missing draft — so the caller
// falls back to today's no-resume behaviour. It never returns an error:
// fail-open is the contract, matching B2's Load.
func (c *DraftCache) LoadSession(ref gh.Ref, headSHA string) (*SessionState, bool) {
	path := c.sessionPathFor(ref, headSHA)
	var s SessionState
	found, err := agentstore.ReadJSONFile(path, &s)
	if err != nil {
		applog.Warn("session cache: read failed (ignoring, no resume)", "path", path, "err", err.Error())
		return nil, false
	}
	if !found {
		return nil, false
	}
	if s.Version != sessionCacheVersion {
		applog.Info("session cache: version mismatch (ignoring, no resume)",
			"path", path, "have", s.Version, "want", sessionCacheVersion)
		return nil, false
	}
	if s.Draft == nil {
		applog.Info("session cache: missing draft snapshot (ignoring, no resume)", "path", path)
		return nil, false
	}
	return &s, true
}

// ClearSession removes the resume session for (ref, headSHA). Fail-open: a
// missing file or delete error is ignored. Called after a successful post (the
// run is done) so a completed review is never offered for resume.
func (c *DraftCache) ClearSession(ref gh.Ref, headSHA string) {
	_ = os.Remove(c.sessionPathFor(ref, headSHA))
}
