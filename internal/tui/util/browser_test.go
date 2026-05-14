package util

import (
	"runtime"
	"strings"
	"testing"
)

func TestBrowserOpenCommandRejectsEmpty(t *testing.T) {
	if _, err := BrowserOpenCommand(""); err == nil {
		t.Fatal("expected error for empty URL")
	}
	if _, err := BrowserOpenCommand("   "); err == nil {
		t.Fatal("expected error for whitespace URL")
	}
}

func TestBrowserOpenCommandRejectsNonHTTPScheme(t *testing.T) {
	for _, raw := range []string{
		"file:///etc/passwd",
		"ssh://example.com",
		"javascript:alert(1)",
		"not a url at all",
	} {
		if _, err := BrowserOpenCommand(raw); err == nil {
			t.Errorf("expected error for %q", raw)
		}
	}
}

func TestBrowserOpenCommandAcceptsHTTPS(t *testing.T) {
	cmd, err := BrowserOpenCommand("https://github.com/owner/repo/pull/1")
	if err != nil {
		t.Fatalf("https URL should be accepted on %s: %v", runtime.GOOS, err)
	}
	if cmd == nil || cmd.Path == "" {
		t.Fatal("expected non-nil command with a path")
	}
	// Final argument should always be the URL on every supported OS.
	args := cmd.Args
	if len(args) == 0 {
		t.Fatal("expected at least one arg")
	}
	if got := args[len(args)-1]; got != "https://github.com/owner/repo/pull/1" {
		t.Errorf("URL not last arg: got %q", got)
	}
}

func TestBrowserOpenCommandPlatformBinary(t *testing.T) {
	cmd, err := BrowserOpenCommand("https://example.com/")
	if err != nil {
		// Some platforms (e.g. plan9) are not supported; skip if so.
		if strings.Contains(err.Error(), "unsupported OS") {
			t.Skipf("platform %s is not supported by BrowserOpenCommand", runtime.GOOS)
		}
		t.Fatalf("unexpected error: %v", err)
	}
	switch runtime.GOOS {
	case "darwin":
		if !strings.HasSuffix(cmd.Args[0], "open") {
			t.Errorf("darwin should use 'open', got %q", cmd.Args[0])
		}
	case "linux", "freebsd", "openbsd", "netbsd":
		if !strings.HasSuffix(cmd.Args[0], "xdg-open") {
			t.Errorf("%s should use 'xdg-open', got %q", runtime.GOOS, cmd.Args[0])
		}
	case "windows":
		if !strings.HasSuffix(strings.ToLower(cmd.Args[0]), "rundll32") {
			t.Errorf("windows should use 'rundll32', got %q", cmd.Args[0])
		}
	}
}
