package cli

import (
	"os/exec"
	"strings"
	"testing"
)

// TestHeadlessPathImportsNoBubbletea enforces U1's requirement that the
// headless review path stays free of the TUI stack so CI images stay lean.
// It walks internal/cli's full transitive dependency set via `go list -deps`
// and fails if any bubbletea / lipgloss / internal-tui package is pulled in.
//
// Skips when the go toolchain isn't on PATH (e.g. a restricted sandbox); the
// import graph is otherwise deterministic.
func TestHeadlessPathImportsNoBubbletea(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; skipping dependency-graph check")
	}
	out, err := exec.Command("go", "list", "-deps", "github.com/madicen/appr-ai-sal/internal/cli").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps failed: %v\n%s", err, out)
	}
	forbidden := []string{
		"charmbracelet/bubbletea",
		"charmbracelet/bubbles",
		"charmbracelet/lipgloss",
		"madicen/appr-ai-sal/internal/tui",
	}
	for _, line := range strings.Split(string(out), "\n") {
		dep := strings.TrimSpace(line)
		if dep == "" {
			continue
		}
		for _, bad := range forbidden {
			if strings.Contains(dep, bad) {
				t.Errorf("headless review path must not import %q (found %q)", bad, dep)
			}
		}
	}
}
