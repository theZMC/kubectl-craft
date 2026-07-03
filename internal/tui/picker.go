package tui

import (
	"cmp"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/thezmc/kubectl-craft/internal/data"
)

var (
	// dimmedStyle renders row metadata — group/version and short names —
	// dimmed, so the kind name stays the row's visual anchor (DESIGN.md —
	// Flow §2: group/version as dimmed row metadata).
	dimmedStyle = lipgloss.NewStyle().Faint(true)

	// highlightedStyle marks the kind name on the row the selection sits on.
	highlightedStyle = lipgloss.NewStyle().Bold(true)
)

// picker is the Kind picker: the fuzzy-filterable flat list of every
// browsable Kind on the cluster (DESIGN.md — Flow §2). It is a dedicated
// search surface, so its keys follow the type-to-filter grammar
// (DESIGN.md — Keybindings): printable keys filter immediately, `↑/↓` and
// `Ctrl-j/k` move the selection, `Enter` selects, `Esc`
// clears-then-dismisses.
type picker struct {
	// rows is every browsable Kind in picker order: one row per
	// Kind+version, grouped by (group, kind) with the Preferred Version
	// row leading its Kind's versions so the default representative is
	// the one the selection reaches first — all versions stay listed.
	rows []data.Kind

	// filter is the active type-to-filter query.
	filter string

	// cursor is the highlighted row's index into matches().
	cursor int

	// offset is the first visible row's index into matches(): the
	// viewport scrolls so the highlighted row stays visible.
	offset int

	// height is the terminal height from the last tea.WindowSizeMsg;
	// zero until the first one arrives, which view() treats as
	// unbounded.
	height int
}

// newPicker shapes the discovered Kind list into picker rows.
func newPicker(kinds []data.Kind) picker {
	rows := slices.Clone(kinds)
	slices.SortStableFunc(rows, comparePickerRows)

	return picker{rows: rows}
}

// comparePickerRows orders rows by (group, kind), each Kind's versions
// together with its Preferred Version first: when a Kind serves multiple
// versions, the default representative is the row the selection lands on
// before its siblings.
func comparePickerRows(a, b data.Kind) int {
	return cmp.Or(
		strings.Compare(a.GVK.Group, b.GVK.Group),
		strings.Compare(a.GVK.Kind, b.GVK.Kind),
		preferredFirst(a, b),
		strings.Compare(a.GVK.Version, b.GVK.Version),
	)
}

// preferredFirst sorts a Kind's Preferred Version row before its other
// version rows.
func preferredFirst(a, b data.Kind) int {
	switch {
	case a.Preferred == b.Preferred:
		return 0
	case a.Preferred:
		return -1
	default:
		return 1
	}
}

// update applies one key press under the type-to-filter grammar. Every key
// outside the grammar's verbs types into the filter — a search surface has
// no command letters, so `q` narrows to Quota instead of quitting.
func (p picker) update(key tea.KeyPressMsg) (picker, tea.Cmd) {
	switch key.String() {
	case "ctrl+c":
		// The conventional escape hatch quits immediately: an empty
		// Session has no Draft, so there is nothing to confirm.
		return p, tea.Quit
	case "esc":
		return p.dismiss()
	case "enter":
		return p.selectHighlighted()
	case "up", "ctrl+k":
		return p.move(-1), nil
	case "down", "ctrl+j":
		return p.move(1), nil
	case "backspace":
		return p.eraseFilterRune(), nil
	default:
		return p.typeIntoFilter(key), nil
	}
}

// dismiss is Esc's clear-then-dismiss order: an active filter clears back
// to the full Kind list; Esc on an empty filter quits the Session — an
// empty Session has no Draft, so no confirmation.
func (p picker) dismiss() (picker, tea.Cmd) {
	if p.filter == "" {
		return p, tea.Quit
	}

	p.filter = ""

	return p.resetToTop(), nil
}

// selectHighlighted hands the highlighted Kind off to the Session shell as
// a typed message; with nothing to highlight, Enter is a no-op.
func (p picker) selectHighlighted() (picker, tea.Cmd) {
	matches := p.matches()
	if len(matches) == 0 {
		return p, nil
	}

	selected := matches[p.cursor]

	return p, func() tea.Msg { return KindSelectedMsg{Kind: selected} }
}

// move slides the selection by delta, clamping at the list's edges.
func (p picker) move(delta int) picker {
	count := len(p.matches())
	if count == 0 {
		return p
	}

	p.cursor = min(max(p.cursor+delta, 0), count-1)

	return p.follow()
}

// eraseFilterRune deletes the filter's last rune, re-widening the match
// list one keystroke at a time.
func (p picker) eraseFilterRune() picker {
	if p.filter == "" {
		return p
	}

	runes := []rune(p.filter)
	p.filter = string(runes[:len(runes)-1])

	return p.resetToTop()
}

// typeIntoFilter appends printable keys to the filter — the fzf-style
// grammar: typing narrows immediately, no edit mode to enter first.
func (p picker) typeIntoFilter(key tea.KeyPressMsg) picker {
	if key.Text == "" || key.Code == tea.KeySpace || key.Mod.Contains(tea.ModAlt) {
		return p
	}

	p.filter += key.Text

	return p.resetToTop()
}

// paste appends pasted text to the filter as-is, the way v1's rune-key
// paste delivery typed it in.
func (p picker) paste(content string) picker {
	p.filter += content

	return p.resetToTop()
}

// resetToTop re-anchors the selection after the filter changes: the best
// vantage over a new match list is its first row.
func (p picker) resetToTop() picker {
	p.cursor = 0
	p.offset = 0

	return p
}

// resize records the terminal height from a tea.WindowSizeMsg and keeps
// the highlighted row visible in the resized viewport.
func (p picker) resize(height int) picker {
	p.height = height

	return p.follow()
}

// follow scrolls the viewport so the highlighted row stays visible.
func (p picker) follow() picker {
	count := len(p.matches())
	visible := p.visibleRows(count)

	if visible <= 0 {
		p.offset = min(p.offset, max(count-1, 0))
		return p
	}

	p.offset = min(p.offset, max(count-visible, 0))
	if p.cursor < p.offset {
		p.offset = p.cursor
	}

	if p.cursor >= p.offset+visible {
		p.offset = p.cursor - visible + 1
	}

	return p
}

// visibleRows is how many list rows fit below the filter prompt; before
// the first tea.WindowSizeMsg the viewport is unbounded.
func (p picker) visibleRows(rowCount int) int {
	if p.height <= 0 {
		return rowCount
	}

	return max(p.height-1, 0)
}

// matches narrows the rows to the active filter: a Kind stays browsable
// when its kind name or any of its short names fuzzy-matches the query.
func (p picker) matches() []data.Kind {
	if p.filter == "" {
		return p.rows
	}

	var matched []data.Kind

	for _, kind := range p.rows {
		if kindMatches(kind, p.filter) {
			matched = append(matched, kind)
		}
	}

	return matched
}

// kindMatches reports whether one Kind matches the type-to-filter query on
// its kind name or any short name — `deploy` and `Deployment` both reach
// apps/v1 Deployment, mirroring how the deep-link arg resolves.
func kindMatches(kind data.Kind, filter string) bool {
	if fuzzyMatches(filter, kind.GVK.Kind) {
		return true
	}

	return slices.ContainsFunc(kind.ShortNames, func(short string) bool {
		return fuzzyMatches(filter, short)
	})
}

// fuzzyMatches is the fzf-style subsequence match: every filter rune
// appears in the candidate in order, case-insensitively.
func fuzzyMatches(filter, candidate string) bool {
	wanted := []rune(strings.ToLower(filter))
	found := 0

	for _, r := range strings.ToLower(candidate) {
		if found < len(wanted) && r == wanted[found] {
			found++
		}
	}

	return found == len(wanted)
}

// view renders the filter prompt and the visible window of matching rows.
func (p picker) view() string {
	var view strings.Builder

	view.WriteString("> " + p.filter + "\n")

	matches := p.matches()
	visible := p.visibleRows(max(len(matches), 1))

	if visible == 0 {
		return view.String()
	}

	if len(matches) == 0 {
		view.WriteString(dimmedStyle.Render("no Kinds match") + "\n")
		return view.String()
	}

	for i := p.offset; i < min(p.offset+visible, len(matches)); i++ {
		view.WriteString(renderRow(matches[i], i == p.cursor))
	}

	return view.String()
}

// renderRow renders one Kind row: the kind name anchors the row, with the
// group/version and short names dimmed alongside it.
func renderRow(kind data.Kind, highlighted bool) string {
	indicator := "  "
	name := kind.GVK.Kind

	if highlighted {
		indicator = "> "
		name = highlightedStyle.Render(name)
	}

	meta := kind.GVK.GroupVersion().String()
	if len(kind.ShortNames) > 0 {
		meta += " (" + strings.Join(kind.ShortNames, ", ") + ")"
	}

	return indicator + name + "  " + dimmedStyle.Render(meta) + "\n"
}
