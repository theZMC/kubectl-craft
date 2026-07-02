package schema

import (
	"fmt"
	"maps"
	"slices"
	"strconv"

	"k8s.io/kube-openapi/pkg/validation/spec"
)

// Drop is one entry of a version switch's drop report: a Draft-level Field
// Path that would not survive the carry-over, and the reason why — the report
// the confirmation renders before switching (DESIGN.md — Compose lifecycle).
// A subtree that cannot carry is reported once, at the highest position that
// failed to place; everything the Draft holds beneath it is implied.
type Drop struct {
	// Path is the dropped Draft-level Field Path.
	Path string
	// Reason says why the position cannot carry, in glossary language.
	Reason string
}

// CarryOver partitions the Draft's values against a target version's field
// tree (DESIGN.md — Compose lifecycle: switching the Kind's version
// mid-compose carries values over by Field Path). A value carries when its
// Draft-level Field Path exists in the target tree with a compatible type —
// the same display-type family, an int-or-string position accepting an
// integer or a string — and a raw-YAML graft carries only onto a position the
// target Type Schema is also blind at; everything else lands in the drop
// report, ordered like Instantiated (fields and keys sorted, items by index).
//
// Instantiated items and keys carry as instantiations whenever their
// collection survives — an item whose values all drop stays an
// instantiated-but-empty item — so carried values stay readable at the same
// paths: indices never shift. The returned Draft is bound to the target tree
// and Kind; the source Draft is never mutated, so cancelling a switch keeps
// composing at the current version untouched.
func (d *Draft) CarryOver(target *Node, kind GroupVersionKind) (*Draft, []Drop) {
	carried := NewDraft(target, kind)
	var drops []Drop
	carryEntry(d.entries, target, carried.entries, "", &drops)
	return carried, drops
}

// carryEntry carries one instantiated position of the source Draft: a filled
// value carries by type compatibility, and a structural position recurses
// into its fields, items, and keys — the same deterministic order
// Instantiated spells.
func carryEntry(source *draftEntry, node *Node, dest *draftEntry, path string, drops *[]Drop) {
	if source.value != nil {
		carryValue(*source.value, node, dest, path, drops)
		return
	}
	carryFields(source, node, dest, path, drops)
	carryItems(source, node, dest, path, drops)
	carryKeys(source, node, dest, path, drops)
}

// carryFields carries the source entry's field children. A field the target
// tree cannot place drops with its whole subtree, reported once; a field
// whose subtree carried nothing is not materialized — ancestors instantiate
// implicitly, so they de-instantiate implicitly too.
func carryFields(source *draftEntry, node *Node, dest *draftEntry, path string, drops *[]Drop) {
	for _, name := range slices.Sorted(maps.Keys(source.fields)) {
		childPath := joinFieldPath(path, name)
		child, err := node.stepInto(draftSegment{kind: fieldSegment, field: name}, path)
		if err != nil {
			*drops = append(*drops, Drop{Path: childPath, Reason: err.Error()})
			continue
		}
		candidate := &draftEntry{}
		carryEntry(source.fields[name], child, candidate, childPath, drops)
		if candidate.isEmpty() {
			continue
		}
		if dest.fields == nil {
			dest.fields = map[string]*draftEntry{}
		}
		dest.fields[name] = candidate
	}
}

// carryItems carries the source entry's array items. Items were explicit
// acts, so every item of a surviving array carries as an instantiation even
// when its values all drop — indices stay aligned, and carried values keep
// their exact paths. A position the target does not spell as an array drops
// every item.
func carryItems(source *draftEntry, node *Node, dest *draftEntry, path string, drops *[]Drop) {
	for index, item := range source.items {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		child, err := node.stepInto(draftSegment{kind: indexSegment, index: index}, path)
		if err != nil {
			*drops = append(*drops, Drop{Path: itemPath, Reason: err.Error()})
			continue
		}
		candidate := &draftEntry{}
		carryEntry(item, child, candidate, itemPath, drops)
		dest.items = append(dest.items, candidate)
	}
}

// carryKeys carries the source entry's map entries, keys sorted. Keys were
// explicit acts, so every key of a surviving map carries as an instantiation
// even when its value drops; a position the target does not spell map-shaped
// drops every entry.
func carryKeys(source *draftEntry, node *Node, dest *draftEntry, path string, drops *[]Drop) {
	for _, key := range slices.Sorted(maps.Keys(source.keys)) {
		keyPath := path + "[" + strconv.Quote(key) + "]"
		child, err := node.stepInto(draftSegment{kind: keySegment, key: key}, path)
		if err != nil {
			*drops = append(*drops, Drop{Path: keyPath, Reason: err.Error()})
			continue
		}
		candidate := &draftEntry{}
		carryEntry(source.keys[key], child, candidate, keyPath, drops)
		if dest.keys == nil {
			dest.keys = map[string]*draftEntry{}
		}
		dest.keys[key] = candidate
	}
}

// carryValue carries one filled value onto the target position it resolved
// to. A raw-YAML graft carries only onto a schema-blind position; a scalar
// carries only when the target's display-type family admits it — carry-over
// is by Field Path and type family, so target-side constraints (enum,
// pattern, bounds) stay server-side Validate's business, exactly like the
// values a Draft already holds.
func carryValue(value Value, node *Node, dest *draftEntry, path string, drops *[]Drop) {
	if value.Type == TypeRawYAML {
		if err := node.requireSchemaBlind(path); err != nil {
			*drops = append(*drops, Drop{Path: path, Reason: err.Error()})
			return
		}
		graft := value
		dest.value = &graft
		return
	}

	chain, err := node.resolveChain(node.schema)
	if err != nil {
		*drops = append(*drops, Drop{Path: path, Reason: err.Error()})
		return
	}
	if isSchemaBlind(chain) {
		*drops = append(*drops, Drop{Path: path, Reason: blindPositionError(path).Error()})
		return
	}
	targetType, err := node.displayType(chain, map[*spec.Schema]bool{})
	if err != nil {
		*drops = append(*drops, Drop{Path: path, Reason: err.Error()})
		return
	}
	if !typeCarries(value, targetType) {
		*drops = append(*drops, Drop{Path: path, Reason: fmt.Sprintf(
			"the target version's Type Schema types it as %s, not %s", targetType, value.Type,
		)})
		return
	}
	dest.value = &Value{Type: targetType, Data: value.Data}
}

// typeCarries reports whether a Draft value's display-type family carries to
// the target's: identical display types carry; an int-or-string target
// accepts an integer or a string; and an int-or-string value carries to the
// single type its data spells — int64 to an integer position, string to a
// string one.
func typeCarries(value Value, targetType string) bool {
	if value.Type == targetType {
		return true
	}
	if targetType == "int-or-string" {
		return value.Type == "integer" || value.Type == "string"
	}
	if value.Type == "int-or-string" {
		if _, isIntegral := value.Data.(int64); isIntegral {
			return targetType == "integer"
		}
		return targetType == "string"
	}
	return false
}
