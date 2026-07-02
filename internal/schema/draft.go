package schema

import (
	"fmt"
	"strconv"

	"k8s.io/kube-openapi/pkg/validation/spec"
)

// Draft is the in-progress state of composing (CONTEXT.md): the values filled
// so far against one Kind's Type Schema, before being Emitted as a Manifest.
// A Draft is sparse — it holds exactly the entries composing has made,
// nothing else — and purely data: every mutation is checked against the
// Kind's field tree, but nothing here touches a cluster or a terminal.
// Drafts are ephemeral by design; nothing persists across processes.
type Draft struct {
	root    *Node
	entries *draftEntry
}

// NewDraft binds an empty Draft to a Kind's field tree — the root Node grown
// by Document.FieldTree. Every Draft-level Field Path is resolved and checked
// against that tree.
func NewDraft(root *Node) *Draft {
	return &Draft{root: root, entries: &draftEntry{}}
}

// Set fills one scalar value at a Draft-level Field Path, instantiating every
// position along the path implicitly — setting spec.replicas instantiates
// spec, and an item index may address an existing item or the next free one,
// which appends implicitly (a Draft's arrays stay dense). The value is
// checked schema-locally first (DESIGN.md — Flow §6): its type
// (int-or-string in both spellings), enum membership, pattern, and numeric or
// length bounds; a rejected set leaves the Draft untouched. Cross-field rules
// stay server-side Validate's business. Setting where a value already sits
// replaces it.
func (d *Draft) Set(fieldPath string, value any) error {
	segments, node, err := d.resolveTarget(fieldPath)
	if err != nil {
		return err
	}
	checked, err := node.checkScalar(fieldPath, value)
	if err != nil {
		return err
	}
	entry, err := d.entries.materialize(segments)
	if err != nil {
		return err
	}
	entry.value = &checked
	return nil
}

// ValueAt reads back the value filled at a Draft-level Field Path. Only
// filled entries answer: structural positions, instantiated-but-empty items
// and keys, positions inside a raw-YAML graft (opaque by design), and paths
// the Draft cannot place all read back as unfilled.
func (d *Draft) ValueAt(fieldPath string) (Value, bool) {
	segments, err := splitDraftPath(fieldPath)
	if err != nil {
		return Value{}, false
	}
	entry := d.entries
	for _, segment := range segments {
		if entry = entry.lookup(segment); entry == nil {
			return Value{}, false
		}
	}
	if entry.value == nil {
		return Value{}, false
	}
	return *entry.value, true
}

// Unset removes the entry at a Draft-level Field Path entirely — sparse
// semantics: never "set to empty" — and reports how many filled values were
// discarded with it (a raw-YAML graft counts as one), feeding the TUI's
// destructive-key confirm. Unsetting an array item removes the position
// itself: the items after it renumber down by one, so their old paths now
// address their successors — that is the renumbering contract. Ancestor
// fields left holding nothing de-instantiate implicitly, mirroring how
// setting instantiated them; items and keys were explicit acts, so they leave
// the Draft only when unset themselves. Unsetting a path the Draft holds
// nothing at is an error.
func (d *Draft) Unset(fieldPath string) (int, error) {
	segments, err := splitDraftPath(fieldPath)
	if err != nil {
		return 0, err
	}
	return d.entries.remove(segments, "")
}

// AppendItem instantiates the next item of the array at a Draft-level Field
// Path, returning the new item's path — its [n] selector. The item starts
// empty but instantiated, so the item schema's required fields count as
// missing (contextual requiredness), and the array's ancestors instantiate
// implicitly. Appending anywhere but an array position is an error.
func (d *Draft) AppendItem(fieldPath string) (string, error) {
	segments, node, err := d.resolveTarget(fieldPath)
	if err != nil {
		return "", err
	}
	if err = node.requireArray(fieldPath); err != nil {
		return "", err
	}
	entry, err := d.entries.materialize(segments)
	if err != nil {
		return "", err
	}
	entry.items = append(entry.items, &draftEntry{})
	return fmt.Sprintf("%s[%d]", fieldPath, len(entry.items)-1), nil
}

// AddKey instantiates one map key at the map-shaped object a Draft-level
// Field Path addresses, returning the new entry's path — its quoted-key
// selector. The entry starts empty but instantiated, so the value schema's
// required fields count as missing, and the map's ancestors instantiate
// implicitly. Adding a key the Draft already holds, or adding anywhere but a
// map-shaped position, is an error.
func (d *Draft) AddKey(fieldPath, key string) (string, error) {
	segments, node, err := d.resolveTarget(fieldPath)
	if err != nil {
		return "", err
	}
	if err = node.requireMap(fieldPath); err != nil {
		return "", err
	}
	entry, err := d.entries.materialize(segments)
	if err != nil {
		return "", err
	}
	keyPath := fieldPath + "[" + strconv.Quote(key) + "]"
	if entry.keys[key] != nil {
		return "", fmt.Errorf("the Draft already holds %s: set its value or unset it first", describeDraftPosition(keyPath))
	}
	if entry.keys == nil {
		entry.keys = map[string]*draftEntry{}
	}
	entry.keys[key] = &draftEntry{}
	return keyPath, nil
}

// GraftYAML parses raw YAML and grafts the parsed value at a schema-blind
// position (Metadata().SchemaBlind) — the raw-YAML escape hatch for subtrees
// the Type Schema says nothing about (DESIGN.md — Flow §4). The grafted value
// is opaque: its leaf paths count as instantiated, but nothing inside it is
// ever schema-checked — server-side Validate is the safety net. Grafting
// where a graft already sits replaces it; grafting where the Type Schema
// describes the position is an error.
func (d *Draft) GraftYAML(fieldPath, rawYAML string) error {
	segments, node, err := d.resolveTarget(fieldPath)
	if err != nil {
		return err
	}
	if err = node.requireSchemaBlind(fieldPath); err != nil {
		return err
	}
	parsed, err := parseGraft(fieldPath, rawYAML)
	if err != nil {
		return err
	}
	entry, err := d.entries.materialize(segments)
	if err != nil {
		return err
	}
	entry.value = &Value{Type: TypeRawYAML, Data: parsed}
	return nil
}

// Instantiated spells the Draft's instantiated Draft-level Field Paths in
// exactly the grammar MissingRequired accepts — dotted fields, [n] item
// selectors, quoted key selectors. Ancestors are implied by their
// descendants, so only the terminal positions are listed: filled values (a
// raw-YAML graft contributes its leaf paths) and instantiated-but-empty items
// and keys. Order is deterministic: fields and keys sorted, items by index.
func (d *Draft) Instantiated() []string {
	var paths []string
	d.entries.collectInstantiated("", &paths)
	return paths
}

// MissingRequired computes the Draft's completeness: the required Draft-level
// Field Paths currently missing given what the Draft has instantiated,
// delegating to the field tree's contextual requiredness (DESIGN.md — Flow
// §3). An empty result is a complete Draft.
func (d *Draft) MissingRequired() ([]string, error) {
	return d.root.MissingRequired(d.Instantiated())
}

// resolveTarget parses a Draft-level Field Path and resolves the Node it
// addresses, enforcing the Draft's structural grammar segment by segment.
func (d *Draft) resolveTarget(fieldPath string) ([]draftSegment, *Node, error) {
	segments, err := splitDraftPath(fieldPath)
	if err != nil {
		return nil, nil, err
	}
	node := d.root
	spelled := ""
	for _, segment := range segments {
		next, err := node.stepInto(segment, spelled)
		if err != nil {
			return nil, nil, err
		}
		node, spelled = next, spellSegment(spelled, segment)
	}
	return segments, node, nil
}

// stepInto resolves one Draft-level Field Path segment against the field
// tree, enforcing the Draft's structural grammar: dots address schema-defined
// fields, item indexes address arrays, quoted keys address map-shaped
// objects — and nothing descends beneath a schema-blind position, whose graft
// is opaque.
func (n *Node) stepInto(segment draftSegment, prefix string) (*Node, error) {
	chain, err := n.resolveChain(n.schema)
	if err != nil {
		return nil, err
	}
	if isSchemaBlind(chain) {
		return nil, blindPositionError(prefix)
	}
	resolved := concreteSchema(chain)
	item, value := itemSchema(resolved), valueSchema(resolved)
	switch segment.kind {
	case indexSegment:
		if item == nil {
			return nil, indexMismatch(prefix, value != nil)
		}
		return &Node{doc: n.doc, path: n.path, schema: item}, nil
	case keySegment:
		if value == nil {
			return nil, keyMismatch(prefix, item != nil)
		}
		return &Node{doc: n.doc, path: n.path, schema: value}, nil
	default:
		if item != nil {
			return nil, fmt.Errorf("%s is an array: a Draft addresses its items with an index selector before any field",
				describeDraftPosition(prefix))
		}
		if value != nil {
			return nil, fmt.Errorf("%s is a map-shaped object: a Draft addresses its values with a quoted key selector before any field",
				describeDraftPosition(prefix))
		}
		return n.Child(segment.field)
	}
}

// requireArray insists the node is an array position a Draft can append items
// to; a schema-blind position routes to the raw-YAML graft instead.
func (n *Node) requireArray(draftPath string) error {
	chain, err := n.resolveChain(n.schema)
	if err != nil {
		return err
	}
	if isSchemaBlind(chain) {
		return blindPositionError(draftPath)
	}
	if itemSchema(concreteSchema(chain)) == nil {
		return fmt.Errorf("%s is not an array: a Draft appends items only to an array position", describeDraftPosition(draftPath))
	}
	return nil
}

// requireMap insists the node is a map-shaped object a Draft can add keys to;
// a schema-blind position routes to the raw-YAML graft instead.
func (n *Node) requireMap(draftPath string) error {
	chain, err := n.resolveChain(n.schema)
	if err != nil {
		return err
	}
	if isSchemaBlind(chain) {
		return blindPositionError(draftPath)
	}
	if valueSchema(concreteSchema(chain)) == nil {
		return fmt.Errorf("%s is not a map-shaped object: a Draft adds keys only to a map-shaped position", describeDraftPosition(draftPath))
	}
	return nil
}

// requireSchemaBlind insists the node is a schema-blind position — the only
// place raw YAML grafts.
func (n *Node) requireSchemaBlind(draftPath string) error {
	chain, err := n.resolveChain(n.schema)
	if err != nil {
		return err
	}
	if isSchemaBlind(chain) {
		return nil
	}
	displayType, err := n.displayType(chain, map[*spec.Schema]bool{})
	if err != nil {
		return err
	}
	return fmt.Errorf("the Type Schema describes %s (%s): raw YAML grafts only onto a schema-blind position",
		describeDraftPosition(draftPath), displayType)
}

// spellSegment extends a spelled Draft-level Field Path by one segment, in
// the canonical grammar: dotted fields, [n] item selectors, quoted keys.
func spellSegment(prefix string, segment draftSegment) string {
	switch segment.kind {
	case indexSegment:
		return fmt.Sprintf("%s[%d]", prefix, segment.index)
	case keySegment:
		return prefix + "[" + strconv.Quote(segment.key) + "]"
	default:
		return joinFieldPath(prefix, segment.field)
	}
}

// blindPositionError says what a Draft does at a schema-blind position:
// grafts raw YAML there — nothing schema-guided, at it or beneath it.
func blindPositionError(draftPath string) error {
	return fmt.Errorf("the Type Schema is blind at %s: a Draft grafts raw YAML there instead of schema-guided entries",
		describeDraftPosition(draftPath))
}

// indexMismatch explains an item index landing where no array is.
func indexMismatch(prefix string, mapShaped bool) error {
	if mapShaped {
		return fmt.Errorf("%s is a map-shaped object: a Draft addresses its values with a quoted key selector, not an item index",
			describeDraftPosition(prefix))
	}
	return notACollection(prefix)
}

// keyMismatch explains a quoted map key landing where no map-shaped object is.
func keyMismatch(prefix string, array bool) error {
	if array {
		return fmt.Errorf("%s is an array: a Draft addresses its items with an index selector, not a quoted map key",
			describeDraftPosition(prefix))
	}
	return notACollection(prefix)
}

// notACollection mirrors contextual requiredness' complaint: brackets belong
// to arrays and map-shaped objects only.
func notACollection(prefix string) error {
	return fmt.Errorf("brackets address array items and map keys in a Draft, but %s is not an array or a map-shaped object",
		describeDraftPosition(prefix))
}
