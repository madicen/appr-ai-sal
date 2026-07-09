package review

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// TestSeverityLadderCoversEveryConstant proves the severity ladder used to
// build every contract and schema enumerates exactly the four Severity
// constants, in rank order. If a fifth severity is ever added and left out of
// severityLadder(), this fails — so the ladder can never silently drift from
// the domain enum.
func TestSeverityLadderCoversEveryConstant(t *testing.T) {
	want := []Severity{SeverityInfo, SeverityWarning, SeverityError, SeverityCritical}
	got := severityLadder()
	if len(got) != len(want) {
		t.Fatalf("severityLadder() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("severityLadder()[%d] = %q, want %q", i, got[i], want[i])
		}
		if severityRank(got[i]) != i+1 {
			t.Fatalf("severityLadder() not in rank order: %q has rank %d, want %d", got[i], severityRank(got[i]), i+1)
		}
	}
	if got := severityLadderEnum(); got != `"info" | "warning" | "error" | "critical"` {
		t.Fatalf("severityLadderEnum() = %q", got)
	}
}

// TestSpecialistContractSeverityEnumIsRegistrySourced is the enum-drift-
// impossible proof for the code-specialist contract: the severity line in the
// generated reviewOutputContract must be exactly the registry-derived enum, so
// the 0.4-class "the contract's severity list drifted from the code" bug cannot
// be reintroduced.
func TestSpecialistContractSeverityEnumIsRegistrySourced(t *testing.T) {
	wantLine := `      "severity": ` + severityLadderEnum() + `,`
	if !strings.Contains(reviewOutputContract, wantLine) {
		t.Fatalf("reviewOutputContract severity line is not the registry-derived enum; want a line %q", wantLine)
	}
	// Behaviour-equivalence guard: the historically-shipped severity line must
	// still be produced verbatim (this is the exact text the built-in
	// specialists saw before Q2).
	if !strings.Contains(reviewOutputContract, `      "severity": "info" | "warning" | "error" | "critical",`) {
		t.Fatalf("reviewOutputContract no longer contains the historical severity line (built-in specialist behaviour changed)")
	}
}

// TestSpecialistContractBehaviorEquivalentMarkers is a companion to the
// existing suggestion/anchor marker tests: it asserts the generated specialist
// contract still carries every structural section the built-ins relied on, so
// "generated from the registry" did not quietly drop content.
func TestSpecialistContractBehaviorEquivalentMarkers(t *testing.T) {
	for _, s := range []string{
		`"suggestion"`, `"anchor_excerpt"`,
		"SPECIALTY SCOPE", "ANCHOR PROOF", "ACTIONABILITY BAR",
		"SUGGESTION CONTRACT", "REPLACEMENT, NOT INSERTION", "LANGUAGE AWARENESS",
		"REPLAYED verbatim", "HCL is NOT Go", "Terraform/HCL",
	} {
		if !strings.Contains(reviewOutputContract, s) {
			t.Errorf("generated reviewOutputContract missing built-in section %q", s)
		}
	}
}

// TestVibeContractAndSchemaEnumEqualRegistrySet is the enum-drift-impossible
// proof for the vibe coach across BOTH the textual contract and the JSON
// schema: the finding_refs.specialist membership in each must equal the exact
// registry-derived set (AllSpecialists followed by AllPRAgents). This is what
// makes the 0.4.1/0.4.2 fixes (the enum that forgot `tech` and the PR agents)
// impossible to reintroduce.
func TestVibeContractAndSchemaEnumEqualRegistrySet(t *testing.T) {
	want := append(append([]string{}, AllSpecialists...), AllPRAgents...)

	// Textual contract: the enum is embedded as "<a | b | ...>".
	wantEnum := strings.Join(want, " | ")
	if !strings.Contains(vibeCoachOutputContract, "<"+wantEnum+">") {
		t.Fatalf("vibeCoachOutputContract does not embed the registry-derived specialist enum <%s>", wantEnum)
	}

	// JSON schema: prompts[].finding_refs.items.specialist.enum must equal the
	// same set.
	got := vibeSchemaSpecialistEnum(t)
	if !equalStrings(got, want) {
		t.Fatalf("vibeCoachSchema finding_refs specialist enum = %v, want %v", got, want)
	}
}

// TestSpecialistSchemaSeverityEnumEqualsRegistry proves the specialist schema's
// severity enum is registry-sourced too (it is what a schema-capable provider
// constrains against), closing the drift door on the schema side.
func TestSpecialistSchemaSeverityEnumEqualsRegistry(t *testing.T) {
	sev := findingsSeverityEnum(t, specialistSchema())
	if !equalStrings(sev, severityStrings()) {
		t.Fatalf("specialistSchema severity enum = %v, want %v", sev, severityStrings())
	}
	sev = findingsSeverityEnum(t, prAgentSchema())
	if !equalStrings(sev, severityStrings()) {
		t.Fatalf("prAgentSchema severity enum = %v, want %v", sev, severityStrings())
	}
}

// TestSlimPRAgentContractIsMateriallySmaller asserts the dedicated PR-agent
// contract drops the suggestion machinery and is materially smaller than the
// full code-specialist contract (the intentional token saving on ~4 of ~10
// calls per run).
func TestSlimPRAgentContractIsMateriallySmaller(t *testing.T) {
	full := len(reviewOutputContract)
	slim := len(prAgentOutputContract)
	if slim >= full {
		t.Fatalf("prAgentOutputContract (%d bytes) is not smaller than reviewOutputContract (%d bytes)", slim, full)
	}
	saved := full - slim
	// The saving must be material — require at least a 40% reduction. (The
	// suggestion contract, anchor proof, language-awareness table, and worked
	// examples together are well over half the full contract.)
	if ratio := float64(slim) / float64(full); ratio > 0.60 {
		t.Fatalf("prAgentOutputContract is only %.0f%% smaller (%d/%d bytes); expected >=40%% reduction", (1-ratio)*100, slim, full)
	}
	t.Logf("slim PR-agent contract: %d bytes vs full %d bytes — saved %d bytes (%.0f%% smaller)",
		slim, full, saved, float64(saved)/float64(full)*100)

	// The slim contract must NOT carry any suggestion mechanics...
	for _, banned := range []string{
		"SUGGESTION CONTRACT", "anchor_excerpt", "ANCHOR PROOF",
		"REPLACEMENT, NOT INSERTION", "LANGUAGE AWARENESS", `"suggestion"`,
	} {
		if strings.Contains(prAgentOutputContract, banned) {
			t.Errorf("prAgentOutputContract should have dropped suggestion mechanic %q", banned)
		}
	}
	// ...while keeping what PR agents still need.
	for _, need := range []string{
		`"summary"`, `"findings"`, `"comment"`, `"severity"`, `"path"`, `"line"`, `"side"`,
		"SPECIALTY SCOPE", "ACTIONABILITY BAR", "INLINE VS PR-WIDE", "strict JSON",
	} {
		if !strings.Contains(prAgentOutputContract, need) {
			t.Errorf("prAgentOutputContract dropped a field/section PR agents need: %q", need)
		}
	}
}

// TestPRAgentPromptUsesSlimContract proves the built PR-agent user prompt
// carries the slim contract, not the full specialist contract.
func TestPRAgentPromptUsesSlimContract(t *testing.T) {
	got := buildPRAgentUserPrompt(SpecChecks, prAgentTestPR(), prAgentTestDiff, PRAgentInput{}, "", "")
	if !strings.Contains(got, prAgentOutputContract) {
		t.Fatalf("PR-agent prompt does not contain the slim contract")
	}
	if strings.Contains(got, "SUGGESTION CONTRACT") {
		t.Fatalf("PR-agent prompt still carries the full suggestion contract")
	}
}

// TestAgentSchemasCoversEveryJSONStage proves a schema is emitted for every
// built-in JSON stage, and that PR agents get the slim schema while code
// specialists get the full one.
func TestAgentSchemasCoversEveryJSONStage(t *testing.T) {
	schemas := AgentSchemas()
	for _, n := range AllSpecialists {
		if len(schemas[n]) == 0 {
			t.Errorf("no schema emitted for code specialist %q", n)
		}
		if !json.Valid(schemas[n]) {
			t.Errorf("schema for %q is not valid JSON", n)
		}
	}
	for _, n := range AllPRAgents {
		if len(schemas[n]) == 0 {
			t.Errorf("no schema emitted for PR agent %q", n)
		}
		// Slim schema must not offer suggestion/anchor_excerpt.
		if strings.Contains(string(schemas[n]), "anchor_excerpt") || strings.Contains(string(schemas[n]), "suggestion") {
			t.Errorf("PR-agent schema %q must not include suggestion fields", n)
		}
	}
	if len(schemas[SpecVibeCoach]) == 0 {
		t.Errorf("no schema emitted for vibe-coach")
	}
	if len(schemas[specRepoArbiter]) == 0 {
		t.Errorf("no schema emitted for repo-arbiter")
	}
	// schemaForAgent Kind routing.
	if string(schemaForAgent(SpecSecurity)) != string(specialistSchema()) {
		t.Errorf("schemaForAgent(security) should be the specialist schema")
	}
	if string(schemaForAgent(SpecChecks)) != string(prAgentSchema()) {
		t.Errorf("schemaForAgent(checks) should be the slim PR-agent schema")
	}
}

// TestSpecialistSchemaWiresIntoGeminiResponseSchema is the R5 wiring proof:
// a specialist JSON stage attaches its registry-derived schema, the
// review.Complete shim forwards it as Request.JSONSchema, and the Gemini
// provider emits generationConfig.responseSchema on the wire.
func TestSpecialistSchemaWiresIntoGeminiResponseSchema(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"{\"summary\":\"\",\"findings\":[]}"}]}}]}`))
	}))
	defer srv.Close()

	cfg := aiconfig.DefaultConfig()
	cfg.Provider = aiconfig.ProviderGemini
	cfg.BaseURL = srv.URL
	cfg.Model = "gemini-x"
	cfg.APIKey = "sk-test"

	if _, err := completeJSONWithSchema(context.Background(), cfg, "sys", "user", "", specialistSchema()); err != nil {
		t.Fatalf("completeJSONWithSchema: %v", err)
	}
	genCfg, ok := body["generationConfig"].(map[string]any)
	if !ok {
		t.Fatalf("gemini request missing generationConfig, body=%#v", body)
	}
	if genCfg["responseMimeType"] != "application/json" {
		t.Fatalf("gemini request missing responseMimeType, genCfg=%#v", genCfg)
	}
	if _, ok := genCfg["responseSchema"]; !ok {
		t.Fatalf("gemini request missing responseSchema (schema not wired into native JSON mode), genCfg=%#v", genCfg)
	}
}

// --- schema-inspection helpers ------------------------------------------

func findingsSeverityEnum(t *testing.T, schema json.RawMessage) []string {
	t.Helper()
	var s struct {
		Properties struct {
			Findings struct {
				Items struct {
					Properties struct {
						Severity struct {
							Enum []string `json:"enum"`
						} `json:"severity"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"findings"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	return s.Properties.Findings.Items.Properties.Severity.Enum
}

func vibeSchemaSpecialistEnum(t *testing.T) []string {
	t.Helper()
	var s struct {
		Properties struct {
			Prompts struct {
				Items struct {
					Properties struct {
						FindingRefs struct {
							Items struct {
								Properties struct {
									Specialist struct {
										Enum []string `json:"enum"`
									} `json:"specialist"`
								} `json:"properties"`
							} `json:"items"`
						} `json:"finding_refs"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"prompts"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(vibeCoachSchema(), &s); err != nil {
		t.Fatalf("unmarshal vibe schema: %v", err)
	}
	return s.Properties.Prompts.Items.Properties.FindingRefs.Items.Properties.Specialist.Enum
}
