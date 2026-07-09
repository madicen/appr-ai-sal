package overlays

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// ResumeOverlay is the U2 resume affordance: when the user reopens a PR that
// has an in-progress saved review session for the current head SHA, this modal
// offers to rehydrate the saved Draft + card decisions (no LLM re-run) or to
// discard the session and start fresh.
//
// ref is the human-readable PR ref (e.g. "owner/repo#42"), savedAt is when the
// session was captured, and pending is how many undecided cards remain, so the
// prompt can tell the reviewer how much work resuming saves.
type ResumeOverlay struct {
	ref     string
	savedAt time.Time
	pending int
}

// NewResumeOverlay constructs the resume prompt for a stored session.
func NewResumeOverlay(ref string, savedAt time.Time, pending int) ResumeOverlay {
	return ResumeOverlay{ref: ref, savedAt: savedAt, pending: pending}
}

// ResumeAnswer is the DismissMsg.Result payload emitted by ResumeOverlay:
// whether the user chose to resume the saved session.
type ResumeAnswer struct {
	Resume bool
}

func (m ResumeOverlay) Init() tea.Cmd { return nil }

func (m ResumeOverlay) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, resumeYesKeys):
			return m, dismiss(ResumeAnswer{Resume: true})
		case key.Matches(msg, resumeNoKeys):
			return m, dismiss(ResumeAnswer{Resume: false})
		case msg.String() == "ctrl+c":
			return m, dismiss(ResumeAnswer{Resume: false})
		}
	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		if z := zone.Get(zones.ResumeYes); z != nil && z.InBounds(msg) {
			return m, dismiss(ResumeAnswer{Resume: true})
		}
		if z := zone.Get(zones.ResumeNo); z != nil && z.InBounds(msg) {
			return m, dismiss(ResumeAnswer{Resume: false})
		}
	}
	return m, nil
}

// resumeAgeLabel renders a compact "just now" / "N min ago" / "N h ago" /
// "N d ago" for the session's SavedAt so the reviewer can judge staleness.
func resumeAgeLabel(savedAt time.Time) string {
	if savedAt.IsZero() {
		return "earlier"
	}
	d := time.Since(savedAt)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d d ago", int(d.Hours()/24))
	}
}

func (m ResumeOverlay) View() string {
	pendingNote := ""
	if m.pending > 0 {
		pendingNote = fmt.Sprintf(" (%d finding(s) still undecided)", m.pending)
	}
	body := styles.BoldStyle.Render("Resume in-progress review?") + "\n\n" +
		styles.DimStyle.Render("A saved review session for "+m.ref+" from "+
			resumeAgeLabel(m.savedAt)+pendingNote+"\nwas found for the current commit. Resume it (no re-run), or discard\nit and start a fresh review.") + "\n\n" +
		zone.Mark(zones.ResumeYes, styles.OkStyle.Render(" Resume (y) ")) + "  " +
		zone.Mark(zones.ResumeNo, styles.ErrStyle.Render(" Discard & re-run (n) "))
	return ModalFrameSized(64).Render(body)
}

var (
	resumeYesKeys = key.NewBinding(key.WithKeys("y", "Y", "enter"))
	resumeNoKeys  = key.NewBinding(key.WithKeys("n", "N", "esc", "q"))
)
