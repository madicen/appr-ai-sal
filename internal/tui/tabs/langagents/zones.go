package langagents

import (
	"fmt"

	la "github.com/madicen/appr-ai-sal/internal/review/langagents"
)

// Stable zone IDs for the language-experts tab. Per-row zones are keyed
// by canonical language so they stay unique and stable across renders
// even as the row set changes (scoped vs unscoped, cache reloads).

// ZoneClose marks the footer Close button (the esc equivalent).
const ZoneClose = "zone:langagents:close"

// zoneRow marks a language row body. Clicking it selects the row (the
// ↑/↓ equivalent).
func zoneRow(l la.Language) string { return fmt.Sprintf("zone:langagents:row:%s", l) }

// zoneRowGen marks a row's Generate/Refresh button (the g/r equivalent).
func zoneRowGen(l la.Language) string { return fmt.Sprintf("zone:langagents:row:%s:gen", l) }

// zoneRowDel marks a row's Delete button (the d equivalent).
func zoneRowDel(l la.Language) string { return fmt.Sprintf("zone:langagents:row:%s:del", l) }
