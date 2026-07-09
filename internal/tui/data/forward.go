package data

// ForwardToOverlay is a marker interface. Any tea.Msg whose concrete type
// implements it is routed to the active overlay by the root model's
// generic forwarder, instead of requiring a bespoke case in the root
// Update switch.
//
// This is the structural fix for the documented deadlock class: the root
// used to hand-forward a fixed set of pipeline messages to the review
// overlay, and any newly-added message that the overlay needed but the
// root forgot to case-match would fall through the switch and strand the
// overlay (e.g. flipping to "Refining summary…" forever — see
// root_routing_vibe_test.go). Implementing this marker on the message
// makes forwarding automatic and impossible to forget.
//
// Only "pure forward" messages — ones whose sole consumer is the overlay
// — should implement it. Messages that also mutate root state (progress,
// PR refresh, dry-run fallback) keep explicit root cases so they can do
// both.
type ForwardToOverlay interface {
	// OverlayBound is a no-op marker method.
	OverlayBound()
}

// OverlayBound marks StagedFindingPostedMsg for generic overlay routing.
// Posting the last card walks advanceCard → enterSummary inside the
// overlay, which returns the vibe-coach goroutine cmd; the root must
// forward that cmd or the summary interstitial hangs.
func (StagedFindingPostedMsg) OverlayBound() {}

// OverlayBound marks ExistingPRCommentsMsg for generic overlay routing.
// The overlay's handler returns a markCardsAlreadyOnGitHub cmd that must
// run for the duplicate-detection pass to complete.
func (ExistingPRCommentsMsg) OverlayBound() {}

// Ensure the data-owned overlay-bound messages satisfy ForwardToOverlay
// at compile time.
var (
	_ ForwardToOverlay = StagedFindingPostedMsg{}
	_ ForwardToOverlay = ExistingPRCommentsMsg{}
)
