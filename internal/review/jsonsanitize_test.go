package review

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizeTripleQuotedSuggestion(t *testing.T) {
	raw := `{"summary":"ok","findings":[{"path":"ec2.tf","line":31,"side":"RIGHT","severity":"warning","comment":"use data source","suggestion":"""data "aws_subnets" "private" {
  filter {
    name = "vpc-id"
  }
}
"""}]}`

	s := sanitizeTripleQuotedStringValues(raw)
	if strings.Contains(s, `"""`) {
		t.Fatalf("triple quotes should be removed:\n%s", s)
	}
	var v specialistJSON
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, s)
	}
	if len(v.Findings) != 1 {
		t.Fatalf("findings: %+v", v)
	}
	if !strings.Contains(v.Findings[0].Suggestion, "aws_subnets") {
		t.Fatalf("suggestion lost: %q", v.Findings[0].Suggestion)
	}
}

func TestParseSpecialistJSON_JSONCComments(t *testing.T) {
	raw := "{\n// note\n\"summary\":\"ok\",\n/* here */\n\"findings\":[]}"
	got, err := parseSpecialistJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "ok" || len(got.Findings) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestParseSpecialistJSON_markdownFence(t *testing.T) {
	raw := "```json\n{\"summary\":\"x\",\"findings\":[]}\n```"
	got, err := parseSpecialistJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "x" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseSpecialistJSON_tripleQuoted(t *testing.T) {
	raw := "{\"summary\":\"s\",\"findings\":[]}"
	if _, err := parseSpecialistJSON(raw); err != nil {
		t.Fatal(err)
	}
	invalid := `{"summary":"x","findings":[{"path":"p","line":1,"side":"RIGHT","severity":"info","comment":"c","suggestion":"""line1
line2"""}]}`
	got, err := parseSpecialistJSON(invalid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "x" || len(got.Findings) != 1 || got.Findings[0].Suggestion != "line1\nline2" {
		t.Fatalf("got %+v", got)
	}
}
