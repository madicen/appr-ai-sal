package agentstore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/madicen/appr-ai-sal/internal/appdirs"
)

// SourceHash returns a stable short (8-byte, hex) hash over the given input
// parts. The generator subsystems record it on each brief so the freshness UI
// can tell whether a regenerate on identical inputs would change anything.
// Parts are separated by a NUL so ["a","b"] and ["ab"] hash differently.
func SourceHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// PromptOverridePath returns the user-writable path that overrides an
// embedded generator prompt: <config>/prompts/<name>. name is the bare file
// name, e.g. "tech-generator.md" or "repo-agent-security.md".
func PromptOverridePath(name string) string {
	return filepath.Join(appdirs.ConfigDir(), "prompts", name)
}

// ReadPromptOverride reads the override file for name. It returns
// (content, true, nil) when present, ("", false, nil) when absent, and an
// error only for real read failures.
func ReadPromptOverride(name string) (string, bool, error) {
	p := PromptOverridePath(name)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read override %s: %w", p, err)
	}
	return string(b), true, nil
}

// LoadPrompt resolves a generator system prompt with override-then-embedded
// precedence: a user override at <config>/prompts/<overrideName> wins;
// otherwise the prompt embedded at embeddedPath within fsys is returned. This
// is the shared port of the three subsystems' loadGeneratorPrompt helpers.
func LoadPrompt(fsys fs.FS, embeddedPath, overrideName string) (string, error) {
	if override, ok, err := ReadPromptOverride(overrideName); err != nil {
		return "", err
	} else if ok {
		return override, nil
	}
	b, err := fs.ReadFile(fsys, embeddedPath)
	if err != nil {
		return "", fmt.Errorf("load prompt %q: %w", embeddedPath, err)
	}
	return string(b), nil
}
