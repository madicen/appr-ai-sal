package demo

import (
	"context"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// FakeComplete is the demo-mode CompleteFunc passed to the repo / lang /
// tech agent generators. It sleeps to simulate inference latency, then
// returns a canned plausible-looking brief synthesised from the user
// prompt's section headers. We keep the body short — VHS recordings
// don't have time to scroll through walls of text — but realistic
// enough that the rendered "fresh" tab shows useful content.
//
// Signature matches the shared ai.CompleteFunc that review.Complete and the
// repo / lang / tech agent generators all use.
func FakeComplete(ctx context.Context, cfg *aiconfig.Config, system, user, worktree string) (string, error) {
	// Sleep so the regen-all flow shows the in-progress chip for a beat
	// instead of snapping to "fresh" on the next render. Cancellable so
	// a tape with a hard exit doesn't leave goroutines hanging.
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(900 * time.Millisecond):
	}

	hint := classifyPrompt(system, user)
	switch hint {
	case "tech-suggest":
		return demoTechSuggestions, nil
	case "language":
		return demoLangBrief, nil
	case "tech":
		return demoTechBrief, nil
	case "convention-witness":
		return demoWitnessBrief, nil
	default:
		return demoRepoBrief, nil
	}
}

// classifyPrompt heuristically picks the brief shape to return. The
// agent generators don't expose a tag in their CompleteFunc contract,
// so we sniff the prompts: each generator writes a stable, distinct
// header that we can grep for.
func classifyPrompt(system, user string) string {
	all := strings.ToLower(system + " " + user)
	switch {
	case strings.Contains(all, "technology suggester") || strings.Contains(all, "propose the technologies"):
		return "tech-suggest"
	case strings.Contains(all, "language brief") || strings.Contains(all, "lang brief"):
		return "language"
	case strings.Contains(all, "technology") || strings.Contains(all, "tech brief"):
		return "tech"
	case strings.Contains(all, "convention witness") || strings.Contains(all, "witness verdict"):
		return "convention-witness"
	default:
		return "repo"
	}
}

const demoRepoBrief = `# Repo brief

This repository is a Go TUI built on Bubble Tea + Lipgloss with a
specialist-AI review pipeline.

## Conventions worth honouring

- Public functions and exported types carry doc comments that explain
  intent (the "why"), not just shape.
- Table-driven tests live next to the code under review; fixtures stay
  inline as ` + "`const`" + ` strings unless they exceed ~30 lines.
- Errors flow up with ` + "`fmt.Errorf(\"...: %w\", err)`" + ` — never log-and-swallow.
- New TUI panes register clickable regions through ` + "`bubblezone`" + `.

## Hot spots

- ` + "`internal/review`" + ` — runner + specialist plumbing; changes here
  ripple through the whole UI.
- ` + "`internal/tui/model`" + ` — root model + persistent overlay state.
- ` + "`internal/gh`" + ` — every external GitHub call funnels here.
`

const demoLangBrief = `# Language brief: Go

Go-specific guidance to surface in code review.

## Idioms

- Prefer composition over inheritance; embed structs instead of
  building deep type hierarchies.
- Return ` + "`(T, error)`" + ` from any operation that can fail. Never panic
  for ordinary error paths.
- Keep ` + "`init()`" + ` minimal — it runs before ` + "`main`" + ` and breaks tests
  that swap globals.

## Things to flag

- Goroutines without a clear cancellation path.
- ` + "`time.Sleep`" + ` in production code paths (smell for missing channel
  signalling).
- Mutex use without a doc comment that says what invariant the mutex
  protects.
`

const demoTechBrief = `# Technology brief: Bubble Tea

The TUI uses Bubble Tea's Elm-style update loop. Keep these in mind:

- All side effects flow through ` + "`tea.Cmd`" + ` returned from ` + "`Update`" + `.
- Widgets exchange messages via ` + "`tea.Msg`" + ` — never reach into another
  model's internals from a sibling view.
- ` + "`viewport.Model`" + ` content must be set via ` + "`SetContent`" + ` for the
  scroll math to stay consistent.
- Mouse zones use ` + "`bubblezone`" + `; register zones inside the view
  function so re-renders rebuild fresh hit boxes.
`

const demoTechSuggestions = `[
  {"tech": "bubble-tea", "label": "Bubble Tea", "seed": "TUI built on Bubble Tea's Elm-style update loop; models under internal/tui.", "rationale": "charmbracelet/bubbletea in go.mod"},
  {"tech": "lipgloss", "label": "Lip Gloss", "seed": "Terminal styling via Lip Gloss; shared styles per tab.", "rationale": "charmbracelet/lipgloss in go.mod"},
  {"tech": "bubblezone", "label": "BubbleZone", "seed": "Mouse hit-testing via bubblezone; zones registered in view functions.", "rationale": "lrstanley/bubblezone in go.mod"},
  {"tech": "github-actions", "label": "GitHub Actions", "seed": "CI + release automation under .github/workflows.", "rationale": "release.yml workflow"}
]`

const demoWitnessBrief = `# Convention witness verdict

Classifying testing/docs findings against the PR's static evidence:

- congruent: the repo already follows the convention the finding
  asserts; the suggested change reinforces existing practice.
- divergent: the repo has direct evidence of the opposite practice;
  the finding contradicts established convention.
- unknown: not enough evidence either way; surface as-is.

Use the strongest signal available: sibling tests, doc.go presence,
exported-symbol coverage, recent merged PRs touching the same paths.
`
