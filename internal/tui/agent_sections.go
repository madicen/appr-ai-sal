package tui

import (
	"github.com/madicen/appr-ai-sal/internal/review"
)

// agentRowCount is specialists + vibe coach row. The review overlay sizes its
// per-agent slices from this.
func agentRowCount() int {
	return len(review.AllSpecialists) + 1
}
