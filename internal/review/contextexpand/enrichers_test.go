package contextexpand

import (
	"context"
	"reflect"
	"testing"
)

func TestParseGoplsReferences(t *testing.T) {
	wt := "/home/u/repo"
	out := "" +
		"/home/u/repo/pkg/a.go:12:6-16\n" +
		"/home/u/repo/pkg/b.go:44:2\n" +
		"/outside/other.go:3:1\n" + // outside worktree → dropped
		"garbage line without colons\n" +
		"/home/u/repo/pkg/a.go:12:6-16\n" // duplicate → deduped

	got := parseGoplsReferences(out, wt)
	want := []Location{
		{Path: "pkg/a.go", Line: 12},
		{Path: "pkg/b.go", Line: 44},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseGoplsReferences mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestParseCtagsLineField(t *testing.T) {
	// universal-ctags with --fields=+n: a `line:N` extension field.
	out := "" +
		"!_TAG_FILE_FORMAT\t2\t/extended/\n" +
		"Build\tpkg/sample.go\t/^func Build(w Widget) string {$/;\"\tf\tline:8\n" +
		"helper\tpkg/sample.go\t/^func helper(s string) string {$/;\"\tf\tline:12\n"
	tags := parseCtags(out)
	if locs := tags["Build"]; len(locs) != 1 || locs[0].Path != "pkg/sample.go" || locs[0].Line != 8 {
		t.Fatalf("Build tag mismatch: %+v", tags["Build"])
	}
	if locs := tags["helper"]; len(locs) != 1 || locs[0].Line != 12 {
		t.Fatalf("helper tag mismatch: %+v", tags["helper"])
	}
}

func TestParseCtagsNumericAddress(t *testing.T) {
	// Numeric line-number address form (ctags -n): address is a bare number.
	out := "Widget\tmodels/widget.go\t3;\"\tt\n"
	tags := parseCtags(out)
	locs := tags["Widget"]
	if len(locs) != 1 || locs[0].Path != "models/widget.go" || locs[0].Line != 3 {
		t.Fatalf("numeric-address tag mismatch: %+v", locs)
	}
}

// TestDefaultCrossReferencesFailOpen: with both tools absent (lookPath stubbed
// to always fail), the default finder returns nothing rather than erroring.
func TestDefaultCrossReferencesFailOpen(t *testing.T) {
	prev := lookPath
	lookPath = func(string) (string, error) { return "", errNotFound }
	defer func() { lookPath = prev }()

	got := defaultCrossReferences(context.Background(), "/tmp/wt", symbolRef{Name: "Build", Path: "a.go", Line: 1, Col: 1})
	if len(got.Locations) != 0 || got.Tool != "" {
		t.Fatalf("expected empty result when no tools available, got %+v", got)
	}
}

var errNotFound = &lookErr{}

type lookErr struct{}

func (*lookErr) Error() string { return "not found" }
