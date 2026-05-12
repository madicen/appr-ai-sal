package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/madicen/appr-ai-sal/internal/review"
)

func TestFormatArbiterRowSummary_ProducesMarkdownShape(t *testing.T) {
	arb := &review.RepoArbiterResult{
		UserSummary:      "This repo prefers light tests; suppressed two heavy refactor asks.",
		Suppressed:       make([]review.SuppressedFindingRef, 2),
		Demoted:          make([]review.DemotedFindingRef, 1),
		EffectiveVerdict: "approve",
		VerdictOverride:  "approve",
		RationaleBullets: []string{
			"Convention witness saw few tests for similar paths.",
			"Two duplicate findings collapsed into one.",
		},
	}
	got := formatArbiterRowSummary(arb)

	// Markdown shape: every non-text section is separated by a blank line
	// so glamour treats it as a paragraph (otherwise consecutive lines
	// fold into one ungainly paragraph).
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("triple newline in output collapses paragraphs:\n%s", got)
	}
	// Bullets must use `- ` (markdown) not the literal • glyph, because
	// glamour only recognises `- `, `*`, or `+` as list markers.
	if strings.Contains(got, "• ") {
		t.Fatalf("output still contains raw bullet glyph; should use `- ` for markdown:\n%s", got)
	}
	if !strings.Contains(got, "- Convention witness") {
		t.Fatalf("output missing markdown bullet for rationale entry:\n%s", got)
	}
	// Labels should be bolded so the rendered preview gives them visual
	// weight without keeping the literal `**`.
	for _, must := range []string{"**Verdict override:**", "**Rationale:**"} {
		if !strings.Contains(got, must) {
			t.Errorf("output missing markdown label %q:\n%s", must, got)
		}
	}
}

func TestFormatArbiterRowSummary_RendersCleanThroughGlamour(t *testing.T) {
	// End-to-end: piping the function output through renderMarkdownIndented
	// (the path the running view actually uses) should produce ANSI-styled
	// text with neither raw `**...**` markup nor the source `- ` bullets.
	arb := &review.RepoArbiterResult{
		UserSummary:      "Light tests preferred.",
		EffectiveVerdict: "approve",
		RationaleBullets: []string{"Reason one."},
	}
	body := formatArbiterRowSummary(arb)
	plain := ansi.Strip(renderMarkdownIndented(body, 70, 2))
	if strings.Contains(plain, "**") {
		t.Fatalf("rendered arbiter summary still contains literal bold markers:\n%s", plain)
	}
	// `- Reason one.` source should render as `• Reason one.` (glamour's
	// bullet glyph) — at minimum, the literal `- ` should be gone.
	if strings.Contains(plain, "- Reason one.") {
		t.Fatalf("rendered arbiter summary still has raw `- ` bullet:\n%s", plain)
	}
	if !strings.Contains(plain, "Reason one.") {
		t.Fatalf("rendered arbiter summary missing rationale text:\n%s", plain)
	}
}
