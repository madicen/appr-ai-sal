package review

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGroundingV1OverrideLoads(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	cfg := filepath.Join(root, "experiments", "grounding-v1")
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", cfg)
	p, err := SpecialistPrompt("security")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, "grounding-v1") {
		t.Fatalf("override not loaded; config=%s exists=%v", cfg, dirExists(cfg))
	}
}

func dirExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
