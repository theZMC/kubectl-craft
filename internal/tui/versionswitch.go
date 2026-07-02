package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/internal/schema"
)

// versionListHints is the hint bar while `V`'s served-version list is open —
// a dedicated search surface, so its keys follow the type-to-filter grammar
// (DESIGN.md — Keybindings). Esc clears an active filter before dismissing,
// exactly like the `/` search overlay, so the hint spells the same order.
const versionListHints = "type to filter · ↑/↓ move · enter switch · esc clear/dismiss"

// switchTransitHints is the hint bar while a version switch's group document
// fetches: cancelling returns to composing — the Draft is never lost to an
// abandoned switch.
const switchTransitHints = "esc/q cancel the version switch · ctrl+c quit"

// versionSwitchRequestedMsg asks the Session shell to switch the open Kind
// to another served version: the target version's group document travels the
// existing lazy fetch path — same group document, likely memoized — and the
// carry-over runs once it lands.
type versionSwitchRequestedMsg struct {
	kind data.Kind
}

// versionSwitchAcceptedMsg confirms the pending switch: Enter on the drop
// report accepts losing the reported Field Paths, and the shell commits the
// prepared switch.
type versionSwitchAcceptedMsg struct{}

// pendingSwitch is a prepared version switch: the target version, the field
// tree grown from its group document, the Draft the carry-over kept, and the
// drop report. With nothing dropped it commits immediately; otherwise the
// drop confirm renders the report first (DESIGN.md — Compose lifecycle).
type pendingSwitch struct {
	target  data.Kind
	root    *schema.Node
	carried *schema.Draft
	drops   []schema.Drop
}

// view renders the drop report as the body overlay: every dropped
// Draft-level Field Path on its own line with its reason — "what would I
// lose" before the switch happens.
func (s pendingSwitch) view() string {
	lines := []string{
		"switching to " + kindDisplayName(s.target) + " would drop these Draft-level Field Paths:",
		"",
	}
	for _, drop := range s.drops {
		lines = append(lines, "  "+drop.Path+" — "+drop.Reason)
	}
	return strings.Join(lines, "\n")
}

// prompt is the drop confirm's footer line, in the modal grammar: Enter
// confirms the switch, Esc keeps composing at the current version untouched.
func (s pendingSwitch) prompt() string {
	noun := "Field Paths"
	if len(s.drops) == 1 {
		noun = "Field Path"
	}
	return fmt.Sprintf("drop %d %s and switch to %s? enter switch · esc keep composing",
		len(s.drops), noun, kindDisplayName(s.target))
}

// versionOutcome is what one key press did to the version list: kept it
// open, dismissed it back to navigate mode, or selected the highlighted
// version.
type versionOutcome int

const (
	versionContinues versionOutcome = iota
	versionDismissed
	versionSelected
)

// versionList is `V`'s served-version list (DESIGN.md — Command map: `V`
// version switch): a type-to-filter surface over the open Kind's other
// served versions, from the discovery data already on the shell.
type versionList struct {
	// rows are the other served versions, in picker order — the Preferred
	// Version leads when it is not the open one.
	rows []data.Kind

	// filter is the active type-to-filter query.
	filter string

	// cursor is the highlighted row's index into matches(); offset scrolls
	// the list so the highlighted row stays visible.
	cursor int
	offset int

	// height is how many rows fit under the list's prompt line; zero means
	// unbounded (no tea.WindowSizeMsg yet).
	height int
}

// update applies one key press under the type-to-filter grammar: printable
// keys filter, ↑/↓ and Ctrl-j/k move, Enter selects, Esc clears-then-
// dismisses — a search surface has no command letters.
func (v versionList) update(key tea.KeyMsg) (versionList, versionOutcome) {
	switch key.String() {
	case "esc":
		if v.filter != "" {
			v.filter = ""
			return v.resetToTop(), versionContinues
		}
		return v, versionDismissed
	case "enter":
		if _, ok := v.highlighted(); !ok {
			return v, versionContinues
		}
		return v, versionSelected
	case "up", "ctrl+k":
		return v.move(-1), versionContinues
	case "down", "ctrl+j":
		return v.move(1), versionContinues
	case "backspace":
		return v.eraseFilterRune(), versionContinues
	default:
		return v.typeIntoFilter(key), versionContinues
	}
}

// move slides the selection by delta, clamping at the list's edges.
func (v versionList) move(delta int) versionList {
	count := len(v.matches())
	if count == 0 {
		return v
	}
	v.cursor = min(max(v.cursor+delta, 0), count-1)
	return v.follow()
}

// eraseFilterRune deletes the filter's last rune, re-widening the list one
// keystroke at a time.
func (v versionList) eraseFilterRune() versionList {
	if v.filter == "" {
		return v
	}
	runes := []rune(v.filter)
	v.filter = string(runes[:len(runes)-1])
	return v.resetToTop()
}

// typeIntoFilter appends printable keys to the filter — the fzf-style
// grammar: typing narrows immediately.
func (v versionList) typeIntoFilter(key tea.KeyMsg) versionList {
	if key.Type != tea.KeyRunes || key.Alt {
		return v
	}
	v.filter += string(key.Runes)
	return v.resetToTop()
}

// resetToTop re-anchors the selection after the filter changes.
func (v versionList) resetToTop() versionList {
	v.cursor = 0
	v.offset = 0
	return v
}

// follow scrolls the list's viewport so the highlighted row stays visible.
func (v versionList) follow() versionList {
	count := len(v.matches())
	if count == 0 {
		v.cursor, v.offset = 0, 0
		return v
	}
	v.cursor = min(max(v.cursor, 0), count-1)

	visible := v.visibleRows(count)
	v.offset = min(v.offset, max(count-visible, 0))
	if v.cursor < v.offset {
		v.offset = v.cursor
	}
	if v.cursor >= v.offset+visible {
		v.offset = v.cursor - visible + 1
	}
	return v
}

// visibleRows is how many rows the viewport shows; before the first
// tea.WindowSizeMsg it is unbounded.
func (v versionList) visibleRows(count int) int {
	if v.height <= 0 {
		return count
	}
	return v.height
}

// matches narrows the versions to the active filter, fuzzy-matching on the
// group/version spelling ("craft.example.com/v2"), so typing v2 reaches it.
func (v versionList) matches() []data.Kind {
	if v.filter == "" {
		return v.rows
	}
	var matched []data.Kind
	for _, version := range v.rows {
		if fuzzyMatches(v.filter, version.GVK.GroupVersion().String()) {
			matched = append(matched, version)
		}
	}
	return matched
}

// highlighted is the version the selection sits on.
func (v versionList) highlighted() (data.Kind, bool) {
	matched := v.matches()
	if len(matched) == 0 {
		return data.Kind{}, false
	}
	return matched[min(v.cursor, len(matched)-1)], true
}

// view renders the list: the filter prompt and the visible window of served
// versions, the Preferred Version marked as dimmed row metadata.
func (v versionList) view() string {
	lines := []string{"switch version > " + v.filter}

	matched := v.matches()
	if len(matched) == 0 {
		lines = append(lines, dimmedStyle.Render("no served versions match"))
		return strings.Join(lines, "\n")
	}

	visible := v.visibleRows(len(matched))
	for index := v.offset; index < min(v.offset+visible, len(matched)); index++ {
		lines = append(lines, renderVersionRow(matched[index], index == v.cursor))
	}
	return strings.Join(lines, "\n")
}

// renderVersionRow renders one served-version row: the group/version anchors
// the row, the Preferred Version marking dimmed alongside it.
func renderVersionRow(version data.Kind, highlighted bool) string {
	cursor := "  "
	name := version.GVK.GroupVersion().String()
	if highlighted {
		cursor = "> "
		name = highlightedStyle.Render(name)
	}
	if version.Preferred {
		name += "  " + dimmedStyle.Render("(Preferred Version)")
	}
	return cursor + name
}

// openVersionList is `V` in navigate mode: it lists the open Kind's other
// served versions as a body overlay; a Kind served at one version leaves a
// non-fatal notice instead.
func (c compose) openVersionList() compose {
	others := c.otherVersions()
	if len(others) == 0 {
		c.notice = kindDisplayName(c.kind) + " is the only served version of " + c.kind.GVK.Kind
		return c
	}
	c.versionList = &versionList{rows: others, height: c.searchListHeight()}
	return c
}

// otherVersions narrows the Kind's served versions to the ones the compose
// view could switch to — every served version but the open one.
func (c compose) otherVersions() []data.Kind {
	var others []data.Kind
	for _, version := range c.versions {
		if version.GVK != c.kind.GVK {
			others = append(others, version)
		}
	}
	return others
}

// updateVersionList routes one key press into the open version list:
// dismissal returns to navigate mode, and selecting a version closes the
// list and asks the shell to switch — the target's group document travels
// the same lazy fetch path a picker selection does.
func (c compose) updateVersionList(key tea.KeyMsg) (compose, tea.Cmd) {
	list, outcome := c.versionList.update(key)

	switch outcome {
	case versionDismissed:
		c.versionList = nil
	case versionSelected:
		target, _ := list.highlighted()
		c.versionList = nil
		return c, func() tea.Msg { return versionSwitchRequestedMsg{kind: target} }
	default:
		c.versionList = &list
	}
	return c, nil
}

// updateSwitchConfirm applies one key press to the open drop confirm, in the
// modal grammar: Enter accepts the drops and commits the switch, Esc keeps
// composing at the current version untouched, and everything else is inert.
func (c compose) updateSwitchConfirm(key tea.KeyMsg) (compose, tea.Cmd) {
	switch key.String() {
	case "enter":
		return c, func() tea.Msg { return versionSwitchAcceptedMsg{} }
	case "esc":
		c.pendingSwitch = nil
	}
	return c, nil
}

// landAfterSwitch restores the focus after a version switch: a previously
// focused schema-level Field Path the target version still defines lands
// under the search overlay's landing rule; a deep-linked or searched
// position that no longer exists degrades gracefully — the focus falls back
// to the root of the field tree.
func (c compose) landAfterSwitch(fieldPath string) compose {
	if fieldPath == "" {
		return c
	}
	if c.search.candidates == nil {
		c.search.candidates = c.root.node.FieldPaths()
	}
	if !slices.Contains(c.search.candidates, fieldPath) {
		return c
	}
	c.landOn(fieldPath)
	return c
}

// startVersionSwitch begins switching the open Kind to another served
// version: a group document the Session already parsed resolves the
// carry-over immediately, anything else transitions to the loading state and
// fetches the target version's group document through the same lazy path a
// picker selection uses (DESIGN.md — Data layer).
func (m Model) startVersionSwitch(target data.Kind) (tea.Model, tea.Cmd) {
	if m.view != composing {
		return m, nil
	}
	if document, parsed := m.documents[target.GroupVersionPath]; parsed {
		return m.resolveVersionSwitch(target, document), nil
	}
	m.switching = &target
	m.view = fetchingDocument
	return m, m.fetchDocumentCmd(target)
}

// resolveVersionSwitch runs the carry-over against the target version's
// field tree: a clean carry-over — nothing dropped — switches immediately,
// and anything else opens the drop confirm over the compose view with the
// report; composing stays untouched until Enter accepts it.
func (m Model) resolveVersionSwitch(target data.Kind, document *schema.Document) Model {
	m.view = composing

	gvk := schema.GroupVersionKind{Group: target.GVK.Group, Version: target.GVK.Version, Kind: target.GVK.Kind}
	root, err := document.FieldTree(gvk)
	if err != nil {
		m.compose.notice = switchFailedNotice(target, err)
		return m
	}

	carried, drops := m.compose.draft.CarryOver(root, gvk)
	pending := pendingSwitch{target: target, root: root, carried: carried, drops: drops}
	if len(drops) == 0 {
		return m.commitVersionSwitch(pending)
	}
	m.compose.pendingSwitch = &pending
	return m
}

// acceptVersionSwitch commits the drop confirm's pending switch — the Enter
// the confirmation asked for.
func (m Model) acceptVersionSwitch() Model {
	if m.view != composing || m.compose.pendingSwitch == nil {
		return m
	}
	return m.commitVersionSwitch(*m.compose.pendingSwitch)
}

// commitVersionSwitch rebuilds the compose view over the target version: the
// carried Draft applies as the view's Draft, completeness recomputes from
// it, the breadcrumb root updates to the target version, and the focus lands
// back on the previously focused position when the target version still
// defines it — the root of the field tree otherwise.
func (m Model) commitVersionSwitch(pending pendingSwitch) Model {
	previous := m.compose
	view, err := composeOverDraft(pending.target, pending.root, pending.carried)
	if err != nil {
		m.compose.pendingSwitch = nil
		m.compose.notice = switchFailedNotice(pending.target, err)
		return m
	}

	view.versions = previous.versions
	view.defaultNamespace = previous.defaultNamespace
	if row := previous.focused(); row != nil {
		view = view.landAfterSwitch(row.node.FieldPath())
	}
	m.compose = view.resize(m.width, m.height)
	m.kind = pending.target
	m.view = composing
	return m
}

// switchTransitKey is the key grammar while a version switch's group
// document fetches: Esc and `q` cancel the switch and return to composing —
// the Draft is never lost to an abandoned switch, and the exit ramp's
// protections stay one keypress away in the compose view — while Ctrl-c
// remains the immediate escape hatch.
func (m Model) switchTransitKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.switching = nil
		m.view = composing
	}
	return m, nil
}

// switchFailedNotice spells a failed version switch for the compose view's
// non-fatal notice: what failed, the fetch or parse's own words, and what
// the Session is doing now — composing continues at the current version,
// the Draft untouched.
func switchFailedNotice(target data.Kind, err error) string {
	return "switching to " + kindDisplayName(target) + " failed: " + err.Error() +
		" — still composing at the current version"
}

// kindVersions lists one Kind's served versions from the discovery data
// already on the shell: the picker rows sharing the Kind's (group, kind), in
// picker order — the Preferred Version leading.
func (m Model) kindVersions(kind data.Kind) []data.Kind {
	var versions []data.Kind
	for _, row := range m.picker.rows {
		if row.GVK.Group == kind.GVK.Group && row.GVK.Kind == kind.GVK.Kind {
			versions = append(versions, row)
		}
	}
	return versions
}

// SwitchingVersion reports whether a version switch's target group document
// is fetching — the loading state between `V`'s selection and the
// carry-over.
func (m Model) SwitchingVersion() bool {
	return m.switching != nil
}

// VersionListOpen reports whether `V`'s served-version list is open over the
// compose view.
func (m Model) VersionListOpen() bool {
	return m.view == composing && m.compose.versionList != nil
}

// VersionFilter returns the version list's active type-to-filter query —
// empty when the list is not open.
func (m Model) VersionFilter() string {
	if !m.VersionListOpen() {
		return ""
	}
	return m.compose.versionList.filter
}

// VersionOptions returns the served versions the version list's active
// filter narrows to, in picker order — nil when the list is not open.
func (m Model) VersionOptions() []data.Kind {
	if !m.VersionListOpen() {
		return nil
	}
	return m.compose.versionList.matches()
}

// HighlightedVersion returns the version the list's selection sits on, and
// false when the list is not open or nothing matches the filter.
func (m Model) HighlightedVersion() (data.Kind, bool) {
	if !m.VersionListOpen() {
		return data.Kind{}, false
	}
	return m.compose.versionList.highlighted()
}

// ConfirmingVersionSwitch reports whether the drop confirm is rendering a
// pending switch's drop report over the compose view.
func (m Model) ConfirmingVersionSwitch() bool {
	return m.view == composing && m.compose.pendingSwitch != nil
}

// DropReport returns the pending switch's drop report — the dropped
// Draft-level Field Paths in Instantiated order, each with its reason — and
// nil when no drop confirm is open.
func (m Model) DropReport() []schema.Drop {
	if !m.ConfirmingVersionSwitch() {
		return nil
	}
	return slices.Clone(m.compose.pendingSwitch.drops)
}
