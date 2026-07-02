package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thezmc/kubectl-craft/internal/data"
)

// KindSelectedMsg is the typed handoff from the Kind picker to the Session
// shell: Enter on the highlighted row emits it, carrying the selected Kind
// (GVK + group-version path) that the compose view opens on. Until compose
// lands in M3, the shell parks the selection in a minimal placeholder
// state that proves the handoff.
type KindSelectedMsg struct {
	Kind data.Kind
}

// Model is the Session shell: the root Bubble Tea model for one Session.
// It opens on the Kind picker — the browsable Kind list is discovered
// before the shell starts — and a selection transitions it toward
// composing that Kind (the compose view itself lands in M3).
type Model struct {
	// picker is the Kind picker, the shell's opening view.
	picker picker

	// selected is the Kind the Session is composing once the picker
	// hands one off; nil while the picker is still open.
	selected *data.Kind
}

// New builds the Session shell over the cluster's browsable Kind list,
// resolved by discovery before the shell starts.
func New(kinds []data.Kind) Model {
	return Model{picker: newPicker(kinds)}
}

// Init starts the Session shell with no initial command: the live index
// and the browsable Kind list are both resolved before the program
// launches, so there is nothing to await.
func (Model) Init() tea.Cmd {
	return nil
}

// Update applies one message to the Session shell. Keys route to the open
// view — the Kind picker until a selection lands, the compose placeholder
// after — and KindSelectedMsg is the picker's typed handoff transitioning
// the Session into composing the selected Kind.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.picker = m.picker.resize(msg.Height)
		return m, nil
	case KindSelectedMsg:
		selected := msg.Kind
		m.selected = &selected

		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// handleKey routes one key press to the open view.
//
// The compose placeholder keeps M0's exit grammar seed (DESIGN.md —
// Keybindings): the Draft is still always empty — value entry lands with
// compose in M3 — so `q` and `Ctrl-c` quit immediately with no prompt; the
// three-way exit menu arrives once a Draft can be non-empty.
func (m Model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.selected == nil {
		var cmd tea.Cmd
		m.picker, cmd = m.picker.update(key)

		return m, cmd
	}

	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	}

	return m, nil
}

// View renders the open view: the Kind picker, or — once a selection has
// landed — the compose placeholder naming the Kind the compose view will
// open on.
func (m Model) View() string {
	if m.selected == nil {
		return m.picker.view()
	}

	return fmt.Sprintf(
		"composing %s (%s) — the compose view lands in M3\n",
		m.selected.GVK.Kind, m.selected.GVK.GroupVersion(),
	)
}

// Filter returns the Kind picker's active type-to-filter query.
func (m Model) Filter() string {
	return m.picker.filter
}

// MatchedKinds returns the browsable Kinds the active filter narrows to,
// in picker order: each Kind's versions together, the Preferred Version
// row leading them.
func (m Model) MatchedKinds() []data.Kind {
	return m.picker.matches()
}

// HighlightedKind returns the picker row the selection sits on, and false
// when the picker is not open or nothing matches the filter.
func (m Model) HighlightedKind() (data.Kind, bool) {
	if m.selected != nil {
		return data.Kind{}, false
	}

	matches := m.picker.matches()
	if len(matches) == 0 {
		return data.Kind{}, false
	}

	return matches[m.picker.cursor], true
}

// SelectedKind returns the Kind the picker handed off for composing, and
// false while the picker is still open.
func (m Model) SelectedKind() (data.Kind, bool) {
	if m.selected == nil {
		return data.Kind{}, false
	}

	return *m.selected, true
}
