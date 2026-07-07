package findingkey

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestNewNormalizes(t *testing.T) {
	k := New("  Docs ", "  a/b.go  ", 10, "right")
	if k.Specialist != "docs" {
		t.Fatalf("specialist not lower/trimmed: %q", k.Specialist)
	}
	if k.Path != "a/b.go" {
		t.Fatalf("path not trimmed/slashed: %q", k.Path)
	}
	if k.Side != "RIGHT" {
		t.Fatalf("side not upper: %q", k.Side)
	}
}

func TestNewDefaultsSideRight(t *testing.T) {
	if got := New("docs", "a.go", 1, "").Side; got != "RIGHT" {
		t.Fatalf("empty side should default RIGHT, got %q", got)
	}
}

// String must be byte-identical to the legacy suppressionKey /
// conventionwitness alignment key format so it is a drop-in replacement.
func TestStringMatchesLegacyFormat(t *testing.T) {
	got := New("Testing", "pkg/a.go", 42, "left").String()
	want := "testing|pkg/a.go|42|LEFT"
	if got != want {
		t.Fatalf("String() = %q want %q", got, want)
	}
}

func TestLocationOmitsSpecialist(t *testing.T) {
	a := New("security", "a.go", 5, "RIGHT").Location()
	b := New("formatting", "a.go", 5, "").Location()
	if a != b {
		t.Fatalf("Location should be specialist-independent: %q vs %q", a, b)
	}
	if a != "a.go|5|RIGHT" {
		t.Fatalf("unexpected location form %q", a)
	}
}

// PerFinding must match the legacy DemotedFindingKey construction:
// suppressionKey + "|" + first-8-bytes sha256(trim(comment)) hex.
func TestPerFindingMatchesLegacyDemotedKey(t *testing.T) {
	comment := "  the description is empty  "
	k := New("description", "", 0, "")
	sum := sha256.Sum256([]byte("the description is empty"))
	want := k.String() + "|" + hex.EncodeToString(sum[:8])
	if got := k.PerFinding(comment); got != want {
		t.Fatalf("PerFinding = %q want %q", got, want)
	}
}
