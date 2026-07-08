package util

import (
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// notify.go implements the unobtrusive run-completion notification (Phase 5
// item 3): a terminal bell plus an OSC-9 desktop-notification escape. Both are
// terminal control sequences the surrounding terminal decides whether to act
// on — a focused terminal typically ignores/flashes them, an unfocused or
// minimized one raises a desktop notification / bounces the dock icon — so we
// can fire them unconditionally on completion and stay out of the user's way.

// notifyWriter is where the bell + OSC-9 escape is emitted. It defaults to
// os.Stderr (kept separate from the OSC52 clipboard writer, which targets
// stdout) so tests can capture the emitted bytes without touching the real
// terminal.
var notifyWriter io.Writer = os.Stderr

// bell is the ASCII BEL control character (^G) — the classic terminal bell.
const bell = "\a"

// OSC9Sequence returns the OSC-9 desktop-notification escape for msg:
//
//	ESC ] 9 ; <message> BEL
//
// Modern terminals (iTerm2, kitty, WezTerm, Windows Terminal, …) surface this
// as a native desktop notification when the window is not focused. Newlines in
// the message are collapsed to spaces so a multi-line message can't terminate
// the sequence early or spill into the terminal.
func OSC9Sequence(msg string) string {
	clean := strings.ReplaceAll(msg, "\n", " ")
	clean = strings.ReplaceAll(clean, "\a", " ")
	return "\x1b]9;" + clean + "\x07"
}

// completionNotification is the bell followed by the OSC-9 escape.
func completionNotification(msg string) string {
	return bell + OSC9Sequence(msg)
}

// NotifyCompleteCmd returns a tea.Cmd that rings the terminal bell and emits
// an OSC-9 desktop notification carrying msg. It is fail-open — a write error
// is swallowed (a notification is a nicety, never worth crashing a run) — and
// emits nothing for an empty message. Callers gate it to real (non-demo,
// non-test) runs so recordings and unit tests stay quiet.
func NotifyCompleteCmd(msg string) tea.Cmd {
	if strings.TrimSpace(msg) == "" {
		return nil
	}
	return func() tea.Msg {
		_, _ = io.WriteString(notifyWriter, completionNotification(msg))
		return nil
	}
}
