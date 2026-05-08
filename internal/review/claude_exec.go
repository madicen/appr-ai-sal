package review

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// claudeEnvelope matches the shape of `claude -p --output-format json`'s
// stdout envelope. Only fields we use are typed; everything else is ignored.
type claudeEnvelope struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Error   any    `json:"error,omitempty"`
}

// runClaude invokes claude in print mode with the given system prompt and
// user prompt, with read-only tool access scoped to worktree. It returns the
// raw assistant response (the `result` field of the JSON envelope).
func runClaude(ctx context.Context, cfg *aiconfig.Config, systemPrompt, userPrompt, worktree string) (string, error) {
	model := cfg.AIModelOrDefault()
	if model == "" {
		model = "sonnet"
	}

	args := []string{
		"-p",
		"--output-format", "json",
		"--append-system-prompt", systemPrompt,
		"--add-dir", worktree,
		"--allowed-tools", "Read,Glob,Grep",
		"--permission-mode", "bypassPermissions",
		"--model", model,
		userPrompt,
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = worktree

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	var env claudeEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		return "", fmt.Errorf("parse claude envelope: %w (stdout: %s)", err, truncate(stdout.String(), 500))
	}
	if env.IsError {
		return "", fmt.Errorf("claude returned an error: %v", env.Error)
	}
	return env.Result, nil
}
