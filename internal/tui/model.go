package tui

import (
	"context"
	"maps"
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/internal/schema"
)

// KindSelectedMsg is the typed handoff from the Kind picker to the Session
// shell: Enter on the highlighted row emits it, carrying the selected Kind
// (GVK + group-version path) that the compose view opens on.
type KindSelectedMsg struct {
	Kind data.Kind
}

// DeepLink is the launch arg's resolved entry point (DESIGN.md — Flow §1:
// the k9s-plugin integration hook): the Kind the Session opens on directly,
// skipping the picker, and optionally a schema-level Field Path to land the
// focus on. It is resolved before the alt screen opens — an unknown kind
// token never reaches the shell.
type DeepLink struct {
	// Kind is the Kind the deep-linked Session opens on, already resolved
	// to its Preferred Version.
	Kind data.Kind

	// FieldPath is the schema-level Field Path to land the focus on via
	// the search overlay's landing rule; empty opens the Kind at the root
	// of its field tree.
	FieldPath string
}

// documentFetchedMsg delivers the parsed OpenAPI v3 group document a picker
// selection asked for. The lazy fetch runs as a tea.Cmd so the shell stays
// responsive in a loading state while the document travels and parses.
type documentFetchedMsg struct {
	kind     data.Kind
	document *schema.Document
}

// documentFetchFailedMsg surfaces a failed group-document fetch or parse as
// the shell's in-TUI error state: mid-Session failures never crash the alt
// screen — hard failures belong before it opens (DESIGN.md — Data layer).
type documentFetchFailedMsg struct {
	kind data.Kind
	err  error
}

// returnToPickerMsg reopens the Kind picker from the compose view. The
// compose view sends it only once returning is safe: silently on an empty
// Draft, and after the discard confirm on a non-empty one (DESIGN.md —
// Compose lifecycle).
type returnToPickerMsg struct{}

// manifestEmittedMsg ends the Session on the emit ramp: it carries the
// Emitted Manifest's bytes out of the compose view — Ctrl-d's direct
// emit-&-quit or the exit menu's Emit & quit — for the shell to record
// before quitting. Stdout stays untouched until the alt screen closes
// (DESIGN.md — Output): the TUI never writes the Manifest itself.
type manifestEmittedMsg struct {
	manifest []byte
}

// sessionView names the view the Session shell has open.
type sessionView int

const (
	// pickingKind is the opening view: the Kind picker.
	pickingKind sessionView = iota
	// fetchingDocument is the loading state between a picker selection
	// and the compose view: the Kind's group document is fetching lazily.
	fetchingDocument
	// composing is the compose view over the selected Kind.
	composing
	// composeFailed is the in-TUI error state for a fetch or parse that
	// failed mid-Session.
	composeFailed
)

// Model is the Session shell: the root Bubble Tea model for one Session.
// It opens on the Kind picker — the browsable Kind list is discovered
// before the shell starts — and a selection lazily fetches that Kind's
// group document through the Session's Fetcher, then opens the read-only
// compose view over its field tree.
type Model struct {
	// ctx bounds the Session's mid-flight fetches: commands this shell
	// starts stop when the program's context does.
	ctx context.Context

	// picker is the Kind picker, the shell's opening view.
	picker picker

	// fetcher sources OpenAPI v3 group documents lazily, on the first
	// open of each group (DESIGN.md — Data layer); production wiring
	// hands over the hash-validated disk cache in front of the live
	// client (ADR-0002), transparently behind this seam.
	fetcher data.Fetcher

	// contentHashes indexes the live /openapi/v3 index's server content
	// hashes by group-version path, so every lazy fetch is addressed by
	// the (path, hash) pair that keeps the cache honest.
	contentHashes map[string]string

	// documents memoizes parsed group documents by group-version path:
	// within one Session, a group parses once no matter how many of its
	// Kinds are opened.
	documents map[string]*schema.Document

	// view is the open view; kind, fetchErr, and compose carry the
	// selected Kind's state once the picker hands it off.
	view     sessionView
	kind     data.Kind
	fetchErr error
	compose  compose

	// pendingFieldPath is the deep link's deferred landing: the group
	// document fetches asynchronously, so the Field Path jump applies when
	// the document lands and the compose view opens — never at launch.
	pendingFieldPath string

	// emitted and manifest record the Session's emit decision: set once,
	// only when the Session ends on an emit ramp — Ctrl-d or the exit
	// menu's Emit & quit — carrying the Emitted Manifest's bytes out to
	// the caller. Every discard ramp leaves them zero.
	emitted  bool
	manifest []byte

	// width and height are the terminal size from the last
	// tea.WindowSizeMsg, replayed onto views as they open.
	width  int
	height int
}

// New builds the Session shell over what the Session resolved before the
// alt screen opened: the browsable Kind list from discovery, the Fetcher
// that sources group documents, the live /openapi/v3 index whose content
// hashes address every lazy fetch, and the launch arg's resolved deep link
// when one was given — a deep-linked Session skips the picker and opens on
// the linked Kind directly (nil launches on the picker as usual).
func New(ctx context.Context, kinds []data.Kind, fetcher data.Fetcher, index []data.GroupVersion, link *DeepLink) Model {
	contentHashes := make(map[string]string, len(index))
	for _, group := range index {
		contentHashes[group.Path] = group.ContentHash
	}

	model := Model{
		ctx:           ctx,
		picker:        newPicker(kinds),
		fetcher:       fetcher,
		contentHashes: contentHashes,
		documents:     map[string]*schema.Document{},
	}

	if link != nil {
		model.view = fetchingDocument
		model.kind = link.Kind
		model.pendingFieldPath = link.FieldPath
	}

	return model
}

// Init starts the Session shell. Launching on the picker awaits nothing —
// the live index and the browsable Kind list are both resolved before the
// program starts. A deep-linked Session instead starts in the loading
// state, fetching the linked Kind's group document exactly as a picker
// selection would.
func (m Model) Init() tea.Cmd {
	if m.view == fetchingDocument {
		return m.fetchDocumentCmd(m.kind)
	}
	return nil
}

// Update applies one message to the Session shell. Keys route to the open
// view; the typed messages carry the Session's transitions — the picker's
// handoff, the lazy document fetch landing or failing, and the compose
// view's return to the picker.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.picker = m.picker.resize(msg.Height)
		m.compose = m.compose.resize(msg.Width, msg.Height)
		return m, nil
	case KindSelectedMsg:
		return m.openKind(msg.Kind)
	case documentFetchedMsg:
		return m.documentFetched(msg)
	case documentFetchFailedMsg:
		return m.documentFetchFailed(msg)
	case returnToPickerMsg:
		return m.returnToPicker(), nil
	case manifestEmittedMsg:
		// The emit ramp: record the decision and the bytes, then quit —
		// the caller writes the Manifest to stdout after the alt screen
		// closes (DESIGN.md — Output).
		m.emitted, m.manifest = true, msg.manifest
		return m, tea.Quit
	case editorFinishedMsg:
		if m.view == composing {
			m.compose = m.compose.editorFinished(msg)
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// openKind starts opening the selected Kind's compose view: a group the
// Session already parsed opens immediately, anything else transitions to
// the loading state and fetches the group document as a command.
func (m Model) openKind(kind data.Kind) (tea.Model, tea.Cmd) {
	m.kind = kind

	if document, parsed := m.documents[kind.GroupVersionPath]; parsed {
		return m.openCompose(document), nil
	}

	m.view = fetchingDocument
	return m, m.fetchDocumentCmd(kind)
}

// fetchDocumentCmd fetches the Kind's group document through the Session's
// Fetcher — lazily, on the group's first open, addressed by the live
// index's (group-version path, content hash) pair — and parses it off the
// Update loop. Failures come back as the in-TUI error state's message.
func (m Model) fetchDocumentCmd(kind data.Kind) tea.Cmd {
	ctx, fetcher := m.ctx, m.fetcher
	contentHash := m.contentHashes[kind.GroupVersionPath]

	return func() tea.Msg {
		raw, err := fetcher.FetchGroupDocument(ctx, kind.GroupVersionPath, contentHash)
		if err != nil {
			return documentFetchFailedMsg{kind: kind, err: err}
		}
		document, err := schema.ParseDocument(raw)
		if err != nil {
			return documentFetchFailedMsg{kind: kind, err: err}
		}
		return documentFetchedMsg{kind: kind, document: document}
	}
}

// documentFetched memoizes the parsed group document and opens the compose
// view — unless the Session has already moved on (for example, Esc went
// back to the picker while the fetch was in flight), in which case the
// parse is kept for the group's next open and nothing else happens.
func (m Model) documentFetched(msg documentFetchedMsg) (tea.Model, tea.Cmd) {
	m.documents[msg.kind.GroupVersionPath] = msg.document

	if m.view != fetchingDocument || msg.kind.GVK != m.kind.GVK {
		return m, nil
	}
	return m.openCompose(msg.document), nil
}

// documentFetchFailed transitions to the in-TUI error state — never a
// crash — when the awaited fetch or parse fails; a stale failure from a
// Kind the Session already left is dropped.
func (m Model) documentFetchFailed(msg documentFetchFailedMsg) (tea.Model, tea.Cmd) {
	if m.view != fetchingDocument || msg.kind.GVK != m.kind.GVK {
		return m, nil
	}

	m.view = composeFailed
	m.fetchErr = msg.err
	return m, nil
}

// openCompose grows the compose view from the parsed group document; a
// document that cannot serve the Kind (no Type Schema, an unexpandable
// root) lands in the in-TUI error state instead. The deep link's deferred
// Field Path landing applies here — the first time the linked Kind's
// document lands — and never again.
func (m Model) openCompose(document *schema.Document) Model {
	view, err := newCompose(m.kind, document)
	if err != nil {
		m.view = composeFailed
		m.fetchErr = err
		return m
	}

	if m.pendingFieldPath != "" {
		view = view.landDeepLink(m.pendingFieldPath)
		m.pendingFieldPath = ""
	}

	m.compose = view.resize(m.width, m.height)
	m.view = composing
	m.fetchErr = nil
	return m
}

// returnToPicker reopens the Kind picker, dropping the selection state —
// the compose view's Draft and an unapplied deep-link landing included
// (the compose view has already confirmed a non-empty Draft's discard); a
// still-in-flight fetch resolves into the document memo and nothing more.
func (m Model) returnToPicker() Model {
	m.view = pickingKind
	m.kind = data.Kind{}
	m.fetchErr = nil
	m.compose = compose{}
	m.pendingFieldPath = ""
	return m
}

// handleKey routes one key press to the open view.
func (m Model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.view {
	case pickingKind:
		var cmd tea.Cmd
		m.picker, cmd = m.picker.update(key)
		return m, cmd
	case composing:
		var cmd tea.Cmd
		m.compose, cmd = m.compose.update(key)
		return m, cmd
	default:
		return m.transitKey(key)
	}
}

// transitKey is the loading and error states' key grammar: Esc returns to
// the Kind picker (abandoning the selection), and the empty-Draft exit
// rules apply — `q` and Ctrl-c quit immediately, no prompt (DESIGN.md —
// Exit ramp).
func (m Model) transitKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m.returnToPicker(), nil
	}
	return m, nil
}

// View renders the open view: the Kind picker, the loading or error state
// between picker and compose, or the compose view itself.
func (m Model) View() string {
	switch m.view {
	case pickingKind:
		return m.picker.view()
	case fetchingDocument:
		return m.transitView("fetching the " + m.kind.GroupVersionPath +
			" OpenAPI v3 Document — a group's Type Schemas load lazily on its first open")
	case composeFailed:
		return m.transitView("opening " + kindDisplayName(m.kind) + " failed: " + m.fetchErr.Error())
	default:
		return m.compose.view()
	}
}

// transitView renders the loading and error states: the selected Kind as
// the breadcrumb, the state's message, and the contextual hint bar.
func (m Model) transitView(body string) string {
	return clipLine(kindDisplayName(m.kind), m.width) + "\n\n" +
		clipLine(body, m.width) + "\n\n" +
		clipLine(dimmedStyle.Render(transitHints), m.width) + "\n"
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
	if m.view != pickingKind {
		return data.Kind{}, false
	}

	matches := m.picker.matches()
	if len(matches) == 0 {
		return data.Kind{}, false
	}

	return matches[m.picker.cursor], true
}

// SelectedKind returns the Kind the picker handed off — loading, failed,
// or composing — and false while the picker is still open.
func (m Model) SelectedKind() (data.Kind, bool) {
	if m.view == pickingKind {
		return data.Kind{}, false
	}
	return m.kind, true
}

// FetchingDocument reports whether the shell is in the loading state,
// awaiting the selected Kind's group document.
func (m Model) FetchingDocument() bool {
	return m.view == fetchingDocument
}

// ComposeOpen reports whether the compose view is open.
func (m Model) ComposeOpen() bool {
	return m.view == composing
}

// ComposeError returns the in-TUI error state's message, and false when
// the Session is not in it.
func (m Model) ComposeError() (string, bool) {
	if m.view != composeFailed {
		return "", false
	}
	return m.fetchErr.Error(), true
}

// Breadcrumb returns the persistent breadcrumb: the selected Kind in
// glossary form, extended by the focused node's schema-level Field Path in
// the compose view — empty while the picker is open.
func (m Model) Breadcrumb() string {
	switch m.view {
	case pickingKind:
		return ""
	case composing:
		return m.compose.breadcrumb()
	default:
		return kindDisplayName(m.kind)
	}
}

// FocusedFieldPath returns the focused node's schema-level Field Path —
// empty at the root of the field tree, and when compose is not open.
func (m Model) FocusedFieldPath() string {
	if m.view != composing {
		return ""
	}
	if row := m.compose.focused(); row != nil {
		return row.node.FieldPath()
	}
	return ""
}

// VisibleFieldPaths returns the schema-level Field Path of every visible
// tree row, in tree order — the root's is empty, and an array's item row
// (or a map's value row) shares its parent's.
func (m Model) VisibleFieldPaths() []string {
	if m.view != composing {
		return nil
	}

	paths := make([]string, 0, len(m.compose.rows))
	for _, row := range m.compose.rows {
		paths = append(paths, row.node.FieldPath())
	}
	return paths
}

// HelpOpen reports whether the `?` full-map help overlay is open.
func (m Model) HelpOpen() bool {
	return m.view == composing && m.compose.helpOpen
}

// SearchOpen reports whether the compose view's `/` field-search overlay
// is open.
func (m Model) SearchOpen() bool {
	return m.view == composing && m.compose.searchOpen
}

// SearchFilter returns the search overlay's active type-to-filter query —
// empty when the overlay is not open.
func (m Model) SearchFilter() string {
	if !m.SearchOpen() {
		return ""
	}
	return m.compose.search.filter
}

// SearchMatches returns the search overlay's ranked matches for the active
// filter: every candidate in tree order when the filter is empty, tighter
// and shorter matches first otherwise, each carrying the rune indices the
// filter matched.
func (m Model) SearchMatches() []SearchMatch {
	if !m.SearchOpen() {
		return nil
	}
	return m.compose.search.matches()
}

// HighlightedSearchMatch returns the match the search overlay's selection
// sits on, and false when the overlay is not open or nothing matches.
func (m Model) HighlightedSearchMatch() (SearchMatch, bool) {
	if !m.SearchOpen() {
		return SearchMatch{}, false
	}
	return m.compose.search.highlighted()
}

// Notice returns the compose view's non-fatal notice — a deep-link Field
// Path the Type Schema doesn't define — and false when none is showing.
func (m Model) Notice() (string, bool) {
	if m.view != composing || m.compose.notice == "" {
		return "", false
	}
	return m.compose.notice, true
}

// MissingRequiredFieldPaths returns the required-but-unset Field Paths the
// compose view flags: the Draft's contextual requiredness over what it has
// instantiated, sorted.
func (m Model) MissingRequiredFieldPaths() []string {
	if m.view != composing {
		return nil
	}
	return slices.Sorted(maps.Keys(m.compose.missing))
}

// Editing reports whether the compose view is in edit mode — a leaf's value
// widget is open.
func (m Model) Editing() bool {
	return m.view == composing && m.compose.editor != nil
}

// FocusedDraftPath returns the focused row's Draft-level Field Path —
// bracket selectors included on instantiated items and keys. Empty at the
// root, on the placeholder rows and everything beneath them (those
// positions address nothing in the Draft), and when compose is not open.
func (m Model) FocusedDraftPath() string {
	if m.view != composing {
		return ""
	}
	row := m.compose.focused()
	if row == nil {
		return ""
	}
	path, addressable := row.draftFieldPath()
	if !addressable {
		return ""
	}
	return path
}

// PromptingForKey reports whether `a` on a map-shaped node is prompting
// inline for the new entry's key.
func (m Model) PromptingForKey() bool {
	return m.view == composing && m.compose.keyPrompt != nil
}

// ConfirmingUnset reports whether `d` is confirming the discard of a subtree
// with filled descendants.
func (m Model) ConfirmingUnset() bool {
	return m.view == composing && m.compose.unset != nil
}

// ConfirmingDiscard reports whether the compose view is asking to confirm
// that Esc-to-picker may discard the non-empty Draft.
func (m Model) ConfirmingDiscard() bool {
	return m.view == composing && m.compose.discardToPicker
}

// ExitMenuOpen reports whether `q`'s three-way exit menu is open over the
// compose view's non-empty Draft.
func (m Model) ExitMenuOpen() bool {
	return m.view == composing && m.compose.exit != nil
}

// HighlightedExitOption returns the exit menu ramp the highlight sits on,
// and false when the menu is not open.
func (m Model) HighlightedExitOption() (string, bool) {
	if !m.ExitMenuOpen() {
		return "", false
	}
	return exitOptions[m.compose.exit.cursor].name, true
}

// EmittedManifest returns the Manifest bytes the Session recorded on an
// emit ramp, and false when the Session has not emitted — every discard
// ramp, and any Session still running.
func (m Model) EmittedManifest() ([]byte, bool) {
	if !m.emitted {
		return nil, false
	}
	return m.manifest, true
}

// DraftValueAt returns the normalized data the Draft holds at a Draft-level
// Field Path, and false when nothing is filled there — or when the compose
// view is not open.
func (m Model) DraftValueAt(fieldPath string) (any, bool) {
	if m.view != composing {
		return nil, false
	}
	value, filled := m.compose.draft.ValueAt(fieldPath)
	if !filled {
		return nil, false
	}
	return value.Data, true
}
