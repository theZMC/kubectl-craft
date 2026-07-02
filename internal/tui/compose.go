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
	// the nodes MissingRequired reports over the empty Draft (DESIGN.md —
	// Flow §3: contextual requiredness).
	requiredMarker = "✱"

	// schemaBlindNote is what the detail pane says on a node the Type
	// Schema can't describe (x-kubernetes-preserve-unknown-fields or a
	// plain untyped object).
	schemaBlindNote = "The Type Schema can't describe what goes here; " +
		"the raw-YAML escape hatch lands in M3."

	// composeHints is the compose view's one-line contextual hint bar
	// (DESIGN.md — Keybindings: k9s-style, the handful of keys for the
	// focused view; `?` opens the full map).
	composeHints = "j/k move · h/l collapse/expand · enter toggle · " +
		"g/G top/bottom · / search · esc Kind picker · ? help · q quit"

	// transitHints is the hint bar for the loading and error states
	// between the picker and the compose view.
	transitHints = "esc Kind picker · q quit"

	// fallbackWidth sizes the panes before the first tea.WindowSizeMsg.
	fallbackWidth = 80
)

// helpText is the `?` full-map help overlay: every key the compose view
// serves in M2, and nothing it doesn't — Validate, version switching, and
// value entry arrive in later milestones (DESIGN.md — Keybindings: fixed
// keys keep the hint bar/help/docs trivially truthful).
const helpText = `Compose view — navigate mode

  j/k, ↑/↓   move focus
  l, →       expand the focused field; on an expanded field: step to its first child
  h, ←       collapse the focused field; on a collapsed field: jump to its parent
  enter      toggle expansion on a parent (value entry lands in M3)
  g / G      jump to the top / bottom of the tree
  /          search the Kind's Field Paths and jump to a match
  ?          open this help

  esc        return to the Kind picker (M2 composes no Draft, nothing to discard)
  q          quit — the Draft is always empty in M2, so no prompt
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

	// missingRequired marks a required-but-unset field: the empty-Draft
	// required chain MissingRequired(nil) reports.
	missingRequired bool

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

// compose is the read-only compose view (DESIGN.md — Flow §3): the full
// schema field tree on the left, the focused node's detail pane on the
// right, a persistent Field Path breadcrumb above and the contextual hint
// bar below. M2 browses; value entry and the Draft land in M3.
type compose struct {
	kind data.Kind

	// missing is the empty-Draft required chain, keyed by schema-level
	// Field Path: the fields flagged required-but-unset from the start.
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

	// notice is the non-fatal one-liner a deep link leaves when its Field
	// Path doesn't exist in the Type Schema: it takes the hint bar's line
	// until the first key press — the Kind stays browsable throughout.
	notice string
}

// newCompose grows the compose view for a Kind from its parsed group
// document: the field tree from the root Type Schema, the empty-Draft
// required chain for the required-but-unset markers, and the root expanded
// one level so the view opens showing the Kind's top-level fields.
func newCompose(kind data.Kind, document *schema.Document) (compose, error) {
	gvk := schema.GroupVersionKind{Group: kind.GVK.Group, Version: kind.GVK.Version, Kind: kind.GVK.Kind}

	root, err := document.FieldTree(gvk)
	if err != nil {
		return compose{}, fmt.Errorf("opening the compose view for %s: %w", kindDisplayName(kind), err)
	}
	missing, err := root.MissingRequired(nil)
	if err != nil {
		return compose{}, fmt.Errorf("flagging %s's required-but-unset fields: %w", kindDisplayName(kind), err)
	}

	view := compose{kind: kind, missing: map[string]bool{}}
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
		extends := childPath != parentPath
		if !extends {
			label = sharedChildLabel(parentType, shared)
			shared++
		}
		row.children = append(row.children, &treeRow{
			node:            child,
			label:           label,
			depth:           row.depth + 1,
			parent:          row,
			missingRequired: extends && c.missing[childPath],
		})
	}

	return true
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

// update applies one navigate-mode key press (DESIGN.md — Keybindings:
// command map). The help overlay consumes every key first — any key
// dismisses it.
func (c compose) update(key tea.KeyMsg) (compose, tea.Cmd) {
	if key.String() == "ctrl+c" {
		// The conventional escape hatch quits immediately from anywhere,
		// even through the help overlay (DESIGN.md — Exit ramp).
		return c, tea.Quit
	}

	// Any key acknowledges the deep link's non-fatal notice: the hint bar
	// returns as soon as browsing starts.
	c.notice = ""

	if c.helpOpen {
		c.helpOpen = false
		return c, nil
	}
	if c.searchOpen {
		return c.updateSearch(key), nil
	}

	switch key.String() {
	case "q":
		// M2 composes no Draft, so `q` needs no three-way prompt and
		// quits as immediately as Ctrl-c (DESIGN.md — Exit ramp).
		return c, tea.Quit
	case "esc":
		// The documented way back to the Kind picker: no Draft exists in
		// M2, so there is nothing to confirm discarding.
		return c, func() tea.Msg { return returnToPickerMsg{} }
	case "?":
		c.helpOpen = true
		return c, nil
	case "/":
		return c.openSearch(), nil
	}

	c.navigate(key.String())
	return c, nil
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
	case "enter":
		c.toggleFocused()
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

// toggleFocused is Enter: toggle expansion on a parent. On a leaf it does
// nothing yet — Enter opens the value-entry widget in M3 (DESIGN.md —
// Command map).
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

// bodyHeight is how many pane rows fit between the breadcrumb and the hint
// bar; zero means unbounded (no tea.WindowSizeMsg yet).
func (c compose) bodyHeight() int {
	if c.height <= 0 {
		return 0
	}
	return max(c.height-2, 1)
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
// help overlay), and the footer line.
func (c compose) view() string {
	return clipLine(c.breadcrumb(), c.width) + "\n" +
		c.bodyView() + "\n" +
		clipLine(c.footer(), c.width) + "\n"
}

// footer is the view's bottom line: the deep link's non-fatal notice until
// the first key press, the contextual hint bar otherwise.
func (c compose) footer() string {
	if c.notice != "" {
		return highlightedStyle.Render(c.notice)
	}
	return dimmedStyle.Render(c.hints())
}

// hints is the contextual one-line hint bar: the search overlay's own hint
// line while it is open, the navigate-mode hints otherwise.
func (c compose) hints() string {
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
// and the required-but-unset marker.
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
	if row.missingRequired {
		label += " " + requiredMarker
	}

	return cursor + strings.Repeat("  ", row.depth) + indicator + label
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

// detailLines assembles the detail pane's lines for the focused row.
func (c compose) detailLines() []string {
	row := c.focused()
	if row == nil {
		return nil
	}
	if row.expandErr != nil {
		return []string{highlightedStyle.Render(row.label), "", "expanding this field failed: " + row.expandErr.Error()}
	}

	meta, err := row.node.Metadata()
	if err != nil {
		return []string{highlightedStyle.Render(row.label), "", "reading this field's Type Schema failed: " + err.Error()}
	}
	return metadataLines(row, meta)
}

// metadataLines renders one node's Metadata() for the detail pane: display
// type, requiredness, the schema default as a dimmed placeholder
// (DESIGN.md — Flow §6: defaults render dimmed, never in the output), enum
// values, constraints (CEL text included), the schema-blind note, and the
// field's documentation.
func metadataLines(row *treeRow, meta schema.Metadata) []string {
	lines := []string{highlightedStyle.Render(row.label), "type: " + meta.Type}

	if meta.Required {
		flag := "required"
		if row.missingRequired {
			flag += " — unset"
		}
		lines = append(lines, flag)
	}
	if meta.Default != nil {
		lines = append(lines, dimmedStyle.Render("default: "+renderDefaultValue(meta.Default)))
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

// renderDefaultValue spells a schema-declared default for display: strings
// verbatim, anything else in its JSON spelling.
func renderDefaultValue(value any) string {
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
