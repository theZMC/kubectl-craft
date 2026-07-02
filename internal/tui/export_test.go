package tui

import (
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
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
