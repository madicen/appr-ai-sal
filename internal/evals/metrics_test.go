package evals

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOriginal12MatchesGateJSON(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "docs", "evals", "plugin-treatment-gate.json"))
	if err != nil {
		t.Fatalf("read gate: %v", err)
	}
	var gate struct {
		Corpus struct {
			IDs []string `json:"original_12_case_ids"`
		} `json:"corpus"`
	}
	if err := json.Unmarshal(raw, &gate); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(gate.Corpus.IDs) != 12 {
		t.Fatalf("gate ids = %d, want 12", len(gate.Corpus.IDs))
	}
	if len(original12Sorted()) != 12 {
		t.Fatalf("local ids = %d, want 12", len(original12CaseIDs))
	}
	for _, id := range gate.Corpus.IDs {
		if !original12CaseIDs[id] {
			t.Errorf("gate id %q missing from original12CaseIDs", id)
		}
	}
	if len(original12CaseIDs) != len(gate.Corpus.IDs) {
		t.Errorf("local set size %d != gate %d", len(original12CaseIDs), len(gate.Corpus.IDs))
	}
}

func TestBuildGateRunMetricsOriginal12Filter(t *testing.T) {
	csr := CorpusScore{
		Provider: "ollama",
		Model:    "qwen2.5-coder:3b",
		Cases: []CaseScore{
			{
				ID: "security-weak-hash",
				Specialists: []SpecialistScore{
					{Specialist: "security", ExpectedMatched: 1, ExpectedTotal: 1, UnsupportedClaims: 1, ClaimsChecked: 2},
				},
			},
			{
				ID: "hallucination-invented-path",
				Specialists: []SpecialistScore{
					{Specialist: "security", ExpectedMatched: 0, ExpectedTotal: 1, UnsupportedClaims: 3, ClaimsChecked: 3},
				},
			},
		},
	}
	m := BuildGateRunMetrics(csr)
	if m.RecallOriginal12Num != 1 || m.RecallOriginal12Den != 1 {
		t.Fatalf("original12 recall = %d/%d, want 1/1", m.RecallOriginal12Num, m.RecallOriginal12Den)
	}
	if m.RecallFullNum != 1 || m.RecallFullDen != 2 {
		t.Fatalf("full recall = %d/%d, want 1/2", m.RecallFullNum, m.RecallFullDen)
	}
	if m.UnsupportedClaimNum != 4 || m.UnsupportedClaimDen != 5 {
		t.Fatalf("claims = %d/%d, want 4/5", m.UnsupportedClaimNum, m.UnsupportedClaimDen)
	}
}
