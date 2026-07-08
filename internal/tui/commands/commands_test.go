package commands

import (
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

func sampleRegistry() *Registry {
	r := New()
	r.Register(Command{ID: "a", Title: "Refresh PR list", Category: "queue",
		Enabled: func(c Context) bool { return c.Mode == "list" },
		Run:     func() tea.Cmd { return nil }})
	r.Register(Command{ID: "b", Title: "Start AI review", Category: "detail",
		Enabled: func(c Context) bool { return c.Mode == "detail" && c.HasPR },
		Run:     func() tea.Cmd { return nil }})
	r.Register(Command{ID: "c", Title: "Quit", Category: "global",
		Run: func() tea.Cmd { return nil }}) // nil Enabled = always
	return r
}

// TestEnabledGating: only commands whose predicate accepts the Context are
// returned; a nil predicate is always enabled.
func TestEnabledGating(t *testing.T) {
	r := sampleRegistry()

	list := r.Enabled(Context{Mode: "list"})
	if !hasID(list, "a") || hasID(list, "b") || !hasID(list, "c") {
		t.Fatalf("list ctx: got %v, want a + c only", ids(list))
	}

	detailNoPR := r.Enabled(Context{Mode: "detail"})
	if hasID(detailNoPR, "b") {
		t.Fatalf("detail w/o PR should not enable b: %v", ids(detailNoPR))
	}

	detailPR := r.Enabled(Context{Mode: "detail", HasPR: true})
	if !hasID(detailPR, "b") || !hasID(detailPR, "c") {
		t.Fatalf("detail w/ PR should enable b + c: %v", ids(detailPR))
	}
}

// TestFilterFuzzyMatch: the fuzzy filter narrows and orders by match; an
// empty query is a passthrough preserving registration order.
func TestFilterFuzzyMatch(t *testing.T) {
	r := sampleRegistry()
	all := r.All()

	if got := Filter(all, ""); len(got) != 3 || got[0].ID != "a" {
		t.Fatalf("empty query should passthrough in order, got %v", ids(got))
	}

	got := Filter(all, "review")
	if len(got) == 0 || got[0].ID != "b" {
		t.Fatalf("query %q should surface the review command first, got %v", "review", ids(got))
	}

	if got := Filter(all, "zzzznope"); len(got) != 0 {
		t.Fatalf("non-matching query should yield nothing, got %v", ids(got))
	}
}

// TestShortcutLabel exposes the binding's display key (or "" when none).
func TestShortcutLabel(t *testing.T) {
	withKey := Command{Binding: key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "filter"))}
	if !withKey.HasBinding() || withKey.ShortcutLabel() != "f" {
		t.Fatalf("expected shortcut label f, got %q (hasBinding=%v)", withKey.ShortcutLabel(), withKey.HasBinding())
	}
	none := Command{}
	if none.HasBinding() || none.ShortcutLabel() != "" {
		t.Fatalf("expected no shortcut, got %q", none.ShortcutLabel())
	}
}

func hasID(cmds []Command, id string) bool {
	for _, c := range cmds {
		if c.ID == id {
			return true
		}
	}
	return false
}

func ids(cmds []Command) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = c.ID
	}
	return out
}
