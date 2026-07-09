package llmjson

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var (
	schemaCacheMu sync.Mutex
	schemaCache   = map[string]*jsonschema.Schema{}
)

// ValidateJSON checks document against a JSON Schema (draft 2020-12 compatible).
// schemaJSON is compiled once and cached by content hash key (the raw string).
func ValidateJSON(document, schemaJSON string) error {
	schemaJSON = strings.TrimSpace(schemaJSON)
	if schemaJSON == "" {
		return nil
	}
	s, err := compiledSchema(schemaJSON)
	if err != nil {
		return err
	}
	doc, uerr := jsonschema.UnmarshalJSON(strings.NewReader(document))
	if uerr != nil {
		return fmt.Errorf("llmjson: document not valid JSON: %w", uerr)
	}
	if err := s.Validate(doc); err != nil {
		return fmt.Errorf("llmjson: schema validation: %w", err)
	}
	return nil
}

// ParseValidating is like Parse but rejects candidates that fail schemaJSON
// validation before unmarshalling into T.
func ParseValidating[T any](raw, schemaJSON string) (T, error) {
	var zero T
	if strings.TrimSpace(schemaJSON) == "" {
		return Parse[T](raw)
	}
	var lastErr error
	for _, cand := range candidates(raw) {
		if err := ValidateJSON(cand, schemaJSON); err != nil {
			lastErr = err
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(cand), &v); err != nil {
			lastErr = err
			continue
		}
		return v, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("llmjson: no JSON value found")
	}
	return zero, lastErr
}

func compiledSchema(schemaJSON string) (*jsonschema.Schema, error) {
	schemaCacheMu.Lock()
	defer schemaCacheMu.Unlock()
	if s, ok := schemaCache[schemaJSON]; ok {
		return s, nil
	}
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(schemaJSON))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("schema.json", doc); err != nil {
		return nil, err
	}
	s, err := c.Compile("schema.json")
	if err != nil {
		return nil, err
	}
	schemaCache[schemaJSON] = s
	return s, nil
}
