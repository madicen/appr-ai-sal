package evals

import (
	"fmt"
	"sort"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/review"
)

// RenderReport renders a single-run markdown report: a per-specialist scores
// table (recall, precision, suggestion-survival, anchor-hit, JSON-first-try),
// a per-case verdict/JSON summary, and the run's token/cost totals.
func RenderReport(csr CorpusScore) string {
	var b strings.Builder
	b.WriteString("# appr-ai-sal eval report\n\n")
	fmt.Fprintf(&b, "**Provider:** %s", providerLabel(csr))
	fmt.Fprintf(&b, " · **Cases:** %d\n\n", len(csr.Cases))

	b.WriteString("## Scores by specialist\n\n")
	b.WriteString(specialistTable(csr.Aggregate()))
	b.WriteString("\n")

	b.WriteString("## Cases\n\n")
	b.WriteString(caseTable(csr.Cases))
	b.WriteString("\n")

	calls, inTok, outTok, cost, costKnown, verdictOK, verdictTotal := csr.Totals()
	b.WriteString("## Totals\n\n")
	fmt.Fprintf(&b, "- Inference: %s\n", usageLine(calls, inTok, outTok, cost, costKnown))
	if verdictTotal > 0 {
		fmt.Fprintf(&b, "- Verdicts matched: %s\n", Ratio{verdictOK, verdictTotal})
	}
	return b.String()
}

// RenderABReport renders an A/B comparison of two runs (typically prompt
// version A vs B), with a delta column per metric. Both runs must have been
// scored over the same corpus.
func RenderABReport(a, bRun CorpusScore) string {
	var b strings.Builder
	b.WriteString("# appr-ai-sal eval report (A/B)\n\n")
	fmt.Fprintf(&b, "- **A:** %s\n", providerLabel(a))
	fmt.Fprintf(&b, "- **B:** %s\n", providerLabel(bRun))
	fmt.Fprintf(&b, "- **Cases:** %d\n\n", len(a.Cases))

	aggA := indexAggregates(a.Aggregate())
	aggB := indexAggregates(bRun.Aggregate())
	names := unionKeys(aggA, aggB)

	for _, metric := range abMetrics {
		fmt.Fprintf(&b, "## %s\n\n", metric.title)
		b.WriteString("| Specialist | A | B | Δ |\n|---|---|---|---|\n")
		for _, n := range names {
			ra := metric.get(aggA[n])
			rb := metric.get(aggB[n])
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", n, ra, rb, deltaPP(ra, rb))
		}
		b.WriteString("\n")
	}

	callsA, inA, outA, costA, ckA, vOKA, vTotA := a.Totals()
	callsB, inB, outB, costB, ckB, vOKB, vTotB := bRun.Totals()
	b.WriteString("## Totals\n\n")
	fmt.Fprintf(&b, "- A inference: %s\n", usageLine(callsA, inA, outA, costA, ckA))
	fmt.Fprintf(&b, "- B inference: %s\n", usageLine(callsB, inB, outB, costB, ckB))
	if vTotA > 0 || vTotB > 0 {
		fmt.Fprintf(&b, "- Verdicts matched: A %s · B %s\n", Ratio{vOKA, vTotA}, Ratio{vOKB, vTotB})
	}
	return b.String()
}

// abMetrics is the ordered set of per-specialist metrics shown in the A/B
// report.
var abMetrics = []struct {
	title string
	get   func(SpecialistAggregate) Ratio
}{
	{"Recall", func(a SpecialistAggregate) Ratio { return a.Recall() }},
	{"Precision", func(a SpecialistAggregate) Ratio { return a.Precision() }},
	{"Suggestion survival", func(a SpecialistAggregate) Ratio { return a.Survival() }},
	{"Anchor-hit rate", func(a SpecialistAggregate) Ratio { return a.Anchor() }},
	{"JSON parse first-try", func(a SpecialistAggregate) Ratio { return a.FirstTry() }},
}

func specialistTable(aggs []SpecialistAggregate) string {
	var b strings.Builder
	b.WriteString("| Specialist | Recall | Precision | Suggestion survival | Anchor-hit | JSON 1st-try |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, a := range aggs {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
			a.Specialist, a.Recall(), a.Precision(), a.Survival(), a.Anchor(), a.FirstTry())
	}
	return b.String()
}

func caseTable(cases []CaseScore) string {
	var b strings.Builder
	b.WriteString("| Case | Target | Verdict | JSON 1st-try |\n|---|---|---|---|\n")
	for _, c := range cases {
		verdict := "—"
		if c.VerdictScored {
			mark := "✓"
			if !c.VerdictOK() {
				mark = "✗"
			}
			verdict = fmt.Sprintf("%s %s→%s", mark, orDash(c.VerdictExpected), orDash(c.VerdictActual))
		}
		firstTry := jsonFirstTrySummary(c)
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", c.ID, c.Title, verdict, firstTry)
	}
	return b.String()
}

// jsonFirstTrySummary reports the fraction of a case's agents that parsed on
// the first try.
func jsonFirstTrySummary(c CaseScore) string {
	ok, total := 0, 0
	for _, s := range c.Specialists {
		total++
		if s.JSONFirstTry {
			ok++
		}
	}
	return Ratio{ok, total}.String()
}

func providerLabel(csr CorpusScore) string {
	model := strings.TrimSpace(csr.Model)
	if model == "" {
		model = "(default)"
	}
	label := csr.Provider + " · " + model
	if strings.TrimSpace(csr.Label) != "" {
		label = csr.Label + ": " + label
	}
	return label
}

func usageLine(calls, inTok, outTok int, cost float64, costKnown bool) string {
	parts := []string{
		fmt.Sprintf("%d calls", calls),
		fmt.Sprintf("%s in / %s out", review.FormatTokenCount(inTok), review.FormatTokenCount(outTok)),
	}
	if costKnown && cost > 0 {
		parts = append(parts, review.FormatCostUSD(cost))
	}
	return strings.Join(parts, " · ")
}

// deltaPP renders the B−A difference in percentage points (only when both
// ratios are defined), e.g. "+17pp" / "−8pp" / "0pp".
func deltaPP(a, b Ratio) string {
	if !a.Defined() || !b.Defined() {
		return "—"
	}
	d := (b.Value() - a.Value()) * 100
	switch {
	case d > 0.5:
		return fmt.Sprintf("+%dpp", int(d+0.5))
	case d < -0.5:
		return fmt.Sprintf("−%dpp", int(-d+0.5))
	default:
		return "0pp"
	}
}

func indexAggregates(aggs []SpecialistAggregate) map[string]SpecialistAggregate {
	out := make(map[string]SpecialistAggregate, len(aggs))
	for _, a := range aggs {
		out[a.Specialist] = a
	}
	return out
}

func unionKeys(a, b map[string]SpecialistAggregate) []string {
	seen := map[string]struct{}{}
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
