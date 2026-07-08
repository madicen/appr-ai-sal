package repoagents

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"
)

// Q3.10: repo-agent generator prompts are assembled from a shared template
// (prompts/repo-agent-template.md) plus per-specialist delta fragments under
// prompts/deltas/. The on-disk repo-agent-*.md files are golden fixtures;
// TestRepoAgentPromptsMatchGenerated asserts byte-equivalence.
//
//go:embed prompts/repo-agent-template.md prompts/deltas
var repoAgentTemplateFS embed.FS

type repoAgentDelta struct {
	Preamble    string
	WhatToCover string
	WhatToSkip  string
	OutputTail  string
}

var repoAgentSpecialists = []string{"testing", "formatting", "design", "docs", "security"}

var repoAgentPromptTmpl = template.Must(template.New("repo-agent").Parse(mustReadRepoAgentTemplate()))

func mustReadRepoAgentTemplate() string {
	b, err := repoAgentTemplateFS.ReadFile("prompts/repo-agent-template.md")
	if err != nil {
		panic("repoagents: load template: " + err.Error())
	}
	return string(b)
}

func readDeltaFragment(specialist, section string) string {
	path := fmt.Sprintf("prompts/deltas/%s-%s.txt", specialist, section)
	b, err := repoAgentTemplateFS.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("repoagents: load delta %s: %v", path, err))
	}
	return string(b)
}

// RenderRepoAgentPrompt assembles the generator system prompt for a specialist
// from the shared template and its per-topic delta fragments (Q3.10).
func RenderRepoAgentPrompt(specialist string) (string, error) {
	specialist = strings.ToLower(strings.TrimSpace(specialist))
	found := false
	for _, s := range repoAgentSpecialists {
		if s == specialist {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("repoagents: unknown specialist %q", specialist)
	}
	delta := repoAgentDelta{
		Preamble:    readDeltaFragment(specialist, "preamble"),
		WhatToCover: strings.TrimRight(readDeltaFragment(specialist, "cover"), "\n"),
		WhatToSkip:  strings.TrimRight(readDeltaFragment(specialist, "skip"), "\n"),
		OutputTail:  readDeltaFragment(specialist, "output"),
	}
	var b bytes.Buffer
	if err := repoAgentPromptTmpl.Execute(&b, delta); err != nil {
		return "", err
	}
	// Golden fixtures end with exactly one trailing newline.
	out := strings.TrimRight(b.String(), "\n")
	return out + "\n", nil
}
