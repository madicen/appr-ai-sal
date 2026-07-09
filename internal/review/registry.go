package review

import (
	"regexp"
	"strings"
	"sync"
)

// registry.go is the declarative source of truth for every review agent —
// both the code-reviewing specialists (formatting, design, testing, docs,
// security, tech) and the whole-PR agents (description, checks, discussion,
// scope). Before Q1 each agent's behaviour was scattered across ~8 hard-coded
// dispatch sites that compared a name against the SpecX consts:
//
//   - lane priority           (finding_dedupe.go specialistLanePriority)
//   - actionability gate       (actionability.go, docs/testing special-casing)
//   - evidence injection        (runner.go, if name == SpecTesting || SpecDocs)
//   - witness filter            (runner.go, testing/docs/tech)
//   - arbiter security guards   (repo_experts.go, never suppress/demote security)
//   - PR-agent scope + rebuttal (pragents.go, description/scope/discussion)
//   - tech activation           (runner.go ActiveSpecialists, tech-briefs gate)
//   - vibe-coach contract enum   (agents.go finding_refs.specialist)
//
// Those sites now consult a SpecialistSpec looked up from this registry, so a
// specialist's behaviour lives in exactly one place and new specialists —
// including user-defined ones loaded from disk (see registry_user.go) — get the
// same treatment without editing the dispatch code. The SpecX name consts are
// kept (they are referenced widely and are the registry keys) but the registry
// is authoritative for behaviour.

// Kind classifies an agent by how it reviews the PR.
type Kind string

const (
	// KindCode is a line-by-line code specialist that reviews the diff
	// (the members of AllSpecialists).
	KindCode Kind = "code"
	// KindPRWide is a whole-PR agent that reviews metadata (title, body,
	// checks, threads, scope) rather than the diff line-by-line (the members
	// of AllPRAgents).
	KindPRWide Kind = "pr-wide"
)

// Input names a data source a spec consumes. It is mostly documentary, but
// InputEvidence drives the per-PR evidence-pack injection in the runner.
type Input string

const (
	InputDiff       Input = "diff"
	InputEvidence   Input = "evidence-pack"
	InputChecks     Input = "checks"
	InputThreads    Input = "threads"
	InputDiscussion Input = "discussion"
)

// InputSet is the set of data sources a spec reads.
type InputSet []Input

// Has reports whether the set contains in.
func (s InputSet) Has(in Input) bool {
	for _, x := range s {
		if x == in {
			return true
		}
	}
	return false
}

// Gate names a deterministic post-processing gate that applies to a spec's
// findings. Only the specialist-scoped gate (actionability) is modelled here;
// the universally-applied gates (anchor-excerpt, IaC schema, naming
// convention, suggestion pruning) run on every spec and need no registry flag.
type Gate string

const (
	// GateActionability demotes bare "X lacks a comment / lacks tests"
	// findings to info. See actionability.go. Only fires when the spec also
	// carries a deficiencyPattern.
	GateActionability Gate = "actionability"
)

// ArbiterPolicy declares whether the repo arbiter may suppress or demote a
// spec's findings. Security is the one built-in lane the arbiter may never
// touch (Suppressible=false, Demotable=false); the severity-based guards
// (never suppress error/critical, never demote critical) are orthogonal and
// still enforced regardless of this policy.
type ArbiterPolicy struct {
	Suppressible bool
	Demotable    bool
}

// PRScope constrains where a KindPRWide agent may anchor its findings. It is
// the registry form of constrainPRAgentScope's per-agent switch.
type PRScope string

const (
	// ScopeInline is the default: the agent may anchor inline findings to a
	// changed line (code specialists and the checks agent).
	ScopeInline PRScope = ""
	// ScopeWholePR forces every inline finding PR-wide (description, scope).
	ScopeWholePR PRScope = "whole-pr"
	// ScopeThreadAnchored allows inline findings only on lines that belong to
	// an unresolved review thread; other inline findings are dropped as
	// code-review drift (discussion).
	ScopeThreadAnchored PRScope = "thread-anchored"
)

// PromptRef locates a spec's system prompt. For built-ins the prompt is
// resolved by SpecialistPrompt from the embedded prompts/<Name>.md (with a
// user override under ConfigDir/prompts/<Name>.md). For user-defined specs the
// prompt text is loaded from disk at registry-build time into the spec's
// prompt field, and SpecialistPrompt returns that verbatim.
type PromptRef struct {
	Name     string
	Embedded bool
}

// SpecialistSpec is the declarative description of one review agent. It
// captures every behaviour that used to be a hard-coded name comparison so the
// dispatch sites can consult a single record instead.
type SpecialistSpec struct {
	Name          string
	Kind          Kind
	PromptSource  PromptRef
	Inputs        InputSet
	Gates         []Gate
	LanePriority  int
	ArbiterPolicy ArbiterPolicy
	Witnessable   bool
	// SeverityLadder is injected into user-defined specs' prompts at load
	// time. Built-ins leave it empty because their ladder is already baked
	// into the embedded prompt (.md) — injecting it again would change the
	// built-in prompt text and violate Q1's zero-behaviour-change guarantee.
	SeverityLadder string

	// PRScope constrains a KindPRWide agent's anchoring (see PRScope).
	PRScope PRScope
	// RequiresTechBriefs marks a spec that only runs when the repo has
	// technology-expert briefs configured (the tech specialist). Such a spec
	// is dropped from ActiveSpecialists when no briefs exist so it never costs
	// a guaranteed-empty API call.
	RequiresTechBriefs bool
	// RebuttalAware marks a PR agent whose findings are passed through
	// downrankAuthorRebuttedThreads (the discussion agent).
	RebuttalAware bool
	// ConventionEvidence marks a spec whose findings get sibling-file
	// convention evidence harvested for the witness (the tech specialist).
	ConventionEvidence bool
	// FormattingEvidence marks a spec whose findings get formatting-specific
	// sibling-file evidence harvested for the witness (Q6.5): token presence
	// plus an identifier-style census. Built-in: formatting. See
	// BuildFormattingConventionEvidence.
	FormattingEvidence bool
	// FormatterSilenceAware marks a spec whose formatting-mechanics findings
	// are downgraded when a formatter/linter ran CLEAN on the same file in the
	// static-analysis pre-pass (Q5.d). Built-in: formatting. The signal is a
	// false-positive filter — a whitespace/indentation/style finding on a
	// gofmt-clean file is almost always noise.
	FormatterSilenceAware bool
	// IntentAware marks a spec that receives the Q8 `## PR author intent`
	// section (the structured description + linked-issue extraction). Built-ins:
	// testing (acceptance_criteria → expected cases) and scope (stops guessing
	// intent from the title). The vibe-coach also consumes intent but is a
	// named agent, not a registry spec, so it is wired directly. When no intent
	// is extracted the section is empty and these specs' prompts are unchanged.
	IntentAware bool

	// userDefined is true for specs loaded from the user config dir.
	userDefined bool
	// prompt is the resolved system prompt for user-defined specs; empty for
	// built-ins (which resolve via the embedded/override loader).
	prompt string
	// deficiencyPattern drives GateActionability: a finding whose comment
	// matches it (and carries no proposed wording / suggestion) is demoted.
	// nil means the gate does not fire even when listed in Gates. Not
	// serialized.
	deficiencyPattern *regexp.Regexp
}

// hasGate reports whether g is in the spec's gate set.
func (s SpecialistSpec) hasGate(g Gate) bool {
	for _, x := range s.Gates {
		if x == g {
			return true
		}
	}
	return false
}

// builtinSpecs is the ordered set of built-in review agents. The code
// specialists come first in run order (formatting → tech), then the PR agents
// (description → scope). AllSpecialists and AllPRAgents are derived from this
// slice so their exported order stays stable for callers while the registry
// remains the single source of truth.
//
// Each spec captures the CURRENT behaviour of the corresponding hard-coded
// dispatch site — see the per-field comments and Q1's report table.
var builtinSpecs = []SpecialistSpec{
	// --- Code specialists (KindCode) ------------------------------------
	{
		Name:                  SpecFormatting,
		Kind:                  KindCode,
		PromptSource:          PromptRef{Name: SpecFormatting, Embedded: true},
		Inputs:                InputSet{InputDiff},
		LanePriority:          2,
		ArbiterPolicy:         ArbiterPolicy{Suppressible: true, Demotable: true},
		Witnessable:           true,
		FormatterSilenceAware: true,
		FormattingEvidence:    true,
	},
	{
		Name:          SpecDesign,
		Kind:          KindCode,
		PromptSource:  PromptRef{Name: SpecDesign, Embedded: true},
		Inputs:        InputSet{InputDiff},
		LanePriority:  3,
		ArbiterPolicy: ArbiterPolicy{Suppressible: true, Demotable: true},
	},
	{
		Name:              SpecTesting,
		Kind:              KindCode,
		PromptSource:      PromptRef{Name: SpecTesting, Embedded: true},
		Inputs:            InputSet{InputDiff, InputEvidence},
		Gates:             []Gate{GateActionability},
		LanePriority:      4,
		ArbiterPolicy:     ArbiterPolicy{Suppressible: true, Demotable: true},
		Witnessable:       true,
		IntentAware:       true,
		deficiencyPattern: testingDeficiencyRe,
	},
	{
		Name:              SpecDocs,
		Kind:              KindCode,
		PromptSource:      PromptRef{Name: SpecDocs, Embedded: true},
		Inputs:            InputSet{InputDiff, InputEvidence},
		Gates:             []Gate{GateActionability},
		LanePriority:      5,
		ArbiterPolicy:     ArbiterPolicy{Suppressible: true, Demotable: true},
		Witnessable:       true,
		deficiencyPattern: docsDeficiencyRe,
	},
	{
		Name:         SpecSecurity,
		Kind:         KindCode,
		PromptSource: PromptRef{Name: SpecSecurity, Embedded: true},
		Inputs:       InputSet{InputDiff},
		LanePriority: 0, // most-protected lane: never lose a dedupe
		// Security is the one lane the arbiter may never suppress or demote.
		ArbiterPolicy: ArbiterPolicy{Suppressible: false, Demotable: false},
	},
	{
		Name:               SpecTech,
		Kind:               KindCode,
		PromptSource:       PromptRef{Name: SpecTech, Embedded: true},
		Inputs:             InputSet{InputDiff},
		LanePriority:       1,
		ArbiterPolicy:      ArbiterPolicy{Suppressible: true, Demotable: true},
		Witnessable:        true,
		RequiresTechBriefs: true,
		ConventionEvidence: true,
	},

	// --- PR-wide agents (KindPRWide) ------------------------------------
	{
		Name:          SpecDescription,
		Kind:          KindPRWide,
		PromptSource:  PromptRef{Name: SpecDescription, Embedded: true},
		Inputs:        InputSet{InputDiff},
		LanePriority:  7,
		ArbiterPolicy: ArbiterPolicy{Suppressible: true, Demotable: true},
		PRScope:       ScopeWholePR,
	},
	{
		Name:          SpecChecks,
		Kind:          KindPRWide,
		PromptSource:  PromptRef{Name: SpecChecks, Embedded: true},
		Inputs:        InputSet{InputDiff, InputChecks},
		LanePriority:  6,
		ArbiterPolicy: ArbiterPolicy{Suppressible: true, Demotable: true},
		PRScope:       ScopeInline,
	},
	{
		Name:          SpecDiscussion,
		Kind:          KindPRWide,
		PromptSource:  PromptRef{Name: SpecDiscussion, Embedded: true},
		Inputs:        InputSet{InputDiff, InputThreads, InputDiscussion},
		LanePriority:  8,
		ArbiterPolicy: ArbiterPolicy{Suppressible: true, Demotable: true},
		PRScope:       ScopeThreadAnchored,
		RebuttalAware: true,
	},
	{
		Name:          SpecScope,
		Kind:          KindPRWide,
		PromptSource:  PromptRef{Name: SpecScope, Embedded: true},
		Inputs:        InputSet{InputDiff},
		LanePriority:  9,
		ArbiterPolicy: ArbiterPolicy{Suppressible: true, Demotable: true},
		PRScope:       ScopeWholePR,
		IntentAware:   true,
	},
}

// specRegistry holds the live set of specs: the built-ins plus any
// user-defined specialists merged in at build time. Lookups are by
// case-normalized name; order preserves builtinSpecs order followed by
// user specs in load order.
type specRegistry struct {
	order  []string
	byName map[string]SpecialistSpec
}

var (
	registryOnce sync.Once
	registryMu   sync.RWMutex
	liveRegistry *specRegistry
)

// getRegistry returns the process-wide registry, building it once (built-ins +
// user-defined specs). Loading user specs is fail-open: a malformed spec logs a
// warning and is skipped (see loadUserSpecialists).
func getRegistry() *specRegistry {
	registryOnce.Do(func() {
		registryMu.Lock()
		liveRegistry = buildRegistry(loadUserSpecialists())
		registryMu.Unlock()
	})
	registryMu.RLock()
	defer registryMu.RUnlock()
	return liveRegistry
}

// buildRegistry assembles a registry from the built-ins and the given
// user-defined specs. A user spec whose name collides with a built-in (or an
// earlier user spec) is skipped so a stray file can never shadow a built-in
// specialist's behaviour.
func buildRegistry(user []SpecialistSpec) *specRegistry {
	r := &specRegistry{byName: make(map[string]SpecialistSpec)}
	add := func(s SpecialistSpec, allowOverride bool) {
		key := specKey(s.Name)
		if key == "" {
			return
		}
		if _, exists := r.byName[key]; exists && !allowOverride {
			return
		}
		if _, exists := r.byName[key]; !exists {
			r.order = append(r.order, key)
		}
		r.byName[key] = s
	}
	for _, s := range builtinSpecs {
		add(s, true)
	}
	for _, s := range user {
		if _, exists := r.byName[specKey(s.Name)]; exists {
			continue // never shadow a built-in or an earlier user spec
		}
		add(s, false)
	}
	return r
}

// specKey normalizes a specialist name for registry lookups.
func specKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// lookupSpec returns the spec registered for name (case-insensitively) and
// whether it was found.
func lookupSpec(name string) (SpecialistSpec, bool) {
	r := getRegistry()
	s, ok := r.byName[specKey(name)]
	return s, ok
}

// builtinNames returns the built-in specs of the given kind, in registry
// order. Used to derive the stable exported AllSpecialists / AllPRAgents.
func builtinNames(kind Kind) []string {
	var out []string
	for _, s := range builtinSpecs {
		if s.Kind == kind {
			out = append(out, s.Name)
		}
	}
	return out
}

// --- dispatch-site helpers (the registry consultation points) ------------

// specialistLanePriority orders agents by whose specialty owns code-level
// findings, so when a line is flagged by several agents the keeper comes from
// the most-relevant lane. Lower is higher priority; unknown agents sort last
// (99). Security is priority 0 — a near-duplicate merge must never let another
// lane swallow a security finding out from under the arbiter's never-suppress
// guard.
func specialistLanePriority(name string) int {
	if s, ok := lookupSpec(name); ok {
		return s.LanePriority
	}
	return 99
}

// specWitnessable reports whether the named spec's findings are fed to the
// convention witness (built-ins: testing, docs, tech).
func specWitnessable(name string) bool {
	s, ok := lookupSpec(name)
	return ok && s.Witnessable
}

// specWantsEvidence reports whether the named spec receives the per-PR
// evidence pack (built-ins: testing, docs).
func specWantsEvidence(name string) bool {
	s, ok := lookupSpec(name)
	return ok && s.Inputs.Has(InputEvidence)
}

// specWantsConventionEvidence reports whether the named spec's findings get
// sibling-file convention evidence harvested for the witness (built-in: tech).
func specWantsConventionEvidence(name string) bool {
	s, ok := lookupSpec(name)
	return ok && s.ConventionEvidence
}

// specWantsFormattingEvidence reports whether the named spec's findings get
// formatting-specific sibling-file evidence harvested for the witness
// (built-in: formatting). See BuildFormattingConventionEvidence.
func specWantsFormattingEvidence(name string) bool {
	s, ok := lookupSpec(name)
	return ok && s.FormattingEvidence
}

// specFormatterSilenceAware reports whether the named spec's formatting
// findings are downgraded when a formatter ran clean on the same file in the
// static-analysis pre-pass (built-in: formatting). See downgradeFormatterSilencedFindings.
func specFormatterSilenceAware(name string) bool {
	s, ok := lookupSpec(name)
	return ok && s.FormatterSilenceAware
}

// specWantsIntent reports whether the named spec receives the Q8 `## PR author
// intent` section (built-ins: testing, scope). See SpecialistSpec.IntentAware.
func specWantsIntent(name string) bool {
	s, ok := lookupSpec(name)
	return ok && s.IntentAware
}

// specSuppressible reports whether the repo arbiter may suppress the named
// spec's findings. Unknown specs default to suppressible, matching the
// pre-registry behaviour where only security was ever blocked.
func specSuppressible(name string) bool {
	s, ok := lookupSpec(name)
	if !ok {
		return true
	}
	return s.ArbiterPolicy.Suppressible
}

// specDemotable reports whether the repo arbiter may demote the named spec's
// findings. Unknown specs default to demotable.
func specDemotable(name string) bool {
	s, ok := lookupSpec(name)
	if !ok {
		return true
	}
	return s.ArbiterPolicy.Demotable
}
