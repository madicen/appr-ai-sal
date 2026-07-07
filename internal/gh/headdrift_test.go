package gh

import "testing"

// TestHeadDrift covers the pure head-drift comparison the posting pre-flight
// relies on: divergent non-empty SHAs yield a *HeadDriftError; matching or
// empty SHAs (nothing to compare, or the lookup was unavailable) yield nil.
func TestHeadDrift(t *testing.T) {
	tests := []struct {
		name     string
		was, now string
		wantErr  bool
	}{
		{"diverged", "aaaaaaa", "bbbbbbb", true},
		{"matched", "abc123", "abc123", false},
		{"matched with surrounding space", "abc123", "  abc123\n", false},
		{"empty was", "", "bbbbbbb", false},
		{"empty now", "aaaaaaa", "", false},
		{"both empty", "", "", false},
		{"whitespace was", "   ", "bbbbbbb", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HeadDrift(tt.was, tt.now)
			if tt.wantErr {
				if got == nil {
					t.Fatalf("HeadDrift(%q,%q) = nil, want *HeadDriftError", tt.was, tt.now)
				}
				return
			}
			if got != nil {
				t.Fatalf("HeadDrift(%q,%q) = %v, want nil", tt.was, tt.now, got)
			}
		})
	}
}

// TestHeadDriftReportsBothSHAs pins the values carried on the returned error so
// the overlay's "head moved from X to Y" message is accurate.
func TestHeadDriftReportsBothSHAs(t *testing.T) {
	d := HeadDrift("oldsha", "newsha")
	if d == nil {
		t.Fatal("expected drift error")
	}
	if d.Was != "oldsha" || d.Now != "newsha" {
		t.Fatalf("drift SHAs: got Was=%q Now=%q want oldsha/newsha", d.Was, d.Now)
	}
}
