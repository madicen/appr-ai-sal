package util

import (
	"errors"
	"strings"
	"testing"
)

// swapClipboardBackends redirects the native writer and OSC52 sink for the
// duration of a test and restores them afterward.
func swapClipboardBackends(t *testing.T, native func(string) error, osc *strings.Builder) {
	t.Helper()
	prevNative := nativeClipboardWrite
	prevOSC := osc52Writer
	nativeClipboardWrite = native
	osc52Writer = osc
	t.Cleanup(func() {
		nativeClipboardWrite = prevNative
		osc52Writer = prevOSC
	})
}

// When the native clipboard succeeds, the copy reports success and does NOT
// fall back to OSC52 (nothing written to the terminal sink).
func TestCopyNativeSucceeds(t *testing.T) {
	var got string
	var osc strings.Builder
	swapClipboardBackends(t, func(s string) error { got = s; return nil }, &osc)

	msg := CopyPlainTextCmd("hello world")().(ClipboardCopiedMsg)
	if !msg.Success || msg.ViaOSC52 || msg.Err != nil {
		t.Fatalf("native success should be Success/!OSC52/nil-err, got %+v", msg)
	}
	if got != "hello world" {
		t.Fatalf("native writer received %q", got)
	}
	if osc.Len() != 0 {
		t.Fatalf("OSC52 sink should be untouched on native success, got %q", osc.String())
	}
}

// When the native clipboard fails, the copy falls back to OSC52: it still
// reports success (fail-open), flags ViaOSC52, and emits an OSC52 escape
// carrying the payload.
func TestCopyFallsBackToOSC52(t *testing.T) {
	var osc strings.Builder
	swapClipboardBackends(t, func(string) error { return errors.New("no clipboard utilities available") }, &osc)

	msg := CopyPlainTextCmd("copy me")().(ClipboardCopiedMsg)
	if !msg.Success {
		t.Fatalf("OSC52 fallback should still report success (fail-open): %+v", msg)
	}
	if !msg.ViaOSC52 {
		t.Fatal("fallback should flag ViaOSC52")
	}
	if msg.Err != nil {
		t.Fatalf("OSC52 fallback should not surface an error: %v", msg.Err)
	}
	// OSC52 escape starts with ESC ] 52 ; and terminates with BEL.
	out := osc.String()
	if !strings.Contains(out, "\x1b]52;") {
		t.Fatalf("expected an OSC52 escape, got %q", out)
	}
}

// A copy that fails on BOTH the native path and the OSC52 write reports
// Success:false with the error — the host flashes a status, never crashes.
func TestCopyFailOpenOnTotalFailure(t *testing.T) {
	prevNative := nativeClipboardWrite
	prevOSC := osc52Writer
	nativeClipboardWrite = func(string) error { return errors.New("native down") }
	osc52Writer = failingWriter{}
	t.Cleanup(func() {
		nativeClipboardWrite = prevNative
		osc52Writer = prevOSC
	})

	msg := CopyPlainTextCmd("x")().(ClipboardCopiedMsg)
	if msg.Success {
		t.Fatal("a total copy failure should report Success:false")
	}
	if msg.Err == nil {
		t.Fatal("a total copy failure should carry the error")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
