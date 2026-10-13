package zones

import "testing"

func TestSummarySectionZoneNames(t *testing.T) {
	if SummarySectionHeader("prompt:0") != "zone:staged:summary:section:prompt:0:header" {
		t.Fatal(SummarySectionHeader("prompt:0"))
	}
	if SummarySectionCheck("headline") != "zone:staged:summary:section:headline:check" {
		t.Fatal(SummarySectionCheck("headline"))
	}
	if SummarySectionEdit("stats") != "zone:staged:summary:section:stats:edit" {
		t.Fatal(SummarySectionEdit("stats"))
	}
}
