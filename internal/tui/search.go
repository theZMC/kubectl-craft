package tui

import (
	"cmp"
	"slices"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

	// lowered memoizes each candidate lowercased as runes — the fuzzy
	// match's working form — so a keystroke's re-rank over 10k+
	// candidates never re-lowers or re-decodes a Field Path it already
	// holds. Built alongside candidates in withCandidates.
	lowered [][]rune

	// runeCounts memoizes each candidate's rune length, the ranking's
	// shorter-Field-Path tiebreak, keyed like candidates.
	runeCounts []int

	// filter is the active type-to-filter query.
	filter string

	// matched memoizes the ranked matches for the active filter: the
	// re-rank over the whole candidate set runs once per filter change
	// (rerank), and every reader — the view's window, the selection, the
	// Session's accessors — shares this one ranking.
	matched []SearchMatch

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
func (s fieldSearch) update(key tea.KeyPressMsg) (fieldSearch, searchOutcome) {
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
func (s fieldSearch) typeIntoFilter(key tea.KeyPressMsg) fieldSearch {
	if key.Text == "" || key.Code == tea.KeySpace || key.Mod.Contains(tea.ModAlt) {
		return s
	}
	s.filter += key.Text
	return s.resetToTop()
}

// paste appends pasted text to the filter as-is, the way v1's rune-key
// paste delivery typed it in.
func (s fieldSearch) paste(content string) fieldSearch {
	s.filter += content
	return s.resetToTop()
}

// withCandidates installs the enumerated candidate set and its memoized
// working forms — lowered runes for matching, rune counts for ranking —
// then ranks the fresh set once under the active filter.
func (s fieldSearch) withCandidates(candidates []string) fieldSearch {
	s.candidates = candidates
	s.lowered = make([][]rune, len(candidates))
	s.runeCounts = make([]int, len(candidates))
	for index, candidate := range candidates {
		s.lowered[index] = []rune(strings.ToLower(candidate))
		s.runeCounts[index] = utf8.RuneCountInString(candidate)
	}
	return s.rerank()
}

// resetToTop re-anchors the selection after the filter changes — the best
// vantage over a new ranking is its first row — and re-ranks the
// candidates once for every reader of the new filter.
func (s fieldSearch) resetToTop() fieldSearch {
	s.cursor = 0
	s.offset = 0
	return s.rerank()
}

// rerank recomputes the memoized ranking for the active filter. Every
// filter change funnels through here (resetToTop) and every candidate-set
// change through withCandidates, so matches() stays a plain read.
func (s fieldSearch) rerank() fieldSearch {
	s.matched = s.computeMatches()
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

// matches is the ranked match list for the active filter: tighter matches
// first (the smallest matched span), shorter Field Paths breaking ties,
// tree order breaking those. An empty filter lists every candidate in tree
// order. The ranking is memoized — rerank recomputes it on every filter or
// candidate change — so the readers a single keystroke fans out to (the
// view, the selection, the Session's accessors) share one re-rank.
func (s fieldSearch) matches() []SearchMatch {
	return s.matched
}

// rankedMatch pairs one match with its precomputed ranking keys, so the
// sort never re-measures a Field Path per comparison.
type rankedMatch struct {
	match   SearchMatch
	span    int
	runeLen int
}

// computeMatches narrows the candidates to the active filter and ranks
// them. The scan works over the memoized lowered runes with one shared
// scratch buffer, so the only per-candidate allocation left is the matched
// positions a surviving match keeps.
func (s fieldSearch) computeMatches() []SearchMatch {
	if s.filter == "" {
		all := make([]SearchMatch, 0, len(s.candidates))
		for _, candidate := range s.candidates {
			all = append(all, SearchMatch{FieldPath: candidate})
		}
		return all
	}

	wanted := []rune(strings.ToLower(s.filter))
	scratch := make([]int, 0, len(wanted))
	var ranked []rankedMatch
	for index, candidate := range s.candidates {
		positions, ok := fuzzySubsequence(wanted, s.lowered[index], scratch)
		if !ok {
			continue
		}
		match := SearchMatch{FieldPath: candidate, Matched: slices.Clone(positions)}
		ranked = append(ranked, rankedMatch{
			match:   match,
			span:    matchSpan(match),
			runeLen: s.runeCounts[index],
		})
	}
	slices.SortStableFunc(ranked, compareRanked)

	matched := make([]SearchMatch, len(ranked))
	for index, entry := range ranked {
		matched[index] = entry.match
	}
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

// compareRanked ranks two matches: the tighter span wins, then the shorter
// Field Path; a full tie keeps tree order (the sort is stable).
func compareRanked(a, b rankedMatch) int {
	return cmp.Or(
		cmp.Compare(a.span, b.span),
		cmp.Compare(a.runeLen, b.runeLen),
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

// fuzzySubsequence is the fzf-style subsequence match with positions:
// every filter rune appears in the candidate in order, case-insensitively —
// both sides arrive pre-lowered as runes. The forward scan proves the
// match; the backward pass then pulls each matched rune as far right as its
// successor allows, tightening the span so ranking and highlighting reflect
// the closest grouping the match admits at its earliest end. The positions
// land in the caller's scratch buffer — cloned only for a match that
// survives — so a 10k-candidate re-rank costs no allocation per miss.
func fuzzySubsequence(wanted, runes []rune, scratch []int) ([]int, bool) {
	if len(wanted) == 0 {
		return nil, true
	}

	positions := scratch[:0]
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
// One frame renders each distinct matched rune through the Structure
// emphasis exactly once — the token resolves once before the loop, and the
// styled map memoizes each rune's styled form across every row, because
// lipgloss's per-Render cost across thousands of unbounded rows is exactly
// what a keystroke's budget cannot afford.
func (s fieldSearch) view(th theme) string {
	lines := []string{"search " + s.scope.label() + " > " + s.filter}

	matched := s.matches()
	if len(matched) == 0 {
		lines = append(lines, th.Meta().Render("no Field Paths match"))
		return strings.Join(lines, "\n")
	}

	structure := th.Structure()
	styled := make(map[rune]string)
	visible := s.visibleRows(len(matched))
	for index := s.offset; index < min(s.offset+visible, len(matched)); index++ {
		lines = append(lines, s.renderMatch(matched[index], index == s.cursor, structure, styled))
	}
	return strings.Join(lines, "\n")
}

// renderMatch renders one overlay row: the selection cursor and the Field
// Path with the filter's matched runes highlighted.
func (fieldSearch) renderMatch(match SearchMatch, selected bool, structure lipgloss.Style, styled map[rune]string) string {
	cursor := "  "
	if selected {
		cursor = "> "
	}
	return cursor + highlightMatched(match, structure, styled)
}

// highlightMatched renders a match's Field Path with its matched runes in
// the Structure emphasis — the "why did this row match" cue. The styled map
// memoizes each rune's styled form for the frame, and the scan walks the
// Field Path's runes in place rather than decoding them into a slice. The
// map assumes one style per matched rune per frame — view resolves the
// Structure token once and every row renders through that one style — so a
// styling layer that varies the highlight per row or per position must
// re-key or drop it.
func highlightMatched(match SearchMatch, structure lipgloss.Style, styled map[rune]string) string {
	if len(match.Matched) == 0 {
		return match.FieldPath
	}

	var view strings.Builder
	next := 0
	index := 0
	for _, r := range match.FieldPath {
		if next < len(match.Matched) && match.Matched[next] == index {
			rendered, ok := styled[r]
			if !ok {
				rendered = structure.Render(string(r))
				styled[r] = rendered
			}
			view.WriteString(rendered)
			next++
		} else {
			view.WriteRune(r)
		}
		index++
	}
	return view.String()
}

// ensureCandidates enumerates the open Kind's schema-level Field Paths on
// first use — the candidate set is the Kind's whole Type Schema, memoized
// per compose view — and installs them through withCandidates, the only
// door to the search's candidate set: a raw write would leave the memoized
// working forms (lowered runes, rune counts, the ranking) behind. Every
// consumer funnels through here — the `/` overlay's first open, a deep
// link's existence check, and the version switch's landing.
func (c compose) ensureCandidates() compose {
	if c.search.candidates == nil {
		c.search = c.search.withCandidates(c.root.node.FieldPaths())
	}
	return c
}

// landDeepLink applies the launch arg's Field Path over a freshly opened
// compose view: a path the Type Schema defines lands under the search
// overlay's landing rule — ancestors expanded, array/map crossings landing
// per the rule — and a path it doesn't define leaves the view at the root
// with a non-fatal notice, keeping the Kind browsable either way. The
// existence check runs over the same candidate set the `/` search
// enumerates, which stays memoized for the overlay's first open.
func (c compose) landDeepLink(fieldPath string) compose {
	c = c.ensureCandidates()

	if !slices.Contains(c.search.candidates, fieldPath) {
		c.notice = "no Field Path " + fieldPath + " in " + kindDisplayName(c.kind) +
			"'s Type Schema — opened at the root"
		return c
	}

	c.landOn(fieldPath)
	return c
}

// landOn is the field search's landing rule (DESIGN.md — Flow §5): expand
// every ancestor along the match's schema-level Field Path and move the
// focus to the match. When the path crosses an array or a map, the jump
// goes into the first instantiated item — and when the Draft holds none, it
// lands on the collection node itself, where `a` adds one.
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
// instantiated item of an array row, or the first instantiated key's entry
// of a map row — the rows loadRow grows from the Draft's ItemCount and Keys
// (items in index order, keys sorted). Nil when the Draft has instantiated
// nothing at the collection: that crossing lands on the collection node
// itself, where `a` adds the first entry.
func (c *compose) firstInstantiatedItem(row *treeRow) *treeRow {
	if !c.loadRow(row) {
		return nil
	}
	for _, child := range row.children {
		if child.kind == rowItem || child.kind == rowKey {
			return child
		}
	}
	return nil
}
