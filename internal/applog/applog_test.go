package applog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Redact must never emit the original secret bytes; an empty string stays
// empty (0.3 acceptance: keys never appear in logs).
func TestRedactHidesKeyMaterial(t *testing.T) {
	const key = "sk-live-0123456789abcdef"
	got := Redact(key)
	if strings.Contains(got, key) {
		t.Fatalf("Redact leaked the key: %q", got)
	}
	if !strings.Contains(got, "REDACTED") {
		t.Fatalf("Redact should mark the value redacted, got %q", got)
	}
	if Redact("") != "" {
		t.Fatalf("Redact(\"\") must stay empty")
	}
	if Redact("   ") != "" {
		t.Fatalf("Redact of blank must stay empty")
	}
}

// LogDir / LogFilePath must resolve deterministically from the explicit
// override env var (0.3 acceptance: the log file path resolves).
func TestLogPathResolvesFromEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPR_AI_SAL_LOG_DIR", dir)
	if got := LogDir(); got != dir {
		t.Fatalf("LogDir = %q, want %q", got, dir)
	}
	want := filepath.Join(dir, LogFileName)
	if got := LogFilePath(); got != want {
		t.Fatalf("LogFilePath = %q, want %q", got, want)
	}
}

// The CONFIG_DIR convention (used by demo/tests) puts logs under a /log
// subdirectory, taking precedence over XDG.
func TestLogDirHonoursConfigDir(t *testing.T) {
	t.Setenv("APPR_AI_SAL_LOG_DIR", "")
	cfg := t.TempDir()
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", cfg)
	want := filepath.Join(cfg, "log")
	if got := LogDir(); got != want {
		t.Fatalf("LogDir = %q, want %q", got, want)
	}
}

// Init must create the log file and never write API-key material even when a
// caller passes a redacted key through the logging helpers.
func TestInitWritesLogWithoutLeakingKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPR_AI_SAL_LOG_DIR", dir)
	if err := Init("test-version"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	const secret = "sk-should-never-appear"
	Info("using key", "api_key", Redact(secret))

	path := filepath.Join(dir, LogFileName)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	content := string(b)
	if !strings.Contains(content, "test-version") {
		t.Fatalf("log should record the startup version, got:\n%s", content)
	}
	if strings.Contains(content, secret) {
		t.Fatalf("log leaked the raw key material:\n%s", content)
	}
	if !strings.Contains(content, "REDACTED") {
		t.Fatalf("expected redacted marker in log, got:\n%s", content)
	}
}
