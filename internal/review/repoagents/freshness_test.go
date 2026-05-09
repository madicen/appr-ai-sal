package repoagents

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestComputeFreshness(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	staleAfter := 30 * 24 * time.Hour

	makeAgents := func(populate map[string]time.Time) *RepoAgents {
		ra := &RepoAgents{Owner: "o", Repo: "r", Agents: map[string]Agent{}}
		for sp, gen := range populate {
			ra.Agents[sp] = Agent{
				Specialist:  sp,
				Context:     "non-empty body for " + sp,
				GeneratedAt: gen,
			}
		}
		return ra
	}

	cases := []struct {
		name string
		ra   *RepoAgents
		want Freshness
	}{
		{
			name: "nil RepoAgents -> missing",
			ra:   nil,
			want: FreshnessMissing,
		},
		{
			name: "empty agents map -> missing",
			ra:   &RepoAgents{Owner: "o", Repo: "r", Agents: map[string]Agent{}},
			want: FreshnessMissing,
		},
		{
			name: "all whitespace contexts -> missing",
			ra: &RepoAgents{Owner: "o", Repo: "r", Agents: map[string]Agent{
				"formatting": {Specialist: "formatting", Context: "   ", GeneratedAt: now},
				"design":     {Specialist: "design", Context: "\n\n", GeneratedAt: now},
				"testing":    {Specialist: "testing", Context: "", GeneratedAt: now},
				"docs":       {Specialist: "docs", Context: "", GeneratedAt: now},
				"security":   {Specialist: "security", Context: "", GeneratedAt: now},
			}},
			want: FreshnessMissing,
		},
		{
			name: "some specialists missing -> incomplete",
			ra: makeAgents(map[string]time.Time{
				"formatting": now.Add(-1 * time.Hour),
				"design":     now.Add(-1 * time.Hour),
			}),
			want: FreshnessIncomplete,
		},
		{
			name: "all specialists, recent -> fresh",
			ra: makeAgents(map[string]time.Time{
				"formatting": now.Add(-1 * time.Hour),
				"design":     now.Add(-1 * time.Hour),
				"testing":    now.Add(-1 * time.Hour),
				"docs":       now.Add(-1 * time.Hour),
				"security":   now.Add(-1 * time.Hour),
			}),
			want: FreshnessFresh,
		},
		{
			name: "all specialists but oldest beyond window -> stale",
			ra: makeAgents(map[string]time.Time{
				"formatting": now.Add(-1 * time.Hour),
				"design":     now.Add(-1 * time.Hour),
				"testing":    now.Add(-31 * 24 * time.Hour),
				"docs":       now.Add(-1 * time.Hour),
				"security":   now.Add(-1 * time.Hour),
			}),
			want: FreshnessStale,
		},
		{
			name: "all specialists but one has zero GeneratedAt -> stale",
			ra: &RepoAgents{Owner: "o", Repo: "r", Agents: map[string]Agent{
				"formatting": {Specialist: "formatting", Context: "x", GeneratedAt: now.Add(-1 * time.Hour)},
				"design":     {Specialist: "design", Context: "x", GeneratedAt: now.Add(-1 * time.Hour)},
				"testing":    {Specialist: "testing", Context: "x"}, // no GeneratedAt
				"docs":       {Specialist: "docs", Context: "x", GeneratedAt: now.Add(-1 * time.Hour)},
				"security":   {Specialist: "security", Context: "x", GeneratedAt: now.Add(-1 * time.Hour)},
			}},
			want: FreshnessStale,
		},
		{
			name: "all specialists, exactly at the window boundary -> fresh",
			ra: makeAgents(map[string]time.Time{
				"formatting": now.Add(-staleAfter),
				"design":     now.Add(-staleAfter),
				"testing":    now.Add(-staleAfter),
				"docs":       now.Add(-staleAfter),
				"security":   now.Add(-staleAfter),
			}),
			want: FreshnessFresh,
		},
		{
			name: "staleAfter <= 0 disables stale check, fully populated -> fresh",
			ra: makeAgents(map[string]time.Time{
				"formatting": now.Add(-365 * 24 * time.Hour),
				"design":     now.Add(-365 * 24 * time.Hour),
				"testing":    now.Add(-365 * 24 * time.Hour),
				"docs":       now.Add(-365 * 24 * time.Hour),
				"security":   now.Add(-365 * 24 * time.Hour),
			}),
			want: FreshnessFresh,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eff := staleAfter
			if tc.name == "staleAfter <= 0 disables stale check, fully populated -> fresh" {
				eff = 0
			}
			got := Compute(tc.ra, now, eff)
			if got != tc.want {
				t.Fatalf("Compute() = %v (%s), want %v (%s)", got, got, tc.want, tc.want)
			}
		})
	}
}

func TestFreshnessNeedsAttention(t *testing.T) {
	for _, tc := range []struct {
		f    Freshness
		want bool
	}{
		{FreshnessUnknown, false},
		{FreshnessFresh, false},
		{FreshnessMissing, true},
		{FreshnessIncomplete, true},
		{FreshnessStale, true},
	} {
		if got := tc.f.NeedsAttention(); got != tc.want {
			t.Errorf("(%s).NeedsAttention() = %v, want %v", tc.f, got, tc.want)
		}
	}
}

func TestLoadFreshness(t *testing.T) {
	setupTempCache(t)
	now := time.Now().UTC()
	staleAfter := 30 * 24 * time.Hour

	if got := LoadFreshness("", "r", now, staleAfter); got != FreshnessUnknown {
		t.Errorf("empty owner: got %s, want unknown", got)
	}
	if got := LoadFreshness("o", "", now, staleAfter); got != FreshnessUnknown {
		t.Errorf("empty repo: got %s, want unknown", got)
	}

	// No file on disk yet; expect missing rather than unknown — the caller
	// can still act (open the tab and generate).
	if got := LoadFreshness("acme", "widget", now, staleAfter); got != FreshnessMissing {
		t.Errorf("absent file: got %s, want missing", got)
	}

	// Write all five specialists, all recent. Should report fresh.
	ra := &RepoAgents{Owner: "acme", Repo: "widget", Agents: map[string]Agent{}}
	for _, sp := range Specialists {
		ra.Agents[sp] = Agent{
			Specialist:  sp,
			Context:     "context for " + sp,
			GeneratedAt: now.Add(-1 * time.Hour),
		}
	}
	if err := Save(ra); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := LoadFreshness("acme", "widget", now, staleAfter); got != FreshnessFresh {
		t.Errorf("fully populated recent: got %s, want fresh", got)
	}

	// Corrupt the file -> Missing (we treat read errors the same as a
	// missing file from the reviewer's POV).
	p := FilePath("acme", "widget")
	if err := os.WriteFile(p, []byte("not json"), 0o644); err != nil {
		t.Fatalf("corrupt write: %v", err)
	}
	if got := LoadFreshness("acme", "widget", now, staleAfter); got != FreshnessMissing {
		t.Errorf("unparseable file: got %s, want missing", got)
	}

	// Sanity: the file we corrupted is the one LoadFreshness opens.
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("expected file to still exist at %s: %v", filepath.Clean(p), err)
	}
}
