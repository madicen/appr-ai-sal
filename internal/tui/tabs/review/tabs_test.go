package review

import (
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

func init() { zone.NewGlobal() }

// focusAgentTabForTest moves the overlay's focus to the named agent's tab
// so finding-level keys (y/n/F) and the renderAgentTab path are exercised.
func focusAgentTabForTest(t *testing.T, ro *Model, agent string) {
	t.Helper()
	for i, tb := range ro.tabs {
		if tb.kind == tabAgent && tb.agent == agent {
			ro.focusTab(i)
			return
		}
	}
	t.Fatalf("no agent tab for %q", agent)
}

const tabsTestDiff = `diff --git a/a.go b/a.go
--- /dev/null
+++ b/a.go
@@ -0,0 +1 @@
+a
diff --git a/b.go b/b.go
--- /dev/null
+++ b/b.go
@@ -0,0 +1 @@
+b
`

func tabsTestDraft() *review.Draft {
	return &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: tabsTestDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Summary: "docs notes", Findings: []review.Finding{
				{Path: "a.go", Line: 1, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "c1"},
			}},
			{Specialist: review.SpecSecurity, Summary: "security notes", Findings: []review.Finding{
				{Path: "b.go", Line: 1, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "c2"},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges, Summary: "verdict"},
	}
}

// New overlays expose a tab bar: overview, one per output agent (5
// specialists + arbiter + vibe), then summary.
func TestNewOverlayBuildsTabBar(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	want := 1 + len(outputAgentOrder(review.AllSpecialists)) + 1
	if len(ro.tabs) != want {
		t.Fatalf("tabs %d want %d", len(ro.tabs), want)
	}
	if ro.tabs[0].kind != tabOverview {
		t.Fatalf("first tab kind %v want overview", ro.tabs[0].kind)
	}
	if ro.tabs[len(ro.tabs)-1].kind != tabSummary {
		t.Fatalf("last tab kind %v want summary", ro.tabs[len(ro.tabs)-1].kind)
	}
	if ro.phase != phaseRunning {
		t.Fatalf("fresh overlay phase %v want phaseRunning", ro.phase)
	}
}

// On completion the overlay auto-focuses the summary tab when the user is
// still on the overview tab.
func TestAdoptDraftAutoFocusesSummaryFromOverview(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.AdoptDraft(tabsTestDraft())
	if !ro.onSummaryTab() {
		t.Fatalf("expected summary tab focused after completion, activeTab=%d", ro.activeTab)
	}
	if ro.phase != phaseSummary {
		t.Fatalf("phase %v want phaseSummary", ro.phase)
	}
}

// Switching to an agent tab focuses that agent's first finding and lets
// the reviewer post it; posting marks the card and advances within the
// agent.
func TestAgentTabPostsFinding(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.AdoptDraft(tabsTestDraft())
	focusAgentTabForTest(t, ro, review.SpecDocs)
	if ro.phase != phaseApprove {
		t.Fatalf("phase %v want phaseApprove on agent tab", ro.phase)
	}
	if got := ro.activeAgent(); got != review.SpecDocs {
		t.Fatalf("activeAgent %q want %q", got, review.SpecDocs)
	}
	idxs := ro.agentCardIndices(review.SpecDocs)
	if len(idxs) != 1 || ro.idx != idxs[0] {
		t.Fatalf("focused idx %d want %v", ro.idx, idxs)
	}
	// Simulate a successful post arriving from the root model.
	_, _ = ro.Update(tea.WindowSizeMsg{Width: 120, Height: 44})
	ro.cards[ro.idx].state = cardPosted
	if onPR, posted, _ := ro.agentCardTally(review.SpecDocs); posted != 1 || onPR != 0 {
		t.Fatalf("docs tally posted=%d onPR=%d want posted=1 onPR=0", posted, onPR)
	}
	body := ansi.Strip(ro.renderAgentTab(review.SpecDocs))
	if !strings.Contains(body, "docs notes") {
		t.Fatalf("agent tab should show the agent summary, got:\n%s", body)
	}
}

// Every agent tab shows a summary even when the agent produced no
// findings (e.g. the vibe coach), so the reviewer can always see what it
// did under the hood.
func TestAgentTabShowsSummaryForFindinglessAgent(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	// Mark the vibe-coach row done with a summary via the progress merge.
	ro.mergeProgress(review.Progress{Stage: "vibe-coach", Detail: "start"})
	ro.mergeProgress(review.Progress{Stage: "vibe-coach", Detail: "done", Vibe: &review.VibeCoachResult{
		Verdict: review.VibeVerdictApprove,
		Summary: "looks good overall",
	}})
	body := ansi.Strip(ro.renderAgentTab(review.SpecVibeCoach))
	if !strings.Contains(body, "looks good overall") {
		t.Fatalf("vibe-coach tab should show its summary, got:\n%s", body)
	}
	if !strings.Contains(body, "Approve") {
		t.Fatalf("vibe-coach tab should show its merge recommendation, got:\n%s", body)
	}
}

// The PR-agent family is wired into the overlay: every PR agent gets its
// own tab and an agent row in the PR-agents stage group.
func TestNewOverlayIncludesPRAgents(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	for _, name := range review.AllPRAgents {
		found := false
		for _, tb := range ro.tabs {
			if tb.kind == tabAgent && tb.agent == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("no tab for PR agent %q", name)
		}
		i := ro.agentIndex(name)
		if i < 0 {
			t.Fatalf("no agent row for PR agent %q", name)
		}
		if ro.agents[i].stage != stageGroupPRAgents {
			t.Fatalf("PR agent %q row stage = %v want stageGroupPRAgents", name, ro.agents[i].stage)
		}
	}
}

// A pr-agent progress message drives the matching agent row to done and
// carries its findings, just like a specialist's progress.
func TestPRAgentProgressUpdatesRow(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.mergeProgress(review.Progress{Stage: "pr-agent", Detail: review.SpecScope + ":start"})
	i := ro.agentIndex(review.SpecScope)
	if i < 0 || ro.agents[i].phase != oaRunning {
		t.Fatalf("scope row not running after start, phase=%d", ro.agents[i].phase)
	}
	ro.mergeProgress(review.Progress{Stage: "pr-agent", Detail: review.SpecScope + ":done", Result: &review.SpecialistResult{
		Specialist: review.SpecScope,
		Summary:    "well scoped",
		Findings:   []review.Finding{{Severity: review.SeverityInfo, Comment: "minor split suggestion"}},
	}})
	if ro.agents[i].phase != oaDone {
		t.Fatalf("scope row phase = %d want oaDone", ro.agents[i].phase)
	}
	if ro.agents[i].findingsN != 1 || ro.agents[i].summary != "well scoped" {
		t.Fatalf("scope row not hydrated from result: findingsN=%d summary=%q", ro.agents[i].findingsN, ro.agents[i].summary)
	}
}

// A pr-agent fetch warning lands in the log rather than crashing or
// matching a non-existent agent row.
func TestPRAgentFetchWarningLogged(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.mergeProgress(review.Progress{Stage: "pr-agent", Detail: "warning: checks fetch: boom"})
	joined := strings.Join(ro.log, "\n")
	if !strings.Contains(joined, "checks fetch: boom") {
		t.Fatalf("expected fetch warning in log, got: %v", ro.log)
	}
}

// The vibe-coach runs last and files no findings, so its tab must never
// claim its findings were "suppressed by the repo arbiter" — even though
// its row carries a non-zero count (the author-prompt count).
func TestVibeCoachTabHasNoArbiterSuppressionMessage(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	// Reproduce the old bug condition: vibe-coach completes with prompts,
	// which set row.findingsN > 0.
	ro.mergeProgress(review.Progress{Stage: "vibe-coach", Detail: "start"})
	ro.mergeProgress(review.Progress{Stage: "vibe-coach", Detail: "done", Vibe: &review.VibeCoachResult{
		Verdict: review.VibeVerdictRequestChanges,
		Summary: "needs work",
		Prompts: []review.AuthorPrompt{{Title: "Fix it", AgentPrompt: "do the thing"}},
	}})
	ro.AdoptDraft(tabsTestDraft())

	body := ansi.Strip(ro.renderAgentTab(review.SpecVibeCoach))
	if strings.Contains(body, "suppressed by the repo arbiter") {
		t.Fatalf("vibe-coach tab must not claim arbiter suppression:\n%s", body)
	}
	if !strings.Contains(body, "doesn't file individual findings") {
		t.Fatalf("vibe-coach tab should explain it files no findings:\n%s", body)
	}
}

// PR agents emit PR-wide (body-only) findings that never become inline
// cards. Their tab should label the feedback as PR-level and render it
// read-only, not claim the repo arbiter suppressed it.
func TestPRAgentTabShowsPRWideFeedback(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: tabsTestDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecScope, Summary: "scope notes", Findings: []review.Finding{
				{Path: "", Line: 0, Severity: review.SeverityWarning, Comment: "split the rename into its own PR"},
			}},
		},
	}
	ro.AdoptDraft(d)

	body := ansi.Strip(ro.renderAgentTab(review.SpecScope))
	if strings.Contains(body, "suppressed by the repo arbiter") {
		t.Fatalf("scope tab must not claim arbiter suppression:\n%s", body)
	}
	if !strings.Contains(body, "PR-level") {
		t.Fatalf("scope tab should label feedback as PR-level:\n%s", body)
	}
	if !strings.Contains(body, "split the rename into its own PR") {
		t.Fatalf("scope tab should render the PR-wide finding text:\n%s", body)
	}
}

// A finding the repo arbiter demoted below the strictness floor is retained
// on draft.DemotedHidden and surfaced as an opt-in card on its specialist's
// tab: skipped by default, marked "post anyway", excluded from the tallies,
// and not part of the verdict-bearing finding set.
func TestDemotedHiddenFindingBecomesOptInCard(t *testing.T) {
	ro := New(120, 44, true /*dryRun*/, false, false, nil, false)
	demoted := review.Finding{Path: "a.go", Line: 1, Side: "RIGHT", Severity: review.SeverityInfo, Comment: "use Mi suffix"}
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: tabsTestDiff,
		Specialists: []review.SpecialistResult{
			// The formatting finding was demoted out, so the specialist now
			// reports no at-floor findings.
			{Specialist: review.SpecFormatting, Summary: "clean except a unit suffix"},
		},
		DemotedHidden: []review.FlatFinding{
			{Specialist: review.SpecFormatting, Finding: demoted},
		},
	}
	ro.AdoptDraft(d)

	idxs := ro.agentCardIndices(review.SpecFormatting)
	if len(idxs) != 1 {
		t.Fatalf("expected one card for formatting, got %d", len(idxs))
	}
	card := ro.cards[idxs[0]]
	if !card.demoted {
		t.Fatalf("expected the formatting card to be marked demoted")
	}
	if card.state != cardSkipped {
		t.Fatalf("demoted card state = %v want cardSkipped (default opt-in)", card.state)
	}

	// Excluded from the summary-level tally so it can't sway verdict routing.
	if onPR, posted, skipped := ro.tallyCardKinds(); onPR+posted+skipped != 0 {
		t.Fatalf("tallyCardKinds counted a demoted card: onPR=%d posted=%d skipped=%d", onPR, posted, skipped)
	}
	// And out of the per-agent strip until actually posted.
	if _, _, skipped := ro.agentCardTally(review.SpecFormatting); skipped != 0 {
		t.Fatalf("agentCardTally counted a default-skipped demoted card as skipped (%d)", skipped)
	}

	focusAgentTabForTest(t, ro, review.SpecFormatting)
	body := ansi.Strip(ro.renderAgentTab(review.SpecFormatting))
	if strings.Contains(body, "No findings from this agent") {
		t.Fatalf("formatting tab should surface the demoted finding, not claim it's clean:\n%s", body)
	}
	if !strings.Contains(body, "post it anyway") && !strings.Contains(body, "post anyway") {
		t.Fatalf("formatting tab should offer to post the demoted finding:\n%s", body)
	}
	if !strings.Contains(body, "use Mi suffix") {
		t.Fatalf("formatting tab should render the demoted finding text:\n%s", body)
	}

	// The reviewer can still post it by hand: pressing y returns a post cmd.
	if _, cmd := ro.actPostCurrent(); cmd == nil {
		t.Fatalf("posting a demoted card should issue a post command")
	}
}

// When no technology experts are configured, the overlay drops the tech
// specialist entirely: no agent row, no tab. The other specialists and the
// PR-agent / arbiter / vibe tabs are unaffected.
func TestSetSpecialistsHidesTechTab(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)

	// Default: tech is present.
	if !hasAgentTab(ro, review.SpecTech) {
		t.Fatalf("tech tab should be present by default")
	}

	ro.SetSpecialists(review.ActiveSpecialists(false /* no tech experts */))

	if hasAgentTab(ro, review.SpecTech) {
		t.Fatalf("tech tab should be hidden when no technology experts are configured")
	}
	for _, tb := range ro.agents {
		if tb.name == review.SpecTech {
			t.Fatalf("tech agent row should be removed when no technology experts are configured")
		}
	}
	// The universal specialists keep their tabs.
	for _, s := range []string{review.SpecFormatting, review.SpecDesign, review.SpecTesting, review.SpecDocs, review.SpecSecurity} {
		if !hasAgentTab(ro, s) {
			t.Fatalf("specialist %q tab should remain when tech is hidden", s)
		}
	}
	// Tab bar shrinks by exactly one (the tech tab).
	want := 1 + len(outputAgentOrder(review.ActiveSpecialists(false))) + 1
	if len(ro.tabs) != want {
		t.Fatalf("tab count = %d, want %d after hiding tech", len(ro.tabs), want)
	}
}

func hasAgentTab(ro *Model, agent string) bool {
	for _, tb := range ro.tabs {
		if tb.kind == tabAgent && tb.agent == agent {
			return true
		}
	}
	return false
}

// A PR-wide finding the repo arbiter demoted below the floor has no diff
// anchor, so it is surfaced read-only on its agent's tab (not as a card) with
// an opt-in toggle. Pressing y includes it in the posted review body.
func TestDemotedPRWideFindingSurfacesAsOptIn(t *testing.T) {
	ro := New(120, 44, true /*dryRun*/, false, false, nil, false)
	demoted := review.Finding{Path: "", Line: 0, Side: "RIGHT", Severity: review.SeverityInfo, Comment: "The PR description is empty."}
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: tabsTestDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDescription, Summary: "description missing"},
		},
		RepoArbiter: &review.RepoArbiterResult{},
		DemotedHidden: []review.FlatFinding{
			{Specialist: review.SpecDescription, Finding: demoted},
		},
	}
	ro.AdoptDraft(d)

	// PR-wide demoted findings must not become interactive cards.
	if idxs := ro.agentCardIndices(review.SpecDescription); len(idxs) != 0 {
		t.Fatalf("PR-wide demoted finding should not create a card; got %d", len(idxs))
	}

	focusAgentTabForTest(t, ro, review.SpecDescription)
	body := ansi.Strip(ro.renderAgentTab(review.SpecDescription))
	if strings.Contains(body, "No findings from this agent") {
		t.Fatalf("description tab should surface the demoted PR-wide finding, not claim it's clean:\n%s", body)
	}
	if !strings.Contains(body, "The PR description is empty.") {
		t.Fatalf("description tab should render the demoted finding text:\n%s", body)
	}
	if !strings.Contains(body, "press y to include") {
		t.Fatalf("description tab should offer to include the demoted finding:\n%s", body)
	}

	// Default: not included in the posted body.
	if strings.Contains(d.RenderBody(), "The PR description is empty.") {
		t.Fatalf("demoted PR-wide finding should not post until opted in:\n%s", d.RenderBody())
	}

	// Pressing y on the tab (no focused card) opts it in. It may schedule a
	// debounced U2 session save, but must never post anything immediately —
	// the opt-in only mutates the draft; posting happens at summary time.
	if ro.idx >= 0 {
		t.Fatalf("expected no focused card on a PR-wide-only tab; idx=%d", ro.idx)
	}
	_, _ = ro.actToggleDemotedPRWide()
	if !d.DemotedPostingEnabled(review.SpecDescription, demoted) {
		t.Fatalf("after toggling, the demoted PR-wide finding should be opted in")
	}
	if !strings.Contains(d.RenderBody(), "The PR description is empty.") {
		t.Fatalf("opted-in demoted PR-wide finding should appear in the posted body:\n%s", d.RenderBody())
	}

	// The tab now reflects the included state.
	body = ansi.Strip(ro.renderAgentTab(review.SpecDescription))
	if !strings.Contains(body, "will be included") {
		t.Fatalf("description tab should reflect the included state after opt-in:\n%s", body)
	}
}

// The agent row's "N finding(s)" header must reflect the post-processing set
// (after cross-specialist dedupe + arbiter demotion), not the raw streamed
// count. A specialist that streamed 2 findings but ended with 0 surviving and
// 1 demoted-but-retained should report 1 — matching the single card shown.
func TestAgentRowCountReflectsFinalDraftNotStreamed(t *testing.T) {
	ro := New(120, 44, true, false, false, nil, false)
	i := ro.agentIndex(review.SpecTesting)
	if i < 0 {
		t.Fatalf("no testing agent row")
	}
	// Simulate the stale streamed count (raw model output, pre-processing).
	ro.agents[i].phase = oaDone
	ro.agents[i].findingsN = 2
	ro.agents[i].findings = []review.Finding{{Severity: review.SeverityWarning}, {Severity: review.SeverityWarning}}

	demoted := review.Finding{Path: "a.go", Line: 1, Side: "RIGHT", Severity: review.SeverityInfo, Comment: "use Mi suffix"}
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: tabsTestDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecTesting, Summary: "no test concerns"}, // 0 surviving findings
		},
		DemotedHidden: []review.FlatFinding{
			{Specialist: review.SpecTesting, Finding: demoted},
		},
	}
	ro.AdoptDraft(d)

	if got := ro.agents[i].findingsN; got != 1 {
		t.Fatalf("testing row findingsN = %d, want 1 (0 surviving + 1 demoted)", got)
	}
	detail := ansi.Strip(agentStatusDetail(&ro.agents[i]))
	if !strings.Contains(detail, "1 finding(s)") {
		t.Fatalf("testing row detail should report 1 finding(s), got %q", detail)
	}
}

// The tab strip flags agents that produced findings with a severity-coloured
// dot, and recedes clean agents to a dim check, so the user can scan the bar
// for the tabs worth reading.
func TestTabAgentGlyphHighlightsFindings(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)

	clean := &overlayAgentRow{name: review.SpecFormatting, stage: stageGroupSpecialists, phase: oaDone}
	if got, want := ro.tabAgentGlyph(clean), styles.DimStyle.Render("✓"); got != want {
		t.Fatalf("clean agent glyph = %q want dim check %q", got, want)
	}

	warn := &overlayAgentRow{
		name: review.SpecDocs, stage: stageGroupSpecialists, phase: oaDone, findingsN: 1,
		findings: []review.Finding{{Severity: review.SeverityWarning, Comment: "x"}},
	}
	if got, want := ro.tabAgentGlyph(warn), styles.SevWarning.Render("●"); got != want {
		t.Fatalf("warning agent glyph = %q want warning dot %q", got, want)
	}

	// The most severe finding decides the dot colour.
	mixed := &overlayAgentRow{
		name: review.SpecDesign, stage: stageGroupSpecialists, phase: oaDone, findingsN: 2,
		findings: []review.Finding{{Severity: review.SeverityWarning}, {Severity: review.SeverityError}},
	}
	if got, want := ro.tabAgentGlyph(mixed), styles.SevError.Render("●"); got != want {
		t.Fatalf("mixed agent glyph = %q want error dot %q", got, want)
	}
}

// The arbiter is flagged when it took suppress/demote actions, and the vibe
// coach by its verdict (request_changes) rather than by findings.
func TestTabAgentGlyphArbiterAndVibe(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)

	arbClean := &overlayAgentRow{name: overlayAgentRepoArbiter, stage: stageGroupArbiter, phase: oaDone}
	if got, want := ro.tabAgentGlyph(arbClean), styles.DimStyle.Render("✓"); got != want {
		t.Fatalf("arbiter-no-action glyph = %q want dim check %q", got, want)
	}
	arbActed := &overlayAgentRow{name: overlayAgentRepoArbiter, stage: stageGroupArbiter, phase: oaDone, findingsN: 2}
	if got, want := ro.tabAgentGlyph(arbActed), styles.SevInfo.Render("●"); got != want {
		t.Fatalf("arbiter-acted glyph = %q want info dot %q", got, want)
	}

	vibeRC := &overlayAgentRow{name: review.SpecVibeCoach, stage: stageGroupVibe, phase: oaDone, verdict: review.VibeVerdictRequestChanges}
	if got, want := ro.tabAgentGlyph(vibeRC), styles.SevError.Render("●"); got != want {
		t.Fatalf("vibe request-changes glyph = %q want error dot %q", got, want)
	}
	vibeOK := &overlayAgentRow{name: review.SpecVibeCoach, stage: stageGroupVibe, phase: oaDone, verdict: review.VibeVerdictApprove}
	if got, want := ro.tabAgentGlyph(vibeOK), styles.DimStyle.Render("✓"); got != want {
		t.Fatalf("vibe approve glyph = %q want dim check %q", got, want)
	}
}

// The vibe coach files no findings — its count is the paste-ready author
// prompt total, so the row detail must say "prompt(s)" not "finding(s)".
func TestVibeCoachRowLabelsPromptsNotFindings(t *testing.T) {
	row := &overlayAgentRow{
		name: review.SpecVibeCoach, stage: stageGroupVibe, phase: oaDone,
		findingsN: 3, verdict: review.VibeVerdictRequestChanges,
	}
	got := ansi.Strip(agentStatusDetail(row))
	if strings.Contains(got, "finding") {
		t.Fatalf("vibe-coach row must not call prompts findings: %q", got)
	}
	if !strings.Contains(got, "3 prompt(s)") {
		t.Fatalf("vibe-coach row should report prompt count: %q", got)
	}
}

// Tab navigation keys move the active tab.
func TestTabNavigationKeys(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	if ro.activeTab != 0 {
		t.Fatalf("activeTab %d want 0", ro.activeTab)
	}
	out, _ := ro.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	ro = out.(*Model)
	if ro.activeTab != 1 {
		t.Fatalf("after tab, activeTab %d want 1", ro.activeTab)
	}
	out, _ = ro.handleKey(tea.KeyMsg{Type: tea.KeyShiftTab})
	ro = out.(*Model)
	if ro.activeTab != 0 {
		t.Fatalf("after shift+tab, activeTab %d want 0", ro.activeTab)
	}
}

// The mouse wheel over the tab strip scrolls it horizontally by moving focus
// one tab over; wheeling anywhere else falls through to the viewport and
// leaves the active tab alone.
func TestWheelOverTabBarScrollsTabs(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	_, _ = ro.Update(tea.WindowSizeMsg{Width: 120, Height: 44})
	// Render + scan so the tab-strip zones get absolute positions.
	// bubblezone registers zone bounds asynchronously after Scan, so poll
	// briefly rather than reading immediately (otherwise it races and the
	// zone reads back nil under load).
	zone.Scan(ro.View())
	tabZoneID := zones.ReviewTab(ro.activeTab)
	deadline := time.Now().Add(time.Second)
	for zone.Get(tabZoneID) == nil {
		if time.Now().After(deadline) {
			t.Fatal("active tab zone not registered after render")
		}
		runtime.Gosched()
	}
	z := zone.Get(tabZoneID)
	tabRow := z.StartY
	start := ro.activeTab

	wheelDown := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown, X: z.StartX, Y: tabRow}
	if _, _ = ro.handleMouse(wheelDown); ro.activeTab != start+1 {
		t.Fatalf("wheel down on tab strip should advance: got %d want %d", ro.activeTab, start+1)
	}

	wheelUp := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp, X: z.StartX, Y: tabRow}
	if _, _ = ro.handleMouse(wheelUp); ro.activeTab != start {
		t.Fatalf("wheel up on tab strip should go back: got %d want %d", ro.activeTab, start)
	}

	// A wheel below the strip must not move tabs (it scrolls the body).
	wheelBody := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown, X: z.StartX, Y: tabRow + 5}
	if _, _ = ro.handleMouse(wheelBody); ro.activeTab != start {
		t.Fatalf("wheel off the strip must not change tab: got %d want %d", ro.activeTab, start)
	}
}
