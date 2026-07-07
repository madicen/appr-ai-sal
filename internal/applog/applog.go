// Package applog is a leaf structured-logging setup for appr-ai-sal.
//
// A TUI app cannot log to stderr — that corrupts the alt-screen — so all
// diagnostics go to a file under the app's state directory. The logger is
// initialised once from main via Init; everything else in the codebase logs
// through the package-level helpers. Until Init succeeds every helper is a
// no-op (writes to io.Discard), so importing this package never forces a file
// to exist.
//
// This package imports only the standard library so any other package can
// depend on it without risking an import cycle.
package applog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LogFileName is the file the logger writes to inside LogDir().
const LogFileName = "appr-ai-sal.log"

var (
	mu     sync.RWMutex
	logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	sink   io.WriteCloser
)

// stageKey is the context key carrying the current pipeline stage label so
// leaf LLM/transport calls can tag their log lines without a wider signature
// change.
type stageKey struct{}

// WithStage returns a context that carries stage as the telemetry label for
// LLM calls made under it (e.g. "specialist security", "repo-arbiter").
func WithStage(ctx context.Context, stage string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, stageKey{}, stage)
}

// StageFromContext returns the stage label set by WithStage, or "".
func StageFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(stageKey{}).(string); ok {
		return v
	}
	return ""
}

// LogDir resolves the directory log files are written to. Precedence:
//
//  1. APPR_AI_SAL_LOG_DIR (explicit override)
//  2. APPR_AI_SAL_CONFIG_DIR/log (honours the codebase's config-dir isolation,
//     used by demo mode and tests)
//  3. XDG_STATE_HOME/appr-ai-sal/log
//  4. ~/.local/state/appr-ai-sal/log
//  5. XDG_CACHE_HOME/appr-ai-sal/log
//  6. ./.appr-ai-sal/log (last resort)
func LogDir() string {
	if v := strings.TrimSpace(os.Getenv("APPR_AI_SAL_LOG_DIR")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("APPR_AI_SAL_CONFIG_DIR")); v != "" {
		return filepath.Join(v, "log")
	}
	if v := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); v != "" {
		return filepath.Join(v, "appr-ai-sal", "log")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "state", "appr-ai-sal", "log")
	}
	if v := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); v != "" {
		return filepath.Join(v, "appr-ai-sal", "log")
	}
	return filepath.Join(".appr-ai-sal", "log")
}

// LogFilePath returns the absolute path of the log file (LogDir/LogFileName).
func LogFilePath() string {
	return filepath.Join(LogDir(), LogFileName)
}

// levelFromEnv reads APPR_AI_SAL_LOG_LEVEL (debug/info/warn/error). Defaults
// to info.
func levelFromEnv() slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APPR_AI_SAL_LOG_LEVEL"))) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Init opens the log file and installs the file-backed slog logger. It is
// idempotent-ish: calling it again replaces the logger and closes the prior
// sink. version is stamped on the first line so a log file names the build
// that produced it. Returns an error only when the log file cannot be opened;
// in that case the previous (or discard) logger stays in place.
func Init(version string) error {
	dir := LogDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create log dir %s: %w", dir, err)
	}
	path := filepath.Join(dir, LogFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", path, err)
	}
	h := slog.NewTextHandler(f, &slog.HandlerOptions{Level: levelFromEnv()})
	newLogger := slog.New(h)

	mu.Lock()
	if sink != nil {
		_ = sink.Close()
	}
	sink = f
	logger = newLogger
	mu.Unlock()

	newLogger.Info("appr-ai-sal started", "version", version, "level", levelFromEnv().String(), "time", time.Now().Format(time.RFC3339))
	return nil
}

// L returns the current logger. Safe to call before Init (no-op logger).
func L() *slog.Logger {
	mu.RLock()
	defer mu.RUnlock()
	return logger
}

// Debug / Info / Warn / Error are thin convenience wrappers over the current
// logger so callers don't have to fetch it first.
func Debug(msg string, args ...any) { L().Debug(msg, args...) }
func Info(msg string, args ...any)  { L().Info(msg, args...) }
func Warn(msg string, args ...any)  { L().Warn(msg, args...) }
func Error(msg string, args ...any) { L().Error(msg, args...) }

// Redact masks secret-like material so keys never reach the log. An empty
// string stays empty; anything else is reduced to a length-tagged mask that
// reveals no key bytes. Use it on anything that might contain an API key or
// token before logging.
func Redact(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return fmt.Sprintf("REDACTED(len=%d)", len(s))
}

// LLMCall logs one inference call (provider, model, stage, duration, retry
// count, error). Never pass API key material here — provider/model/stage are
// non-secret labels. duration is the wall-clock of the whole call including
// retries.
func LLMCall(ctx context.Context, provider, model string, retries int, dur time.Duration, err error) {
	stage := StageFromContext(ctx)
	if err != nil {
		L().Warn("llm call failed",
			"provider", provider, "model", model, "stage", stage,
			"retries", retries, "duration_ms", dur.Milliseconds(), "err", err.Error())
		return
	}
	L().Info("llm call",
		"provider", provider, "model", model, "stage", stage,
		"retries", retries, "duration_ms", dur.Milliseconds())
}

// GHInvocation logs one gh CLI invocation (args, duration, error). Callers
// should pass args that carry no secrets — gh inherits its own auth, so the
// argv never contains a token, but keep this in mind if that ever changes.
func GHInvocation(args []string, dur time.Duration, err error) {
	if err != nil {
		L().Warn("gh invocation failed",
			"args", strings.Join(args, " "), "duration_ms", dur.Milliseconds(), "err", err.Error())
		return
	}
	L().Debug("gh invocation",
		"args", strings.Join(args, " "), "duration_ms", dur.Milliseconds())
}
