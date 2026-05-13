package theme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultThemeResolvesEverySlot(t *testing.T) {
	d := Default()
	for _, s := range Slots() {
		got := d.Color(s.Key)
		if got == "" {
			t.Errorf("default theme missing colour for %s", s.Key)
		}
		if !validHex(got) {
			t.Errorf("default colour for %s is not valid hex: %q", s.Key, got)
		}
	}
}

func TestThemeColorFallsBackToDefault(t *testing.T) {
	t1 := &Theme{}
	for _, s := range Slots() {
		if got, want := t1.Color(s.Key), DefaultColor(s.Key); got != want {
			t.Errorf("Color(%s) on empty theme: got %q want %q", s.Key, got, want)
		}
	}
}

func TestThemeSetIgnoresInvalidHex(t *testing.T) {
	t1 := Default()
	orig := t1.Color(KeyTagFormatting)
	t1.Set(KeyTagFormatting, "not-a-color")
	if got := t1.Color(KeyTagFormatting); got != orig {
		t.Errorf("Set with invalid hex should be a no-op; got %q want %q", got, orig)
	}
	t1.Set(KeyTagFormatting, "#ABC")
	if got := t1.Color(KeyTagFormatting); got != "#aabbcc" {
		t.Errorf("3-digit hex should expand and lowercase: got %q want #aabbcc", got)
	}
	t1.Set(KeyTagFormatting, "#AAbbCC")
	if got := t1.Color(KeyTagFormatting); got != "#aabbcc" {
		t.Errorf("hex normalization to lowercase failed: got %q", got)
	}
}

func TestThemeApplyAndCurrentAreThreadSafe(t *testing.T) {
	// Smoke test: writers don't deadlock with readers. The mutex is the
	// real guarantee; this test just exercises the public API.
	orig := Current()
	defer Apply(orig)

	custom := Default()
	custom.Set(KeyTagFormatting, "#112233")
	Apply(custom)
	if got := Color(KeyTagFormatting); got != "#112233" {
		t.Errorf("Apply did not take effect: got %q want #112233", got)
	}
	Apply(nil)
	if got := Color(KeyTagFormatting); got != DefaultColor(KeyTagFormatting) {
		t.Errorf("Apply(nil) should reset to defaults: got %q want %q", got, DefaultColor(KeyTagFormatting))
	}
}

func TestSaveOmitsValuesEqualToDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)

	t1 := Default()
	t1.Set(KeyTagFormatting, "#aabbcc")
	if err := Save(t1, ""); err != nil {
		t.Fatalf("Save: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "theme.json"))
	if err != nil {
		t.Fatalf("read theme.json: %v", err)
	}
	var raw struct {
		Colors map[string]string `json:"colors"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw.Colors) != 1 {
		t.Fatalf("expected exactly one override (the changed slot); got %d: %v", len(raw.Colors), raw.Colors)
	}
	if raw.Colors[string(KeyTagFormatting)] != "#aabbcc" {
		t.Errorf("override hex mismatch: %v", raw.Colors)
	}
}

func TestLoadAcceptsDefaultsWhenFileMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)

	got, err := Load()
	if err != nil {
		t.Fatalf("Load with missing file should not error: %v", err)
	}
	if !got.Equal(Default()) {
		t.Errorf("Load with missing file should equal Default()")
	}
}

func TestLoadIgnoresInvalidHexEntries(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)

	body := `{"colors": {"tag_formatting": "totally bogus", "tag_design": "#abc"}}`
	if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed theme.json: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Color(KeyTagFormatting) != DefaultColor(KeyTagFormatting) {
		t.Errorf("invalid hex should fall back to default; got %q", got.Color(KeyTagFormatting))
	}
	if got.Color(KeyTagDesign) != "#aabbcc" {
		t.Errorf("3-digit hex should expand on Load; got %q", got.Color(KeyTagDesign))
	}
}

func TestSlotsCoverEveryKeyExactlyOnce(t *testing.T) {
	seen := map[Key]int{}
	for _, s := range Slots() {
		seen[s.Key]++
	}
	for _, s := range Slots() {
		if seen[s.Key] != 1 {
			t.Errorf("Slots() lists %s %d times; expected 1", s.Key, seen[s.Key])
		}
	}
	for k := range defaultColors() {
		if _, ok := seen[k]; !ok {
			t.Errorf("Slots() missing key %s; UI panel will skip it", k)
		}
	}
}

func TestEqualReportsAcrossDefaultsAndOverrides(t *testing.T) {
	a := Default()
	b := Default()
	if !a.Equal(b) {
		t.Errorf("two fresh defaults should be equal")
	}
	b.Set(KeyTagFormatting, "#000000")
	if a.Equal(b) {
		t.Errorf("themes with different colours should not be equal")
	}
}

func TestDefaultPathHonoursConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)
	got := DefaultPath()
	if !strings.HasPrefix(got, dir) {
		t.Errorf("DefaultPath should live under APPR_AI_SAL_CONFIG_DIR; got %q", got)
	}
	if filepath.Base(got) != "theme.json" {
		t.Errorf("DefaultPath should end in theme.json; got %q", got)
	}
}
