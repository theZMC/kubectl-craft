package tui

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// Run launches the Session shell as an alt-screen Bubble Tea program on
// the controlling terminal and blocks until the Session ends.
//
// The clean-stdout contract (DESIGN.md — Output): the TUI renders to
// /dev/tty, never stdout — stdout carries nothing but the Emitted
// Manifest, so `kubectl craft > x.yaml` still displays the TUI and leaves
// the file untouched. Input is read from the same terminal, keeping stdin
// free for the same reason.
//
// Without a controlling terminal (for example, a non-interactive CI job),
// opening /dev/tty fails and Run returns before any program starts; the
// caller surfaces that on stderr as a non-zero exit.
func Run(ctx context.Context, groupCount int) error {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf(
			"kubectl craft is interactive and needs a controlling terminal: %w", err,
		)
	}
	defer func() { _ = tty.Close() }()

	program := tea.NewProgram(
		New(groupCount),
		tea.WithContext(ctx),
		tea.WithAltScreen(),
		tea.WithInput(tty),
		tea.WithOutput(tty),
	)
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("running the Session shell: %w", err)
	}

	return nil
}
