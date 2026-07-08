package util

import (
	"strings"
	"testing"
)

// OSC9Sequence wraps the message in the ESC ] 9 ; <msg> BEL frame and collapses
// newlines so a multi-line message can't terminate the escape early.
func TestOSC9SequenceFraming(t *testing.T) {
	seq := OSC9Sequence("review done\nfor PR #1")
	if !strings.HasPrefix(seq, "\x1b]9;") {
		t.Fatalf("OSC-9 sequence should start with ESC]9;, got %q", seq)
	}
	if !strings.HasSuffix(seq, "\x07") {
		t.Fatalf("OSC-9 sequence should end with BEL, got %q", seq)
	}
	if strings.Contains(seq[4:len(seq)-1], "\n") {
		t.Fatalf("newlines in the message should be collapsed, got %q", seq)
	}
	if !strings.Contains(seq, "review done for PR #1") {
		t.Fatalf("message text should be preserved: %q", seq)
	}
}

// NotifyCompleteCmd emits the terminal bell followed by the OSC-9 escape to the
// notify writer when a run completes.
func TestNotifyCompleteEmitsBellAndOSC9(t *testing.T) {
	var buf strings.Builder
	prev := notifyWriter
	notifyWriter = &buf
	t.Cleanup(func() { notifyWriter = prev })

	cmd := NotifyCompleteCmd("Review complete")
	if cmd == nil {
		t.Fatal("a non-empty message should return a command")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("notify command should emit no follow-up message, got %T", msg)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "\a") {
		t.Fatalf("notification should start with the terminal bell, got %q", out)
	}
	if !strings.Contains(out, "\x1b]9;Review complete\x07") {
		t.Fatalf("notification should carry the OSC-9 escape, got %q", out)
	}
}

// An empty message is a no-op (no command, nothing emitted): a run with no
// summary shouldn't ring the bell.
func TestNotifyCompleteEmptyIsNoop(t *testing.T) {
	if cmd := NotifyCompleteCmd("   "); cmd != nil {
		t.Fatal("an empty/whitespace message should return a nil command")
	}
}
