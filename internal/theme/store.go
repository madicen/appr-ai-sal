package theme

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// DefaultPath is ~/.config/appr-ai-sal/theme.json (honours
// APPR_AI_SAL_CONFIG_DIR / XDG_CONFIG_HOME via aiconfig.ConfigDir).
func DefaultPath() string {
	return filepath.Join(aiconfig.ConfigDir(), "theme.json")
}

// Load reads theme.json if present and returns the merged theme. A missing
// file is not an error — defaults are returned. The returned theme always
// resolves Color() lookups even when overrides are partial.
func Load() (*Theme, error) {
	path := DefaultPath()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var t Theme
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	out := Default()
	for k, v := range t.Colors {
		if validHex(v) {
			out.Colors[k] = normalizeHex(v)
		}
	}
	return out, nil
}

// Save writes t to path (DefaultPath when empty). Only entries that differ
// from the default are persisted so the file stays minimal as the shipped
// palette evolves.
func Save(t *Theme, path string) error {
	if t == nil {
		return fmt.Errorf("nil theme")
	}
	if path == "" {
		path = DefaultPath()
	}
	overrides := map[Key]string{}
	for _, s := range Slots() {
		v := t.Color(s.Key)
		if v != "" && v != DefaultColor(s.Key) {
			overrides[s.Key] = normalizeHex(v)
		}
	}
	b, err := json.MarshalIndent(Theme{Colors: overrides}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal theme: %w", err)
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
