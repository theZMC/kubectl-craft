// Package tui is the presentation layer.
//
// All Bubble Tea code — models, Update loops, widgets, lipgloss
// styling — is isolated here. It renders to /dev/tty so stdout stays
// clean for the Emitted Manifest, and it delegates all compose logic
// to the pure core in internal/schema and all cluster access to
// internal/data.
package tui
