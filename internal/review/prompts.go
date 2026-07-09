package review

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/madicen/appr-ai-sal/internal/appdirs"
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
	// User-defined specialists carry their prompt (loaded from
	// <ConfigDir>/specialists/<name>.md, with any severity ladder appended) in
	// the registry. They have no embedded default to fall back to.
	if s, ok := lookupSpec(name); ok && s.userDefined {
		return s.prompt, nil
	}

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
	return filepath.Join(appdirs.ConfigDir(), "prompts", name+".md")
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
