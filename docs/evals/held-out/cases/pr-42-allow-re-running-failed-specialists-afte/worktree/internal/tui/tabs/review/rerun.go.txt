package review

import (
	"context"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
)

// AgentRerunDoneMsg is delivered when a single post-pipeline agent re-run finishes.
type AgentRerunDoneMsg struct {
	Name     string
	Kind     string // "specialist" | "pr-agent" | "arbiter" | "vibe"
	Result   *review.SpecialistResult
	Arbiter  *review.RepoArbiterResult
	Vibe     *review.VibeCoachResult
	Err      error
	Snapshot *agentRowSnapshot
	Canceled bool
}

func (AgentRerunDoneMsg) OverlayBound() {}

var _ data.ForwardToOverlay = AgentRerunDoneMsg{}

// AgentRerunBatchDoneMsg carries results for "re-run all failed".
type AgentRerunBatchDoneMsg struct {
	Items     []AgentRerunBatchItem
	Snapshots map[string]*agentRowSnapshot
	Canceled  bool
}

// AgentRerunBatchItem is one specialist/PR-agent result in a batch re-run.
type AgentRerunBatchItem struct {
	Name   string
	Result review.SpecialistResult
}

func (AgentRerunBatchDoneMsg) OverlayBound() {}

var _ data.ForwardToOverlay = AgentRerunBatchDoneMsg{}

// agentRowSnapshot captures a row before a re-run so Esc can restore it.
type agentRowSnapshot struct {
	name       string
	phase      int
	err        error
	summary    string
	findingsN  int
	findings   []review.Finding
	verdict    string
	retries    int
	lastRetry  string
	startedAt  time.Time
	finishedAt time.Time
}

func snapshotAgentRow(a *overlayAgentRow) *agentRowSnapshot {
	if a == nil {
		return nil
	}
	return &agentRowSnapshot{
		name:       a.name,
		phase:      a.phase,
		err:        a.err,
		summary:    a.summary,
		findingsN:  a.findingsN,
		findings:   append([]review.Finding(nil), a.findings...),
		verdict:    a.verdict,
		retries:    a.retries,
		lastRetry:  a.lastRetry,
		startedAt:  a.startedAt,
		finishedAt: a.finishedAt,
	}
}

func (s *agentRowSnapshot) restore(a *overlayAgentRow) {
	if s == nil || a == nil {
		return
	}
	a.phase = s.phase
	a.err = s.err
	a.summary = s.summary
	a.findingsN = s.findingsN
	a.findings = append([]review.Finding(nil), s.findings...)
	a.verdict = s.verdict
	a.retries = s.retries
	a.lastRetry = s.lastRetry
	a.startedAt = s.startedAt
	a.finishedAt = s.finishedAt
}

func (m *Model) rerunAvailable() bool {
	return m != nil && m.done && m.draft != nil && m.draft.Rerun != nil && m.aiConfig != nil
}

func (m *Model) canRerunNamed(name string) bool {
	if !m.rerunAvailable() {
		return false
	}
	switch name {
	case overlayAgentRepoArbiter:
		return m.draft.Rerun.RepoExpertPanel
	case review.SpecVibeCoach:
		return true
	default:
		return m.draft.CanRerunAgent(name)
	}
}

func (m *Model) cancelRerun() {
	if m.rerunCancel != nil {
		m.rerunCancel()
		m.rerunCancel = nil
	}
}

func (m *Model) markRowRunning(name string) *agentRowSnapshot {
	i := m.agentIndex(name)
	if i < 0 {
		return nil
	}
	snap := snapshotAgentRow(&m.agents[i])
	m.agents[i].phase = oaRunning
	m.agents[i].err = nil
	m.agents[i].startedAt = time.Now()
	m.agents[i].finishedAt = time.Time{}
	m.agents[i].retries = 0
	m.agents[i].lastRetry = ""
	return snap
}

// rerunFocusedAgent starts a re-run for the overview cursor agent or active agent tab.
func (m *Model) rerunFocusedAgent() tea.Cmd {
	if !m.rerunAvailable() || m.rerunInFlight {
		return nil
	}
	name := ""
	if m.phase == phaseRunning && m.cursor >= 0 && m.cursor < len(m.agents) {
		name = m.agents[m.cursor].name
	} else if m.phase == phaseApprove {
		name = m.activeAgent()
	}
	if name == "" || !m.canRerunNamed(name) {
		return nil
	}
	return m.startAgentRerun(name)
}

// rerunAllFailed starts re-runs for every currently failed specialist/PR agent.
func (m *Model) rerunAllFailed() tea.Cmd {
	if !m.rerunAvailable() || m.rerunInFlight {
		return nil
	}
	names := m.draft.FailedAgentNames()
	if len(names) == 0 {
		return nil
	}
	return m.startFailedAgentsRerun(names)
}

func (m *Model) startAgentRerun(name string) tea.Cmd {
	snap := m.markRowRunning(name)
	m.rerunInFlight = true
	ctx, cancel := context.WithCancel(context.Background())
	m.rerunCancel = cancel
	m.rebuildBody()

	draft := m.draft
	cfg := m.aiConfig
	return func() tea.Msg {
		msg := AgentRerunDoneMsg{Name: name, Snapshot: snap}
		switch {
		case name == overlayAgentRepoArbiter:
			msg.Kind = "arbiter"
			arb := review.RunRepoArbiterForDraft(ctx, cfg, draft, nil)
			msg.Arbiter = arb
			if arb != nil {
				msg.Err = arb.Err
			}
		case name == review.SpecVibeCoach:
			msg.Kind = "vibe"
			vibe := review.RunVibeCoachForDraft(ctx, cfg, draft, nil)
			msg.Vibe = vibe
			if vibe != nil {
				msg.Err = vibe.Err
			}
		case review.IsPRAgent(name):
			msg.Kind = "pr-agent"
			res := review.RunPRAgentForDraft(ctx, cfg, draft, name, nil)
			msg.Result = &res
			msg.Err = res.Err
		default:
			msg.Kind = "specialist"
			res := review.RunSpecialistForDraft(ctx, cfg, draft, name, nil)
			msg.Result = &res
			msg.Err = res.Err
		}
		if ctx.Err() != nil {
			msg.Canceled = true
			msg.Err = ctx.Err()
		}
		return msg
	}
}

func (m *Model) startFailedAgentsRerun(names []string) tea.Cmd {
	snaps := make(map[string]*agentRowSnapshot, len(names))
	for _, name := range names {
		snaps[name] = m.markRowRunning(name)
	}
	m.rerunInFlight = true
	ctx, cancel := context.WithCancel(context.Background())
	m.rerunCancel = cancel
	m.rebuildBody()

	draft := m.draft
	cfg := m.aiConfig
	parallel := draft.Rerun != nil && draft.Rerun.ParallelSpecialists
	return func() tea.Msg {
		items := make([]AgentRerunBatchItem, len(names))
		runOne := func(i int, name string) {
			var res review.SpecialistResult
			if review.IsPRAgent(name) {
				res = review.RunPRAgentForDraft(ctx, cfg, draft, name, nil)
			} else {
				res = review.RunSpecialistForDraft(ctx, cfg, draft, name, nil)
			}
			items[i] = AgentRerunBatchItem{Name: name, Result: res}
		}
		if parallel && len(names) > 1 {
			var wg sync.WaitGroup
			for i, name := range names {
				wg.Add(1)
				i, name := i, name
				go func() {
					defer wg.Done()
					runOne(i, name)
				}()
			}
			wg.Wait()
		} else {
			for i, name := range names {
				if ctx.Err() != nil {
					break
				}
				runOne(i, name)
			}
		}
		return AgentRerunBatchDoneMsg{
			Items:     items,
			Snapshots: snaps,
			Canceled:  ctx.Err() != nil,
		}
	}
}

func (m *Model) handleAgentRerunDone(msg AgentRerunDoneMsg) tea.Cmd {
	m.rerunInFlight = false
	m.rerunCancel = nil
	if msg.Canceled {
		if i := m.agentIndex(msg.Name); i >= 0 {
			msg.Snapshot.restore(&m.agents[i])
		}
		m.rebuildBody()
		return nil
	}
	switch msg.Kind {
	case "arbiter":
		m.hydrateAgentRowsFromDraft(m.draft)
		m.rebuildCardsFromDraft()
	case "vibe":
		if m.draft != nil && msg.Vibe != nil {
			m.draft.VibeCoach = msg.Vibe
			m.draft.ClearVibeStale()
		}
		if i := m.agentIndex(review.SpecVibeCoach); i >= 0 {
			row := &m.agents[i]
			row.finishedAt = time.Now()
			if msg.Err != nil {
				row.phase = oaErr
				row.err = msg.Err
			} else {
				row.phase = oaDone
				row.err = nil
				if msg.Vibe != nil {
					row.summary = msg.Vibe.Summary
					row.verdict = msg.Vibe.Verdict
					row.findingsN = len(msg.Vibe.Prompts)
				}
			}
		}
		if m.draft != nil && m.draft.VibeCoach != nil {
			m.lastCoachHash = skipSetHash(m.draft.UserSkipPostKeys)
		}
	default:
		if msg.Result != nil && m.draft != nil {
			_ = review.RefreshDraftAfterAgentReplace(m.draft, msg.Name, *msg.Result)
		}
		m.applySpecialistResultToRow(msg.Name, msg.Result, msg.Err)
		m.rebuildCardsFromDraft()
		m.lastCoachHash = "" // vibe is stale
	}
	m.rebuildBody()
	return nil
}

func (m *Model) handleAgentRerunBatchDone(msg AgentRerunBatchDoneMsg) tea.Cmd {
	m.rerunInFlight = false
	m.rerunCancel = nil
	if msg.Canceled {
		for name, snap := range msg.Snapshots {
			if i := m.agentIndex(name); i >= 0 {
				snap.restore(&m.agents[i])
			}
		}
		m.rebuildBody()
		return nil
	}
	for _, it := range msg.Items {
		if it.Name == "" {
			continue
		}
		_ = review.RefreshDraftAfterAgentReplace(m.draft, it.Name, it.Result)
		err := it.Result.Err
		res := it.Result
		m.applySpecialistResultToRow(it.Name, &res, err)
	}
	// Mark stale once after the whole batch (spec: avoid thrashing mid-batch).
	if m.draft != nil {
		m.draft.MarkSynthesisStale()
	}
	m.rebuildCardsFromDraft()
	m.lastCoachHash = ""
	m.rebuildBody()
	return nil
}

func (m *Model) applySpecialistResultToRow(name string, res *review.SpecialistResult, err error) {
	i := m.agentIndex(name)
	if i < 0 {
		return
	}
	row := &m.agents[i]
	row.finishedAt = time.Now()
	if err != nil || (res != nil && res.Err != nil) {
		row.phase = oaErr
		if err != nil {
			row.err = err
		} else {
			row.err = res.Err
		}
		return
	}
	row.phase = oaDone
	row.err = nil
	if res != nil {
		row.summary = res.Summary
		row.findings = append([]review.Finding(nil), res.Findings...)
		row.findingsN = len(res.Findings)
	}
}

// rebuildCardsFromDraft rebuilds approval cards from the current draft without
// changing tab focus (used after a specialist re-run changes findings).
func (m *Model) rebuildCardsFromDraft() {
	d := m.draft
	if d == nil {
		return
	}
	m.files = review.ParseDiff(d.Diff)
	flat := d.FlatPostableFindingsForPost()
	m.cards = make([]approvalCard, 0, len(flat)+len(d.DemotedHidden)+len(d.MemorySuppressed))
	for _, f := range flat {
		card := approvalCard{finding: f}
		anchorCardToDiff(&card, m.files)
		m.cards = append(m.cards, card)
	}
	for i, ms := range d.MemorySuppressed {
		if strings.TrimSpace(ms.Finding.Path) == "" || ms.Finding.Line <= 0 {
			continue
		}
		card := approvalCard{
			finding:             review.FlatFinding{Specialist: ms.Specialist, Finding: ms.Finding},
			memorySuppressed:    true,
			memorySuppIdx:       i,
			memorySuppSkipCount: ms.SkipCount,
			state:               cardSkipped,
		}
		anchorCardToDiff(&card, m.files)
		m.cards = append(m.cards, card)
	}
	for _, f := range d.DemotedHidden {
		if strings.TrimSpace(f.Finding.Path) == "" || f.Finding.Line <= 0 {
			continue
		}
		card := approvalCard{finding: f, demoted: true, state: cardSkipped}
		anchorCardToDiff(&card, m.files)
		m.cards = append(m.cards, card)
	}
	if m.activeTab >= 0 && m.activeTab < len(m.tabs) && m.tabs[m.activeTab].kind == tabAgent {
		m.idx = m.firstCardForAgent(m.activeAgent())
	}
}
