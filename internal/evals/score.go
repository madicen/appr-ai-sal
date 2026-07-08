package evals

import (
	"fmt"
	"sort"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/review"
)

// Ratio is a Num/Den fraction that reports "n/a" when it has no denominator,
// so a metric with nothing to measure (e.g. anchor-hit rate for a specialist
// that proposed no suggestions) is never reported as a misleading 0%.
type Ratio struct{ Num, Den int }

// Defined reports whether the ratio has a denominator to divide by.
func (r Ratio) Defined() bool { return r.Den > 0 }

// Value returns the ratio in [0,1], or 0 when undefined.
func (r Ratio) Value() float64 {
	if r.Den == 0 {
		return 0
	}
	return float64(r.Num) / float64(r.Den)
}

// String renders "83% (5/6)" or "n/a" (no denominator).
func (r Ratio) String() string {
	if !r.Defined() {
		return "n/a"
	}
	return fmt.Sprintf("%d%% (%d/%d)", int(r.Value()*100+0.5), r.Num, r.Den)
}

// SpecialistScore is the scored outcome for one agent (specialist or PR agent)
// on one case.
type SpecialistScore struct {
	Specialist string
	Kind       review.Kind
	ParsedOK   bool
	Emitted    int // findings emitted after gates

	// Recall: labelled must-appear findings and how many were matched.
	ExpectedTotal   int
	ExpectedMatched int

	// Precision (labelled): findings matching a must-appear (TP) vs a
	// must-not-appear scar (FP). Findings matching neither are ignored.
	TruePositives int
	ForbiddenHits int

	// Suggestion machinery: the model's pre-gate inline suggestion attempts,
	// how many survived the deterministic gates as GitHub-postable
	// suggestions, and how many of those kept the model's OWN anchor (no
	// relocation, not synthesized, not repaired).
	RawSuggestionAttempts int
	SurvivingSuggestions  int
	AnchorHits            int

	// JSONFirstTry records whether the model parsed on the first attempt and,
	// when the case pinned an expectation, whether it matched.
	JSONFirstTry        bool
	JSONFirstTryPinned  bool
	JSONFirstTryMatched bool
}

// Recall is matched / expected must-appear findings.
func (s SpecialistScore) Recall() Ratio { return Ratio{s.ExpectedMatched, s.ExpectedTotal} }

// Precision is TP / (TP+FP) over labelled findings.
func (s SpecialistScore) Precision() Ratio {
	return Ratio{s.TruePositives, s.TruePositives + s.ForbiddenHits}
}

// SuggestionSurvival is surviving / attempted model suggestions.
func (s SpecialistScore) SuggestionSurvival() Ratio {
	return Ratio{s.SurvivingSuggestions, s.RawSuggestionAttempts}
}

// AnchorHitRate is anchor-correct / attempted model suggestions.
func (s SpecialistScore) AnchorHitRate() Ratio { return Ratio{s.AnchorHits, s.RawSuggestionAttempts} }

// CaseScore is the full scoring of one case.
type CaseScore struct {
	ID          string
	Title       string
	Specialists []SpecialistScore

	VerdictExpected string
	VerdictActual   string
	VerdictScored   bool

	Calls        int
	InputTokens  int
	OutputTokens int
	CostUSD      float64
	CostKnown    bool
}

// VerdictOK reports whether the run reached the expected verdict (true when no
// verdict was scored).
func (c CaseScore) VerdictOK() bool {
	if !c.VerdictScored {
		return true
	}
	return strings.EqualFold(c.VerdictExpected, c.VerdictActual)
}

// JSONFirstTryFailures returns the specialists whose first-try parse
// expectation was pinned and not met.
func (c CaseScore) JSONFirstTryFailures() []string {
	var out []string
	for _, s := range c.Specialists {
		if s.JSONFirstTryPinned && !s.JSONFirstTryMatched {
			out = append(out, s.Specialist)
		}
	}
	return out
}

// ScoreCase scores one observation against a case's expectations. It is pure:
// no I/O, deterministic in the observation. This is the unit-tested core.
func ScoreCase(c Case, obs review.EvalObservation) CaseScore {
	exp := c.Expectations
	appearBySpec := groupExpectations(exp.MustAppear)
	forbidBySpec := groupExpectations(exp.MustNotAppear)

	cs := CaseScore{ID: c.Meta.ID, Title: c.Meta.Title}

	for _, a := range obs.Agents {
		ss := SpecialistScore{
			Specialist:            a.Agent,
			Kind:                  a.Kind,
			ParsedOK:              a.ParsedOK,
			Emitted:               len(a.Findings),
			RawSuggestionAttempts: a.RawSuggestionAttempts,
		}

		// Recall: each must-appear for this specialist is matched when some
		// finding matches it.
		for _, want := range appearBySpec[specKey(a.Agent)] {
			ss.ExpectedTotal++
			if anyFindingMatches(want, a.Agent, a.Findings) {
				ss.ExpectedMatched++
			}
		}

		// Precision (labelled) + suggestion machinery, per finding.
		appears := appearBySpec[specKey(a.Agent)]
		forbids := forbidBySpec[specKey(a.Agent)]
		for _, f := range a.Findings {
			if matchesAny(appears, a.Agent, f) {
				ss.TruePositives++
			} else if matchesAny(forbids, a.Agent, f) {
				ss.ForbiddenHits++
			}
			if suggestionSurvived(f) {
				ss.SurvivingSuggestions++
				if anchorHit(f) {
					ss.AnchorHits++
				}
			}
		}

		// JSON-first-try expectation.
		ss.JSONFirstTry = a.ParsedOK
		if exp.ExpectJSONFirstTry != nil {
			if want, ok := exp.ExpectJSONFirstTry[a.Agent]; ok {
				ss.JSONFirstTryPinned = true
				ss.JSONFirstTryMatched = (want == a.ParsedOK)
			}
		}

		cs.Specialists = append(cs.Specialists, ss)
	}

	if v := strings.TrimSpace(exp.ExpectedVerdict); v != "" {
		cs.VerdictScored = true
		cs.VerdictExpected = review.NormalizeVibeVerdict(v)
		cs.VerdictActual = obs.FinalVerdict()
	}

	cs.Calls = obs.Usage.Calls
	cs.InputTokens = obs.Usage.InputTokens
	cs.OutputTokens = obs.Usage.OutputTokens
	cs.CostUSD = obs.Usage.CostUSD
	cs.CostKnown = obs.Usage.CostKnown
	return cs
}

// suggestionSurvived reports whether the model's own suggestion is still a
// GitHub-postable one-click fix after the gates. Synthesized / repaired
// suggestions are excluded: they are the tool's fixes, not the model's, so
// they do not count toward the model's survival rate.
func suggestionSurvived(f review.Finding) bool {
	if f.SuggestionSynthesized || f.SuggestionRepaired {
		return false
	}
	return review.SuggestionPostsToGitHub(f)
}

// anchorHit reports whether a surviving suggestion kept the model's ORIGINAL
// anchor line (no relocation by the excerpt gate).
func anchorHit(f review.Finding) bool {
	return f.AnchorRelocatedFrom == 0
}

// anyFindingMatches reports whether any finding matches the expectation.
func anyFindingMatches(want ExpectFinding, specialist string, findings []review.Finding) bool {
	for _, f := range findings {
		if want.matches(specialist, f) {
			return true
		}
	}
	return false
}

// matchesAny reports whether f matches any of the expectations.
func matchesAny(exps []ExpectFinding, specialist string, f review.Finding) bool {
	for _, e := range exps {
		if e.matches(specialist, f) {
			return true
		}
	}
	return false
}

// groupExpectations buckets expectations by normalized specialist name.
func groupExpectations(exps []ExpectFinding) map[string][]ExpectFinding {
	out := map[string][]ExpectFinding{}
	for _, e := range exps {
		k := specKey(e.Specialist)
		out[k] = append(out[k], e)
	}
	return out
}

func specKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// CorpusScore aggregates per-case scores plus corpus-wide roll-ups.
type CorpusScore struct {
	Provider string
	Model    string
	Label    string // A/B label ("A", "B", ...) or "" for a single run
	Cases    []CaseScore
}

// SpecialistAggregate is a corpus-wide roll-up for one specialist.
type SpecialistAggregate struct {
	Specialist                 string
	RecallNum, RecallDen       int
	PrecisionNum, PrecisionDen int
	SurvivalNum, SurvivalDen   int
	AnchorNum, AnchorDen       int
	FirstTryOK, FirstTryTotal  int
}

func (a SpecialistAggregate) Recall() Ratio    { return Ratio{a.RecallNum, a.RecallDen} }
func (a SpecialistAggregate) Precision() Ratio { return Ratio{a.PrecisionNum, a.PrecisionDen} }
func (a SpecialistAggregate) Survival() Ratio  { return Ratio{a.SurvivalNum, a.SurvivalDen} }
func (a SpecialistAggregate) Anchor() Ratio    { return Ratio{a.AnchorNum, a.AnchorDen} }
func (a SpecialistAggregate) FirstTry() Ratio  { return Ratio{a.FirstTryOK, a.FirstTryTotal} }

// Aggregate rolls the per-case specialist scores up into one row per
// specialist, in stable (sorted) order.
func (csr CorpusScore) Aggregate() []SpecialistAggregate {
	bySpec := map[string]*SpecialistAggregate{}
	var order []string
	get := func(name string) *SpecialistAggregate {
		if a, ok := bySpec[name]; ok {
			return a
		}
		a := &SpecialistAggregate{Specialist: name}
		bySpec[name] = a
		order = append(order, name)
		return a
	}
	for _, cse := range csr.Cases {
		for _, s := range cse.Specialists {
			a := get(s.Specialist)
			a.RecallNum += s.Recall().Num
			a.RecallDen += s.Recall().Den
			a.PrecisionNum += s.Precision().Num
			a.PrecisionDen += s.Precision().Den
			a.SurvivalNum += s.SuggestionSurvival().Num
			a.SurvivalDen += s.SuggestionSurvival().Den
			a.AnchorNum += s.AnchorHitRate().Num
			a.AnchorDen += s.AnchorHitRate().Den
			a.FirstTryTotal++
			if s.JSONFirstTry {
				a.FirstTryOK++
			}
		}
	}
	sort.Strings(order)
	out := make([]SpecialistAggregate, 0, len(order))
	for _, n := range order {
		out = append(out, *bySpec[n])
	}
	return out
}

// Totals returns corpus-wide call / token / cost totals and the verdict
// hit-rate.
func (csr CorpusScore) Totals() (calls, inTok, outTok int, cost float64, costKnown bool, verdictOK, verdictTotal int) {
	for _, c := range csr.Cases {
		calls += c.Calls
		inTok += c.InputTokens
		outTok += c.OutputTokens
		cost += c.CostUSD
		if c.CostKnown {
			costKnown = true
		}
		if c.VerdictScored {
			verdictTotal++
			if c.VerdictOK() {
				verdictOK++
			}
		}
	}
	return
}
