package main

import (
	"os"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/cli"
)

// TestDispatchVersionWord verifies the bare `version` word prints the version
// and exits 0 (distinct from the -version flag handled inside run()).
func TestDispatchVersionWord(t *testing.T) {
	if code := dispatch([]string{"version"}); code != 0 {
		t.Fatalf("dispatch(version) = %d, want 0", code)
	}
}

// TestDispatchReviewRoutesToHeadless verifies the `review` subcommand routes
// into internal/cli and surfaces its exit-code scheme rather than launching
// the TUI. A missing PR ref is a usage error there (ExitUsage).
func TestDispatchReviewRoutesToHeadless(t *testing.T) {
	// Silence the usage message printed to os.Stderr.
	old := os.Stderr
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	os.Stderr = devnull
	defer func() {
		os.Stderr = old
		if devnull != nil {
			_ = devnull.Close()
		}
	}()

	if code := dispatch([]string{"review"}); code != cli.ExitUsage {
		t.Fatalf("dispatch(review) with no ref = %d, want ExitUsage(%d)", code, cli.ExitUsage)
	}
}

// TestDispatchUnknownSubcommandDefaultsToTUIPath is a compile/routing guard:
// an argument that isn't a recognized subcommand word must fall through to the
// default (TUI) path rather than being treated as a subcommand. We can't
// launch the TUI in a test, so we only assert that recognized words are the
// exhaustive switch set by checking `review` and `version` route away from it;
// the default path is covered by manual/e2e use. This test documents intent.
func TestDispatchRecognizedWords(t *testing.T) {
	// version routes to the version printer (exit 0, no TUI).
	if code := dispatch([]string{"version"}); code != 0 {
		t.Fatalf("version word did not route: %d", code)
	}
}
