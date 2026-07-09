package gh

import "testing"

func TestParseGHVersion(t *testing.T) {
	cases := map[string]string{
		"gh version 2.40.1 (2023-12-13)\nhttps://github.com/cli/cli/releases/tag/v2.40.1\n": "2.40.1",
		"gh version 2.0.0 (2021-08-24)": "2.0.0",
		"gh version 1.14.0":             "1.14.0",
		"no version here":               "",
		"":                              "",
	}
	for in, want := range cases {
		if got := parseGHVersion(in); got != want {
			t.Errorf("parseGHVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGHVersionAtLeast(t *testing.T) {
	cases := []struct {
		have, min string
		want      bool
	}{
		{"2.40.1", "2.0.0", true},
		{"2.0.0", "2.0.0", true},
		{"1.14.0", "2.0.0", false},
		{"2.0.1", "2.0.0", true},
		{"1.99.99", "2.0.0", false},
		{"3.0.0", "2.0.0", true},
		{"2.0", "2.0.0", true},    // missing patch treated as .0
		{"2", "2.0.0", true},      // "2" == "2.0.0"
		{"", "2.0.0", true},       // unparseable version fails open
		{"2.10.0", "2.9.0", true}, // numeric (not lexical) comparison
		{"2.9.0", "2.10.0", false},
	}
	for _, c := range cases {
		if got := ghVersionAtLeast(c.have, c.min); got != c.want {
			t.Errorf("ghVersionAtLeast(%q, %q) = %v, want %v", c.have, c.min, got, c.want)
		}
	}
}

func TestCompareDottedVersions(t *testing.T) {
	if compareDottedVersions("2.0.0", "2.0.0") != 0 {
		t.Error("equal versions should compare 0")
	}
	if compareDottedVersions("2.1.0", "2.0.9") <= 0 {
		t.Error("2.1.0 should be greater than 2.0.9")
	}
	if compareDottedVersions("1.9.9", "2.0.0") >= 0 {
		t.Error("1.9.9 should be less than 2.0.0")
	}
}
