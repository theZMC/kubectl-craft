package schema

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"k8s.io/kube-openapi/pkg/validation/spec"
)

// MissingRequired computes contextual requiredness (DESIGN.md — Flow §3):
// given the Draft-level Field Paths the Draft has instantiated, it returns
// the required Field Paths that are currently missing, ordered by tree
// position. Requiredness follows JSON Schema semantics — the root-level
// required chain counts as missing immediately, but required fields nested
// inside objects, array items, or map values the Draft hasn't instantiated
// don't (containers[0].name is missing only once a container item exists).
//
// The walk is as lazy as the tree itself: only required fields and
// instantiated positions are ever expanded, so a self-referential Type
// Schema (JSONSchemaProps) never forces full tree materialization.
func (n *Node) MissingRequired(instantiated []string) ([]string, error) {
	draft, err := parseDraftPaths(instantiated)
	if err != nil {
		return nil, err
	}
	var missing []string
	if err := n.collectMissing(draft, "", &missing); err != nil {
		return nil, err
	}
	return missing, nil
}

// collectMissing walks one present position of the Draft — the root, an
// instantiated field, array item, or map value — appending the missing
// required Draft-level Field Paths beneath it in tree order: siblings by
// field name, items by index, keys lexically.
func (n *Node) collectMissing(position *draftPosition, draftPath string, missing *[]string) error {
	chain, err := n.resolveChain(n.schema)
	if err != nil {
		return err
	}
	if isSchemaBlind(chain) {
		// The Type Schema says nothing about this subtree; composing
		// routes it to the raw-YAML escape hatch, so schema-guided
		// requiredness has nothing to track beneath it.
		return nil
	}
	resolved := concreteSchema(chain)

	if item := itemSchema(resolved); item != nil {
		return n.collectItems(item, position, draftPath, missing)
	}
	if value := valueSchema(resolved); value != nil {
		return n.collectValues(value, position, draftPath, missing)
	}
	if len(position.items) > 0 || len(position.keys) > 0 {
		return fmt.Errorf("brackets address array items and map keys in a Draft, but %s is not an array or a map-shaped object",
			describeDraftPosition(draftPath))
	}
	return n.collectFields(resolved, position, draftPath, missing)
}

// collectFields walks the fields contextual requiredness looks at beneath a
// present object position: instantiated fields descend as present positions;
// declared-required fields the Draft hasn't instantiated are missing, along
// with their assured chains.
func (n *Node) collectFields(resolved *spec.Schema, position *draftPosition, draftPath string, missing *[]string) error {
	for _, name := range presentFieldNames(resolved, position) {
		child, err := n.Child(name)
		if err != nil {
			return err
		}
		childPath := joinFieldPath(draftPath, name)
		if childPosition, isInstantiated := position.fields[name]; isInstantiated {
			if err := child.collectMissing(childPosition, childPath, missing); err != nil {
				return err
			}
			continue
		}
		*missing = append(*missing, childPath)
		if err := child.collectAssured(childPath, map[*spec.Schema]bool{}, missing); err != nil {
			return err
		}
	}
	return nil
}

// collectItems steps into the instantiated items of an array position, in
// index order. The item schema's required fields bind per item, so an array
// with no instantiated items misses nothing beneath it.
func (n *Node) collectItems(item *spec.Schema, position *draftPosition, draftPath string, missing *[]string) error {
	if len(position.fields) > 0 || len(position.keys) > 0 {
		return fmt.Errorf("%s is an array: a Draft addresses its items with an index selector before any field",
			describeDraftPosition(draftPath))
	}
	itemNode := &Node{doc: n.doc, path: n.path, schema: item}
	for _, index := range slices.Sorted(maps.Keys(position.items)) {
		itemPath := fmt.Sprintf("%s[%d]", draftPath, index)
		if err := itemNode.collectMissing(position.items[index], itemPath, missing); err != nil {
			return err
		}
	}
	return nil
}

// collectValues steps into the instantiated map values of a map-shaped
// object position, keys in lexical order. The value schema's required fields
// bind per key, so a map with no instantiated keys misses nothing beneath it.
func (n *Node) collectValues(value *spec.Schema, position *draftPosition, draftPath string, missing *[]string) error {
	if len(position.fields) > 0 || len(position.items) > 0 {
		return fmt.Errorf("%s is a map-shaped object: a Draft addresses its values with a quoted key selector before any field",
			describeDraftPosition(draftPath))
	}
	valueNode := &Node{doc: n.doc, path: n.path, schema: value}
	for _, key := range slices.Sorted(maps.Keys(position.keys)) {
		keyPath := draftPath + "[" + strconv.Quote(key) + "]"
		if err := valueNode.collectMissing(position.keys[key], keyPath, missing); err != nil {
			return err
		}
	}
	return nil
}

// collectAssured walks the required chain beneath a missing required field:
// the field's object must eventually exist, so its own required fields are
// already missing too — the "always-present chain" (DESIGN.md — Flow §3).
// Arrays and map-shaped objects stop the chain, because their required
// fields bind per item or key and none are instantiated beneath a missing
// field. onChain keeps the walk cycle-safe: a Type Schema already being
// descended on the current chain is reported once and not entered again, so
// a self-referential required chain terminates instead of materializing.
func (n *Node) collectAssured(draftPath string, onChain map[*spec.Schema]bool, missing *[]string) error {
	chain, err := n.resolveChain(n.schema)
	if err != nil {
		return err
	}
	if isSchemaBlind(chain) {
		return nil
	}
	resolved := concreteSchema(chain)
	if itemSchema(resolved) != nil || valueSchema(resolved) != nil {
		return nil
	}
	if onChain[resolved] {
		return nil
	}
	onChain[resolved] = true
	defer delete(onChain, resolved)

	names := slices.Clone(resolved.Required)
	slices.Sort(names)
	for _, name := range slices.Compact(names) {
		child, err := n.Child(name)
		if err != nil {
			return err
		}
		childPath := joinFieldPath(draftPath, name)
		*missing = append(*missing, childPath)
		if err := child.collectAssured(childPath, onChain, missing); err != nil {
			return err
		}
	}
	return nil
}

// presentFieldNames is the sorted union of the object's declared-required
// field names and the Draft's instantiated field names at this position —
// the only children contextual requiredness ever expands. Sorting by name
// matches the sibling order Children uses, so results follow tree position.
func presentFieldNames(resolved *spec.Schema, position *draftPosition) []string {
	names := slices.AppendSeq(slices.Clone(resolved.Required), maps.Keys(position.fields))
	slices.Sort(names)
	return slices.Compact(names)
}

// draftPosition is one instantiated position of the Draft: the root, an
// object field, an array item, or a map value. Instantiating a Draft-level
// Field Path instantiates every position along it — a container item cannot
// exist without its containers array, or the array without its pod spec.
type draftPosition struct {
	fields map[string]*draftPosition
	items  map[int]*draftPosition
	keys   map[string]*draftPosition
}

// parseDraftPaths folds the instantiated Draft-level Field Paths into the
// position trie the requiredness walk descends.
func parseDraftPaths(paths []string) (*draftPosition, error) {
	root := &draftPosition{}
	for _, path := range paths {
		segments, err := splitDraftPath(path)
		if err != nil {
			return nil, err
		}
		position := root
		for _, segment := range segments {
			position = position.instantiate(segment)
		}
	}
	return root, nil
}

// instantiate descends to the position the segment selects, materializing it
// (and thereby marking it instantiated) when the Draft hasn't named it yet.
func (p *draftPosition) instantiate(segment draftSegment) *draftPosition {
	switch segment.kind {
	case indexSegment:
		if p.items == nil {
			p.items = map[int]*draftPosition{}
		}
		if p.items[segment.index] == nil {
			p.items[segment.index] = &draftPosition{}
		}
		return p.items[segment.index]
	case keySegment:
		if p.keys == nil {
			p.keys = map[string]*draftPosition{}
		}
		if p.keys[segment.key] == nil {
			p.keys[segment.key] = &draftPosition{}
		}
		return p.keys[segment.key]
	default:
		if p.fields == nil {
			p.fields = map[string]*draftPosition{}
		}
		if p.fields[segment.field] == nil {
			p.fields[segment.field] = &draftPosition{}
		}
		return p.fields[segment.field]
	}
}

// segmentKind distinguishes the three steps a Draft-level Field Path can
// take: dots address schema-defined fields; brackets address array items and
// map keys (CONTEXT.md — Field Path).
type segmentKind int

const (
	fieldSegment segmentKind = iota
	indexSegment
	keySegment
)

// draftSegment is one parsed step of a Draft-level Field Path.
type draftSegment struct {
	kind  segmentKind
	field string
	index int
	key   string
}

// splitDraftPath parses a Draft-level Field Path — dotted field names with
// bracket selectors for items and keys, e.g.
// spec.template.spec.containers[0].name or metadata.labels["app"] — into its
// segments. A malformed path yields a clear error naming it.
func splitDraftPath(path string) ([]draftSegment, error) {
	field, rest, err := cutFieldName(path)
	if err != nil {
		return nil, fmt.Errorf("malformed Draft-level Field Path %q: %w", path, err)
	}
	segments := []draftSegment{{kind: fieldSegment, field: field}}
	for rest != "" {
		var segment draftSegment
		switch rest[0] {
		case '.':
			var name string
			name, rest, err = cutFieldName(rest[1:])
			segment = draftSegment{kind: fieldSegment, field: name}
		case '[':
			segment, rest, err = cutSelector(rest)
		default:
			err = fmt.Errorf("expected '.' or '[' after a selector, not %q", string(rest[0]))
		}
		if err != nil {
			return nil, fmt.Errorf("malformed Draft-level Field Path %q: %w", path, err)
		}
		segments = append(segments, segment)
	}
	return segments, nil
}

// cutFieldName reads one dotted field-name segment: everything up to the
// next '.' or '['. Dots address schema-defined fields, so the name must be
// non-empty.
func cutFieldName(s string) (string, string, error) {
	end := strings.IndexAny(s, ".[")
	if end == -1 {
		end = len(s)
	}
	if end == 0 {
		return "", "", fmt.Errorf("expected a field name")
	}
	return s[:end], s[end:], nil
}

// cutSelector reads one bracket selector: an item index like [0] or a quoted
// map key like ["app.kubernetes.io/name"].
func cutSelector(s string) (draftSegment, string, error) {
	body := s[1:]
	if strings.HasPrefix(body, `"`) {
		key, rest, err := cutQuotedKey(body)
		if err != nil {
			return draftSegment{}, "", err
		}
		return draftSegment{kind: keySegment, key: key}, rest, nil
	}
	end := strings.IndexByte(body, ']')
	if end == -1 {
		return draftSegment{}, "", fmt.Errorf("a selector is missing its closing ']'")
	}
	index, err := strconv.Atoi(body[:end])
	if err != nil || index < 0 {
		return draftSegment{}, "", fmt.Errorf("selector [%s] is neither an item index nor a quoted map key", body[:end])
	}
	return draftSegment{kind: indexSegment, index: index}, body[end+1:], nil
}

// cutQuotedKey reads a double-quoted map key (Go string syntax, so escaped
// quotes work) and its closing bracket.
func cutQuotedKey(s string) (string, string, error) {
	quoted, err := strconv.QuotedPrefix(s)
	if err != nil {
		return "", "", fmt.Errorf("a map key selector must be a double-quoted key")
	}
	key, err := strconv.Unquote(quoted)
	if err != nil {
		return "", "", fmt.Errorf("a map key selector must be a double-quoted key")
	}
	rest := s[len(quoted):]
	if !strings.HasPrefix(rest, "]") {
		return "", "", fmt.Errorf("a map key selector is missing its closing ']'")
	}
	return key, rest[1:], nil
}

// joinFieldPath extends a Draft-level Field Path by one dotted field segment.
func joinFieldPath(draftPath, name string) string {
	if draftPath == "" {
		return name
	}
	return draftPath + "." + name
}

// describeDraftPosition names a Draft position for error messages.
func describeDraftPosition(draftPath string) string {
	if draftPath == "" {
		return "the root of the Draft"
	}
	return fmt.Sprintf("Draft-level Field Path %q", draftPath)
}
