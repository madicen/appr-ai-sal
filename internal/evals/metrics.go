package evals

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// original12CaseIDs matches docs/evals/plugin-treatment-gate.json corpus.original_12_case_ids.
// Duplicated here so metrics JSON stays usable offline without reading the gate file.
var original12CaseIDs = map[string]bool{
	"design-deep-nesting":      true,
	"docs-missing-godoc":       true,
	"formatting-spacing":       true,
	"pr-checks-failing":        true,
	"pr-description-empty":     true,
	"pr-discussion-unresolved": true,
	"pr-scope-creep":           true,
	"security-sql-injection":   true,
	"security-weak-hash":       true,
	"tech-iac-s3-tags":         true,
	"tech-memory-units":        true,
	"testing-error-branch":     true,
}

// GateRunMetrics is the per-run slice used by docs/evals/baselines/ snapshots.
type GateRunMetrics struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Label    string `json:"label,omitempty"`

	UnsupportedClaimNum int `json:"unsupported_claim_num"`
	UnsupportedClaimDen int `json:"unsupported_claim_den"`

	SuggestionCompileNum int `json:"suggestion_compile_num"`
	SuggestionCompileDen int `json:"suggestion_compile_den"`

	// Breakage = compile failures / attempted (1 − compile rate).
	SuggestionBreakageNum int `json:"suggestion_breakage_num"`
	SuggestionBreakageDen int `json:"suggestion_breakage_den"`

	RecallOriginal12Num int `json:"recall_original_12_num"`
	RecallOriginal12Den int `json:"recall_original_12_den"`

	RecallFullNum int `json:"recall_full_num"`
	RecallFullDen int `json:"recall_full_den"`

	Calls        int `json:"calls"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CaseCount    int `json:"case_count"`
}

// BuildGateRunMetrics rolls CorpusScore into the Phase 5 gate fields.
func BuildGateRunMetrics(csr CorpusScore) GateRunMetrics {
	m := GateRunMetrics{
		Provider:  csr.Provider,
		Model:     csr.Model,
		Label:     csr.Label,
		CaseCount: len(csr.Cases),
	}
	for _, cse := range csr.Cases {
		orig := original12CaseIDs[cse.ID]
		for _, s := range cse.Specialists {
			claim := s.UnsupportedClaimRate()
			m.UnsupportedClaimNum += claim.Num
			m.UnsupportedClaimDen += claim.Den

			comp := s.SuggestionCompileRate()
			m.SuggestionCompileNum += comp.Num
			m.SuggestionCompileDen += comp.Den
			m.SuggestionBreakageNum += comp.Den - comp.Num
			m.SuggestionBreakageDen += comp.Den

			rec := s.Recall()
			m.RecallFullNum += rec.Num
			m.RecallFullDen += rec.Den
			if orig {
				m.RecallOriginal12Num += rec.Num
				m.RecallOriginal12Den += rec.Den
			}
		}
	}
	calls, inTok, outTok, _, _, _, _ := csr.Totals()
	m.Calls = calls
	m.InputTokens = inTok
	m.OutputTokens = outTok
	return m
}

// WriteGateRunMetricsJSON writes one run's gate metrics as JSON.
func WriteGateRunMetricsJSON(path string, m GateRunMetrics) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write metrics json: %w", err)
	}
	return nil
}

// original12Sorted returns sorted original-12 ids (for tests).
func original12Sorted() []string {
	out := make([]string, 0, len(original12CaseIDs))
	for id := range original12CaseIDs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
