// Package tui is the public entry point for the appr-ai-sal terminal UI.
// All implementation lives in internal/tui/model and the supporting
// sub-packages (data, overlays, state, styles, tabs/*, util, zones).
//
// This file is intentionally a thin re-export shim — see jj-tui's
// internal/tui/tui.go for the same pattern. cmd/appr-ai-sal/main.go
// only consumes this surface, so the rest of the tree can be reorganised
// freely without breaking the binary's import path.
package tui

import (
	"github.com/madicen/appr-ai-sal/internal/tui/model"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
)

// Model is the root Bubble Tea model.
type Model = model.Model

// Options configures the root TUI model. See [model.Options] for fields.
type Options = model.Options

// New constructs a new root TUI model.
func New(opts Options) *Model { return model.New(opts) }

// FlushMouse resets the terminal's mouse-tracking state. Call via defer
// from the binary's main so a panic / interrupt doesn't leave the
// terminal stuck in cell-motion-tracking mode.
var FlushMouse = util.FlushMouse
