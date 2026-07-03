package tui

import (
	"context"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/thezmc/kubectl-craft/internal/data"
)

// openTTY opens the controlling terminal the Session shell renders on and
// reads from. It is a seam only so specs can pin the no-TTY failure — a
// spec process has a controlling terminal, so the real open cannot be made
// to fail hermetically.
var openTTY = func() (*os.File, error) {
	return os.OpenFile("/dev/tty", os.O_RDWR, 0)
}

// Result is what one Session leaves behind for the caller: the emit
// decision, and — only on an emit ramp — the Emitted Manifest's bytes. The
// caller writes them to stdout after the alt screen closes (DESIGN.md —
// Output): the TUI itself never touches stdout, so `kubectl craft > x.yaml`
// captures exactly the Manifest and nothing else. A discard ramp leaves the
// zero Result, and every ramp exits zero.
type Result struct {
	// Emitted reports whether the Session ended on an emit ramp — Ctrl-d,
	// or the exit menu's Emit & quit.
	Emitted bool

	// Manifest is the Emitted Manifest's bytes, pure emission from the
	// Draft; nil when the Session discarded instead.
	Manifest []byte
}

// Run launches the Session shell as an alt-screen Bubble Tea program on
// the controlling terminal and blocks until the Session ends, returning
// the Session's Result. The shell opens on the Kind picker over the
// discovered Kind list — or, when the launch arg deep-linked a Kind,
// directly on that Kind's compose view; the Fetcher and the live
// /openapi/v3 index feed the compose view's lazy group-document fetches
// either way.
//
// The clean-stdout contract (DESIGN.md — Output): the TUI renders to
// /dev/tty, never stdout — stdout carries nothing but the Emitted
// Manifest, written by the caller after Run returns and the alt screen has
// closed, so `kubectl craft > x.yaml` still displays the TUI and the file
// receives exactly the Manifest. Input is read from the same terminal,
// keeping stdin free for the same reason.
//
// Without a controlling terminal (for example, a non-interactive CI job),
// opening /dev/tty fails and Run returns before any program starts; the
// caller surfaces that on stderr as a non-zero exit.
//
// The Validator and the Session's default namespace arrive through the
// same seam the Fetcher does — resolved by the command before the alt
// screen opens — and feed the manual `v` Validate: the dry-run POST goes
// through the Validator, and the namespace resolves from the Draft's
// metadata.namespace, else the Session default (data.ResolveNamespace).
func Run(
	ctx context.Context,
	kinds []data.Kind,
	fetcher data.Fetcher,
	index []data.GroupVersion,
	validator data.Validator,
	defaultNamespace string,
	link *DeepLink,
) (Result, error) {
	tty, err := openTTY()
	if err != nil {
		return Result{}, fmt.Errorf(
			"kubectl craft is interactive and needs a controlling terminal: %w", err,
		)
	}
	defer func() { _ = tty.Close() }()

	// The alt screen itself is declared by the shell's View — Bubble Tea
	// v2's declarative replacement for the WithAltScreen program option.
	program := tea.NewProgram(
		New(ctx, kinds, fetcher, index, validator, defaultNamespace, link),
		tea.WithContext(ctx),
		tea.WithInput(tty),
		tea.WithOutput(tty),
	)
	final, err := program.Run()
	if err != nil {
		return Result{}, fmt.Errorf("running the Session shell: %w", err)
	}

	shell, isShell := final.(Model)
	if !isShell {
		return Result{}, nil
	}
	manifest, emitted := shell.EmittedManifest()
	return Result{Emitted: emitted, Manifest: manifest}, nil
}
