package model

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Tab is the uniform contract every full-screen tab the root can switch
// to satisfies (settings, repo-agents, lang-agents, …). The root holds a
// map[state.ViewMode]Tab and drives whichever tab is active through this
// interface, so message routing is one generic loop instead of a hand-
// forwarded case per concrete tab pointer.
//
// Update returns a Tab (not tea.Model) so the root never has to type-
// assert the result back to a concrete pointer the way the old two-phase
// forwarder did.
type Tab interface {
	Init() tea.Cmd
	Update(tea.Msg) (Tab, tea.Cmd)
	View() string
	Resize(w, h int)
	SetContentOrigin(top int)
}

// teaTab is the shape the concrete tab sub-models already expose. Their
// Update follows the standard Bubble Tea signature (returns tea.Model);
// the tabAdapter bridges that to the root's Tab interface so the sub-
// packages don't need to import this package (which would be a cycle).
type teaTab interface {
	Init() tea.Cmd
	Update(tea.Msg) (tea.Model, tea.Cmd)
	View() string
	Resize(w, h int)
	SetContentOrigin(top int)
}

// tabAdapter wraps a teaTab sub-model and presents it as a Tab. The sub-
// models use pointer receivers and mutate in place, but we still capture
// the model Update returns so a value-semantics sub-model would work too.
type tabAdapter struct{ inner teaTab }

// newTab wraps a concrete tab sub-model so the root can store and route it
// as a Tab.
func newTab(inner teaTab) *tabAdapter { return &tabAdapter{inner: inner} }

func (a *tabAdapter) Init() tea.Cmd { return a.inner.Init() }

func (a *tabAdapter) Update(msg tea.Msg) (Tab, tea.Cmd) {
	upd, cmd := a.inner.Update(msg)
	if t, ok := upd.(teaTab); ok {
		a.inner = t
	}
	return a, cmd
}

func (a *tabAdapter) View() string           { return a.inner.View() }
func (a *tabAdapter) Resize(w, h int)        { a.inner.Resize(w, h) }
func (a *tabAdapter) SetContentOrigin(t int) { a.inner.SetContentOrigin(t) }
