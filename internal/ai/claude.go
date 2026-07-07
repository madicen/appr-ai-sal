package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	stdout, err := runClaude(ctx, req.Worktree, args)
	if err != nil {
		return Result{}, err
	}

	var env claudeEnvelope
	if err := json.Unmarshal(stdout, &env); err != nil {
		// A malformed envelope after a clean exit is a transient CLI glitch
		// (partial/streamed output) more often than a permanent one, so
		// classify it as transient-network so the shared retry budget gets a
		// chance to clear it, while still carrying the raw stdout for
		// diagnosis.
		return Result{}, &ClaudeExecError{
			ExitCode: 0,
			Class:    ClaudeClassTransientNetwork,
			Stderr:   fmt.Sprintf("parse claude envelope: %v (stdout: %s)", err, truncate(string(stdout), 500)),
			Err:      err,
		}
	}
	if env.IsError {
		// The process exited 0 but the JSON envelope reports an error. Classify
		// from the envelope's error text so a rate-limit / transient failure
		// surfaced this way is still retryable.
		msg := fmt.Sprintf("%v", env.Error)
		return Result{}, &ClaudeExecError{
			ExitCode: 0,
			Class:    classifyClaudeStderr(msg + " " + env.Subtype),
			Stderr:   strings.TrimSpace(msg),
			Err:      fmt.Errorf("claude returned an error: %s", strings.TrimSpace(msg)),
		}
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

// runClaude executes the `claude` CLI in dir with args and returns its stdout.
// On failure it returns a typed *ClaudeExecError carrying the process exit code
// and a stderr-derived classification, so retry logic can decide via
// errors.As(&ClaudeExecError) + (*ClaudeExecError).Retryable() instead of
// scanning the whole error string for magic substrings.
func runClaude(ctx context.Context, dir string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Prefer the context error: a cancelled/expired run is not a Claude
		// classification and must not be treated as a retryable subprocess
		// failure (IsRetryableCompleteError already handles ctx errors).
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		exit := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
		}
		return nil, &ClaudeExecError{
			ExitCode: exit,
			Class:    classifyClaudeStderr(stderr.String()),
			Stderr:   strings.TrimSpace(stderr.String()),
			Err:      err,
		}
	}
	return stdout.Bytes(), nil
}
