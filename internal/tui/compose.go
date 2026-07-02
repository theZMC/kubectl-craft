package tui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
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
		"Enter composes it as raw YAML, and e pops the subtree out to $EDITOR."

	// composeHints is the compose view's one-line hint bar in navigate
	// mode (DESIGN.md — Keybindings: k9s-style, the handful of keys for
	// the focused view; `?` opens the full map). The mutation verbs `a`
	// and `d` — and the Validate follow-ups `n` and `r` once findings
	// exist — prepend contextually; see navigateHints.
	composeHints = "j/k move · h/l collapse/expand · enter edit/toggle · " +
		"g/G top/bottom · / search · v validate · V version · esc Kind picker · ? help · ctrl+d emit · q quit"

	// keyPromptHints is the hint bar while `a`'s inline key prompt is
	// open on a map-shaped node, in the modal grammar: Enter confirms,
	// Esc cancels.
	keyPromptHints = "type the new entry's key · enter confirm · esc cancel"

	// discardToPickerPrompt is the confirm Esc opens over a non-empty
	// Draft (DESIGN.md — Compose lifecycle: returning to the picker
	// mid-compose warns that the Draft will be discarded). It keeps the
	// modal grammar: Enter confirms, Esc cancels.
	discardToPickerPrompt = "discard the Draft and return to the Kind picker? " +
		"enter discard · esc keep composing"

	// exitMenuHints is the hint bar while `q`'s three-way exit menu is
	// open (DESIGN.md — Exit ramp), in the modal grammar: Enter confirms
	// the highlighted ramp, Esc cancels back to composing.
	exitMenuHints = "↑/↓ choose · enter confirm · esc cancel"

	// transitHints is the hint bar for the loading and error states
	// between the picker and the compose view.
	transitHints = "esc Kind picker · q quit"

	// fallbackWidth sizes the panes before the first tea.WindowSizeMsg.
	fallbackWidth = 80
)

// helpText is the `?` full-map help overlay: every key the compose view
// serves, and nothing it doesn't (DESIGN.md — Keybindings: fixed keys keep
// the hint bar/help/docs trivially truthful).
const helpText = `Compose view — navigate mode

  j/k, ↑/↓   move focus
  l, →       expand the focused field; on an expanded field: step to its first child
  h, ←       collapse the focused field; on a collapsed field: jump to its parent
  enter      open the value widget on a leaf; toggle expansion on a parent
  a          append an item on an array node; prompt for a key on a map node
  d          unset the focused value — a subtree with filled values confirms the discard count first
  e          pop a schema-blind subtree out to $EDITOR as raw YAML
  g / G      jump to the top / bottom of the tree
  /          search the Kind's Field Paths and jump to a match
  v          Validate the Draft with a server-side dry-run — nothing persists;
             findings mark the offending tree nodes, and anything unmappable
             lands in the results pane. Editing the Draft marks findings stale
             (v revalidates); switching the version drops them entirely
  n          jump to the first Validate finding's node; pressing again cycles
             through the findings in order
  r          open the Validate results pane
  V          switch the open Kind's version — values carry over by Field Path,
             and a drop report confirms anything that would not survive
  ?          open this help

Edit mode — Enter on a leaf opens its type-appropriate widget
(a schema-blind leaf opens the raw-YAML text area instead)

  enter      confirm the value into the Draft; rejections render inline
  esc        cancel back to navigate mode without touching the Draft
  space ←/→  flip a boolean toggle
  ↑/↓        choose from an enum select
  ctrl+s     confirm the raw-YAML text area — enter types its newlines

  esc        return to the Kind picker — a non-empty Draft warns before discarding
  q          quit — a non-empty Draft offers Emit & quit / Discard & quit / Cancel
  ctrl+d     emit the Manifest to stdout and quit — no menu
  ctrl+c     quit immediately, discarding the Draft

Any key closes this help.`

// rowKind names what a tree row stands for at the Draft layer: a
// schema-defined field, an uninstantiated collection's structural
// placeholder, or an instantiated array item or map key the Draft holds.
type rowKind int

const (
	// rowField is a schema-defined field row (the root included): its
	// Draft-level Field Path extends its parent's by one dotted segment.
	rowField rowKind = iota
	// rowPlaceholder is the schema-level "[items]"/"[value]" row kept for
	// structure browsing: it shares its parent's Field Path and addresses
	// nothing in the Draft — instantiating goes through `a`.
	rowPlaceholder
	// rowItem is one instantiated array item, addressed by its [n]
	// selector at the Draft layer.
	rowItem
	// rowKey is one instantiated map entry, addressed by its quoted-key
	// selector at the Draft layer.
	rowKey
)

// treeRow is one visible position of the compose view's field tree: a
// schema.Node plus the presentation state the tree pane needs. Rows are
// materialized lazily as their parents expand, so a self-referential Type
// Schema (JSONSchemaProps) simply keeps yielding rows instead of recursing.
type treeRow struct {
	node *schema.Node

	// label is the row's display name: the Field Path's last segment for
	// a schema-defined field, "[items]"/"[value]" for an uninstantiated
	// collection's placeholder (which shares its parent's Field Path —
	// dots address schema-defined fields, never items or keys), and the
	// bracketed Draft-level spelling for an instantiated item or key
	// (`containers[0]`, `labels["app"]`).
	label string

	// kind is what the row stands for at the Draft layer; index carries a
	// rowItem's Draft-level position (renumbered when an earlier sibling
	// is unset), and key carries a rowKey's map key.
	kind  rowKind
	index int
	key   string

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

	// keyPrompt is `a`'s open inline key prompt on a map-shaped node; nil
	// otherwise. Enter confirms the typed key into the Draft (a duplicate
	// rejects inline), Esc cancels without touching the Draft.
	keyPrompt *keyPrompt

	// unset is `d`'s open destructive confirm over a subtree with filled
	// descendants; nil otherwise. y/Enter discards, n/Esc keeps — no undo
	// in MVP, so the confirm is the safety net (DESIGN.md — Keybindings).
	unset *unsetConfirm

	// popOut is `e`'s in-flight $EDITOR pop-out — the subtree the editor
	// holds while the Session's terminal is handed over; nil otherwise.
	popOut *popOut

	// discardToPicker reports Esc's open discard confirm over a non-empty
	// Draft — returning to the picker would discard it. Enter discards,
	// Esc keeps composing.
	discardToPicker bool

	// exit is `q`'s open three-way exit menu over a non-empty Draft
	// (DESIGN.md — Exit ramp): Emit & quit / Discard & quit / Cancel; nil
	// otherwise.
	exit *exitMenu

	// notice is the non-fatal one-liner a deep link leaves when its Field
	// Path doesn't exist in the Type Schema: it takes the hint bar's line
	// until the first key press — the Kind stays browsable throughout.
	notice string

	// versions is the open Kind's served versions from the discovery data
	// already on the shell — the picker rows sharing this (group, kind),
	// the Preferred Version leading. `V`'s version list is grown from it.
	versions []data.Kind

	// versionList is `V`'s open served-version list — a type-to-filter
	// surface over the Kind's other served versions; nil when closed.
	versionList *versionList

	// pendingSwitch is the version switch awaiting the drop confirm: the
	// carry-over already ran against the target version's field tree, and
	// the confirm renders its drop report; nil otherwise. Enter accepts
	// the drops and commits; Esc keeps composing at the current version
	// untouched (DESIGN.md — Compose lifecycle).
	pendingSwitch *pendingSwitch

	// defaultNamespace is the Session's default namespace, copied from
	// the shell: the required-to-Validate gate skips its namespace
	// prompt whenever it resolves (DESIGN.md — Output).
	defaultNamespace string

	// gate is `v`'s open required-to-Validate prompt — metadata the
	// dry-run cannot run without is typed inline and confirmed into the
	// Draft at its real Field Path; nil otherwise. Esc cancels the whole
	// Validate.
	gate *gatePrompt

	// validation is the last Validate's rendered outcome — findings
	// pinned to tree rows, the unmappable rest, a clean pass, or an
	// unavailability; nil before the first Validate and after anything
	// that drops results (a version switch, a mutation over a non-finding
	// outcome).
	validation *validationState

	// resultsOpen renders the Validate results pane as the body overlay
	// (one open at a time, like every other overlay); Esc dismisses it
	// and `r` reopens it while results exist.
	resultsOpen bool
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
	return composeOverDraft(kind, root, schema.NewDraft(root, gvk))
}

// composeOverDraft grows the compose view over an existing Draft — the empty
// Draft of a Kind's first open, or the carried Draft of a version switch,
// whose instantiated items and keys the tree rows reflect as they expand.
func composeOverDraft(kind data.Kind, root *schema.Node, draft *schema.Draft) (compose, error) {
	missing, err := draft.MissingRequired()
	if err != nil {
		return compose{}, fmt.Errorf("flagging %s's required-but-unset fields: %w", kindDisplayName(kind), err)
	}

	view := compose{kind: kind, draft: draft, missing: map[string]bool{}}
	for _, fieldPath := range missing {
		view.missing[fieldPath] = true
	}

	view.root = &treeRow{node: root, label: kind.GVK.Kind, kind: rowField}
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
		if child.FieldPath() != parentPath {
			row.children = append(row.children, &treeRow{
				node:   child,
				label:  lastPathSegment(child.FieldPath()),
				depth:  row.depth + 1,
				parent: row,
			})
			continue
		}

		// A shared child is a collection's structure: the rows for what
		// the Draft has instantiated there first, the schema-level
		// placeholder row after them, kept for structure browsing.
		label := sharedChildLabel(parentType, shared)
		shared++
		row.children = append(row.children, c.instantiatedRows(row, child, label)...)
		row.children = append(row.children, &treeRow{
			node:   child,
			label:  label,
			kind:   rowPlaceholder,
			depth:  row.depth + 1,
			parent: row,
		})
	}

	return true
}

// instantiatedRows grows the rows for the items or keys the Draft holds at a
// collection row — consulted from the Draft itself, so a reloaded subtree
// always reflects the Draft's current state. A collection inside a
// placeholder subtree has no Draft-level Field Path and so never carries
// instantiated rows.
func (c *compose) instantiatedRows(row *treeRow, child *schema.Node, label string) []*treeRow {
	collectionPath, addressable := row.draftFieldPath()
	if !addressable || collectionPath == "" {
		return nil
	}

	var rows []*treeRow
	if label == "[items]" {
		for index := range c.draft.ItemCount(collectionPath) {
			rows = append(rows, newItemRow(row, child, index))
		}
		return rows
	}
	for _, key := range c.draft.Keys(collectionPath) {
		rows = append(rows, newKeyRow(row, child, key))
	}
	return rows
}

// newItemRow is one instantiated array item's row: the item Node under its
// Draft-level [n] selector.
func newItemRow(parent *treeRow, item *schema.Node, index int) *treeRow {
	return &treeRow{
		node:   item,
		label:  itemRowLabel(parent, index),
		kind:   rowItem,
		index:  index,
		depth:  parent.depth + 1,
		parent: parent,
	}
}

// newKeyRow is one instantiated map entry's row: the value Node under its
// Draft-level quoted-key selector.
func newKeyRow(parent *treeRow, value *schema.Node, key string) *treeRow {
	return &treeRow{
		node:   value,
		label:  keyRowLabel(parent, key),
		kind:   rowKey,
		key:    key,
		depth:  parent.depth + 1,
		parent: parent,
	}
}

// itemRowLabel spells an item row's display name in the Draft-level bracket
// grammar, e.g. "containers[0]".
func itemRowLabel(parent *treeRow, index int) string {
	return fmt.Sprintf("%s[%d]", parent.label, index)
}

// keyRowLabel spells a key row's display name in the Draft-level bracket
// grammar, e.g. `labels["app"]`.
func keyRowLabel(parent *treeRow, key string) string {
	return parent.label + "[" + strconv.Quote(key) + "]"
}

// draftFieldPath spells the row's Draft-level Field Path — dotted fields,
// [n] item selectors, quoted key selectors — and whether it has one at all:
// a placeholder row and everything beneath it address nothing in the Draft
// (those positions become addressable through an instantiated item or key).
// The root's is the empty path.
func (row *treeRow) draftFieldPath() (string, bool) {
	if row.parent == nil {
		return "", true
	}
	prefix, addressable := row.parent.draftFieldPath()
	if !addressable {
		return "", false
	}
	switch row.kind {
	case rowPlaceholder:
		return "", false
	case rowItem:
		return fmt.Sprintf("%s[%d]", prefix, row.index), true
	case rowKey:
		return prefix + "[" + strconv.Quote(row.key) + "]", true
	default:
		if prefix == "" {
			return row.label, true
		}
		return prefix + "." + row.label, true
	}
}

// rowMissingRequired marks a required-but-unset field: the Draft's current
// missing required chain names the row's Draft-level Field Path — so an
// instantiated item's required fields flag as soon as the item exists
// (contextual requiredness). Placeholder rows address nothing in the Draft
// and never carry the marker.
func (c compose) rowMissingRequired(row *treeRow) bool {
	path, addressable := row.draftFieldPath()
	return addressable && path != "" && c.missing[path]
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
// value widget, the confirms and menus — only one body overlay is ever open
// at a time, because each opens from navigate mode — and the search
// surfaces; what remains is navigate mode's command map.
func (c compose) update(key tea.KeyMsg) (compose, tea.Cmd) {
	if key.String() == "ctrl+c" {
		// The conventional escape hatch quits immediately from anywhere —
		// even through the overlays and the exit menu, discarding the
		// Draft without a prompt (DESIGN.md — Exit ramp).
		return c, tea.Quit
	}

	// Any key acknowledges a non-fatal notice: the hint bar returns as
	// soon as browsing continues.
	c.notice = ""

	if view, cmd, handled := c.updateOverlay(key); handled {
		return view, cmd
	}
	return c.command(key)
}

// updateOverlay routes one key press into whichever overlay, prompt, or
// confirm is open, reporting false when none is — navigate mode's command
// map takes the key instead.
func (c compose) updateOverlay(key tea.KeyMsg) (compose, tea.Cmd, bool) {
	switch {
	case c.helpOpen:
		c.helpOpen = false
		return c, nil, true
	case c.editor != nil:
		return c.updateEditor(key), nil, true
	case c.keyPrompt != nil:
		return c.updateKeyPrompt(key), nil, true
	case c.gate != nil:
		view, cmd := c.updateGatePrompt(key)
		return view, cmd, true
	case c.resultsOpen:
		return c.updateResultsPane(key), nil, true
	case c.unset != nil:
		return c.updateUnsetConfirm(key), nil, true
	case c.pendingSwitch != nil:
		view, cmd := c.updateSwitchConfirm(key)
		return view, cmd, true
	case c.versionList != nil:
		view, cmd := c.updateVersionList(key)
		return view, cmd, true
	case c.exit != nil:
		view, cmd := c.updateExitMenu(key)
		return view, cmd, true
	case c.discardToPicker:
		view, cmd := c.updateDiscardConfirm(key)
		return view, cmd, true
	case c.searchOpen:
		return c.updateSearch(key), nil, true
	default:
		return c, nil, false
	}
}

// command applies one navigate-mode command key (DESIGN.md — Keybindings:
// command map); anything outside the map is tree navigation.
func (c compose) command(key tea.KeyMsg) (compose, tea.Cmd) {
	switch key.String() {
	case "q":
		return c.pressQuit()
	case "ctrl+d":
		// The EOF idiom: a direct emit-&-quit, no menu (DESIGN.md — Exit
		// ramp). An empty Draft emits the identity-only Manifest — the
		// sparse-emission contract's floor.
		return c.emitAndQuit()
	case "esc":
		return c.pressEscape()
	case "enter":
		return c.pressEnter(), nil
	case "a":
		return c.pressAdd(), nil
	case "d":
		return c.pressUnset(), nil
	case "e":
		return c.pressPopOut()
	case "v", "n", "r":
		// The manual Validate and its follow-ups (DESIGN.md — Command
		// map: `v` Validate): the dry-run request, the finding jump, and
		// the results pane.
		return c.validateCommand(key.String())
	case "V":
		// The version switch (DESIGN.md — Command map: `V` version
		// switch): values carry over by Field Path, a drop report
		// confirming anything that would not survive.
		return c.openVersionList(), nil
	case "?":
		c.helpOpen = true
		return c, nil
	case "/":
		return c.openSearch(), nil
	}

	c.navigate(key.String())
	return c, nil
}

// pressQuit is `q`, the single exit verb (DESIGN.md — Exit ramp): a
// non-empty Draft opens the three-way exit menu — emitting, discarding, and
// cancelling all stay one keypress away; an empty Draft quits as immediately
// as Ctrl-c.
func (c compose) pressQuit() (compose, tea.Cmd) {
	if len(c.draft.Instantiated()) == 0 {
		return c, tea.Quit
	}
	c.exit = &exitMenu{}
	return c, nil
}

// pressEscape is Esc, the documented way back to the Kind picker. A
// non-empty Draft would be discarded by returning, so it warns first
// (DESIGN.md — Compose lifecycle); an empty Draft returns silently.
func (c compose) pressEscape() (compose, tea.Cmd) {
	if len(c.draft.Instantiated()) == 0 {
		return c, func() tea.Msg { return returnToPickerMsg{} }
	}
	c.discardToPicker = true
	return c, nil
}

// updateEditor routes one key press into the open value widget: the confirm
// key confirms the value into the Draft, Esc cancels back to navigate mode
// without mutating, and every other key is the widget's own — navigate keys
// never move the tree while editing. The raw-YAML text area confirms on
// Ctrl-s instead of Enter, which types its newlines.
func (c compose) updateEditor(key tea.KeyMsg) compose {
	confirm := "enter"
	if c.editor.kind == editorRawYAML {
		confirm = "ctrl+s"
	}
	switch key.String() {
	case "esc":
		c.editor = nil
		return c
	case confirm:
		return c.confirmEditor()
	}
	edited := c.editor.update(key)
	c.editor = &edited
	return c
}

// confirmEditor confirms the widget's value into the Draft — the raw-YAML
// text area grafts its parsed buffer, every other widget Sets its scalar. A
// rejection — the widget's own parse, the graft's, or the Draft's
// schema-local check — renders inline in the widget and commits nothing; a
// confirmed value closes the widget and recomputes the Draft's completeness.
func (c compose) confirmEditor() compose {
	var err error
	if c.editor.kind == editorRawYAML {
		err = c.draft.GraftYAML(c.editor.path, c.editor.input)
	} else {
		var value any
		if value, err = c.editor.confirmValue(); err == nil {
			err = c.draft.Set(c.editor.path, value)
		}
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

// updateDiscardConfirm applies one key press to Esc's open discard confirm,
// in the modal grammar: Enter confirms the discard and returns to the Kind
// picker, Esc keeps composing, and everything else is inert.
func (c compose) updateDiscardConfirm(key tea.KeyMsg) (compose, tea.Cmd) {
	switch key.String() {
	case "enter":
		c.discardToPicker = false
		return c, func() tea.Msg { return returnToPickerMsg{} }
	case "esc":
		c.discardToPicker = false
	}
	return c, nil
}

// exitOption is one ramp of `q`'s three-way exit menu: its name and what
// choosing it does to the Draft.
type exitOption struct {
	name   string
	detail string
}

// exitOptions are the exit menu's ramps in menu order (DESIGN.md — Exit
// ramp): Emit & quit leads — the ramp composing exists for.
var exitOptions = []exitOption{
	{name: "Emit & quit", detail: "write the Manifest to stdout"},
	{name: "Discard & quit", detail: "quit with nothing emitted"},
	{name: "Cancel", detail: "keep composing"},
}

// exitMenu is `q`'s open three-way exit menu over a non-empty Draft: the
// cursor indexes the highlighted exitOptions ramp.
type exitMenu struct {
	cursor int
}

// updateExitMenu applies one key press to the open exit menu, in the modal
// grammar: ↑/↓ move the highlight, Enter confirms the highlighted ramp, Esc
// cancels back to composing, and everything else is inert (Ctrl-c never
// reaches here — it quits from anywhere first).
func (c compose) updateExitMenu(key tea.KeyMsg) (compose, tea.Cmd) {
	menu := *c.exit
	switch key.String() {
	case "up", "k":
		menu.cursor = max(menu.cursor-1, 0)
	case "down", "j":
		menu.cursor = min(menu.cursor+1, len(exitOptions)-1)
	case "esc":
		c.exit = nil
		return c, nil
	case "enter":
		c.exit = nil
		switch exitOptions[menu.cursor].name {
		case "Emit & quit":
			return c.emitAndQuit()
		case "Discard & quit":
			return c, tea.Quit
		default:
			return c, nil
		}
	}
	c.exit = &menu
	return c, nil
}

// emitAndQuit Emits the Draft as the Manifest and ends the Session on the
// emit ramp: the bytes travel to the Session shell as a typed message — the
// shell records them and quits, and the Manifest reaches stdout only after
// the alt screen closes, never from inside the TUI (DESIGN.md — Output). A
// failed emission surfaces as a non-fatal notice and keeps composing: the
// Draft is never lost to a rendering error.
func (c compose) emitAndQuit() (compose, tea.Cmd) {
	manifest, err := c.draft.Emit()
	if err != nil {
		c.notice = "emitting the Manifest failed: " + err.Error()
		return c, nil
	}
	return c, func() tea.Msg { return manifestEmittedMsg{manifest: manifest} }
}

// view renders the open exit menu as the body overlay: every ramp on its
// own line, the highlighted one carrying the cursor — the completeness
// status line keeps rendering beneath it, so "what would I be emitting" and
// "how complete is it" coexist.
func (m exitMenu) view() string {
	lines := []string{"quit composing — what happens to the Draft?", ""}
	for index, option := range exitOptions {
		cursor := "  "
		name := option.name
		if index == m.cursor {
			cursor = "> "
			name = highlightedStyle.Render(name)
		}
		lines = append(lines, cursor+name+" — "+option.detail)
	}
	return strings.Join(lines, "\n")
}

// keyPrompt is `a`'s inline key prompt over a map-shaped node: the collection
// row it adds to, the type-to-enter buffer, and the last confirm's inline
// rejection (a duplicate key, an empty one).
type keyPrompt struct {
	row       *treeRow
	input     string
	rejection string
}

// unsetConfirm is `d`'s destructive confirm: the row being unset, its
// Draft-level Field Path, and how many filled values the Draft reports the
// unset would discard — or, on a grafted subtree, the graft's parsed value,
// whose size speaks for the discard instead of the count (the Draft counts
// a whole graft as one).
type unsetConfirm struct {
	row   *treeRow
	path  string
	count int
	graft any
}

// prompt is the confirm's footer line, count included — the "how much am I
// about to lose" the Draft layer feeds the destructive key.
func (u unsetConfirm) prompt() string {
	if u.graft != nil {
		return fmt.Sprintf("discard the %s grafted at %s? y/enter discard · n/esc keep", graftSummary(u.graft), u.path)
	}
	noun := "values"
	if u.count == 1 {
		noun = "value"
	}
	return fmt.Sprintf("discard %d %s under %s? y/enter discard · n/esc keep", u.count, noun, u.path)
}

// pressAdd is `a` in navigate mode (DESIGN.md — Keybindings: mutation verbs):
// on an array node it appends an item; on a map-shaped node it opens the
// inline key prompt; anywhere else it is a no-op with a hint-bar flash — not
// an error state.
func (c compose) pressAdd() compose {
	row := c.focused()
	if row == nil || !c.loadRow(row) {
		return c
	}

	collectionPath, addressable := row.draftFieldPath()
	if !addressable {
		c.notice = row.node.FieldPath() + " sits under an uninstantiated collection — " +
			"a adds items and keys on the collection node itself"
		return c
	}
	items, value := collectionPlaceholders(row)
	switch {
	case items != nil:
		return c.appendItem(row, collectionPath)
	case value != nil:
		c.keyPrompt = &keyPrompt{row: row}
		return c
	default:
		c.notice = "a appends array items and adds map keys — " + rowDisplayName(row) +
			" is not an array or a map-shaped object"
		return c
	}
}

// appendItem instantiates the array's next item in the Draft, grows its [n]
// row before the [items] placeholder, and moves the focus into it.
func (c compose) appendItem(row *treeRow, collectionPath string) compose {
	if _, err := c.draft.AppendItem(collectionPath); err != nil {
		c.notice = err.Error()
		return c
	}

	item := newItemRow(row, sharedChildNode(row), c.draft.ItemCount(collectionPath)-1)
	c.insertInstantiatedRow(row, item)
	c.rebuildRows()
	c.focusRow(item)
	c.refreshCompleteness()
	return c
}

// updateKeyPrompt routes one key press into the open key prompt, under the
// modal grammar: Enter confirms the typed key into the Draft, Esc cancels
// without touching it, and printable keys type — any typing clears a
// lingering rejection, which belongs to the confirm it answered.
func (c compose) updateKeyPrompt(key tea.KeyMsg) compose {
	prompt := *c.keyPrompt
	switch {
	case key.String() == "esc":
		c.keyPrompt = nil
		return c
	case key.String() == "enter":
		return c.confirmKeyPrompt()
	case key.String() == "backspace":
		prompt.rejection = ""
		if prompt.input != "" {
			runes := []rune(prompt.input)
			prompt.input = string(runes[:len(runes)-1])
		}
	case key.Type == tea.KeySpace:
		prompt.rejection = ""
		prompt.input += " "
	case key.Type == tea.KeyRunes && !key.Alt:
		prompt.rejection = ""
		prompt.input += string(key.Runes)
	}
	c.keyPrompt = &prompt
	return c
}

// confirmKeyPrompt confirms the typed key into the Draft. A rejection — an
// empty key, or a key the Draft already holds — renders inline in the prompt
// and commits nothing; a confirmed key grows the entry's row, sorted among
// its key siblings, and moves the focus into it.
func (c compose) confirmKeyPrompt() compose {
	prompt := *c.keyPrompt
	if prompt.input == "" {
		prompt.rejection = "a map entry needs a key — type one, or Esc cancels"
		c.keyPrompt = &prompt
		return c
	}

	collectionPath, _ := prompt.row.draftFieldPath()
	if _, err := c.draft.AddKey(collectionPath, prompt.input); err != nil {
		prompt.rejection = err.Error()
		c.keyPrompt = &prompt
		return c
	}

	c.keyPrompt = nil
	entry := newKeyRow(prompt.row, sharedChildNode(prompt.row), prompt.input)
	c.insertInstantiatedRow(prompt.row, entry)
	c.rebuildRows()
	c.focusRow(entry)
	c.refreshCompleteness()
	return c
}

// viewLines renders the open key prompt for the detail pane.
func (p keyPrompt) viewLines() []string {
	lines := []string{
		highlightedStyle.Render(p.label()),
		"adding a map entry — type its key",
		"",
		"> " + p.input + "▏",
	}
	if p.rejection != "" {
		lines = append(lines, "", highlightedStyle.Render(p.rejection))
	}
	return lines
}

// label previews the entry the prompt is composing, in the Draft-level
// bracket grammar the confirmed row will carry.
func (p keyPrompt) label() string {
	return keyRowLabel(p.row, p.input)
}

// pressUnset is `d` in navigate mode (DESIGN.md — Keybindings: mutation
// verbs): unset — sparse semantics, back to the dimmed placeholder, never
// "set to empty". A set scalar unsets instantly; a subtree with filled
// descendants confirms with the Draft-reported discard count first; a node
// the Draft holds nothing at is a no-op.
func (c compose) pressUnset() compose {
	row := c.focused()
	if row == nil {
		return c
	}
	path, addressable := row.draftFieldPath()
	if !addressable || path == "" {
		return c
	}
	count, held := c.draft.DiscardCount(path)
	if !held {
		return c
	}

	value, filledHere := c.draft.ValueAt(path)
	if value.Type == schema.TypeRawYAML {
		// A graft is a whole subtree behind one Draft entry, so it takes
		// the subtree confirm — sized by the graft itself, not the count.
		c.unset = &unsetConfirm{row: row, path: path, count: count, graft: value.Data}
		return c
	}
	if count == 0 || (count == 1 && filledHere) {
		// The row's own value (or an instantiated-but-empty item or key):
		// nothing beneath it is lost, so the unset is instant.
		return c.performUnset(row, path)
	}
	c.unset = &unsetConfirm{row: row, path: path, count: count}
	return c
}

// updateUnsetConfirm applies one key press to the open destructive confirm:
// y/Enter discards the subtree, n/Esc keeps composing, and everything else
// is inert.
func (c compose) updateUnsetConfirm(key tea.KeyMsg) compose {
	confirm := *c.unset
	switch key.String() {
	case "enter", "y":
		c.unset = nil
		return c.performUnset(confirm.row, confirm.path)
	case "esc", "n":
		c.unset = nil
	}
	return c
}

// performUnset removes the entry from the Draft and reconciles the tree: an
// item or key row leaves the tree (an item's later siblings renumber down by
// one, per the Draft's renumbering contract), while a schema-defined field
// row stays — its subtree collapses and reloads from the Draft on the next
// expansion, so any instantiated rows beneath it are gone with the values.
func (c compose) performUnset(row *treeRow, path string) compose {
	if _, err := c.draft.Unset(path); err != nil {
		c.notice = err.Error()
		return c
	}

	switch row.kind {
	case rowItem, rowKey:
		removeInstantiatedRow(row)
	default:
		if !row.leaf {
			row.loaded, row.expanded, row.children = false, false, nil
			c.loadRow(row)
		}
	}
	c.rebuildRows()
	c.focusIndex(c.cursor)
	c.refreshCompleteness()
	return c
}

// removeInstantiatedRow deletes an item or key row from its parent's
// children; an item's later item siblings renumber down by one, mirroring
// the Draft's renumbering contract — their old paths now address their
// successors, and every descendant's Draft-level Field Path follows, because
// paths are spelled through the parent chain.
func removeInstantiatedRow(row *treeRow) {
	siblings := row.parent.children
	position := slices.Index(siblings, row)
	if position < 0 {
		return
	}
	row.parent.children = slices.Delete(siblings, position, position+1)

	if row.kind != rowItem {
		return
	}
	for _, sibling := range row.parent.children {
		if sibling.kind == rowItem && sibling.index > row.index {
			sibling.index--
			sibling.label = itemRowLabel(row.parent, sibling.index)
		}
	}
}

// insertInstantiatedRow places a fresh item or key row among the collection
// row's children — an item right before its [items] placeholder (items stay
// in index order), a key sorted among its key siblings — and expands the
// collection so the new row is visible and truthfully indicated.
func (c *compose) insertInstantiatedRow(row *treeRow, entry *treeRow) {
	position := len(row.children)
	for index, child := range row.children {
		if entry.kind == rowKey && child.kind == rowKey && child.key > entry.key {
			position = index
			break
		}
		if child.kind == rowPlaceholder && placeholderMatches(child, entry.kind) {
			position = index
			break
		}
	}
	row.children = slices.Insert(row.children, position, entry)
	c.loadRow(entry)
	c.expandRow(row)
}

// placeholderMatches pairs a placeholder row with the instantiated row kind
// it stands in for: "[items]" for items, "[value]" for keys.
func placeholderMatches(placeholder *treeRow, kind rowKind) bool {
	if kind == rowItem {
		return placeholder.label == "[items]"
	}
	return placeholder.label == "[value]"
}

// collectionPlaceholders finds a loaded row's "[items]" and "[value]"
// placeholder children — the structural mark of an array or a map-shaped
// object. A bare untyped or schema-blind node has neither, so the mutation
// verbs stay no-ops there.
func collectionPlaceholders(row *treeRow) (items, value *treeRow) {
	for _, child := range row.children {
		if child.kind != rowPlaceholder {
			continue
		}
		if child.label == "[items]" {
			items = child
		} else {
			value = child
		}
	}
	return items, value
}

// sharedChildNode is the collection row's shared child Node — the array's
// item schema or the map's value schema — that instantiated rows are grown
// over.
func sharedChildNode(row *treeRow) *schema.Node {
	items, value := collectionPlaceholders(row)
	if items != nil {
		return items.node
	}
	return value.node
}

// rowDisplayName names a row for notices: its Field Path when it has one,
// its label at the root.
func rowDisplayName(row *treeRow) string {
	if row.node.FieldPath() != "" {
		return row.node.FieldPath()
	}
	return row.label
}

// refreshCompleteness recomputes the Draft's completeness — the missing
// required Draft-level Field Paths given what the Draft has instantiated —
// after a confirmed mutation. Because every confirmed mutation funnels
// through here, it is also the marker lifecycle's one hook: the mutation
// outdates the last Validate (see markValidationOutdated).
func (c *compose) refreshCompleteness() {
	c.markValidationOutdated()
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

// openEditor opens the leaf's value widget: a schema-blind leaf opens the
// raw-YAML text area — the escape hatch (DESIGN.md — Flow §4) — and every
// other leaf its type-appropriate widget. A leaf no widget can serve leaves
// a one-line hint instead of opening anything: a leaf inside an
// uninstantiated placeholder subtree needs an item or key first (`a` on the
// collection), and a leaf without a scalar type has nothing to type into.
// An instantiated item or key leaf edits exactly like an ordinary leaf.
func (c compose) openEditor(row *treeRow) compose {
	meta, err := row.node.Metadata()
	if err != nil {
		// The detail pane already surfaces the resolution error.
		return c
	}
	path, addressable := row.draftFieldPath()
	if !addressable {
		c.notice = row.node.FieldPath() + " sits under an uninstantiated collection — " +
			"press a on the collection node to add its first item or key"
		return c
	}
	if meta.SchemaBlind {
		return c.openRawYAMLEditor(row, path, meta)
	}
	kind, editable := widgetFor(meta)
	if !editable {
		c.notice = "the Type Schema types " + row.node.FieldPath() + " as " + meta.Type +
			" — no value widget serves it"
		return c
	}

	value, filled := c.draft.ValueAt(path)
	editor := newFieldEditor(row, path, meta, kind, value, filled)
	c.editor = &editor
	return c
}

// openRawYAMLEditor opens the raw-YAML text area over a schema-blind leaf:
// a graft the Draft already holds prefills the buffer in its canonical
// spelling, so editing amends instead of retyping; otherwise it starts
// empty.
func (c compose) openRawYAMLEditor(row *treeRow, path string, meta schema.Metadata) compose {
	editor := fieldEditor{row: row, path: path, meta: meta, kind: editorRawYAML}
	if value, filled := c.draft.ValueAt(path); filled && value.Type == schema.TypeRawYAML {
		editor.input = strings.TrimRight(graftYAMLText(value.Data), "\n")
	}
	c.editor = &editor
	return c
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
	if c.versionList != nil {
		list := *c.versionList
		list.height = c.searchListHeight()
		list = list.follow()
		c.versionList = &list
	}
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
// form, extended by the focused row's Draft-level Field Path — bracket
// selectors included on instantiated items and keys — or its schema-level
// Field Path inside a placeholder subtree. At the root it shows the Kind
// alone, e.g. "apps/v1 Deployment".
func (c compose) breadcrumb() string {
	crumb := kindDisplayName(c.kind)
	row := c.focused()
	if row == nil {
		return crumb
	}
	if path, addressable := row.draftFieldPath(); addressable && path != "" {
		return crumb + " › " + path
	}
	if row.node.FieldPath() != "" {
		return crumb + " › " + row.node.FieldPath()
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

// statusLine is the persistent status line: the completeness segment
// (DESIGN.md — Flow §3: a status line tracks completeness), the
// required-to-Validate metadata flag — distinct from the schema-required
// count, and shown from session start — and the last Validate's state.
func (c compose) statusLine() string {
	segments := []string{c.completenessSegment()}
	if gate := c.validateGateSegment(); gate != "" {
		segments = append(segments, gate)
	}
	if state := c.validateStateSegment(); state != "" {
		segments = append(segments, state)
	}
	return strings.Join(segments, "   ")
}

// completenessSegment is the schema-required half of the status line: how
// many required fields the Draft is still missing, or the complete marker
// once none are.
func (c compose) completenessSegment() string {
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

// footer is the view's bottom line: an open confirm — the destructive
// unset's or the Draft discard's — a non-fatal notice until the next key
// press, the contextual hint bar otherwise.
func (c compose) footer() string {
	if c.unset != nil {
		return highlightedStyle.Render(c.unset.prompt())
	}
	if c.gate != nil {
		return highlightedStyle.Render(c.gate.prompt())
	}
	if c.pendingSwitch != nil {
		return highlightedStyle.Render(c.pendingSwitch.prompt())
	}
	if c.discardToPicker {
		return highlightedStyle.Render(discardToPickerPrompt)
	}
	if c.notice != "" {
		return highlightedStyle.Render(c.notice)
	}
	return dimmedStyle.Render(c.hints())
}

// hints is the contextual one-line hint bar: the open value widget's own
// hints in edit mode, the key prompt's while it is open, the exit menu's
// while it is open, the search overlay's while it is open, the
// navigate-mode hints otherwise.
func (c compose) hints() string {
	if c.editor != nil {
		return c.editor.hints()
	}
	if c.keyPrompt != nil {
		return keyPromptHints
	}
	if c.exit != nil {
		return exitMenuHints
	}
	if c.versionList != nil {
		return versionListHints
	}
	if c.resultsOpen {
		return resultsPaneHints
	}
	if c.searchOpen {
		return searchHints
	}
	return c.navigateHints()
}

// navigateHints prepends the verbs the compose view serves right now
// (DESIGN.md — Keybindings: the hint bar is contextual to the focused view):
// the Validate follow-ups `n` and `r` while findings exist, `a` on an array
// or map-shaped node, `d` wherever the Draft holds something to unset, `e`
// on a schema-blind node — and none where they would no-op.
func (c compose) navigateHints() string {
	verbs := c.validationVerbs()
	row := c.focused()
	if row == nil || !row.loaded {
		return verbs + composeHints
	}
	path, addressable := row.draftFieldPath()
	if !addressable || path == "" {
		return verbs + composeHints
	}
	return verbs + c.rowVerbs(row, path) + composeHints
}

// rowVerbs spells the hint-bar verbs an addressable, non-root row serves.
func (c compose) rowVerbs(row *treeRow, path string) string {
	verbs := ""
	if items, value := collectionPlaceholders(row); items != nil {
		verbs += "a append item · "
	} else if value != nil {
		verbs += "a add key · "
	}
	if _, held := c.draft.DiscardCount(path); held {
		verbs += "d unset · "
	}
	if meta, err := row.node.Metadata(); err == nil && meta.SchemaBlind {
		verbs += "e $EDITOR · "
	}
	return verbs
}

// bodyView renders the pane area: the `?` help overlay, the exit menu, or
// the `/` search overlay when one is open, the tree and detail panes
// otherwise.
func (c compose) bodyView() string {
	if c.helpOpen {
		return limitLines(helpText, c.bodyHeight())
	}
	if c.exit != nil {
		return limitLines(c.exit.view(), c.bodyHeight())
	}
	if c.resultsOpen {
		return limitLines(clipLines(c.resultsView(), c.width), c.bodyHeight())
	}
	if c.pendingSwitch != nil {
		return limitLines(clipLines(c.pendingSwitch.view(), c.width), c.bodyHeight())
	}
	if c.versionList != nil {
		return limitLines(clipLines(c.versionList.view(), c.width), c.bodyHeight())
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
	return clipLines(c.search.view(), c.width)
}

// clipLines clips every line of rendered text to the given width,
// ANSI-safely; zero width leaves them unbounded.
func clipLines(text string, width int) string {
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		lines[index] = clipLine(line, width)
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
	if c.rowHasFindings(row) {
		label += " " + findingMarker
	}

	return cursor + strings.Repeat("  ", row.depth) + indicator + label
}

// rowValue is the Draft state a leaf row shows after its label: the open
// widget's live buffer while the row is being edited, the key prompt's live
// buffer while the row is prompting, the value the Draft holds once one is
// set, or the schema default as a dimmed placeholder — visually distinct
// from a set value, and never in the output (DESIGN.md — Flow §6).
func (c compose) rowValue(row *treeRow) string {
	if c.editor != nil && c.editor.row == row {
		return ": " + c.editor.inlineValue()
	}
	if c.keyPrompt != nil && c.keyPrompt.row == row {
		return ` + ["` + c.keyPrompt.input + `▏"]`
	}
	if !row.loaded || !row.leaf || row.kind == rowPlaceholder {
		return ""
	}
	if spelled, filled := c.draftRowValue(row); filled {
		return spelled
	}
	meta, err := row.node.Metadata()
	if err != nil || meta.Default == nil {
		return ""
	}
	return dimmedStyle.Render(": " + renderScalar(meta.Default))
}

// draftRowValue spells the value the Draft holds at the row, when one is
// filled there — a grafted subtree rendering distinctly: opaque to the Type
// Schema, sized instead of spelled inline.
func (c compose) draftRowValue(row *treeRow) (string, bool) {
	path, addressable := row.draftFieldPath()
	if !addressable {
		return "", false
	}
	value, filled := c.draft.ValueAt(path)
	if !filled {
		return "", false
	}
	if value.Type == schema.TypeRawYAML {
		return ": " + graftSummary(value.Data), true
	}
	return ": " + renderScalar(value.Data), true
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
	if c.keyPrompt != nil {
		return c.keyPrompt.viewLines()
	}
	if row.expandErr != nil {
		return []string{highlightedStyle.Render(row.label), "", "expanding this field failed: " + row.expandErr.Error()}
	}

	meta, err := row.node.Metadata()
	if err != nil {
		return []string{highlightedStyle.Render(row.label), "", "reading this field's Type Schema failed: " + err.Error()}
	}
	lines := append(metadataLines(row, meta, c.rowMissingRequired(row)), c.graftDetailLines(row)...)
	return append(lines, c.findingDetailLines(row)...)
}

// graftDetailLines renders the raw-YAML graft the Draft holds at the focused
// row — its distinct summary and the grafted YAML itself — and nothing when
// no graft sits there.
func (c compose) graftDetailLines(row *treeRow) []string {
	path, addressable := row.draftFieldPath()
	if !addressable {
		return nil
	}
	value, filled := c.draft.ValueAt(path)
	if !filled || value.Type != schema.TypeRawYAML {
		return nil
	}
	lines := []string{"", "grafted: " + graftSummary(value.Data), ""}
	return append(lines, graftLines(value.Data)...)
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
