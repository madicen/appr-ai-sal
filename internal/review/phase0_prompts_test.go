package review

import (
	"strings"
	"testing"
)

// 0.4 fix #1: the vibe-coach output contract's finding_refs.specialist enum
// must enumerate every lane the pipeline can emit — the tech specialist and
// the four PR agents — so the model can reference their findings.
func TestVibeContractEnumIncludesTechAndPRAgents(t *testing.T) {
	for _, tok := range []string{"tech", "description", "checks", "discussion", "scope"} {
		if !strings.Contains(vibeCoachOutputContract, tok) {
			t.Errorf("vibeCoachOutputContract finding_refs enum missing %q", tok)
		}
	}
	// The canonical specialist names must all be valid enum members too.
	for _, name := range AllSpecialists {
		if !strings.Contains(vibeCoachOutputContract, name) {
			t.Errorf("vibeCoachOutputContract enum missing specialist %q", name)
		}
	}
}

// 0.4 fix #2: the repo-arbiter prompt must list the tech specialist in its
// roster and describe multi-rank demotion so its prose matches the code (which
// allows dropping more than one rank).
func TestRepoArbiterPromptRosterAndMultiRankDemote(t *testing.T) {
	body, err := SpecialistPrompt(specRepoArbiter)
	if err != nil {
		t.Fatalf("load repo-arbiter prompt: %v", err)
	}
	if !strings.Contains(body, "tech") {
		t.Errorf("repo-arbiter roster must include the tech specialist")
	}
	// The multi-rank demote must be spelled out; the classic example is a
	// two-rank drop from error straight to info.
	if !strings.Contains(body, "error`→`info") {
		t.Errorf("repo-arbiter prompt must document multi-rank demote (error→info)")
	}
	// And it must not contradict itself by forbidding more than a single step.
	if strings.Contains(body, "only one rank") || strings.Contains(body, "exactly one rank") {
		t.Errorf("repo-arbiter prompt still contains one-rank-only contradiction")
	}
}

// 0.4 fix #10: security.md must define the `critical` severity (RCE, auth
// bypass, secret exfiltration) so the critical_only strictness floor isn't
// starved by a prompt that caps at error.
func TestSecurityPromptDefinesCritical(t *testing.T) {
	body, err := SpecialistPrompt(SpecSecurity)
	if err != nil {
		t.Fatalf("load security prompt: %v", err)
	}
	mustContain := []string{"critical", "RCE", "exfiltration"}
	for _, tok := range mustContain {
		if !strings.Contains(body, tok) {
			t.Errorf("security prompt missing critical-severity marker %q", tok)
		}
	}
	// The critical_only floor rationale should be called out so authors know
	// why escalation matters.
	if !strings.Contains(body, "critical_only") {
		t.Errorf("security prompt should reference the critical_only floor")
	}
}
