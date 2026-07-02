package tui

import (
	"cmp"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// searchHints is the `/` field-search overlay's own hint line: the overlay
// is a dedicated search surface, so its keys follow the type-to-filter
// grammar (DESIGN.md — Keybindings).
const searchHints = "type to filter · ↑/↓ move · enter jump · esc clear/dismiss"

// searchScope names one scope of the `/` field-search overlay. M2 ships the
// single fixed SCHEMA scope; the DRAFT scope is the later-phase second
// scope inside this same overlay — Tab flips SCHEMA ⇄ DRAFT there
// (DESIGN.md — Flow §5) — and slots into this seam without UI rework.
type searchScope int

// scopeSchema searches the open Kind's schema-level Field Paths.
const scopeSchema searchScope = iota

// label names the scope in the overlay's prompt line.
func (searchScope) label() string { return "SCHEMA" }

// SearchMatch is one row of the `/` field-search overlay: a candidate
// schema-level Field Path and the rune indices the active filter matched —
// the overlay highlights exactly those runes.
type SearchMatch struct {
	FieldPath string
	Matched   []int
}

// searchOutcome is what one key press did to the search overlay: kept it
// open, dismissed it back to navigate mode, or selected the highlighted
// match.
type searchOutcome int

const (
	searchContinues searchOutcome = iota
	searchDismissed
	searchSelected
)

// fieldSearch is the `/` field-search overlay (DESIGN.md — Flow §5): a
// dedicated type-to-filter search surface over the open Kind's Field Paths.
type fieldSearch struct {
	// scope is the searched scope, fixed to SCHEMA in M2; the DRAFT
	// scope's Tab toggle lands with the Draft.
	scope searchScope

	// candidates is the open Kind's schema-level Field Paths in tree
	// order, enumerated once per compose view on the overlay's first open.
	candidates []string

	// filter is the active type-to-filter query.
	filter string

	// cursor is the highlighted row's index into matches(); offset
	// scrolls the match list so the highlighted row stays visible.
	cursor int
	offset int

	// height is how many match rows fit under the overlay's prompt line;
	// zero means unbounded (no tea.WindowSizeMsg yet).
	height int
}

// update applies one key press under the type-to-filter grammar: printable
// keys filter, ↑/↓ and Ctrl-j/k move, Enter selects, Esc clears-then-
// dismisses. Every other printable key types into the filter — a search
// surface has no command letters, so `q` narrows instead of quitting.
func (s fieldSearch) update(key tea.KeyMsg) (fieldSearch, searchOutcome) {
	switch key.String() {
	case "esc":
		if s.filter != "" {
			s.filter = ""
			return s.resetToTop(), searchContinues
		}
		return s, searchDismissed
	case "enter":
		if _, ok := s.highlighted(); !ok {
			return s, searchContinues
		}
		return s, searchSelected
	case "up", "ctrl+k":
		return s.move(-1), searchContinues
	case "down", "ctrl+j":
		return s.move(1), searchContinues
	case "backspace":
		return s.eraseFilterRune(), searchContinues
	default:
		return s.typeIntoFilter(key), searchContinues
	}
}

// move slides the selection by delta, clamping at the match list's edges.
func (s fieldSearch) move(delta int) fieldSearch {
	count := len(s.matches())
	if count == 0 {
		return s
	}
	s.cursor = min(max(s.cursor+delta, 0), count-1)
	return s.follow()
}

// eraseFilterRune deletes the filter's last rune, re-widening the match
// list one keystroke at a time.
func (s fieldSearch) eraseFilterRune() fieldSearch {
	if s.filter == "" {
		return s
	}
	runes := []rune(s.filter)
	s.filter = string(runes[:len(runes)-1])
	return s.resetToTop()
}

// typeIntoFilter appends printable keys to the filter — the fzf-style
// grammar: typing narrows immediately, no edit mode to enter first.
func (s fieldSearch) typeIntoFilter(key tea.KeyMsg) fieldSearch {
	if key.Type != tea.KeyRunes || key.Alt {
		return s
	}
	s.filter += string(key.Runes)
	return s.resetToTop()
}

// resetToTop re-anchors the selection after the filter changes: the best
// vantage over a new ranking is its first row.
func (s fieldSearch) resetToTop() fieldSearch {
	s.cursor = 0
	s.offset = 0
	return s
}

// reset clears the overlay for its next open, keeping the enumerated
// candidates — selecting a match completes that search.
func (s fieldSearch) reset() fieldSearch {
	s.filter = ""
	return s.resetToTop()
}

// follow scrolls the overlay's viewport so the highlighted row stays
// visible.
func (s fieldSearch) follow() fieldSearch {
	count := len(s.matches())
	if count == 0 {
		s.cursor, s.offset = 0, 0
		return s
	}
	s.cursor = min(max(s.cursor, 0), count-1)

	visible := s.visibleRows(count)
	s.offset = min(s.offset, max(count-visible, 0))
	if s.cursor < s.offset {
		s.offset = s.cursor
	}
	if s.cursor >= s.offset+visible {
		s.offset = s.cursor - visible + 1
	}
	return s
}

// visibleRows is how many match rows the viewport shows; before the first
// tea.WindowSizeMsg it is unbounded.
func (s fieldSearch) visibleRows(count int) int {
	if s.height <= 0 {
		return count
	}
	return s.height
}

// matches narrows the candidates to the active filter and ranks them:
// tighter matches first (the smallest matched span), shorter Field Paths
// breaking ties, tree order breaking those. An empty filter lists every
// candidate in tree order.
func (s fieldSearch) matches() []SearchMatch {
	if s.filter == "" {
		all := make([]SearchMatch, 0, len(s.candidates))
		for _, candidate := range s.candidates {
			all = append(all, SearchMatch{FieldPath: candidate})
		}
		return all
	}

	var matched []SearchMatch
	for _, candidate := range s.candidates {
		if positions, ok := fuzzyMatchPositions(s.filter, candidate); ok {
			matched = append(matched, SearchMatch{FieldPath: candidate, Matched: positions})
		}
	}
	slices.SortStableFunc(matched, compareMatches)
	return matched
}

// highlighted is the match the selection sits on.
func (s fieldSearch) highlighted() (SearchMatch, bool) {
	matched := s.matches()
	if len(matched) == 0 {
		return SearchMatch{}, false
	}
	return matched[min(s.cursor, len(matched)-1)], true
}

// compareMatches ranks two matches: the tighter span wins, then the shorter
// Field Path; a full tie keeps tree order (the sort is stable).
func compareMatches(a, b SearchMatch) int {
	return cmp.Or(
		cmp.Compare(matchSpan(a), matchSpan(b)),
		cmp.Compare(len([]rune(a.FieldPath)), len([]rune(b.FieldPath))),
	)
}

// matchSpan is how many runes the match stretches across its Field Path —
// the ranking prefers filters found close together.
func matchSpan(match SearchMatch) int {
	if len(match.Matched) == 0 {
		return 0
	}
	return match.Matched[len(match.Matched)-1] - match.Matched[0] + 1
}

// fuzzyMatchPositions is the fzf-style subsequence match with positions:
// every filter rune appears in the candidate in order, case-insensitively.
// The forward scan proves the match; the backward pass then pulls each
// matched rune as far right as its successor allows, tightening the span so
// ranking and highlighting reflect the closest grouping the match admits at
// its earliest end.
func fuzzyMatchPositions(filter, candidate string) ([]int, bool) {
	wanted := []rune(strings.ToLower(filter))
	if len(wanted) == 0 {
		return nil, true
	}
	runes := []rune(strings.ToLower(candidate))

	positions := make([]int, 0, len(wanted))
	for index, r := range runes {
		if len(positions) < len(wanted) && r == wanted[len(positions)] {
			positions = append(positions, index)
		}
	}
	if len(positions) < len(wanted) {
		return nil, false
	}

	for i := len(positions) - 2; i >= 0; i-- {
		next := positions[i+1] - 1
		for runes[next] != wanted[i] {
			next--
		}
		positions[i] = next
	}
	return positions, true
}

// view renders the overlay: the scope-labelled filter prompt and the
// visible window of ranked matches, the filter's runes highlighted on each.
func (s fieldSearch) view() string {
	lines := []string{"search " + s.scope.label() + " > " + s.filter}

	matched := s.matches()
	if len(matched) == 0 {
		lines = append(lines, dimmedStyle.Render("no Field Paths match"))
		return strings.Join(lines, "\n")
	}

	visible := s.visibleRows(len(matched))
	for index := s.offset; index < min(s.offset+visible, len(matched)); index++ {
		lines = append(lines, s.renderMatch(matched[index], index == s.cursor))
	}
	return strings.Join(lines, "\n")
}

// renderMatch renders one overlay row: the selection cursor and the Field
// Path with the filter's matched runes highlighted.
func (fieldSearch) renderMatch(match SearchMatch, selected bool) string {
	cursor := "  "
	if selected {
		cursor = "> "
	}
	return cursor + highlightMatched(match)
}

// highlightMatched renders a match's Field Path with its matched runes in
// the highlight style — the "why did this row match" cue.
func highlightMatched(match SearchMatch) string {
	if len(match.Matched) == 0 {
		return match.FieldPath
	}

	var view strings.Builder
	next := 0
	for index, r := range []rune(match.FieldPath) {
		if next < len(match.Matched) && match.Matched[next] == index {
			view.WriteString(highlightedStyle.Render(string(r)))
			next++
			continue
		}
		view.WriteRune(r)
	}
	return view.String()
}

// landOn is the field search's landing rule (DESIGN.md — Flow §5): expand
// every ancestor along the match's schema-level Field Path and move the
// focus to the match. When the path crosses an array or a map, the jump
// goes into the first instantiated item — and when none exist, it lands on
// the collection node itself (where `a` adds one, come M3). M2 composes no
// Draft, so nothing is ever instantiated and every crossing lands on the
// collection node; the branch stays real because M3's Draft reuses this
// rule unchanged.
func (c *compose) landOn(fieldPath string) {
	target := c.root
	for _, segment := range strings.Split(fieldPath, ".") {
		next := c.stepToward(target, segment)
		if next == nil {
			break
		}
		target = next
	}
	c.rebuildRows()
	c.focusRow(target)
}

// stepToward resolves one dotted segment beneath a row, expanding the row —
// it becomes an ancestor of the landing — whenever the walk continues. A
// segment not among the row's schema-defined fields continues through the
// row's array/map structure, so the path crosses a collection: the walk
// descends into the first instantiated item when one exists and stops on
// the collection node otherwise. It also stops on a row whose children
// cannot load (a dangling $ref): the closest reachable row is the landing.
func (c *compose) stepToward(row *treeRow, segment string) *treeRow {
	if !c.loadRow(row) {
		return nil
	}
	for _, child := range row.children {
		if child.node.FieldPath() != row.node.FieldPath() && child.label == segment {
			c.expandRow(row)
			return child
		}
	}
	if item := c.firstInstantiatedItem(row); item != nil {
		c.expandRow(row)
		return c.stepToward(item, segment)
	}
	return nil
}

// firstInstantiatedItem is the landing rule's Draft-side branch: the first
// instantiated item of an array row, or the first instantiated key's value
// of a map row. M2 composes no Draft, so nothing is ever instantiated and
// every crossing lands on the collection node; M3's Draft gives this a real
// answer.
func (*compose) firstInstantiatedItem(*treeRow) *treeRow {
	return nil
}
