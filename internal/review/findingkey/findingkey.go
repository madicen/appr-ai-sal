// Package findingkey defines the single canonical identity for a review
// finding. Three subsystems used to hand-roll their own key strings —
// review.suppressionKey (specialist|path|line|side), the finding_dedupe.go
// grouping key (path\x00line\x00side), and the conventionwitness alignment
// key (specialist|path|line|side) — with subtly different normalization.
// Key unifies all three: build a Key with New, then ask for the exact form
// the call site needs (String, Location, or PerFinding).
//
// The package is a leaf: it imports only the standard library so both the
// review package and its conventionwitness subpackage can depend on it
// without an import cycle.
package findingkey

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"
)

// Key is the normalized identity of a finding location attributed to a
// specialist. All fields are normalized by New so two logically-equal
// findings always produce equal keys:
//   - Specialist is lower-cased and trimmed.
//   - Path is trimmed and forward-slashed.
//   - Side is upper-cased and defaults to "RIGHT" when empty.
type Key struct {
	Specialist string
	Path       string
	Line       int
	Side       string
}

// New returns a normalized Key.
func New(specialist, path string, line int, side string) Key {
	s := strings.ToUpper(strings.TrimSpace(side))
	if s == "" {
		s = "RIGHT"
	}
	return Key{
		Specialist: strings.ToLower(strings.TrimSpace(specialist)),
		Path:       filepath.ToSlash(strings.TrimSpace(path)),
		Line:       line,
		Side:       s,
	}
}

// String is the canonical "specialist|path|line|side" form. It is
// byte-identical to the legacy review.suppressionKey and conventionwitness
// alignment key, so it is safe to use anywhere either was used.
func (k Key) String() string {
	return k.Specialist + "|" + k.Path + "|" + strconv.Itoa(k.Line) + "|" + k.Side
}

// Location is the specialist-independent "path|line|side" form used to group
// findings across specialists (near-duplicate dedupe). Findings on the same
// diff location share a Location regardless of which specialist filed them.
func (k Key) Location() string {
	return k.Path + "|" + strconv.Itoa(k.Line) + "|" + k.Side
}

// PerFinding augments String with a short hash of the finding's comment,
// yielding a stable per-finding key that stays unique even for PR-wide
// findings (path "", line 0) where several findings from one specialist
// otherwise collapse onto the same (specialist, side) String. This is the
// key the "post anyway" opt-in flow uses to toggle one PR-wide finding
// independently of its siblings.
func (k Key) PerFinding(comment string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(comment)))
	return k.String() + "|" + hex.EncodeToString(sum[:8])
}
