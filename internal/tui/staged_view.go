package tui

// The staged-post mode used to live in this file: a separate full-page screen
// that the user entered after a review finished, walking findings one by one.
//
// The persistent review overlay (review_overlay.go) now hosts the entire
// approval flow — running, per-finding approval, and summary post — without
// closing between phases, so this file no longer needs any code. It is kept
// to avoid dead-link surprises in editor history; future contributors should
// add new approval-flow logic to review_overlay.go.
