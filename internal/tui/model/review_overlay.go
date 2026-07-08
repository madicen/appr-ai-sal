package model

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	overlay "github.com/madicen/bubble-overlay"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/overlays"
	reviewtab "github.com/madicen/appr-ai-sal/internal/tui/tabs/review"
)

func (m *Model) startReviewOverlay() tea.Cmd {
	if m.currentPR == nil {
		return nil
	}
	ref := gh.Ref{Owner: m.currentPR.Owner, Repo: m.currentPR.Repo, Number: m.currentPR.Number}
	m.draft = nil
	m.cancelReview()
	ctx, cancel := context.WithCancel(context.Background())
	m.reviewCancel = cancel
	if dt := m.detailTab(); dt != nil {
		dt.OnDraftUpdated(m.parsedDiff, m.draft)
	}
	parallelSpec, parallelRE, _ := repoParallelExecutionFlags()
	ro := reviewtab.New(m.width, m.height, m.opts.DryRun, parallelSpec, parallelRE, m.opts.AIConfig, m.opts.Demo)
	ro.SetSpecialists(review.ActiveSpecialists(m.techExpertsConfigured()))
	m.currentReviewOverlay = ro
	cfg := reviewWindowConfig()
	startMinimized := false
	if dt := m.detailTab(); dt != nil {
		startMinimized = dt.StartReviewMinimized()
		dt.SetStartReviewMinimized(false)
	}
	prep := tea.Sequence(
		m.overlayStack.Push(ro, cfg),
		func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
	)
	if startMinimized {
		prep = tea.Sequence(prep, func() tea.Msg { return reviewOverlayMinimizeRequestMsg{} })
	}
	return tea.Batch(prep, data.StartReviewCmd(ctx, ref, m.opts.AIConfig, m.opts.Demo))
}

type reviewOverlayMinimizeRequestMsg struct{}

func (m *Model) minimizeReviewOverlay() tea.Cmd {
	if m.reviewOverlayOnTop() == nil {
		return nil
	}
	if m.width <= 0 || m.height <= 0 {
		return nil
	}
	top, left, regions, ok := m.overlayStack.TopChromeLayout(m.width, m.height)
	if !ok || regions.MinimizeW == 0 {
		return nil
	}
	press := tea.MouseMsg{
		X:      left + regions.MinimizeX + regions.MinimizeW/2,
		Y:      top + regions.MinimizeY,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	return m.overlayStack.Update(press)
}

func reviewWindowConfig() overlay.OverlayConfig {
	cfg := overlay.DefaultOverlayConfig()
	cfg.CloseOnEscape = false
	cfg.CloseOnClickOutside = false
	cfg.DimOpacity = 0
	cfg.WindowChrome = overlay.EnableWindowChrome(reviewtab.ChromeTitleFallback)
	cfg.WindowChrome.Resizable = true
	cfg.WindowChrome.Keyboard = true
	cfg.WindowChrome.KeyStep = 2
	cfg.WindowChrome.MinWidth = 60
	cfg.WindowChrome.MinHeight = 14
	cfg.WindowChrome.ShowMinimizeButton = true
	return cfg
}

func (m *Model) maybeOfferResumeCmd() tea.Cmd {
	if m.opts.Demo || m.currentPR == nil {
		return nil
	}
	if m.overlayStack.Top() != nil {
		return nil
	}
	sha := strings.TrimSpace(m.currentPR.HeadSHA)
	if sha == "" {
		return nil
	}
	ref := gh.Ref{Owner: m.currentPR.Owner, Repo: m.currentPR.Repo, Number: m.currentPR.Number}
	sess, ok := review.NewDraftCache().LoadSession(ref, sha)
	if !ok || sess.Draft == nil {
		return nil
	}
	m.pendingResume = sess
	pending := 0
	for _, d := range sess.Decisions {
		if d.Decision == "" || d.Decision == "pending" {
			pending++
		}
	}
	modal := overlays.NewResumeOverlay(ref.String(), sess.SavedAt, pending)
	cfg := overlay.DefaultOverlayConfig()
	cfg.CloseOnClickOutside = false
	return tea.Batch(
		m.overlayStack.Push(modal, cfg),
		func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
	)
}

func (m *Model) resumeFromSession(popCmd tea.Cmd) tea.Cmd {
	sess := m.pendingResume
	m.pendingResume = nil
	if sess == nil {
		return popCmd
	}
	parallelSpec, parallelRE, _ := repoParallelExecutionFlags()
	ro, adopt := reviewtab.NewResumed(m.width, m.height, m.opts.DryRun, parallelSpec, parallelRE, m.opts.AIConfig, m.opts.Demo, sess)
	m.currentReviewOverlay = ro
	m.draft = ro.Draft()
	if dt := m.detailTab(); dt != nil {
		dt.OnDraftUpdated(m.parsedDiff, m.draft)
	}
	cfg := reviewWindowConfig()
	prep := tea.Sequence(
		popCmd,
		m.overlayStack.Push(ro, cfg),
		func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
	)
	if adopt != nil {
		return tea.Batch(prep, adopt)
	}
	return prep
}

func (m *Model) reopenApproval() tea.Cmd {
	if m.draft == nil {
		return nil
	}
	parallelSpec, parallelRE, _ := repoParallelExecutionFlags()
	ro := reviewtab.New(m.width, m.height, m.opts.DryRun, parallelSpec, parallelRE, m.opts.AIConfig, m.opts.Demo)
	adoptCmd := ro.AdoptDraft(m.draft)
	m.currentReviewOverlay = ro
	cfg := reviewWindowConfig()
	cmds := []tea.Cmd{
		m.overlayStack.Push(ro, cfg),
		func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
	}
	if adoptCmd != nil {
		cmds = append(cmds, adoptCmd)
	}
	if fetch := ro.CmdAfterAdoptIfNeeded(); fetch != nil {
		cmds = append(cmds, fetch)
	}
	return tea.Batch(cmds...)
}
