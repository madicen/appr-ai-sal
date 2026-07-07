package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// claudeProvider runs inference through the local `claude` CLI in print mode.
// It is the only backend with repository tools (Read/Glob/Grep), scoped to the
// PR worktree.
type claudeProvider struct {
	cfg *aiconfig.Config
}

func (p *claudeProvider) Name() string { return string(aiconfig.ProviderClaude) }

func (p *claudeProvider) Capabilities() Capabilities {
	// The Claude subprocess is the only backend that can read the repo.
	// NativeJSON/Streaming stay conservative until later workstreams wire them.
	return Capabilities{RepoTools: true}
}

// claudeEnvelope matches the shape of `claude -p --output-format json`'s
// stdout envelope. Only fields we use are typed; everything else is ignored.
type claudeEnvelope struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Error   any    `json:"error,omitempty"`
	// TotalCostUSD and Usage carry the CLI's own accounting; captured into
	// Result.Usage so cost/token telemetry (R1) has a source.
	TotalCostUSD float64 `json:"total_cost_usd"`
	Usage        struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Complete invokes claude in print mode with the given system prompt and user
// prompt, with read-only tool access scoped to the worktree. It returns the
// raw assistant response (the `result` field of the JSON envelope) plus any
// usage/cost the CLI reported.
func (p *claudeProvider) Complete(ctx context.Context, req Request) (Result, error) {
	cfg := p.cfg
	model := cfg.AIModelOrDefault()
	if model == "" {
		model = "sonnet"
	}

	args := []string{
		"-p",
		"--output-format", "json",
		"--append-system-prompt", req.System,
		"--add-dir", req.Worktree,
		"--allowed-tools", "Read,Glob,Grep",
		"--permission-mode", "bypassPermissions",
		"--model", model,
		req.User,
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = req.Worktree

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return Result{}, fmt.Errorf("claude failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	var env claudeEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		return Result{}, fmt.Errorf("parse claude envelope: %w (stdout: %s)", err, truncate(stdout.String(), 500))
	}
	if env.IsError {
		return Result{}, fmt.Errorf("claude returned an error: %v", env.Error)
	}
	return Result{
		Text:  env.Result,
		Model: model,
		Usage: Usage{
			InputTokens:  env.Usage.InputTokens,
			OutputTokens: env.Usage.OutputTokens,
			CostUSD:      env.TotalCostUSD,
		},
	}, nil
}
