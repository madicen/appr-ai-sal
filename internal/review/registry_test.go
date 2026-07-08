package review

import (
	"strings"
	"testing"
)

// TestRegistryDerivesStableExportedOrder pins the exported specialist/PR-agent
// name lists that are now derived from the registry: their order must match the
// pre-registry hand-maintained slices exactly (callers range over them and rely
// on the order).
func TestRegistryDerivesStableExportedOrder(t *testing.T) {
	wantSpecialists := []string{SpecFormatting, SpecDesign, SpecTesting, SpecDocs, SpecSecurity, SpecTech}
	if got := AllSpecialists; !equalStrings(got, wantSpecialists) {
		t.Fatalf("AllSpecialists = %v, want %v", got, wantSpecialists)
	}
	wantPRAgents := []string{SpecDescription, SpecChecks, SpecDiscussion, SpecScope}
	if got := AllPRAgents; !equalStrings(got, wantPRAgents) {
		t.Fatalf("AllPRAgents = %v, want %v", got, wantPRAgents)
	}
}

// TestRegistryPreservesBuiltinDispatchBehavior is the Q1 characterization
// oracle: for every built-in specialist and PR agent, every registry-consulting
// dispatch helper must return the exact value the pre-registry hard-coded switch
// returned. If any of these drift, a "behaviour-preserving" refactor silently
// changed the built-in panel.
func TestRegistryPreservesBuiltinDispatchBehavior(t *testing.T) {
	type want struct {
		lane          int
		witnessable   bool
		wantsEvidence bool
		convEvidence  bool
		suppressible  bool
		demotable     bool
		actionability bool
		prScope       PRScope
		reqTech       bool
		rebuttal      bool
		kind          Kind
	}
	// Values transcribed from the pre-Q1 hard-coded dispatch sites:
	//   specialistLanePriority (finding_dedupe.go), witness filter + evidence
	//   injection + ActiveSpecialists (runner.go), arbiter guards
	//   (repo_experts.go), actionability (actionability.go), constrainPRAgentScope
	//   + downrankAuthorRebuttedThreads (pragents.go).
	cases := map[string]want{
		SpecSecurity: {lane: 0, suppressible: false, demotable: false, kind: KindCode},
		SpecTech:     {lane: 1, witnessable: true, convEvidence: true, suppressible: true, demotable: true, reqTech: true, kind: KindCode},
		// Q6.5 made formatting witnessable (its findings now get an
		// identifier-style census fed to the convention witness); the rest of
		// its dispatch behaviour is unchanged.
		SpecFormatting:  {lane: 2, witnessable: true, suppressible: true, demotable: true, kind: KindCode},
		SpecDesign:      {lane: 3, suppressible: true, demotable: true, kind: KindCode},
		SpecTesting:     {lane: 4, witnessable: true, wantsEvidence: true, suppressible: true, demotable: true, actionability: true, kind: KindCode},
		SpecDocs:        {lane: 5, witnessable: true, wantsEvidence: true, suppressible: true, demotable: true, actionability: true, kind: KindCode},
		SpecChecks:      {lane: 6, suppressible: true, demotable: true, prScope: ScopeInline, kind: KindPRWide},
		SpecDescription: {lane: 7, suppressible: true, demotable: true, prScope: ScopeWholePR, kind: KindPRWide},
		SpecDiscussion:  {lane: 8, suppressible: true, demotable: true, prScope: ScopeThreadAnchored, rebuttal: true, kind: KindPRWide},
		SpecScope:       {lane: 9, suppressible: true, demotable: true, prScope: ScopeWholePR, kind: KindPRWide},
	}
	for name, w := range cases {
		if got := specialistLanePriority(name); got != w.lane {
			t.Errorf("%s: lane priority = %d, want %d", name, got, w.lane)
		}
		if got := specWitnessable(name); got != w.witnessable {
			t.Errorf("%s: witnessable = %v, want %v", name, got, w.witnessable)
		}
		if got := specWantsEvidence(name); got != w.wantsEvidence {
			t.Errorf("%s: wantsEvidence = %v, want %v", name, got, w.wantsEvidence)
		}
		if got := specWantsConventionEvidence(name); got != w.convEvidence {
			t.Errorf("%s: convEvidence = %v, want %v", name, got, w.convEvidence)
		}
		if got := specSuppressible(name); got != w.suppressible {
			t.Errorf("%s: suppressible = %v, want %v", name, got, w.suppressible)
		}
		if got := specDemotable(name); got != w.demotable {
			t.Errorf("%s: demotable = %v, want %v", name, got, w.demotable)
		}
		spec, ok := lookupSpec(name)
		if !ok {
			t.Errorf("%s: not found in registry", name)
			continue
		}
		if spec.Kind != w.kind {
			t.Errorf("%s: kind = %q, want %q", name, spec.Kind, w.kind)
		}
		gotAction := spec.hasGate(GateActionability) && spec.deficiencyPattern != nil
		if gotAction != w.actionability {
			t.Errorf("%s: actionability gate = %v, want %v", name, gotAction, w.actionability)
		}
		if spec.Kind == KindPRWide && spec.PRScope != w.prScope {
			t.Errorf("%s: pr scope = %q, want %q", name, spec.PRScope, w.prScope)
		}
		if spec.RequiresTechBriefs != w.reqTech {
			t.Errorf("%s: requiresTechBriefs = %v, want %v", name, spec.RequiresTechBriefs, w.reqTech)
		}
		if spec.RebuttalAware != w.rebuttal {
			t.Errorf("%s: rebuttalAware = %v, want %v", name, spec.RebuttalAware, w.rebuttal)
		}
	}
}

// TestUnknownSpecialistDefaults documents the fall-through values for a name
// not in the registry, which the pre-registry code also implied: sorts last in
// dedupe, not witnessed, no evidence, and (crucially) suppressible/demotable
// like every non-security lane.
func TestUnknownSpecialistDefaults(t *testing.T) {
	const unknown = "no-such-specialist"
	if got := specialistLanePriority(unknown); got != 99 {
		t.Errorf("unknown lane priority = %d, want 99", got)
	}
	if specWitnessable(unknown) || specWantsEvidence(unknown) || specWantsConventionEvidence(unknown) {
		t.Errorf("unknown specialist should not be witnessed / get evidence")
	}
	if !specSuppressible(unknown) || !specDemotable(unknown) {
		t.Errorf("unknown specialist should default suppressible+demotable")
	}
	if IsPRAgent(unknown) {
		t.Errorf("unknown specialist should not be classified as a PR agent")
	}
}

// TestVibeContractEnumIsRegistryDerived proves the finding_refs.specialist enum
// in the vibe-coach contract is generated from the registry (the "contract enum
// consults the registry" half of Q1) and matches the exact string the pre-Q1
// hand-maintained const carried — so the enum can never drift from the set of
// lanes the pipeline emits.
func TestVibeContractEnumIsRegistryDerived(t *testing.T) {
	const wantEnum = "formatting | design | testing | docs | security | tech | description | checks | discussion | scope"
	if got := vibeFindingRefSpecialistEnum(); got != wantEnum {
		t.Fatalf("vibeFindingRefSpecialistEnum() = %q, want %q", got, wantEnum)
	}
	if !strings.Contains(vibeCoachOutputContract, "<"+wantEnum+">") {
		t.Fatalf("vibeCoachOutputContract does not embed the registry-derived enum <%s>", wantEnum)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
