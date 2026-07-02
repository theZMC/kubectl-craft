// Package schema is the pure compose core.
//
// Type Schema trees, the Draft model, contextual requiredness and
// completeness, version carry-over, and Manifest emission live here,
// unit-tested hermetically against the fixture corpus.
//
// This package must stay pure: no Bubble Tea imports — nothing under
// github.com/charmbracelet may be imported here, directly or via a
// helper. TUI concerns belong in internal/tui; cluster access belongs
// in internal/data.
package schema
