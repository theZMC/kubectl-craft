package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/internal/schema"
)

const (
	// requiredMarker flags a required-but-unset field in the tree pane —
	// the nodes the Draft's MissingRequired reports (DESIGN.md — Flow §3:
	// contextual requiredness).
	requiredMarker = "✱"

	// schemaBlindNote is what the detail pane says on a node the Type
	// Schema can't describe (x-kubernetes-preserve-unknown-fields or a
	// plain untyped object).
	schemaBlindNote = "The Type Schema can't describe what goes here; " +
		"the raw-YAML escape hatch composes it, and its editor isn't wired yet."

	// composeHints is the compose view's one-line contextual hint bar
	// (DESIGN.md — Keybindings: k9s-style, the handful of keys for the
	// focused view; `?` opens the full map).
	composeHints = "j/k move · h/l collapse/expand · enter edit/toggle · " +
		"g/G top/bottom · / search · esc Kind picker · ? help · q quit"

	// discardToPickerPrompt is the confirm Esc opens over a non-empty
	// Draft (DESIGN.md — Compose lifecycle: returning to the picker
	// mid-compose warns that the Draft will be discarded). It keeps the
	// modal grammar: Enter confirms, Esc cancels.
	discardToPickerPrompt = "discard the Draft and return to the Kind picker? " +
		"enter discard · esc keep composing"

	// discardQuitPrompt is the confirm `q` opens over a non-empty Draft —
	// the interim guard until the exit ramp's three-way prompt lands with
	// emission (DESIGN.md — Exit ramp): quitting discards the Draft, so
	// it warns exactly like Esc-to-picker does.
	discardQuitPrompt = "discard the Draft and quit? enter quit · esc keep composing"

	// transitHints is the hint bar for the loading and error states
	// between the picker and the compose view.
	transitHints = "esc Kind picker · q quit"

	// fallbackWidth sizes the panes before the first tea.WindowSizeMsg.
	fallbackWidth = 80
)

// helpText is the `?` full-map help overlay: every key the compose view
// serves, and nothing it doesn't — Validate, version switching, mutation
// verbs, and the exit ramp's prompt arrive in later issues (DESIGN.md —
// Keybindings: fixed keys keep the hint bar/help/docs trivially truthful).
const helpText = `Compose view — navigate mode

  j/k, ↑/↓   move focus
  l, →       expand the focused field; on an expanded field: step to its first child
  h, ←       collapse the focused field; on a collapsed field: jump to its parent
  enter      open the value widget on a leaf; toggle expansion on a parent
  g / G      jump to the top / bottom of the tree
  /          search the Kind's Field Paths and jump to a match
  ?          open this help

Edit mode — Enter on a leaf opens its type-appropriate widget

  enter      confirm the value into the Draft; rejections render inline
  esc        cancel back to navigate mode without touching the Draft
  space ←/→  flip a boolean toggle
  ↑/↓        choose from an enum select

  esc        return to the Kind picker — a non-empty Draft warns before discarding
  q          quit — a non-empty Draft warns first (the three-way exit ramp lands with emission)
  ctrl+c     quit immediately

Any key closes this help.`

// treeRow is one visible position of the compose view's field tree: a
// schema.Node plus the presentation state the tree pane needs. Rows are
// materialized lazily as their parents expand, so a self-referential Type
// Schema (JSONSchemaProps) simply keeps yielding rows instead of recursing.
type treeRow struct {
	node *schema.Node

	// label is the row's display name: the Field Path's last segment for
	// a schema-defined field, "[items]" for an array's item Node, and
	// "[value]" for a map's value Node (which share their parent's Field
	// Path — dots address schema-defined fields, never items or keys).
	label string

	// depth indents the row: the root sits at zero.
	depth int

	// parent is the row this one expanded out of; nil at the root.
	parent *treeRow

	// loaded reports whether children has been materialized; leaf is
	// meaningful only once it has.
	loaded   bool
	leaf     bool
	expanded bool
	children []*treeRow

	// expandErr is the last failed expansion ($ref resolution), surfaced
	// in the detail pane rather than crashing the Session.
	expandErr error
}

// compose is the compose view (DESIGN.md — Flow §3): the full schema field
// tree on the left, the focused node's detail pane on the right, a
// persistent Field Path breadcrumb above, and the completeness status line
// and contextual hint bar below. Enter on a leaf opens its value widget in
// edit mode, confirming into the Draft.
type compose struct {
	kind data.Kind

	// draft is the in-progress state of composing, bound to the open
	// Kind+version; it lives and dies with this compose view — returning
	// to the picker discards it (DESIGN.md — Compose lifecycle).
	draft *schema.Draft

	// missing is the Draft's current completeness — MissingRequired over
	// the Draft's instantiated paths, keyed by Draft-level Field Path —
	// recomputed as values are confirmed.
	missing map[string]bool

	root *treeRow

	// rows flattens the expanded tree into the visible list the cursor
	// moves over, root first.
	rows []*treeRow

	// cursor is the focused row's index into rows; offset scrolls the
	// tree pane so the focused row stays visible.
	cursor int
	offset int

	// width and height come from the last tea.WindowSizeMsg; zero until
	// the first one arrives, which the panes treat as unbounded.
	width  int
	height int

	// helpOpen renders the `?` full-map help overlay in place of the
	// panes; any key dismisses it.
	helpOpen bool

	// searchOpen renders the `/` field-search overlay in place of the
	// panes; search carries its state, candidates enumerated on the
	// overlay's first open.
	searchOpen bool
	search     fieldSearch

	// editor is the open value widget — edit mode; nil in navigate mode
	// (DESIGN.md — Keybindings: modal grammar).
	editor *fieldEditor

	// discard is the open discard confirm over a non-empty Draft — Esc's
	// return to the picker or `q`'s quit; discardNone otherwise. Enter
	// discards, Esc keeps composing.
	discard discardConfirm

	// notice is the non-fatal one-liner a deep link leaves when its Field
	// Path doesn't exist in the Type Schema: it takes the hint bar's line
	// until the first key press — the Kind stays browsable throughout.
	notice string
}

// newCompose grows the compose view for a Kind from its parsed group
// document: the field tree from the root Type Schema, an empty Draft bound
// to the Kind at this version, its required chain for the
// required-but-unset markers, and the root expanded one level so the view
// opens showing the Kind's top-level fields.
func newCompose(kind data.Kind, document *schema.Document) (compose, error) {
	gvk := schema.GroupVersionKind{Group: kind.GVK.Group, Version: kind.GVK.Version, Kind: kind.GVK.Kind}

	root, err := document.FieldTree(gvk)
	if err != nil {
		return compose{}, fmt.Errorf("opening the compose view for %s: %w", kindDisplayName(kind), err)
	}
	draft := schema.NewDraft(root, gvk)
	missing, err := draft.MissingRequired()
	if err != nil {
		return compose{}, fmt.Errorf("flagging %s's required-but-unset fields: %w", kindDisplayName(kind), err)
	}

	view := compose{kind: kind, draft: draft, missing: map[string]bool{}}
	for _, fieldPath := range missing {
		view.missing[fieldPath] = true
	}

	view.root = &treeRow{node: root, label: kind.GVK.Kind}
	if !view.loadRow(view.root) {
		return compose{}, fmt.Errorf("expanding %s's root Type Schema: %w", kindDisplayName(kind), view.root.expandErr)
	}
	view.expandRow(view.root)
	view.rebuildRows()

	return view, nil
}

// kindDisplayName renders a Kind in glossary form, e.g. "apps/v1 Deployment"
// — the core group renders bare, e.g. "v1 Pod".
func kindDisplayName(kind data.Kind) string {
	return kind.GVK.GroupVersion().String() + " " + kind.GVK.Kind
}

// loadRow materializes the row's children through the lazy Children() —
// $refs resolve now, at expansion, never at construction — and reports
// whether they loaded. A $ref-resolution failure is kept on the row and
// surfaces in the detail pane; the row stays collapsed and retryable.
func (c *compose) loadRow(row *treeRow) bool {
	if row.loaded {
		return true
	}

	children, err := row.node.Children()
	if err != nil {
		row.expandErr = err
		return false
	}

	row.loaded, row.leaf, row.expandErr = true, len(children) == 0, nil
	parentPath := row.node.FieldPath()
	parentType := ""
	if meta, metaErr := row.node.Metadata(); metaErr == nil {
		parentType = meta.Type
	}

	shared := 0
	row.children = make([]*treeRow, 0, len(children))
	for _, child := range children {
		childPath := child.FieldPath()
		label := lastPathSegment(childPath)
		if childPath == parentPath {
			label = sharedChildLabel(parentType, shared)
			shared++
		}
		row.children = append(row.children, &treeRow{
			node:   child,
			label:  label,
			depth:  row.depth + 1,
			parent: row,
		})
	}

	return true
}

// extendsParent reports whether the row extends its parent's Field Path —
// a schema-defined field row, as opposed to an array's item row or a map's
// value row, which share their parent's Field Path. The root extends
// nothing.
func extendsParent(row *treeRow) bool {
	return row.parent != nil && row.node.FieldPath() != row.parent.node.FieldPath()
}

// rowMissingRequired marks a required-but-unset field: the Draft's current
// missing required chain names the row's Field Path. Item and value rows
// share their parent's Field Path and never carry the marker themselves.
func (c compose) rowMissingRequired(row *treeRow) bool {
	return extendsParent(row) && c.missing[row.node.FieldPath()]
}

// sharedChildLabel names a child Node that shares its parent's Field Path:
// an array's item Node first, a map's value Node otherwise.
func sharedChildLabel(parentType string, shared int) string {
	if shared == 0 && (parentType == "array" || strings.HasPrefix(parentType, "[]")) {
		return "[items]"
	}
	return "[value]"
}

// lastPathSegment is the display name of a schema-level Field Path: its
// final dotted segment.
func lastPathSegment(fieldPath string) string {
	if dot := strings.LastIndex(fieldPath, "."); dot >= 0 {
		return fieldPath[dot+1:]
	}
	return fieldPath
}

// expandRow expands one parent row, loading its children plus one level of
// lookahead so every visible row's leaf-vs-parent indicator is truthful. A
// child whose $ref fails to resolve stays collapsed and surfaces its error
// in the detail pane when focused. One bounded level per expansion is what
// keeps a JSONSchemaProps-style cycle safe: it just keeps yielding rows.
func (c *compose) expandRow(row *treeRow) {
	if !c.loadRow(row) || row.leaf {
		return
	}
	row.expanded = true
	for _, child := range row.children {
		c.loadRow(child)
	}
}

// rebuildRows re-flattens the expanded tree into the visible row list and
// keeps the focus in view. Re-slicing to zero length reuses the previous
// list's backing array — the rows are freshly appended either way, so no
// stale row survives.
func (c *compose) rebuildRows() {
	c.rows = appendVisibleRows(c.rows[:0], c.root)
	c.follow()
}

// appendVisibleRows collects a row and, when it is expanded, its subtree.
func appendVisibleRows(rows []*treeRow, row *treeRow) []*treeRow {
	rows = append(rows, row)
	if !row.expanded {
		return rows
	}
	for _, child := range row.children {
		rows = appendVisibleRows(rows, child)
	}
	return rows
}

// update applies one key press under the modal grammar (DESIGN.md —
// Keybindings): the help overlay consumes every key first, then the open
// value widget, the discard confirm, and the search overlay; what remains
// is navigate mode's command map.
func (c compose) update(key tea.KeyMsg) (compose, tea.Cmd) {
	if key.String() == "ctrl+c" {
		// The conventional escape hatch quits immediately from anywhere,
		// even through the overlays (DESIGN.md — Exit ramp).
		return c, tea.Quit
	}

	// Any key acknowledges a non-fatal notice: the hint bar returns as
	// soon as browsing continues.
	c.notice = ""

	if c.helpOpen {
		c.helpOpen = false
		return c, nil
	}
	if c.editor != nil {
		return c.updateEditor(key), nil
	}
	if c.discard != discardNone {
		return c.updateDiscardConfirm(key)
	}
	if c.searchOpen {
		return c.updateSearch(key), nil
	}

	return c.command(key)
}

// command applies one navigate-mode command key (DESIGN.md — Keybindings:
// command map); anything outside the map is tree navigation.
func (c compose) command(key tea.KeyMsg) (compose, tea.Cmd) {
	switch key.String() {
	case "q":
		// Quitting discards the Draft, so a non-empty one warns first —
		// the interim guard until the exit ramp's three-way prompt lands
		// with emission (DESIGN.md — Exit ramp); an empty Draft quits as
		// immediately as Ctrl-c.
		if len(c.draft.Instantiated()) == 0 {
			return c, tea.Quit
		}
		c.discard = discardQuit
		return c, nil
	case "esc":
		// The documented way back to the Kind picker. A non-empty Draft
		// would be discarded by returning, so it warns first (DESIGN.md —
		// Compose lifecycle); an empty Draft returns silently.
		if len(c.draft.Instantiated()) == 0 {
			return c, func() tea.Msg { return returnToPickerMsg{} }
		}
		c.discard = discardToPicker
		return c, nil
	case "enter":
		return c.pressEnter(), nil
	case "?":
		c.helpOpen = true
		return c, nil
	case "/":
		return c.openSearch(), nil
	}

	c.navigate(key.String())
	return c, nil
}

// updateEditor routes one key press into the open value widget: Enter
// confirms the value into the Draft, Esc cancels back to navigate mode
// without mutating, and every other key is the widget's own — navigate keys
// never move the tree while editing.
func (c compose) updateEditor(key tea.KeyMsg) compose {
	switch key.String() {
	case "esc":
		c.editor = nil
		return c
	case "enter":
		return c.confirmEditor()
	}
	edited := c.editor.update(key)
	c.editor = &edited
	return c
}

// confirmEditor confirms the widget's value into the Draft. A rejection —
// the widget's own parse or the Draft's schema-local check — renders inline
// in the widget and commits nothing; a confirmed value closes the widget
// and recomputes the Draft's completeness.
func (c compose) confirmEditor() compose {
	value, err := c.editor.confirmValue()
	if err == nil {
		err = c.draft.Set(c.editor.row.node.FieldPath(), value)
	}
	if err != nil {
		edited := *c.editor
		edited.rejection = err.Error()
		c.editor = &edited
		return c
	}
	c.editor = nil
	c.refreshCompleteness()
	return c
}

// discardConfirm names the open discard confirm's destination: none, back
// to the Kind picker (Esc), or out of the Session (`q`).
type discardConfirm int

const (
	discardNone discardConfirm = iota
	discardToPicker
	discardQuit
)

// prompt is the confirm's footer line.
func (d discardConfirm) prompt() string {
	if d == discardQuit {
		return discardQuitPrompt
	}
	return discardToPickerPrompt
}

// updateDiscardConfirm applies one key press to the open discard confirm,
// in the modal grammar: Enter confirms the discard — returning to the Kind
// picker or quitting, whichever was asked — Esc keeps composing, and
// everything else is inert.
func (c compose) updateDiscardConfirm(key tea.KeyMsg) (compose, tea.Cmd) {
	confirmed := c.discard
	switch key.String() {
	case "enter":
		c.discard = discardNone
		if confirmed == discardQuit {
			return c, tea.Quit
		}
		return c, func() tea.Msg { return returnToPickerMsg{} }
	case "esc":
		c.discard = discardNone
	}
	return c, nil
}

// refreshCompleteness recomputes the Draft's completeness — the missing
// required Draft-level Field Paths given what the Draft has instantiated —
// after a confirmed mutation.
func (c *compose) refreshCompleteness() {
	missing, err := c.draft.MissingRequired()
	if err != nil {
		c.notice = "recomputing the Draft's completeness failed: " + err.Error()
		return
	}
	clear(c.missing)
	for _, fieldPath := range missing {
		c.missing[fieldPath] = true
	}
}

// openSearch opens the `/` field-search overlay, enumerating the open
// Kind's schema-level Field Paths on the first open — the candidate set is
// the Kind's whole Type Schema, not the tree's expansion state.
func (c compose) openSearch() compose {
	if c.search.candidates == nil {
		c.search.candidates = c.root.node.FieldPaths()
	}
	// M2's only scope; the DRAFT scope's Tab toggle switches this field
	// when the Draft lands (DESIGN.md — Flow §5).
	c.search.scope = scopeSchema
	c.search.height = c.searchListHeight()
	c.searchOpen = true
	return c
}

// updateSearch routes one key press into the open search overlay and
// applies its outcome: dismissal returns to navigate mode; selecting the
// highlighted match closes the overlay, resets it for the next search, and
// jumps the tree under the landing rule.
func (c compose) updateSearch(key tea.KeyMsg) compose {
	overlay, outcome := c.search.update(key)
	c.search = overlay

	switch outcome {
	case searchDismissed:
		c.searchOpen = false
	case searchSelected:
		match, _ := c.search.highlighted()
		c.searchOpen = false
		c.search = c.search.reset()
		c.landOn(match.FieldPath)
	}
	return c
}

// searchListHeight is how many match rows fit under the search overlay's
// prompt line; zero means unbounded (no tea.WindowSizeMsg yet).
func (c compose) searchListHeight() int {
	if c.bodyHeight() <= 0 {
		return 0
	}
	return max(c.bodyHeight()-1, 1)
}

// navigate applies one tree-navigation key.
func (c *compose) navigate(key string) {
	switch key {
	case "j", "down":
		c.focusIndex(c.cursor + 1)
	case "k", "up":
		c.focusIndex(c.cursor - 1)
	case "g":
		c.focusIndex(0)
	case "G":
		c.focusIndex(len(c.rows) - 1)
	case "l", "right":
		c.expandFocused()
	case "h", "left":
		c.collapseFocused()
	}
}

// focusIndex moves the focus to the given row, clamping at the tree's
// edges, and scrolls the viewport after it.
func (c *compose) focusIndex(index int) {
	if len(c.rows) == 0 {
		return
	}
	c.cursor = min(max(index, 0), len(c.rows)-1)
	c.follow()
}

// focused is the row the focus sits on.
func (c *compose) focused() *treeRow {
	if len(c.rows) == 0 {
		return nil
	}
	return c.rows[min(c.cursor, len(c.rows)-1)]
}

// expandFocused is `l`/`→`: expand a collapsed parent; on an already
// expanded parent, step to its first child; on a leaf, nothing.
func (c *compose) expandFocused() {
	row := c.focused()
	if row == nil || row.leaf {
		return
	}
	if !row.expanded {
		c.expandRow(row)
		c.rebuildRows()
		return
	}
	if len(row.children) > 0 {
		c.focusIndex(c.cursor + 1)
	}
}

// collapseFocused is `h`/`←`: collapse an expanded parent; on a collapsed
// row or a leaf, jump to its parent.
func (c *compose) collapseFocused() {
	row := c.focused()
	if row == nil {
		return
	}
	if row.expanded {
		row.expanded = false
		c.rebuildRows()
		return
	}
	if row.parent != nil {
		c.focusRow(row.parent)
	}
}

// pressEnter is Enter in navigate mode (DESIGN.md — Command map): on a
// parent it toggles expansion; on a leaf it opens the field's value widget
// in edit mode.
func (c compose) pressEnter() compose {
	row := c.focused()
	if row == nil {
		return c
	}
	if !row.loaded || !row.leaf {
		c.toggleFocused()
		return c
	}
	return c.openEditor(row)
}

// openEditor opens the leaf's value widget. A leaf no widget can serve yet
// leaves a one-line hint instead of opening anything: a schema-blind leaf
// routes to the raw-YAML escape hatch, a leaf beneath an array or map
// crossing waits for the mutation verbs' item and key rows, and a leaf
// without a scalar type has nothing to type into.
func (c compose) openEditor(row *treeRow) compose {
	meta, err := row.node.Metadata()
	if err != nil {
		// The detail pane already surfaces the resolution error.
		return c
	}
	if meta.SchemaBlind {
		c.notice = "the Type Schema can't describe " + row.node.FieldPath() +
			" — raw YAML composes it, and the escape hatch's editor isn't wired yet"
		return c
	}
	if !draftAddressable(row) {
		c.notice = row.node.FieldPath() + " is inside an array or map — " +
			"its item and key rows land with the mutation verbs"
		return c
	}
	kind, editable := widgetFor(meta)
	if !editable {
		c.notice = "the Type Schema types " + row.node.FieldPath() + " as " + meta.Type +
			" — no value widget serves it"
		return c
	}

	value, filled := c.draft.ValueAt(row.node.FieldPath())
	editor := newFieldEditor(row, meta, kind, value, filled)
	c.editor = &editor
	return c
}

// draftAddressable reports whether the row's position is addressable by its
// schema-level Field Path alone — no array or map crossing between it and
// the root. A leaf beneath a crossing needs an item or key selector, and
// those rows arrive with the mutation verbs.
func draftAddressable(row *treeRow) bool {
	for current := row; current.parent != nil; current = current.parent {
		if !extendsParent(current) {
			return false
		}
	}
	return true
}

// toggleFocused toggles expansion on a parent row; a loaded leaf has
// nothing to toggle.
func (c *compose) toggleFocused() {
	row := c.focused()
	if row == nil || (row.loaded && row.leaf) {
		return
	}
	if row.expanded {
		row.expanded = false
	} else {
		c.expandRow(row)
	}
	c.rebuildRows()
}

// focusRow moves the focus onto the given row, when it is visible.
func (c *compose) focusRow(target *treeRow) {
	for index, row := range c.rows {
		if row == target {
			c.focusIndex(index)
			return
		}
	}
}

// follow scrolls the tree pane's viewport so the focused row stays visible.
func (c *compose) follow() {
	count := len(c.rows)
	if count == 0 {
		c.offset = 0
		return
	}
	c.cursor = min(max(c.cursor, 0), count-1)

	visible := c.bodyHeight()
	if visible <= 0 {
		visible = count
	}
	c.offset = min(c.offset, max(count-visible, 0))
	if c.cursor < c.offset {
		c.offset = c.cursor
	}
	if c.cursor >= c.offset+visible {
		c.offset = c.cursor - visible + 1
	}
}

// resize records the terminal size and keeps the focused row — and the
// search overlay's highlighted match — visible.
func (c compose) resize(width, height int) compose {
	c.width, c.height = width, height
	c.search.height = c.searchListHeight()
	c.search = c.search.follow()
	c.follow()
	return c
}

// bodyHeight is how many pane rows fit between the breadcrumb and the
// status line + hint bar; zero means unbounded (no tea.WindowSizeMsg yet).
func (c compose) bodyHeight() int {
	if c.height <= 0 {
		return 0
	}
	return max(c.height-3, 1)
}

// paneWidths splits the terminal between the tree and detail panes; a
// terminal too narrow for two panes keeps the tree and drops the detail.
func (c compose) paneWidths() (int, int) {
	width := c.width
	if width <= 0 {
		width = fallbackWidth
	}
	if width < 40 {
		return width, 0
	}
	treeWidth := min(width*2/5, 48)
	return treeWidth, width - treeWidth - 2
}

// breadcrumb is the persistent Field Path breadcrumb: the Kind in glossary
// form, extended by the focused node's schema-level Field Path — at the
// root it shows the Kind alone, e.g. "apps/v1 Deployment".
func (c compose) breadcrumb() string {
	crumb := kindDisplayName(c.kind)
	if row := c.focused(); row != nil && row.node.FieldPath() != "" {
		crumb += " › " + row.node.FieldPath()
	}
	return crumb
}

// view renders the compose view: breadcrumb, tree + detail panes (or the
// help overlay), the completeness status line, and the footer line.
func (c compose) view() string {
	return clipLine(c.breadcrumb(), c.width) + "\n" +
		c.bodyView() + "\n" +
		clipLine(c.statusLine(), c.width) + "\n" +
		clipLine(c.footer(), c.width) + "\n"
}

// statusLine is the persistent completeness line (DESIGN.md — Flow §3: a
// status line tracks completeness): how many required fields the Draft is
// still missing, or the complete marker once none are.
func (c compose) statusLine() string {
	count := len(c.missing)
	if count == 0 {
		return dimmedStyle.Render("✔ no required fields missing")
	}
	noun := "fields"
	if count == 1 {
		noun = "field"
	}
	return highlightedStyle.Render(fmt.Sprintf("%s %d required %s missing", requiredMarker, count, noun))
}

// footer is the view's bottom line: the discard confirm while one is open,
// a non-fatal notice until the next key press, the contextual hint bar
// otherwise.
func (c compose) footer() string {
	if c.discard != discardNone {
		return highlightedStyle.Render(c.discard.prompt())
	}
	if c.notice != "" {
		return highlightedStyle.Render(c.notice)
	}
	return dimmedStyle.Render(c.hints())
}

// hints is the contextual one-line hint bar: the open value widget's own
// hints in edit mode, the search overlay's while it is open, the
// navigate-mode hints otherwise.
func (c compose) hints() string {
	if c.editor != nil {
		return c.editor.hints()
	}
	if c.searchOpen {
		return searchHints
	}
	return composeHints
}

// bodyView renders the pane area: the `?` help overlay or the `/` search
// overlay when one is open, the tree and detail panes otherwise.
func (c compose) bodyView() string {
	if c.helpOpen {
		return limitLines(helpText, c.bodyHeight())
	}
	if c.searchOpen {
		return limitLines(c.searchView(), c.bodyHeight())
	}

	treeWidth, detailWidth := c.paneWidths()
	tree := c.treePane(treeWidth)
	if detailWidth <= 0 {
		return tree
	}

	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		lipgloss.NewStyle().Width(treeWidth).Render(tree),
		"  ",
		c.detailPane(detailWidth, c.bodyHeight()),
	)
}

// searchView renders the search overlay's lines, each clipped to the
// terminal width.
func (c compose) searchView() string {
	lines := strings.Split(c.search.view(), "\n")
	for index, line := range lines {
		lines[index] = clipLine(line, c.width)
	}
	return strings.Join(lines, "\n")
}

// treePane renders the visible window of tree rows.
func (c compose) treePane(width int) string {
	visible := c.bodyHeight()
	if visible <= 0 {
		visible = len(c.rows)
	}

	lines := make([]string, 0, visible)
	for index := c.offset; index < min(c.offset+visible, len(c.rows)); index++ {
		lines = append(lines, clipLine(c.renderTreeRow(index), width))
	}
	return strings.Join(lines, "\n")
}

// renderTreeRow renders one tree row: focus cursor, depth indent, a
// truthful expand indicator (loaded leaves render as leaves), the label,
// the Draft's state at the node — the being-edited buffer, the set value,
// or the schema default as a dimmed placeholder — and the
// required-but-unset marker.
func (c compose) renderTreeRow(index int) string {
	row := c.rows[index]

	cursor := "  "
	if index == c.cursor {
		cursor = "> "
	}

	indicator := "  "
	if !row.loaded || !row.leaf {
		indicator = "▸ "
		if row.expanded {
			indicator = "▾ "
		}
	}

	label := row.label
	if index == c.cursor {
		label = highlightedStyle.Render(label)
	}
	label += c.rowValue(row)
	if c.rowMissingRequired(row) {
		label += " " + requiredMarker
	}

	return cursor + strings.Repeat("  ", row.depth) + indicator + label
}

// rowValue is the Draft state a leaf row shows after its label: the open
// widget's live buffer while the row is being edited, the value the Draft
// holds once one is set, or the schema default as a dimmed placeholder —
// visually distinct from a set value, and never in the output (DESIGN.md —
// Flow §6).
func (c compose) rowValue(row *treeRow) string {
	if c.editor != nil && c.editor.row == row {
		return ": " + c.editor.inlineValue()
	}
	if !row.loaded || !row.leaf || !extendsParent(row) {
		return ""
	}
	if value, filled := c.draft.ValueAt(row.node.FieldPath()); filled {
		return ": " + renderScalar(value.Data)
	}
	meta, err := row.node.Metadata()
	if err != nil || meta.Default == nil {
		return ""
	}
	return dimmedStyle.Render(": " + renderScalar(meta.Default))
}

// detailPane renders the focused node's detail: its Metadata() when it
// resolves, the $ref-resolution error otherwise.
func (c compose) detailPane(width, height int) string {
	content := strings.Join(c.detailLines(), "\n")
	if width > 0 {
		content = lipgloss.NewStyle().Width(width).Render(content)
	}
	return limitLines(content, height)
}

// detailLines assembles the detail pane's lines for the focused row: the
// open value widget in edit mode, the node's metadata otherwise.
func (c compose) detailLines() []string {
	row := c.focused()
	if row == nil {
		return nil
	}
	if c.editor != nil {
		return c.editor.viewLines()
	}
	if row.expandErr != nil {
		return []string{highlightedStyle.Render(row.label), "", "expanding this field failed: " + row.expandErr.Error()}
	}

	meta, err := row.node.Metadata()
	if err != nil {
		return []string{highlightedStyle.Render(row.label), "", "reading this field's Type Schema failed: " + err.Error()}
	}
	return metadataLines(row, meta, c.rowMissingRequired(row))
}

// metadataLines renders one node's Metadata() for the detail pane: display
// type, requiredness, the schema default as a dimmed placeholder
// (DESIGN.md — Flow §6: defaults render dimmed, never in the output), enum
// values, constraints (CEL text included), the schema-blind note, and the
// field's documentation.
func metadataLines(row *treeRow, meta schema.Metadata, missingRequired bool) []string {
	lines := []string{highlightedStyle.Render(row.label), "type: " + meta.Type}

	if meta.Required {
		flag := "required"
		if missingRequired {
			flag += " — unset"
		}
		lines = append(lines, flag)
	}
	if meta.Default != nil {
		lines = append(lines, dimmedStyle.Render("default: "+renderScalar(meta.Default)))
	}
	if len(meta.Enum) > 0 {
		lines = append(lines, "enum: "+strings.Join(meta.Enum, " · "))
	}
	if len(meta.Constraints) > 0 {
		lines = append(lines, "constraints:")
		for _, constraint := range meta.Constraints {
			lines = append(lines, "  "+constraint)
		}
	}
	if meta.SchemaBlind {
		lines = append(lines, "", schemaBlindNote)
	}
	if meta.Description != "" {
		lines = append(lines, "", meta.Description)
	}
	return lines
}

// renderScalar spells a scalar — a schema-declared default or a Draft value
// — for display: strings verbatim, anything else in its JSON spelling.
func renderScalar(value any) string {
	if text, isString := value.(string); isString {
		return text
	}
	if encoded, err := json.Marshal(value); err == nil {
		return string(encoded)
	}
	return fmt.Sprint(value)
}

// clipLine truncates one rendered line to the given width, ANSI-safely;
// zero width leaves it unbounded.
func clipLine(line string, width int) string {
	if width <= 0 {
		return line
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(line)
}

// limitLines truncates rendered text to the given number of lines; a zero
// limit leaves it unbounded.
func limitLines(text string, limit int) string {
	if limit <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	if len(lines) <= limit {
		return text
	}
	return strings.Join(lines[:limit], "\n")
}
