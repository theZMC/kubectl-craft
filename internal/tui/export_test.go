package tui

import (
	"os"
	"os/exec"

	tea "charm.land/bubbletea/v2"
)

// SwapExecEditor replaces the $EDITOR pop-out's Bubble Tea terminal handoff
// with a fake for specs — typically one that runs the editor command
// synchronously, so the whole round trip drives through Update, state-first.
// The returned restore puts the real handoff back.
func SwapExecEditor(fake func(*exec.Cmd, tea.ExecCallback) tea.Cmd) (restore func()) {
	previous := execEditor
	execEditor = fake
	return func() { execEditor = previous }
}

// SwapOpenTTY replaces Run's controlling-terminal open with a fake for
// specs — the no-TTY hard-fail cannot be reached hermetically from a spec
// process that has one. The returned restore puts the real open back.
func SwapOpenTTY(fake func() (*os.File, error)) (restore func()) {
	previous := openTTY
	openTTY = fake
	return func() { openTTY = previous }
}

// EmitFailureNotice spells the exit ramp's non-fatal emission-failure notice
// for specs: the fixture corpus cannot make a real Draft.Emit fail, so the
// load-bearing wording pins through this seam instead.
func EmitFailureNotice(err error) string {
	return emitFailureNotice(err)
}
