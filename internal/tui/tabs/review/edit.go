package review

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
)

// edit.go is the TUI implementation of Phase 5 item 2 "edit findings before
// posting". A tool that posts inline comments under the reviewer's own name has
// to let the reviewer own the words: from a finding card, `e` opens a bubbles
// textarea pre-filled with the comment body; `E` (or ctrl+e from inside the
// editor) rounds the same buffer through $EDITOR via tea.ExecProcess. Saving
// replaces the finding's comment (so the posted payload uses the edited text),
// flags the card edited, and persists into CardDecision.EditedBody so a U2
// resume restores the edit. Cancelling reverts.

// EditorFinishedMsg is delivered when the $EDITOR round-trip (openEditorCmd)
// returns. CardIdx echoes the request so a stale completion (the reviewer
// navigated away or closed the editor) is dropped. Body is the file contents
// read back after the editor exited; Err is non-nil on a spawn / read failure
// (the finding is left unchanged — fail-open).
type EditorFinishedMsg struct {
	CardIdx int
	Body    string
	Err     error
}

// OverlayBound marks EditorFinishedMsg for the root's generic overlay
// forwarder (data.ForwardToOverlay), like VibeCoachDoneMsg / ChallengeDoneMsg:
// the tea.ExecProcess callback's message only means something to the overlay,
// so generic routing keeps a future root refactor from stranding the edit.
func (EditorFinishedMsg) OverlayBound() {}

var _ data.ForwardToOverlay = EditorFinishedMsg{}

// editableCardIdx returns the focused card index when it is a finding whose
// comment can be edited (an inline card with a non-empty comment, review done),
// or -1 otherwise. Shared by the `e` / `E` entry points.
func (m *Model) editableCardIdx() int {
	if !m.done {
		return -1
	}
	if m.idx < 0 || m.idx >= len(m.cards) {
		return -1
	}
	if strings.TrimSpace(m.cards[m.idx].finding.Finding.Comment) == "" {
		return -1
	}
	return m.idx
}

// newEditInput builds the textarea for editing a comment, sized to the current
// viewport and pre-filled with body.
func (m *Model) newEditInput(body string) textarea.Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 0 // comment bodies can be long; don't clip them
	ta.Placeholder = "Edit the comment posted for this finding…"
	ta.SetWidth(max(20, m.vp.Width-2))
	ta.SetHeight(max(6, m.vp.Height-6))
	ta.SetValue(body)
	ta.CursorEnd()
	return ta
}

// actOpenEdit opens the inline comment editor for the focused card. No-op when
// no editable card is focused or an edit / challenge is already open.
func (m *Model) actOpenEdit() (tea.Model, tea.Cmd) {
	if m.editActive || m.challengeActive {
		return m, nil
	}
	idx := m.editableCardIdx()
	if idx < 0 {
		return m, nil
	}
	m.editActive = true
	m.editCardIdx = idx
	m.editErr = nil
	m.editInput = m.newEditInput(m.cards[idx].finding.Finding.Comment)
	m.vp.GotoTop()
	m.rebuildBody()
	return m, m.editInput.Focus()
}

// actOpenEditor opens the focused card's comment in $EDITOR (Phase 5 item 2).
// When $EDITOR is unset it falls back to the inline textarea editor so the
// reviewer can still edit. No-op when no editable card is focused.
func (m *Model) actOpenEditor() (tea.Model, tea.Cmd) {
	if m.editActive || m.challengeActive {
		return m, nil
	}
	idx := m.editableCardIdx()
	if idx < 0 {
		return m, nil
	}
	if editorCommand() == "" {
		// No $EDITOR configured — fall back to the inline textarea.
		return m.actOpenEdit()
	}
	return m, openEditorCmd(idx, m.cards[idx].finding.Finding.Comment)
}

// handleEditKey owns keyboard input while the inline comment editor is open.
// esc cancels (reverts); ctrl+s saves; ctrl+e hands the current buffer to
// $EDITOR; every other key edits the textarea.
func (m *Model) handleEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		return m.closeEdit(), nil
	case "ctrl+s":
		return m.saveEdit()
	case "ctrl+e":
		// Hand the current (possibly already-modified) buffer to $EDITOR.
		if editorCommand() == "" {
			m.editErr = fmt.Errorf("$EDITOR is not set")
			m.rebuildBody()
			return m, nil
		}
		return m, openEditorCmd(m.editCardIdx, m.editInput.Value())
	}
	var cmd tea.Cmd
	m.editInput, cmd = m.editInput.Update(msg)
	m.rebuildBody()
	return m, cmd
}

// saveEdit commits the textarea's contents to the focused card's comment,
// flags the card edited, closes the editor, and schedules a session save so
// the edit survives a U2 resume. An empty edit is treated as a cancel (a
// finding must keep some comment body to post).
func (m *Model) saveEdit() (tea.Model, tea.Cmd) {
	idx := m.editCardIdx
	if idx < 0 || idx >= len(m.cards) {
		return m.closeEdit(), nil
	}
	body := strings.TrimRight(m.editInput.Value(), "\n")
	if strings.TrimSpace(body) == "" {
		// Nothing to save — revert rather than post an empty comment.
		return m.closeEdit(), nil
	}
	m.cards[idx].finding.Finding.Comment = body
	m.cards[idx].edited = true
	saveCmd := m.scheduleSessionSave()
	return m.closeEdit(), saveCmd
}

// closeEdit tears the editor down and returns to the normal card view without
// touching the finding (used for cancel and after a save has already applied).
func (m *Model) closeEdit() tea.Model {
	m.editActive = false
	m.editInput.Blur()
	m.editErr = nil
	m.vp.GotoTop()
	m.rebuildBody()
	return m
}

// onEditorFinished folds a completed $EDITOR round-trip back into the card. A
// stale completion (card index moved / editor closed) is dropped. On error the
// message is surfaced (the finding is unchanged). On success the file contents
// replace the comment and the card is flagged edited + a session save is
// scheduled. If the inline editor happened to be open (ctrl+e path) it is
// closed so the reviewer sees the applied result.
func (m *Model) onEditorFinished(msg EditorFinishedMsg) tea.Cmd {
	if msg.CardIdx < 0 || msg.CardIdx >= len(m.cards) {
		return nil
	}
	if msg.Err != nil {
		m.editErr = msg.Err
		m.rebuildBody()
		return nil
	}
	body := strings.TrimRight(msg.Body, "\n")
	if strings.TrimSpace(body) == "" {
		// Editor returned an empty buffer — treat as a no-op edit.
		if m.editActive && m.editCardIdx == msg.CardIdx {
			m.closeEdit()
		}
		return nil
	}
	m.cards[msg.CardIdx].finding.Finding.Comment = body
	m.cards[msg.CardIdx].edited = true
	if m.editActive && m.editCardIdx == msg.CardIdx {
		m.editInput.SetValue(body)
	}
	m.editActive = false
	m.editInput.Blur()
	m.editErr = nil
	m.vp.GotoTop()
	m.rebuildBody()
	return m.scheduleSessionSave()
}

// editorCommand resolves the user's preferred editor: $VISUAL, then $EDITOR
// (the conventional precedence). Returns "" when neither is set.
func editorCommand() string {
	if v := strings.TrimSpace(os.Getenv("VISUAL")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("EDITOR"))
}

// openEditorCmd writes body to a temp file, spawns $EDITOR on it via
// tea.ExecProcess (which suspends the TUI while the editor runs), and reads the
// file back when the editor exits — emitting an EditorFinishedMsg. Fully
// fail-open: any temp-file / editor error is packed into the message's Err so
// the overlay surfaces it and leaves the finding unchanged.
func openEditorCmd(cardIdx int, body string) tea.Cmd {
	editor := editorCommand()
	if editor == "" {
		return func() tea.Msg {
			return EditorFinishedMsg{CardIdx: cardIdx, Err: fmt.Errorf("$EDITOR is not set")}
		}
	}
	f, err := os.CreateTemp("", "appr-ai-sal-comment-*.md")
	if err != nil {
		return func() tea.Msg {
			return EditorFinishedMsg{CardIdx: cardIdx, Err: fmt.Errorf("create temp file: %w", err)}
		}
	}
	path := f.Name()
	if _, werr := f.WriteString(body); werr != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return func() tea.Msg {
			return EditorFinishedMsg{CardIdx: cardIdx, Err: fmt.Errorf("write temp file: %w", werr)}
		}
	}
	_ = f.Close()

	// $EDITOR may carry flags (e.g. "code --wait", "emacsclient -c"); split on
	// whitespace so the invocation matches what a shell would do for the
	// common cases.
	fields := strings.Fields(editor)
	name := fields[0]
	args := append(append([]string(nil), fields[1:]...), path)
	c := exec.Command(name, args...) //nolint:gosec // editor is user-controlled by design ($EDITOR)
	return tea.ExecProcess(c, func(execErr error) tea.Msg {
		defer os.Remove(path)
		if execErr != nil {
			return EditorFinishedMsg{CardIdx: cardIdx, Err: fmt.Errorf("run $EDITOR: %w", execErr)}
		}
		out, rerr := os.ReadFile(path)
		if rerr != nil {
			return EditorFinishedMsg{CardIdx: cardIdx, Err: fmt.Errorf("read edited file: %w", rerr)}
		}
		return EditorFinishedMsg{CardIdx: cardIdx, Body: string(out)}
	})
}

// renderEdit draws the inline comment editor: the finding under edit, the
// textarea, any $EDITOR error, and the key hints. It replaces the normal card
// detail while editing is open.
func (m *Model) renderEdit(rowW int) string {
	var b strings.Builder
	b.WriteString(styles.BoldStyle.Render("Edit comment before posting") + "\n")
	b.WriteString(styles.DimStyle.Render("This is the exact comment body posted under your name. ctrl+s save · esc cancel · ctrl+e open $EDITOR.") + "\n\n")

	if m.editCardIdx >= 0 && m.editCardIdx < len(m.cards) {
		cur := m.cards[m.editCardIdx]
		b.WriteString(styles.RenderTag(cur.finding.Specialist) + "  ")
		loc := cur.finding.Finding.Path
		if cur.finding.Finding.Line > 0 {
			loc += fmt.Sprintf(":%d", cur.finding.Finding.Line)
		}
		b.WriteString(styles.BoldStyle.Render(loc) + "  ")
		b.WriteString(styles.RenderSeverity(string(cur.finding.Finding.Severity)) + "\n\n")
	}

	if m.editErr != nil {
		b.WriteString(styles.ErrStyle.Render("✗ editor failed: "+m.editErr.Error()) + "\n")
		b.WriteString(styles.DimStyle.Render("Edit inline instead, or press esc to cancel.") + "\n\n")
	}

	b.WriteString(m.editInput.View() + "\n\n")
	b.WriteString(styles.DimStyle.Render("ctrl+s save · esc cancel · ctrl+e $EDITOR"))
	b.WriteString("\n")
	return b.String()
}

// onClipboardCopied records the outcome of a copy (Phase 5 item 9) as a brief
// transient footer note. Fail-open: a failed copy sets a status line, never an
// error overlay.
func (m *Model) onClipboardCopied(msg util.ClipboardCopiedMsg) {
	switch {
	case msg.Success && msg.ViaOSC52:
		m.copyStatus = "copied (via terminal)"
	case msg.Success:
		m.copyStatus = "copied to clipboard"
	case msg.Err != nil:
		m.copyStatus = "copy failed: " + msg.Err.Error()
	default:
		m.copyStatus = "copy failed"
	}
	m.rebuildBody()
}

// actCopyFinding copies the focused finding (location + comment) to the
// clipboard (Phase 5 item 9). No-op when no finding is focused.
func (m *Model) actCopyFinding() (tea.Model, tea.Cmd) {
	if m.idx < 0 || m.idx >= len(m.cards) {
		return m, nil
	}
	f := m.cards[m.idx].finding
	loc := f.Finding.Path
	if f.Finding.Line > 0 {
		loc += fmt.Sprintf(":%d", f.Finding.Line)
	}
	var b strings.Builder
	if loc != "" {
		b.WriteString(loc)
		if sev := strings.TrimSpace(string(f.Finding.Severity)); sev != "" {
			b.WriteString("  [" + sev + "]")
		}
		if spec := strings.TrimSpace(f.Specialist); spec != "" {
			b.WriteString("  (" + spec + ")")
		}
		b.WriteString("\n\n")
	}
	b.WriteString(strings.TrimSpace(f.Finding.Comment))
	m.copyStatus = "copying finding…"
	return m, util.CopyPlainTextCmd(b.String())
}

// actCopyHunk copies the focused finding's diff hunk to the clipboard (Phase 5
// item 9). No-op when the focused card has no anchored hunk.
func (m *Model) actCopyHunk() (tea.Model, tea.Cmd) {
	if m.idx < 0 || m.idx >= len(m.cards) {
		return m, nil
	}
	h := m.cards[m.idx].hunk
	if h == nil {
		m.copyStatus = "no hunk to copy"
		m.rebuildBody()
		return m, nil
	}
	m.copyStatus = "copying hunk…"
	return m, util.CopyPlainTextCmd(challengeHunkText(h))
}
