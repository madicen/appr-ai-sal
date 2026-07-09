package repoagents

import (
	"io/fs"
	"strings"
	"testing"
)

// Q3.10: generated prompts must byte-match the embedded golden files so the
// templating refactor is behavior-preserving.
func TestRepoAgentPromptsMatchGenerated(t *testing.T) {
	for _, specialist := range repoAgentSpecialists {
		wantPath := "prompts/repo-agent-" + specialist + ".md"
		wantB, err := fs.ReadFile(promptFS, wantPath)
		if err != nil {
			t.Fatalf("read embedded %s: %v", wantPath, err)
		}
		want := string(wantB)
		got, err := RenderRepoAgentPrompt(specialist)
		if err != nil {
			t.Fatalf("RenderRepoAgentPrompt(%q): %v", specialist, err)
		}
		if got != want {
			t.Fatalf("generated prompt for %q differs from embedded %s\n--- got ---\n%s\n--- want ---\n%s", specialist, wantPath, got, want)
		}
		if strings.TrimSpace(got) == "" {
			t.Fatalf("empty prompt for %q", specialist)
		}
	}
}
