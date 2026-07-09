package review

import "testing"

// These tests assert the review layer's specialist parse path (which now
// delegates salvage to internal/llmjson and then applies domain
// normalization). The exhaustive sanitize-ladder cases live in
// internal/llmjson; here we keep the integration-level assertions that the
// specialist envelope survives the common noisy-output shapes.

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
