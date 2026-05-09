package review

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:prompts
var promptFS embed.FS

// SpecialistPrompt returns the system prompt for the named specialist.
//
// It first checks for a user override at
// $XDG_CONFIG_HOME/appr-ai-sal/prompts/<name>.md (or
// ~/.config/appr-ai-sal/prompts/<name>.md), and falls back to the prompt
// embedded in the binary at internal/review/prompts/<name>.md. This lets
// users tweak specialist behavior without rebuilding from source.
func SpecialistPrompt(name string) (string, error) {
	if override, ok, err := readOverride(name); err != nil {
		return "", err
	} else if ok {
		return override, nil
	}

	path := "prompts/" + name + ".md"
	b, err := fs.ReadFile(promptFS, path)
	if err != nil {
		return "", fmt.Errorf("load specialist prompt %q: %w", name, err)
	}
	return string(b), nil
}

// OverridePath returns the path the user may write to in order to override the
// embedded prompt for a named specialist.
func OverridePath(name string) string {
	return filepath.Join(configDir(), "prompts", name+".md")
}

func readOverride(name string) (string, bool, error) {
	p := OverridePath(name)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read override %s: %w", p, err)
	}
	return string(b), true, nil
}

func configDir() string {
	if v := os.Getenv("APPR_AI_SAL_CONFIG_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "appr-ai-sal")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".appr-ai-sal"
	}
	return filepath.Join(home, ".config", "appr-ai-sal")
}
