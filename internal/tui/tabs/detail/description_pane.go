package detail

import (
	"strings"

	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

func renderDescriptionPane(body string, width int) string {
	width = max(8, width)
	var b strings.Builder
	b.WriteString(styles.BoldStyle.Render("Description") + "  " +
		zone.Mark(zones.DescriptionToggle, styles.DimStyle.Render(" hide (g) ")) + "\n\n")
	body = strings.TrimSpace(body)
	if body == "" {
		b.WriteString(styles.DimStyle.Render("(this PR has no description)"))
		return b.String()
	}
	b.WriteString(util.RenderMarkdownIndented(body, width, 0))
	return b.String()
}
