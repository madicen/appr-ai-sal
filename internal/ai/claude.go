package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// The Claude subprocess is the only backend that can read the repo. It
	// streams via --output-format stream-json (P6). NativeJSON stays off (JSON
	// goes through the CLI's own --output-format).
	return Capabilities{RepoTools: true, Streaming: true}
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

	if req.Stream {
		return p.completeStreaming(ctx, req, model)
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
	return resultFromClaudeEnvelope(env, model)
}

// resultFromClaudeEnvelope maps a parsed claude JSON envelope (from either the
// whole-response `--output-format json` path or the final `result` event of the
// `--output-format stream-json` path) onto a Result, returning a typed
// ClaudeExecError for an envelope-level error. Shared so the streaming and
// non-streaming paths produce identical Result/Usage.
func resultFromClaudeEnvelope(env claudeEnvelope, model string) (Result, error) {
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

// completeStreaming runs the claude CLI in stream-json mode and parses the
// streamed NDJSON events. It surfaces one liveness heartbeat per event (so a
// long call visibly progresses) and applies the idle / first-byte timeouts to
// stdout — a slow-but-alive run keeps resetting the idle timer instead of dying
// at TimeoutSec. The final `result` event carries the same fields as the
// whole-response envelope, so Result/Usage are identical to the non-streaming
// path.
func (p *claudeProvider) completeStreaming(ctx context.Context, req Request, model string) (Result, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	args := []string{
		"-p",
		// stream-json requires --verbose in print mode.
		"--output-format", "stream-json", "--verbose",
		"--append-system-prompt", req.System,
		"--add-dir", req.Worktree,
		"--allowed-tools", "Read,Glob,Grep",
		"--permission-mode", "bypassPermissions",
		"--model", model,
		req.User,
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = req.Worktree
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return Result{}, &ClaudeExecError{
			ExitCode: -1,
			Class:    classifyClaudeStderr(stderr.String()),
			Stderr:   strings.TrimSpace(stderr.String()),
			Err:      err,
		}
	}

	tr := newStreamTimeoutReader(stdout, cancel, p.cfg.StreamFirstByteTimeout(), p.cfg.StreamIdleTimeout())
	emit := newActivityEmitter(ctx)
	env, parseErr := parseClaudeStreamJSON(tr, func(delta string) { emit.tick(delta) })
	tr.stop()
	waitErr := cmd.Wait()
	emit.flush()

	// A cancelled context takes precedence over the subprocess exit status: a
	// killed process's non-zero exit is not a Claude classification.
	if ctxErr := ctx.Err(); ctxErr != nil {
		if cause := context.Cause(ctx); errors.Is(cause, ErrStreamIdleTimeout) || errors.Is(cause, ErrStreamFirstByteTimeout) {
			return Result{}, cause
		}
		return Result{}, ctxErr
	}
	if waitErr != nil {
		exit := -1
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			exit = ee.ExitCode()
		}
		return Result{}, &ClaudeExecError{
			ExitCode: exit,
			Class:    classifyClaudeStderr(stderr.String()),
			Stderr:   strings.TrimSpace(stderr.String()),
			Err:      waitErr,
		}
	}
	if parseErr != nil {
		// A clean exit but no parseable result event: treat as a transient CLI
		// glitch (like the non-streaming malformed-envelope case) so the shared
		// retry budget gets a chance.
		return Result{}, &ClaudeExecError{
			ExitCode: 0,
			Class:    ClaudeClassTransientNetwork,
			Stderr:   fmt.Sprintf("parse claude stream: %v", parseErr),
			Err:      parseErr,
		}
	}
	return resultFromClaudeEnvelope(env, model)
}

// parseClaudeStreamJSON reads the claude CLI's --output-format stream-json
// output (newline-delimited JSON events) from r, invoking onActivity once per
// event for liveness, and returns the final `result` event's envelope. Unknown
// / non-JSON lines are skipped (fail-open). It is pure over an io.Reader so it
// is tested directly with canned NDJSON — no subprocess required.
func parseClaudeStreamJSON(r io.Reader, onActivity func(delta string)) (claudeEnvelope, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var final claudeEnvelope
	haveResult := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			continue
		}
		if onActivity != nil {
			// Count every event as a liveness tick; the byte length grows with
			// streamed assistant output.
			onActivity(line)
		}
		if probe.Type == "result" {
			if err := json.Unmarshal([]byte(line), &final); err != nil {
				return claudeEnvelope{}, err
			}
			haveResult = true
		}
	}
	if err := sc.Err(); err != nil {
		return claudeEnvelope{}, err
	}
	if !haveResult {
		return claudeEnvelope{}, errors.New("no result event in claude stream output")
	}
	return final, nil
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
