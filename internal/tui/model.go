package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// Model is the Session shell: the root Bubble Tea model for one Session.
// The M0 walking skeleton renders only the connected-cluster line; the
// Kind picker, schema tree, and detail pane land in M2.
type Model struct {
	// groupCount is how many group versions the Session's live
	// /openapi/v3 index serves, resolved before the shell starts.
	groupCount int
}

// New builds the Session shell for a Session whose live index serves
// groupCount API group versions.
func New(groupCount int) Model {
	return Model{groupCount: groupCount}
}

// Init starts the Session shell with no initial command: the live index
// is resolved before the program launches, so there is nothing to await.
func (Model) Init() tea.Cmd {
	return nil
}

// Update applies one message to the Session shell.
//
// The exit grammar seed (DESIGN.md — Keybindings): `q` on an empty Draft
// quits immediately with no prompt — M0 has no Draft yet, so the Draft is
// always empty and `q` always just quits; the three-way exit menu arrives
// with compose in M3. `Ctrl-c` is the conventional escape hatch and also
// quits immediately.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}

	return m, nil
}

// View renders the Session shell: the walking-skeleton proof that the
// Session is connected and its live index is resolved. The count names
// group versions — the live index lists apps/v1 and apps/v1beta1 as two
// entries — and the whole line is replaced by the Kind picker in M2.
func (m Model) View() string {
	return fmt.Sprintf("connected: %d API group versions\n", m.groupCount)
}
