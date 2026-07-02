// Package validate is the pure Validate core.
//
// When a server-side dry-run fails, the API server answers with a
// metav1.Status whose details.causes carry field paths in the server's
// error-cause spelling. This package maps that Status into typed findings
// (DESIGN.md — Output: Validate feedback maps to the tree): Field Path
// findings whose paths are normalized to the Draft-level bracket grammar the
// TUI already speaks, unmappable findings for the validation results pane,
// and the Status-level summary.
//
// This package must stay pure: no Bubble Tea imports — nothing under
// github.com/charmbracelet may be imported here, directly or via a helper.
// TUI concerns belong in internal/tui; cluster access belongs in
// internal/data — Validate never sees the wire, only the Status it carried.
package validate
