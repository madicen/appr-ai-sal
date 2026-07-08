package detail

// dividerTarget identifies which pane seam an active mouse drag is resizing.
type dividerTarget int

const (
	dividerNone dividerTarget = iota
	dividerTreeDiff
	dividerDiffControls
)

// paneDrag tracks an in-flight drag on one of the pane seams.
type paneDrag struct {
	target          dividerTarget
	originX         int
	originTreeW     int
	originControlsW int
}

const (
	defaultTreePaneWidth         = 30
	defaultControlsPaneWidth     = 38
	minTreePaneWidth             = 12
	minControlsPaneWidth         = 16
	controlsAutoHideMinDiffWidth = 36
)
