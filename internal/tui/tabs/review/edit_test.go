package review

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
)

// openEditForTest sets up a completed overlay focused on the docs agent's card
// and opens the inline comment editor via the `e` key.
func openEditForTest(t *testing.T) *Model {
	t.Helper()
	ro := New(120, 44, false, false, false, nil, false)
	ro.AdoptDraft(tabsTestDraft())
	focusAgentTabForTest(t, ro, review.SpecDocs)
	if ro.phase != phaseApprove {
		t.Fatalf("expected phaseApprove after focusing an agent tab, got %v", ro.phase)
	}
	ro.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if !ro.editActive {
		t.Fatal("pressing e should open the inline comment editor")
	}
	return ro
}

// e opens the editor pre-filled with the finding's comment; ctrl+s saves the
// edited text back onto the finding so the posted body uses it, and flags the
// card edited. The Draft's posting payload reflects the edit.
func TestEditKeyUpdatesPostedBody(t *testing.T) {
	ro := openEditForTest(t)
	idx := ro.editCardIdx
	if got := ro.editInput.Value(); got != "c1" {
		t.Fatalf("editor should be pre-filled with the comment, got %q", got)
	}
	// Type a replacement and save.
	ro.editInput.SetValue("Reworded by the reviewer.")
	out, _ := ro.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	ro = out.(*Model)
	if ro.editActive {
		t.Fatal("ctrl+s should close the editor")
	}
	if !ro.cards[idx].edited {
		t.Fatal("saved card should be flagged edited")
	}
	if got := ro.cards[idx].finding.Finding.Comment; got != "Reworded by the reviewer." {
		t.Fatalf("finding comment not updated: %q", got)
	}
	// The posted body (what GitHub receives) uses the edited text.
	body := review.ReviewCommentBody(ro.cards[idx].finding.Specialist, ro.cards[idx].finding.Finding)
	if !strings.Contains(body, "Reworded by the reviewer.") {
		t.Fatalf("posted comment body should carry the edited text:\n%s", body)
	}
}

// esc cancels the edit and leaves the finding's original comment untouched.
func TestEditEscCancelsRevertsComment(t *testing.T) {
	ro := openEditForTest(t)
	idx := ro.editCardIdx
	ro.editInput.SetValue("this should be discarded")
	out, _ := ro.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	ro = out.(*Model)
	if ro.editActive {
		t.Fatal("esc should close the editor")
	}
	if ro.cards[idx].edited {
		t.Fatal("cancel must not flag the card edited")
	}
	if got := ro.cards[idx].finding.Finding.Comment; got != "c1" {
		t.Fatalf("cancel should leave the original comment: %q", got)
	}
}

// Saving an empty buffer is treated as a cancel — a finding must keep a comment.
func TestEditEmptySaveReverts(t *testing.T) {
	ro := openEditForTest(t)
	idx := ro.editCardIdx
	ro.editInput.SetValue("   \n  ")
	ro.saveEdit()
	if ro.cards[idx].edited {
		t.Fatal("saving an empty buffer must not flag the card edited")
	}
	if ro.cards[idx].finding.Finding.Comment != "c1" {
		t.Fatalf("empty save should leave the original comment intact: %q", ro.cards[idx].finding.Finding.Comment)
	}
}

// With no $EDITOR set, E falls back to the inline textarea editor rather than
// doing nothing.
func TestEditorUnsetFallsBackToInline(t *testing.T) {
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")
	ro := New(120, 44, false, false, false, nil, false)
	ro.AdoptDraft(tabsTestDraft())
	focusAgentTabForTest(t, ro, review.SpecDocs)
	out, cmd := ro.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("E")})
	ro = out.(*Model)
	if !ro.editActive {
		t.Fatal("E with no $EDITOR should fall back to the inline editor")
	}
	// The fallback opens the inline textarea (focus cmd), not an ExecProcess.
	_ = cmd
}

// With $EDITOR set, E hands off to $EDITOR via a command and does NOT open the
// inline textarea (the round-trip result arrives later as EditorFinishedMsg).
func TestEditorSetReturnsExecCommand(t *testing.T) {
	t.Setenv("EDITOR", "vi")
	ro := New(120, 44, false, false, false, nil, false)
	ro.AdoptDraft(tabsTestDraft())
	focusAgentTabForTest(t, ro, review.SpecDocs)
	out, cmd := ro.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("E")})
	ro = out.(*Model)
	if ro.editActive {
		t.Fatal("E with $EDITOR set should not open the inline editor")
	}
	if cmd == nil {
		t.Fatal("E with $EDITOR set should return an exec command")
	}
}

// onEditorFinished folds a successful $EDITOR round-trip back onto the card.
func TestOnEditorFinishedAppliesBody(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.AdoptDraft(tabsTestDraft())
	focusAgentTabForTest(t, ro, review.SpecDocs)
	idx := ro.idx
	ro.onEditorFinished(EditorFinishedMsg{CardIdx: idx, Body: "edited via $EDITOR\n"})
	if !ro.cards[idx].edited {
		t.Fatal("a successful editor round-trip should flag the card edited")
	}
	if got := ro.cards[idx].finding.Finding.Comment; got != "edited via $EDITOR" {
		t.Fatalf("editor body should be applied (trailing newline trimmed): %q", got)
	}
}

// A failed $EDITOR round-trip surfaces the error and leaves the finding
// unchanged (fail-open).
func TestOnEditorFinishedFailOpen(t *testing.T) {
	ro := openEditForTest(t)
	idx := ro.editCardIdx
	ro.onEditorFinished(EditorFinishedMsg{CardIdx: idx, Err: errForTest("editor blew up")})
	if ro.cards[idx].edited {
		t.Fatal("a failed round-trip must not flag the card edited")
	}
	if ro.cards[idx].finding.Finding.Comment != "c1" {
		t.Fatalf("a failed round-trip must leave the comment unchanged: %q", ro.cards[idx].finding.Finding.Comment)
	}
	if ro.editErr == nil {
		t.Fatal("editErr should be set on a failed round-trip")
	}
}

// editorCommand prefers $VISUAL over $EDITOR, and returns "" when neither set.
func TestEditorCommandPrecedence(t *testing.T) {
	t.Setenv("VISUAL", "code --wait")
	t.Setenv("EDITOR", "vi")
	if got := editorCommand(); got != "code --wait" {
		t.Fatalf("editorCommand should prefer $VISUAL, got %q", got)
	}
	t.Setenv("VISUAL", "")
	if got := editorCommand(); got != "vi" {
		t.Fatalf("editorCommand should fall back to $EDITOR, got %q", got)
	}
	t.Setenv("EDITOR", "")
	if got := editorCommand(); got != "" {
		t.Fatalf("editorCommand should be empty with neither set, got %q", got)
	}
}

// An edit survives a U2 session round-trip: collectSession records the edited
// body and applySession reapplies it onto a freshly-adopted overlay.
func TestEditSurvivesSessionRoundTrip(t *testing.T) {
	ro := openEditForTest(t)
	idx := ro.editCardIdx
	ro.editInput.SetValue("persisted edit")
	ro.saveEdit()

	sess := ro.collectSession()
	if sess == nil {
		t.Fatal("collectSession returned nil")
	}
	// The edited body is captured in the decision layer.
	key := ro.cardIdentity(ro.cards[idx])
	var found bool
	for _, d := range sess.Decisions {
		if d.Key == key {
			found = true
			if d.EditedBody != "persisted edit" {
				t.Fatalf("session decision EditedBody = %q want %q", d.EditedBody, "persisted edit")
			}
		}
	}
	if !found {
		t.Fatal("edited card's decision not found in session")
	}

	// Rehydrate a fresh overlay from the same draft + apply the session.
	ro2 := New(120, 44, false, false, false, nil, false)
	ro2.AdoptDraft(tabsTestDraft())
	ro2.applySession(sess)
	// Find the matching card by identity and confirm the edit was restored.
	for i := range ro2.cards {
		if ro2.cardIdentity(ro2.cards[i]) == key {
			if !ro2.cards[i].edited {
				t.Fatal("restored card should be flagged edited")
			}
			if ro2.cards[i].finding.Finding.Comment != "persisted edit" {
				t.Fatalf("restored comment = %q want %q", ro2.cards[i].finding.Finding.Comment, "persisted edit")
			}
			return
		}
	}
	t.Fatal("edited card not found after session restore")
}

// ctrl+y copies the focused finding (location + comment): it issues a copy
// command and sets a transient "copying…" status. Applying the resulting
// ClipboardCopiedMsg flips the status to a success note. We don't invoke the
// copy command here (that would touch the real system clipboard); the native /
// OSC52 helper is exercised hermetically in the util package.
func TestCopyFindingCommand(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.AdoptDraft(tabsTestDraft())
	focusAgentTabForTest(t, ro, review.SpecDocs)
	out, cmd := ro.handleKey(tea.KeyMsg{Type: tea.KeyCtrlY})
	ro = out.(*Model)
	if cmd == nil {
		t.Fatal("ctrl+y should return a copy command")
	}
	if !strings.Contains(ro.copyStatus, "copying") {
		t.Fatalf("ctrl+y should set a transient copying status, got %q", ro.copyStatus)
	}
	ro.onClipboardCopied(util.ClipboardCopiedMsg{Success: true, ViaOSC52: true})
	if !strings.Contains(ro.copyStatus, "copied") {
		t.Fatalf("copy status should reflect success, got %q", ro.copyStatus)
	}
	// Fail-open: a failed copy sets a status, never crashes.
	ro.onClipboardCopied(util.ClipboardCopiedMsg{Success: false, Err: errForTest("no clipboard")})
	if !strings.Contains(ro.copyStatus, "failed") {
		t.Fatalf("failed copy should surface a status, got %q", ro.copyStatus)
	}
}

// ctrl+o copies the focused finding's diff hunk when one is anchored.
func TestCopyHunkCommand(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.AdoptDraft(tabsTestDraft())
	focusAgentTabForTest(t, ro, review.SpecDocs)
	if ro.idx < 0 || ro.cards[ro.idx].hunk == nil {
		t.Skip("docs card has no anchored hunk in this fixture")
	}
	out, cmd := ro.handleKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	ro = out.(*Model)
	if cmd == nil {
		t.Fatal("ctrl+o should return a copy command when a hunk is anchored")
	}
	if !strings.Contains(ro.copyStatus, "copying") {
		t.Fatalf("ctrl+o should set a transient copying status, got %q", ro.copyStatus)
	}
}
