package llmjson

import "testing"

func TestValidateJSON(t *testing.T) {
	schema := `{
  "type": "object",
  "required": ["findings"],
  "properties": {
    "findings": {"type": "array"}
  }
}`
	if err := ValidateJSON(`{"findings": []}`, schema); err != nil {
		t.Fatalf("valid doc: %v", err)
	}
	if err := ValidateJSON(`{"nope": true}`, schema); err == nil {
		t.Fatal("expected schema failure")
	}
}

func TestParseValidating(t *testing.T) {
	schema := `{
  "type": "object",
  "required": ["count"],
  "properties": {"count": {"type": "integer"}}
}`
	type out struct {
		Count int `json:"count"`
	}
	v, err := ParseValidating[out](`Here is the result: {"count": 3}`, schema)
	if err != nil {
		t.Fatalf("ParseValidating: %v", err)
	}
	if v.Count != 3 {
		t.Fatalf("count = %d", v.Count)
	}
}
