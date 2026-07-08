package review

import "github.com/madicen/appr-ai-sal/internal/aiconfig"

// Q7 per-stage model routing + ensemble.
//
// A profile may route each review stage to a different model (stage_models) and
// opt specific stages into multi-model ensemble mode (ensemble) — see
// aiconfig.Profile.StageModels / Profile.Ensemble for the config shape and
// precedence. This file names the synthesis stages (which have no SpecX name
// const) and wires the specialist / PR-agent phases into the ensemble
// union+dedupe path.
//
// How the stage identifier reaches provider selection: every AI stage already
// takes the run *aiconfig.Config and eventually hands it to ai.ProviderFor,
// which selects a backend from cfg.Provider and a model from cfg.Model. Q7
// threads the stage by calling cfg.ForStage(stage) at the point a stage obtains
// its config: the resulting clone carries the stage's routed model in Model (or
// is the same config unchanged when the stage keeps the profile model, so a
// profile with no stage_models is behavior-identical to before). Ensemble
// stages instead fan out over cfg.WithModel(model) per configured model. The
// same provider/base-url/key are reused for every stage model — a stage model
// overrides only the model id.
const (
	// StageArbiter is the stage_models / ensemble key for the repo arbiter.
	StageArbiter = "arbiter"
	// StageWitness is the stage_models / ensemble key for the convention
	// witness. Setting stage_models["witness"] to a different model family than
	// the specialists it audits decorrelates their hallucinations (Q7).
	StageWitness = "witness"
	// StageVibeCoach is the stage_models / ensemble key for the vibe-coach.
	StageVibeCoach = "vibe-coach"
)

// mergeEnsembleMembers unions the results of running one stage across several
// models into a single SpecialistResult under name. Findings are combined and
// run through the same cross-specialist dedupe the runner already applies
// (dedupeInlineFindingsAcrossSpecialists), so a finding both models filed on
// the same line collapses to one while findings unique to either model all
// survive. The dedupe groups by (path, side) independent of specialist, so
// same-named ensemble members union cleanly.
//
// It is fail-open: members that errored are dropped and the union is built from
// the successful ones; only when every member errored does it return an errored
// result (the first error) so the stage is still marked failed. Repair and
// raw-suggestion counters are summed across members for telemetry, and the
// first non-empty summary is kept.
func mergeEnsembleMembers(name string, members []SpecialistResult) SpecialistResult {
	ok := make([]SpecialistResult, 0, len(members))
	var firstErr *SpecialistResult
	for i := range members {
		if members[i].Err != nil {
			if firstErr == nil {
				firstErr = &members[i]
			}
			continue
		}
		ok = append(ok, members[i])
	}
	if len(ok) == 0 {
		if firstErr != nil {
			return *firstErr
		}
		return SpecialistResult{Specialist: name, Findings: []Finding{}}
	}

	deduped := dedupeInlineFindingsAcrossSpecialists(ok)
	merged := SpecialistResult{Specialist: name, Findings: []Finding{}}
	for _, m := range deduped {
		merged.Findings = append(merged.Findings, m.Findings...)
		merged.RepairFired += m.RepairFired
		merged.RepairSucceeded += m.RepairSucceeded
		merged.RawSuggestionAttempts += m.RawSuggestionAttempts
		if merged.Summary == "" {
			merged.Summary = m.Summary
		}
	}
	return merged
}

// runEnsemble runs a stage once per model in models and unions the results with
// mergeEnsembleMembers. runMember performs one member run against the supplied
// per-model config (already carrying the stage's retry/timeout wiring at the
// call site); the R2 concurrency semaphore installed on the run context still
// caps total concurrent inference, so ensemble members never exceed the run's
// inference budget. Each member reuses baseCfg's provider/key; only the model
// changes (via aiconfig.WithModel).
func runEnsemble(name string, models []string, baseCfg *aiconfig.Config, runMember func(cfg *aiconfig.Config) SpecialistResult) SpecialistResult {
	members := make([]SpecialistResult, 0, len(models))
	for _, model := range models {
		members = append(members, runMember(baseCfg.WithModel(model)))
	}
	return mergeEnsembleMembers(name, members)
}
