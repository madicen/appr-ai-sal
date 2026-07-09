package settings

import "github.com/madicen/appr-ai-sal/internal/tui/styles"

// Styles route through internal/tui/styles so the palette lives in one
// place (no per-tab hex copies). The local names are kept so the render
// call sites read the same as before.
var (
	dimStyle  = styles.DimStyle
	boldStyle = styles.BoldStyle
	errStyle  = styles.ErrStyle
	okStyle   = styles.OkStyle
)
