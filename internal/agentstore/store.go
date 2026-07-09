// Package agentstore holds the storage, freshness, and prompt-override
// machinery shared by the three per-repo/per-language brief subsystems:
// internal/review/{repoagents,techagents,langagents}.
//
// Before F3 each of those packages re-implemented an owner/repo-slugged JSON
// store, a freshness state machine, and an override-then-embedded prompt
// loader (~1k duplicated lines). This package keeps one copy of each and lets
// the three subsystems be thin domain layers over it: they own their domain
// types and their embedded default prompts, and delegate everything else here.
//
// agentstore depends only on the standard library and internal/appdirs (both
// leaves), so it can be imported by the review subpackages without creating a
// cycle. It is generic over the document type T so it never needs to know the
// concrete domain shapes.
package agentstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/appdirs"
)

// Slug builds the on-disk directory name for an owner/repo pair, e.g.
// "acme__widget". Owner and repo are lower-cased and have path-hostile
// characters folded to underscores. This is the shared port of the former
// repoagents/techagents slugify+repoDir logic.
func Slug(owner, repo string) string {
	return slugifyPart(owner) + "__" + slugifyPart(repo)
}

func slugifyPart(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

// SplitSlug reverses Slug: "acme__widget" -> ("acme", "widget", true). It
// returns ok=false for names that don't contain a non-edge "__" separator.
func SplitSlug(name string) (owner, repo string, ok bool) {
	idx := strings.Index(name, "__")
	if idx <= 0 || idx >= len(name)-2 {
		return "", "", false
	}
	owner = name[:idx]
	repo = name[idx+2:]
	if owner == "" || repo == "" {
		return "", "", false
	}
	return owner, repo, true
}

func normKey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// Store is a generic per-repo JSON document store. A document of type T for
// owner/repo lives at:
//
//	<cache>/<Subdir>/<owner-slug>__<repo-slug>/<FileName>
//
// The caller supplies two hooks so the store stays decoupled from the domain
// shape:
//
//   - New(owner, repo) returns a fresh empty document (used for first-run /
//     absent-file states).
//   - Clean(doc, owner, repo) returns a *normalized copy* of doc safe to
//     marshal: it canonicalizes entry keys/values and fills owner/repo from
//     the arguments when the persisted values are blank. It must not mutate
//     the input (Save is expected not to disturb the caller's value).
type Store[T any] struct {
	Subdir   string
	FileName string
	New      func(owner, repo string) *T
	Clean    func(doc *T, owner, repo string) *T
}

// Dir returns the cache subdirectory holding every repo's document.
func (s Store[T]) Dir() string { return appdirs.CacheSubdir(s.Subdir) }

// RepoDir returns the per-repo directory for owner/repo.
func (s Store[T]) RepoDir(owner, repo string) string {
	return filepath.Join(s.Dir(), Slug(owner, repo))
}

// FilePath returns the absolute path to the document file for owner/repo.
func (s Store[T]) FilePath(owner, repo string) string {
	return filepath.Join(s.RepoDir(owner, repo), s.FileName)
}

// Load reads and normalizes the document for owner/repo. A missing file is
// not an error — a fresh empty document is returned so first-run is a normal
// state. Empty owner/repo is an error.
func (s Store[T]) Load(owner, repo string) (*T, error) {
	owner, repo = normKey(owner), normKey(repo)
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("agentstore.Load: empty owner/repo")
	}
	p := s.FilePath(owner, repo)
	var raw T
	found, err := ReadJSONFile(p, &raw)
	if err != nil {
		return nil, err
	}
	if !found {
		return s.New(owner, repo), nil
	}
	return s.Clean(&raw, owner, repo), nil
}

// Save writes doc atomically as the document for owner/repo. doc is cleaned
// (via Clean) before marshaling; the caller's value is left untouched.
func (s Store[T]) Save(owner, repo string, doc *T) error {
	owner, repo = normKey(owner), normKey(repo)
	if owner == "" || repo == "" {
		return fmt.Errorf("agentstore.Save: empty owner/repo")
	}
	clean := s.Clean(doc, owner, repo)
	return WriteJSONAtomic(s.FilePath(owner, repo), clean)
}

// DeleteRepo removes the entire document file for owner/repo and its parent
// directory when that directory is otherwise empty. Absent files are a no-op.
func (s Store[T]) DeleteRepo(owner, repo string) error {
	owner, repo = normKey(owner), normKey(repo)
	p := s.FilePath(owner, repo)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	dir := s.RepoDir(owner, repo)
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		_ = os.Remove(dir)
	}
	return nil
}

// ListRepos returns "owner/repo" keys (lower-cased) for every repo under Dir
// that has a document file present, sorted alphabetically.
func (s Store[T]) ListRepos() ([]string, error) {
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		owner, repo, ok := SplitSlug(e.Name())
		if !ok {
			continue
		}
		if _, statErr := os.Stat(s.FilePath(owner, repo)); statErr == nil {
			out = append(out, owner+"/"+repo)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ReadJSONFile reads path and unmarshals it into v. It returns found=false
// (and a nil error) when the file is absent so callers can treat first-run as
// a normal empty state rather than an error.
func ReadJSONFile(path string, v any) (found bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	return true, nil
}

// WriteJSONAtomic marshals v (indented, trailing newline) and writes it to
// path via a temp-file rename so a crash never leaves a half-written file.
// The parent directory is created if needed.
func WriteJSONAtomic(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	return os.Rename(tmp, path)
}
