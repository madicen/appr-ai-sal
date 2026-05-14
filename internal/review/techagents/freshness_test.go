package techagents

import (
	"testing"
	"time"
)

func TestComputeMissingWhenNil(t *testing.T) {
	if got := Compute(nil, time.Now(), DefaultStaleAfter); got != FreshnessMissing {
		t.Fatalf("nil: got %s, want missing", got)
	}
}

func TestComputeMissingWhenNoPopulated(t *testing.T) {
	ta := &TechAgents{Agents: map[string]Agent{
		"kestra": {Tech: "kestra", Context: ""},
	}}
	if got := Compute(ta, time.Now(), DefaultStaleAfter); got != FreshnessMissing {
		t.Fatalf("empty context: got %s, want missing", got)
	}
}

func TestComputeFresh(t *testing.T) {
	now := time.Now()
	ta := &TechAgents{Agents: map[string]Agent{
		"kestra": {Tech: "kestra", Context: "ok", GeneratedAt: now.Add(-1 * time.Hour)},
		"kafka":  {Tech: "kafka", Context: "ok", GeneratedAt: now.Add(-24 * time.Hour)},
	}}
	if got := Compute(ta, now, DefaultStaleAfter); got != FreshnessFresh {
		t.Fatalf("fresh: got %s", got)
	}
}

func TestComputeStaleWhenOldEnough(t *testing.T) {
	now := time.Now()
	ta := &TechAgents{Agents: map[string]Agent{
		"kestra": {Tech: "kestra", Context: "ok", GeneratedAt: now.Add(-2 * DefaultStaleAfter)},
	}}
	if got := Compute(ta, now, DefaultStaleAfter); got != FreshnessStale {
		t.Fatalf("stale: got %s", got)
	}
}

func TestComputeStaleOnZeroTimestamp(t *testing.T) {
	ta := &TechAgents{Agents: map[string]Agent{
		"kestra": {Tech: "kestra", Context: "ok"},
	}}
	if got := Compute(ta, time.Now(), DefaultStaleAfter); got != FreshnessStale {
		t.Fatalf("zero timestamp: got %s", got)
	}
}

func TestLoadFreshnessUnknownOnEmptyRef(t *testing.T) {
	if got := LoadFreshness("", "", time.Now(), DefaultStaleAfter); got != FreshnessUnknown {
		t.Fatalf("empty owner/repo: got %s", got)
	}
}
