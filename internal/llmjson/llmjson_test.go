package llmjson

import (
	"strings"
	"testing"
)

// specialistLike mirrors the review-layer specialist output shape closely
// enough to exercise the full salvage ladder without importing the review
// package (llmjson is a leaf).
type specialistLike struct {
	Summary  string        `json:"summary"`
	Findings []findingLike `json:"findings"`
}

type findingLike struct {
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Side       string `json:"side"`
	Severity   string `json:"severity"`
	Comment    string `json:"comment"`
	Suggestion string `json:"suggestion"`
}

// TestParseObjectLadder covers every sanitize stage the ladder must handle for
// object-shaped payloads: bare JSON, markdown fences (with and without a
// language tag), leading/trailing prose, // and /* */ comments, Python-style
// triple-quoted values, trailing commas, and combinations thereof.
func TestParseObjectLadder(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantSummary  string
		wantFindings int
		wantSuggest  string // asserted on Findings[0] when non-empty
	}{
		{
			name:         "bare object",
			raw:          `{"summary":"ok","findings":[]}`,
			wantSummary:  "ok",
			wantFindings: 0,
		},
		{
			name:         "markdown fence with json tag",
			raw:          "```json\n{\"summary\":\"x\",\"findings\":[]}\n```",
			wantSummary:  "x",
			wantFindings: 0,
		},
		{
			name:         "markdown fence no tag",
			raw:          "```\n{\"summary\":\"y\",\"findings\":[]}\n```",
			wantSummary:  "y",
			wantFindings: 0,
		},
		{
			name:         "fence immediately followed by brace",
			raw:          "```json{\"summary\":\"z\",\"findings\":[]}```",
			wantSummary:  "z",
			wantFindings: 0,
		},
		{
			name:         "leading and trailing prose",
			raw:          "Here is the review:\n{\"summary\":\"p\",\"findings\":[]}\nHope this helps!",
			wantSummary:  "p",
			wantFindings: 0,
		},
		{
			name:         "line and block comments",
			raw:          "{\n// note\n\"summary\":\"ok\",\n/* here */\n\"findings\":[]}",
			wantSummary:  "ok",
			wantFindings: 0,
		},
		{
			name:         "trailing commas in object and array",
			raw:          `{"summary":"tc","findings":[{"path":"p","line":1,"side":"RIGHT","severity":"info","comment":"c","suggestion":"s",},],}`,
			wantSummary:  "tc",
			wantFindings: 1,
			wantSuggest:  "s",
		},
		{
			name: "triple-quoted multiline suggestion",
			raw: `{"summary":"x","findings":[{"path":"p","line":1,"side":"RIGHT","severity":"info","comment":"c","suggestion":"""line1
line2"""}]}`,
			wantSummary:  "x",
			wantFindings: 1,
			wantSuggest:  "line1\nline2",
		},
		{
			name: "triple-quoted with braces inside value",
			raw: `{"summary":"ok","findings":[{"path":"ec2.tf","line":31,"side":"RIGHT","severity":"warning","comment":"use data source","suggestion":"""data "aws_subnets" "private" {
  filter {
    name = "vpc-id"
  }
}
"""}]}`,
			wantSummary:  "ok",
			wantFindings: 1,
			wantSuggest:  "aws_subnets", // substring assertion below
		},
		{
			name:         "fence plus comments plus trailing comma",
			raw:          "```json\n{\n  // top comment\n  \"summary\": \"combo\",\n  \"findings\": [],\n}\n```",
			wantSummary:  "combo",
			wantFindings: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse[specialistLike](tc.raw)
			if err != nil {
				t.Fatalf("Parse error: %v\nraw:\n%s", err, tc.raw)
			}
			if got.Summary != tc.wantSummary {
				t.Fatalf("summary = %q, want %q", got.Summary, tc.wantSummary)
			}
			if len(got.Findings) != tc.wantFindings {
				t.Fatalf("findings = %d, want %d (%+v)", len(got.Findings), tc.wantFindings, got.Findings)
			}
			if tc.wantSuggest != "" {
				if len(got.Findings) == 0 {
					t.Fatalf("expected a finding to assert suggestion on")
				}
				sug := got.Findings[0].Suggestion
				// The braces-inside-value case only checks a substring.
				if tc.name == "triple-quoted with braces inside value" {
					if !strings.Contains(sug, tc.wantSuggest) {
						t.Fatalf("suggestion %q should contain %q", sug, tc.wantSuggest)
					}
				} else if sug != tc.wantSuggest {
					t.Fatalf("suggestion = %q, want %q", sug, tc.wantSuggest)
				}
			}
		})
	}
}

// arrayLike is a []struct payload to exercise ExtractArray-backed salvage.
type candidateLike struct {
	Tech  string `json:"tech"`
	Label string `json:"label"`
}

func TestParseArrayLadder(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantLen  int
		wantTech string
	}{
		{
			name:     "bare array",
			raw:      `[{"tech":"redis","label":"Redis"}]`,
			wantLen:  1,
			wantTech: "redis",
		},
		{
			name:     "array with surrounding prose",
			raw:      "Here are the technologies:\n[{\"tech\":\"nginx\",\"label\":\"NGINX\"}]\nHope that helps!",
			wantLen:  1,
			wantTech: "nginx",
		},
		{
			name:     "fenced array",
			raw:      "```json\n[{\"tech\":\"kafka\",\"label\":\"Kafka\"}]\n```",
			wantLen:  1,
			wantTech: "kafka",
		},
		{
			name:     "array with trailing comma",
			raw:      `[{"tech":"redis","label":"Redis"},]`,
			wantLen:  1,
			wantTech: "redis",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse[[]candidateLike](tc.raw)
			if err != nil {
				t.Fatalf("Parse error: %v\nraw:\n%s", err, tc.raw)
			}
			if len(got) != tc.wantLen {
				t.Fatalf("len = %d, want %d (%+v)", len(got), tc.wantLen, got)
			}
			if got[0].Tech != tc.wantTech {
				t.Fatalf("tech = %q, want %q", got[0].Tech, tc.wantTech)
			}
		})
	}
}

// arbiterLike mirrors the repo-arbiter output. These fixtures — fenced AND
// commented arbiter JSON — would have FAILED the old arbiter parser (which had
// only bare-parse + extractJSONObject, no fence/comment ladder). They now pass
// through the shared ladder. See F2 acceptance criterion #3.
type arbiterLike struct {
	UserSummary     string   `json:"user_summary"`
	RationaleBull   []string `json:"rationale_bullets"`
	VerdictOverride string   `json:"verdict_override"`
	SummaryMode     string   `json:"summary_mode"`
}

func TestParseArbiterFencedAndCommented(t *testing.T) {
	fenced := "```json\n" +
		`{"user_summary":"looks fine","rationale_bullets":["a"],"verdict_override":"","summary_mode":"none"}` +
		"\n```"
	got, err := Parse[arbiterLike](fenced)
	if err != nil {
		t.Fatalf("fenced arbiter parse: %v", err)
	}
	if got.UserSummary != "looks fine" {
		t.Fatalf("user_summary = %q", got.UserSummary)
	}

	commented := "Here is my verdict:\n{\n" +
		"  // overall this PR is safe\n" +
		"  \"user_summary\": \"safe\",\n" +
		"  /* no blockers */\n" +
		"  \"rationale_bullets\": [\"ok\"],\n" +
		"  \"verdict_override\": \"approve\",\n" +
		"  \"summary_mode\": \"append\",\n" +
		"}"
	got2, err := Parse[arbiterLike](commented)
	if err != nil {
		t.Fatalf("commented arbiter parse: %v", err)
	}
	if got2.UserSummary != "safe" || got2.VerdictOverride != "approve" {
		t.Fatalf("commented arbiter mismatch: %+v", got2)
	}
}

// witnessLike mirrors the convention-witness output. Like the arbiter, these
// fenced/commented fixtures would have failed the old witness parser.
type witnessLike struct {
	Witnesses []struct {
		Specialist string `json:"specialist"`
		Verdict    string `json:"verdict"`
	} `json:"witnesses"`
}

func TestParseWitnessFencedAndCommented(t *testing.T) {
	fenced := "Here is the result:\n```json\n" +
		`{"witnesses":[{"specialist":"docs","verdict":"divergent"}]}` +
		"\n```"
	got, err := Parse[witnessLike](fenced)
	if err != nil {
		t.Fatalf("fenced witness parse: %v", err)
	}
	if len(got.Witnesses) != 1 || got.Witnesses[0].Verdict != "divergent" {
		t.Fatalf("fenced witness mismatch: %+v", got)
	}

	commented := "{\n" +
		"  // one witness per finding\n" +
		"  \"witnesses\": [\n" +
		"    {\"specialist\": \"testing\", \"verdict\": \"congruent\"}, /* aligned */\n" +
		"  ],\n" +
		"}"
	got2, err := Parse[witnessLike](commented)
	if err != nil {
		t.Fatalf("commented witness parse: %v", err)
	}
	if len(got2.Witnesses) != 1 || got2.Witnesses[0].Verdict != "congruent" {
		t.Fatalf("commented witness mismatch: %+v", got2)
	}
}

// repairLike mirrors the suggestion-repair envelope.
type repairLike struct {
	Repairs []struct {
		ID          int    `json:"id"`
		AnchorLine  int    `json:"anchor_line"`
		Replacement string `json:"replacement"`
		Decline     bool   `json:"decline"`
	} `json:"repairs"`
}

func TestParseRepairEnvelope(t *testing.T) {
	bare := `{"repairs":[{"id":0,"anchor_line":13,"replacement":"        memory: 717Mi"},{"id":1,"decline":true}]}`
	got, err := Parse[repairLike](bare)
	if err != nil {
		t.Fatalf("bare repair parse: %v", err)
	}
	if len(got.Repairs) != 2 {
		t.Fatalf("repairs = %d, want 2", len(got.Repairs))
	}

	fenced := "```json\n{\"repairs\":[{\"id\":0,\"anchor_line\":13,\"replacement\":\"        memory: 717Mi\"}]}\n```"
	got2, err := Parse[repairLike](fenced)
	if err != nil {
		t.Fatalf("fenced repair parse: %v", err)
	}
	if len(got2.Repairs) != 1 || got2.Repairs[0].AnchorLine != 13 {
		t.Fatalf("fenced repair mismatch: %+v", got2)
	}
}

func TestParseNonJSONReturnsError(t *testing.T) {
	if _, err := Parse[specialistLike]("sorry, I cannot comply and here is some prose"); err == nil {
		t.Fatal("expected error on non-JSON input")
	}
	if _, err := Parse[[]candidateLike]("I could not find any technologies, sorry."); err == nil {
		t.Fatal("expected error on non-JSON array input")
	}
}

func TestStripCodeFence(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{"```\n## Heading\n\nbody\n```", "## Heading\n\nbody"},
		{"## Not fenced\n\nbody", "## Not fenced\n\nbody"},
		{"```markdown\n## Naming\n\nGo uses MixedCaps.\n```", "## Naming\n\nGo uses MixedCaps."},
	}
	for _, tc := range tests {
		if got := StripCodeFence(tc.in); got != tc.want {
			t.Errorf("StripCodeFence(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStripTrailingCommasPreservesStrings(t *testing.T) {
	// A comma before } inside a string value must not be touched.
	in := `{"comment":"a, b, and c,","findings":[],}`
	got := StripTrailingCommas(in)
	if !strings.Contains(got, `"a, b, and c,"`) {
		t.Fatalf("string content mutated: %q", got)
	}
	if strings.Contains(got, "],}") || strings.Contains(got, "[],}") {
		t.Fatalf("trailing comma before } not removed: %q", got)
	}
}
