package conventionwitness

import (
	"context"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

func TestNormalizeVerdict(t *testing.T) {
	cases := map[string]Verdict{
		"congruent":           VerdictContradictsFinding,
		"contradicts_finding": VerdictContradictsFinding,
		"divergent":           VerdictSupportsFinding,
		"supports_finding":    VerdictSupportsFinding,
		"unknown":             VerdictUnknown,
		"":                    "",
		"random":              "",
		"INSUFFICIENT":        VerdictUnknown,
	}
	for in, want := range cases {
		if got := NormalizeVerdict(in); got != want {
			t.Errorf("NormalizeVerdict(%q) = %q want %q", in, got, want)
		}
	}
}

func TestRunReturnsEmptyForNoFindings(t *testing.T) {
	r := Run(context.Background(), aiconfig.DefaultConfig(), nil, "", PrWideRef{}, nil, "")
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	if len(r.Witnesses) != 0 {
		t.Fatalf("expected empty witnesses, got %d", len(r.Witnesses))
	}
}

func TestRunErrsWhenCompleteIsNil(t *testing.T) {
	r := Run(context.Background(), aiconfig.DefaultConfig(), nil, "", PrWideRef{}, []FindingInput{
		{Specialist: "testing", Path: "a.go", Line: 1, Severity: "warning", Comment: "x"},
	}, "")
	if r.Err == nil {
		t.Fatal("expected error when complete is nil")
	}
}

func TestRunUsesCompleteAndAlignsResults(t *testing.T) {
	complete := func(_ context.Context, _ *aiconfig.Config, system, user, _ string) (string, error) {
		if !strings.Contains(system, "convention witness") {
			t.Errorf("system prompt missing expected text: %q", system[:min(200, len(system))])
		}
		if !strings.Contains(user, "Findings to classify") {
			t.Errorf("user prompt missing findings section: %q", user)
		}
		return `{"witnesses":[
			{"specialist":"testing","path":"a.go","line":1,"side":"RIGHT","verdict":"congruent","citation":"no sibling tests"}
		]}`, nil
	}
	r := Run(context.Background(), aiconfig.DefaultConfig(), complete, "", PrWideRef{Repository: "x/y", Number: 7, Title: "t"}, []FindingInput{
		{Specialist: "testing", Path: "a.go", Line: 1, Side: "RIGHT", Severity: "warning", Comment: "missing test"},
		{Specialist: "docs", Path: "b.go", Line: 2, Side: "RIGHT", Severity: "warning", Comment: "missing doc"},
	}, "evidence body")
	if r.Err != nil {
		t.Fatalf("unexpected error: %v", r.Err)
	}
	if len(r.Witnesses) != 2 {
		t.Fatalf("witness count = %d, want 2", len(r.Witnesses))
	}
	if r.Witnesses[0].Verdict != VerdictContradictsFinding {
		t.Fatalf("witness 0 verdict = %s, want contradicts_finding", r.Witnesses[0].Verdict)
	}
	if r.Witnesses[1].Verdict != VerdictUnknown {
		t.Fatalf("witness 1 verdict = %s, want unknown (no model output for it)", r.Witnesses[1].Verdict)
	}
}

func TestParseWitnessJSONHandlesPrefixedOutput(t *testing.T) {
	raw := "Here is the result:\n```json\n{\"witnesses\":[{\"specialist\":\"docs\",\"path\":\"a.go\",\"line\":3,\"verdict\":\"divergent\",\"citation\":\"x\"}]}\n```"
	got, err := parseWitnessJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Witnesses) != 1 || NormalizeVerdict(string(got.Witnesses[0].Verdict)) != VerdictSupportsFinding {
		t.Fatalf("unexpected: %+v", got)
	}
}

// F2: before consolidation, the witness parser only did a bare json.Unmarshal
// plus extractJSONObject — no fence stripping (the prefixed-output test above
// only worked because extractJSONObject happened to find the inner object) and
// no comment removal. A witness response carrying JSON5 // or /* */ comments
// failed to parse. Routing the witness through llmjson.Parse gives it the full
// salvage ladder, so commented witness output now parses too.
func TestParseWitnessJSONHandlesCommentedOutput(t *testing.T) {
	raw := "{\n" +
		"  // one witness per finding\n" +
		"  \"witnesses\": [\n" +
		"    {\"specialist\": \"testing\", \"path\": \"a.go\", \"line\": 1, \"side\": \"RIGHT\", \"verdict\": \"congruent\", \"citation\": \"aligned\"}, /* ok */\n" +
		"  ],\n" +
		"}"
	got, err := parseWitnessJSON(raw)
	if err != nil {
		t.Fatalf("commented witness JSON must now parse via the shared ladder: %v", err)
	}
	if len(got.Witnesses) != 1 || NormalizeVerdict(string(got.Witnesses[0].Verdict)) != VerdictContradictsFinding {
		t.Fatalf("commented witness parse mismatch: %+v", got)
	}
}

func TestFormatMarkdownEmpty(t *testing.T) {
	if FormatMarkdown(nil) != "" {
		t.Fatal("expected empty for nil")
	}
}

func TestFormatMarkdownRenders(t *testing.T) {
	out := FormatMarkdown([]Witness{
		{Specialist: "testing", Path: "a.go", Line: 1, Side: "RIGHT", Verdict: VerdictContradictsFinding, Citation: "no sib"},
		{Specialist: "docs", Path: "b.go", Line: 2, Verdict: VerdictSupportsFinding},
	})
	if !strings.Contains(out, "[testing] contradicts_finding `a.go:1`") {
		t.Fatalf("missing testing line: %q", out)
	}
	if !strings.Contains(out, "side=RIGHT") {
		t.Fatalf("missing side default: %q", out)
	}
	if !strings.Contains(out, "no sib") {
		t.Fatalf("missing citation: %q", out)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
