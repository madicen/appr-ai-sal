package evals

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestPluginTreatmentGateRegistered locks the Phase 5 pre-registered gate
// file: required keys exist and outcome labels match the plan. Editing the
// gate after treatments start is a process violation; this test only checks
// the registered shape still loads.
func TestPluginTreatmentGateRegistered(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	path := filepath.Join(root, "docs", "evals", "plugin-treatment-gate.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read gate: %v", err)
	}
	var gate map[string]any
	if err := json.Unmarshal(raw, &gate); err != nil {
		t.Fatalf("parse gate JSON: %v", err)
	}
	if gate["immutable"] != true {
		t.Fatalf("immutable = %v, want true", gate["immutable"])
	}
	g, ok := gate["gate"].(map[string]any)
	if !ok {
		t.Fatal("missing gate object")
	}
	wantNums := map[string]float64{
		"min_unsupported_claim_reduction_pp":      10,
		"max_suggestion_breakage_increase_pp":     0,
		"max_recall_regression_pp_on_original_12": 2,
		"max_token_inflation_pct":                 25,
	}
	for k, want := range wantNums {
		got, ok := g[k].(float64)
		if !ok || got != want {
			t.Errorf("gate.%s = %v, want %v", k, g[k], want)
		}
	}
	if g["ci_must_exclude_zero"] != true {
		t.Errorf("ci_must_exclude_zero = %v, want true", g["ci_must_exclude_zero"])
	}
	outcomes, ok := gate["outcomes"].([]any)
	if !ok || len(outcomes) != 3 {
		t.Fatalf("outcomes = %#v, want 3 labels", gate["outcomes"])
	}
	wantOut := map[string]bool{"measured-helpful": true, "measured-no-uplift": true, "pending": true}
	for _, o := range outcomes {
		s, _ := o.(string)
		if !wantOut[s] {
			t.Errorf("unexpected outcome %q", s)
		}
		delete(wantOut, s)
	}
	if len(wantOut) != 0 {
		t.Errorf("missing outcomes: %v", wantOut)
	}
	corpus, ok := gate["corpus"].(map[string]any)
	if !ok {
		t.Fatal("missing corpus")
	}
	ids, ok := corpus["original_12_case_ids"].([]any)
	if !ok || len(ids) != 12 {
		t.Fatalf("original_12_case_ids len = %d, want 12", len(ids))
	}
}
