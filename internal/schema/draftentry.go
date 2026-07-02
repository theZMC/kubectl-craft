package schema

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
)

// draftEntry is one instantiated position of a Draft's entry tree: a filled
// leaf (value), an object position (fields), an array position (items —
// always dense), or a map position (keys). An entry holding none of these is
// an instantiated-but-empty item or key.
type draftEntry struct {
	value  *Value
	fields map[string]*draftEntry
	items  []*draftEntry
	keys   map[string]*draftEntry
}

// isEmpty reports whether the entry holds nothing: no value, no entries.
func (e *draftEntry) isEmpty() bool {
	return e.value == nil && len(e.fields) == 0 && len(e.items) == 0 && len(e.keys) == 0
}

// lookup descends one segment without instantiating anything; nil when the
// Draft holds nothing there.
func (e *draftEntry) lookup(segment draftSegment) *draftEntry {
	switch segment.kind {
	case indexSegment:
		if segment.index >= len(e.items) {
			return nil
		}
		return e.items[segment.index]
	case keySegment:
		return e.keys[segment.key]
	default:
		return e.fields[segment.field]
	}
}

// materialize descends to the entry the segments address, instantiating every
// position along the way — implicit ancestor instantiation: setting
// spec.replicas instantiates spec. Placeability is checked up front, so a
// rejected path instantiates nothing.
func (e *draftEntry) materialize(segments []draftSegment) (*draftEntry, error) {
	if err := e.ensurePlaceable(segments); err != nil {
		return nil, err
	}
	entry := e
	for _, segment := range segments {
		entry = entry.materializeChild(segment)
	}
	return entry, nil
}

// ensurePlaceable walks the segments against the current entry tree and
// rejects any item index that would leave a gap: an index may address an
// existing item or the next free one (appending implicitly) — a Draft's
// arrays stay dense.
func (e *draftEntry) ensurePlaceable(segments []draftSegment) error {
	entry := e
	spelled := ""
	for _, segment := range segments {
		if segment.kind == indexSegment {
			held := 0
			if entry != nil {
				held = len(entry.items)
			}
			if segment.index > held {
				return fmt.Errorf("a Draft's array items stay dense: %s holds %d items, so the next instantiable index is [%d], not [%d]",
					describeDraftPosition(spelled), held, held, segment.index)
			}
		}
		if entry != nil {
			entry = entry.lookup(segment)
		}
		spelled = spellSegment(spelled, segment)
	}
	return nil
}

// materializeChild descends one segment, instantiating the position when the
// Draft has not named it yet. Density was ensured up front, so an index is
// at most one past the held items.
func (e *draftEntry) materializeChild(segment draftSegment) *draftEntry {
	switch segment.kind {
	case indexSegment:
		if segment.index == len(e.items) {
			e.items = append(e.items, &draftEntry{})
		}
		return e.items[segment.index]
	case keySegment:
		if e.keys == nil {
			e.keys = map[string]*draftEntry{}
		}
		if e.keys[segment.key] == nil {
			e.keys[segment.key] = &draftEntry{}
		}
		return e.keys[segment.key]
	default:
		if e.fields == nil {
			e.fields = map[string]*draftEntry{}
		}
		if e.fields[segment.field] == nil {
			e.fields[segment.field] = &draftEntry{}
		}
		return e.fields[segment.field]
	}
}

// remove deletes the entry the segments address, returning how many filled
// values were discarded with it. Removing an item renumbers the items after
// it down by one — dense arrays, the renumbering contract. Ancestor fields
// along the path left holding nothing de-instantiate implicitly, mirroring
// how setting instantiated them; items and keys were explicit acts, so they
// leave the Draft only when removed themselves.
func (e *draftEntry) remove(segments []draftSegment, prefix string) (int, error) {
	segment := segments[0]
	child := e.lookup(segment)
	if child == nil {
		return 0, fmt.Errorf("the Draft holds nothing at %s", describeDraftPosition(spellSegment(prefix, segment)))
	}
	if len(segments) == 1 {
		e.deleteChild(segment)
		return child.countValues(), nil
	}
	discarded, err := child.remove(segments[1:], spellSegment(prefix, segment))
	if err != nil {
		return 0, err
	}
	if segment.kind == fieldSegment && child.isEmpty() {
		delete(e.fields, segment.field)
	}
	return discarded, nil
}

// deleteChild removes one direct entry: a field or key entry by name, an item
// by position — the items after it renumber down by one.
func (e *draftEntry) deleteChild(segment draftSegment) {
	switch segment.kind {
	case indexSegment:
		e.items = slices.Delete(e.items, segment.index, segment.index+1)
	case keySegment:
		delete(e.keys, segment.key)
	default:
		delete(e.fields, segment.field)
	}
}

// countValues counts the filled values at and beneath the entry — the discard
// count a destructive unset reports. A raw-YAML graft counts as one value: it
// was one confirmed entry.
func (e *draftEntry) countValues() int {
	if e.value != nil {
		return 1
	}
	count := 0
	for _, child := range e.fields {
		count += child.countValues()
	}
	for _, child := range e.items {
		count += child.countValues()
	}
	for _, child := range e.keys {
		count += child.countValues()
	}
	return count
}

// collectInstantiated appends the entry tree's terminal Draft-level Field
// Paths — filled values (a raw-YAML graft contributes its leaf paths) and
// instantiated-but-empty items and keys — in deterministic order: fields and
// keys sorted, items by index. Ancestors are implied by their descendants.
func (e *draftEntry) collectInstantiated(spelled string, paths *[]string) {
	if e.value != nil {
		if e.value.Type == TypeRawYAML {
			graftLeafPaths(spelled, e.value.Data, paths)
			return
		}
		*paths = append(*paths, spelled)
		return
	}
	if e.isEmpty() {
		if spelled != "" {
			*paths = append(*paths, spelled)
		}
		return
	}
	for _, name := range slices.Sorted(maps.Keys(e.fields)) {
		e.fields[name].collectInstantiated(joinFieldPath(spelled, name), paths)
	}
	for index, item := range e.items {
		item.collectInstantiated(fmt.Sprintf("%s[%d]", spelled, index), paths)
	}
	for _, key := range slices.Sorted(maps.Keys(e.keys)) {
		e.keys[key].collectInstantiated(spelled+"["+strconv.Quote(key)+"]", paths)
	}
}
