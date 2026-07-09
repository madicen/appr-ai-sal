// Package commands is the real command registry shared by the keymap
// (internal/tui/keys) and the ctrl+k fuzzy command palette.
//
// Each Command is a declarative record: an id, a human title, a category, an
// optional key.Binding (the same one the keymap uses, so a palette entry and
// its keyboard shortcut are one thing), an Enabled predicate that gates it on
// the current app state, and a Run closure that returns the tea.Cmd the model
// already understands. The model builds the registry by wiring Run to its
// existing handlers — commands never reimplement actions.
//
// The palette (internal/tui/overlays) filters the registry to currently
// enabled commands and fuzzy-matches over their titles (sahilm/fuzzy). It
// "subsumes status-bar overflow": actions that don't fit the bottom status
// bar are still discoverable and runnable here.
//
// Registration seam: later Phase 5 groups register a Command via
// Registry.Register from the model without touching core routing — e.g.
// item 2 "edit finding" (e), item 5 triage sort/filter, item 10 queue.
//
// This package is a leaf over bubbles/key + bubbletea + sahilm/fuzzy; it
// imports no model/overlay code so it stays cycle-free.
package commands

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"
)

// Context is the snapshot of app state a Command's Enabled predicate reads
// to decide whether the command should be offered right now. The model
// fills it in when it opens the palette.
type Context struct {
	Mode         string // "list" | "detail" | "settings" | "repoagents" | "langagents"
	HasPR        bool   // a PR is loaded (detail)
	HasDraft     bool   // a review draft exists (approvable / postable)
	HasSelection bool   // the list has a highlighted PR
}

// Command is one registry entry.
type Command struct {
	// ID is a stable identifier (used by tests and future persistence).
	ID string
	// Title is the palette label and the fuzzy-match target.
	Title string
	// Category groups related commands in the palette listing.
	Category string
	// Binding is the optional keyboard shortcut for this command. The zero
	// value means "no shortcut"; HasBinding reports which.
	Binding key.Binding
	// Enabled gates the command on the current Context. A nil predicate
	// means "always enabled".
	Enabled func(Context) bool
	// Run performs the command, returning the tea.Cmd the model handles.
	// A nil Run is a no-op.
	Run func() tea.Cmd
}

// HasBinding reports whether the command advertises a keyboard shortcut.
func (c Command) HasBinding() bool {
	return len(c.Binding.Keys()) > 0
}

// ShortcutLabel returns the display key for the command's shortcut, or ""
// when it has none (e.g. "f", "ctrl+k").
func (c Command) ShortcutLabel() string {
	if !c.HasBinding() {
		return ""
	}
	return c.Binding.Help().Key
}

// Registry is an ordered set of commands.
type Registry struct {
	cmds []Command
}

// New returns an empty registry.
func New() *Registry { return &Registry{} }

// Register appends a command. Registration order is preserved as the
// default palette order (before any fuzzy filtering).
func (r *Registry) Register(c Command) { r.cmds = append(r.cmds, c) }

// All returns every registered command (regardless of enablement).
func (r *Registry) All() []Command { return r.cmds }

// Enabled returns the commands whose Enabled predicate accepts ctx (nil
// predicate = always enabled), preserving registration order.
func (r *Registry) Enabled(ctx Context) []Command {
	out := make([]Command, 0, len(r.cmds))
	for _, c := range r.cmds {
		if c.Enabled == nil || c.Enabled(ctx) {
			out = append(out, c)
		}
	}
	return out
}

// Find returns the command with the given id and whether it was found.
func (r *Registry) Find(id string) (Command, bool) {
	for _, c := range r.cmds {
		if c.ID == id {
			return c, true
		}
	}
	return Command{}, false
}

// Filter fuzzy-matches cmds against query over their titles, returning the
// matches ordered by descending score. An empty/whitespace query returns
// cmds unchanged (registration order).
func Filter(cmds []Command, query string) []Command {
	query = strings.TrimSpace(query)
	if query == "" {
		return cmds
	}
	titles := make([]string, len(cmds))
	for i, c := range cmds {
		titles[i] = c.Title
	}
	matches := fuzzy.Find(query, titles)
	out := make([]Command, 0, len(matches))
	for _, mt := range matches {
		out = append(out, cmds[mt.Index])
	}
	return out
}
