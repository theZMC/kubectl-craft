package schema

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"gopkg.in/yaml.v3"
)

// The resolved YAML tags of the nodes Emit assembles by hand; scalar values
// tag themselves through yaml.Node.Encode.
const (
	yamlMapTag    = "!!map"
	yamlSeqTag    = "!!seq"
	yamlStringTag = "!!str"
)

// Emit produces the composed Manifest as YAML — the act that ends composing,
// and the only way a Manifest leaves the tool (CONTEXT.md). The document is
// sparse by default (DESIGN.md — Flow §6): apiVersion and kind spell the
// Draft's Kind, and beneath them appear exactly the positions where a filled
// value sits. Set-ness comes from the Draft's entries, never from value
// truthiness: an explicitly set zero value ("", 0, false) is present, an
// unset field — defaulted or optional alike — is absent, and an
// instantiated-but-empty item or key, holding no value yet, is absent too.
// Arrays emit as sequences in index order, map-shaped objects as YAML maps,
// and a raw-YAML graft splices its parsed value at its Field Path. Values
// keep the spelling that was set: booleans and numbers unquoted, strings
// quoted only when plain YAML would read them as another type, so an
// int-or-string emits 80 or "80%", whichever was filled.
//
// Output is deterministic: the same Draft emits byte-identical YAML.
// apiVersion and kind lead the document, and every other mapping sorts its
// keys lexically — the ordering Children and Instantiated already speak,
// chosen over schema order because an OpenAPI v3 document's JSON objects
// preserve no declaration order to emit by. Identity always comes from the
// Kind: a root-level apiVersion or kind entry a Draft happens to hold is
// superseded, never emitted alongside.
func (d *Draft) Emit() ([]byte, error) {
	document := &yaml.Node{Kind: yaml.MappingNode, Tag: yamlMapTag}
	document.Content = append(
		document.Content,
		stringScalar("apiVersion"), stringScalar(d.kind.APIVersion()),
		stringScalar("kind"), stringScalar(d.kind.Kind),
	)
	if err := d.entries.emitEntriesInto(document, "", isIdentityField); err != nil {
		return nil, err
	}
	return renderManifest(document)
}

// isIdentityField names the two root fields Emit always derives from the
// Draft's Kind rather than from entries.
func isIdentityField(name string) bool {
	return name == "apiVersion" || name == "kind"
}

// emitEntriesInto appends the entry's field and key children onto a mapping
// node — fields then keys, each sorted lexically (a position holds one flavor
// or the other, so the mapping stays sorted) — skipping subtrees that hold
// nothing filled, and, at the root, the identity fields.
func (e *draftEntry) emitEntriesInto(mapping *yaml.Node, spelled string, skip func(string) bool) error {
	for _, name := range slices.Sorted(maps.Keys(e.fields)) {
		if skip != nil && skip(name) {
			continue
		}
		if err := appendPair(mapping, name, e.fields[name], joinFieldPath(spelled, name)); err != nil {
			return err
		}
	}
	for _, key := range slices.Sorted(maps.Keys(e.keys)) {
		if err := appendPair(mapping, key, e.keys[key], spelled+"["+strconv.Quote(key)+"]"); err != nil {
			return err
		}
	}
	return nil
}

// appendPair renders one child subtree and appends it onto the mapping as a
// key/value pair — unless the subtree holds nothing filled, which the sparse
// contract leaves out of the Manifest entirely.
func appendPair(mapping *yaml.Node, key string, child *draftEntry, spelled string) error {
	rendered, filled, err := child.emitNode(spelled)
	if err != nil {
		return err
	}
	if !filled {
		return nil
	}
	mapping.Content = append(mapping.Content, stringScalar(key), rendered)
	return nil
}

// emitNode renders one entry subtree as a YAML node: a filled value as the
// scalar (or spliced graft) it holds, an array as a sequence in index order,
// structure as a mapping. It reports not-filled when nothing filled sits at
// or beneath the entry.
func (e *draftEntry) emitNode(spelled string) (*yaml.Node, bool, error) {
	if e.value != nil {
		node, err := valueNode(e.value, spelled)
		if err != nil {
			return nil, false, err
		}
		return node, true, nil
	}
	if len(e.items) > 0 {
		return e.emitSequence(spelled)
	}
	mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: yamlMapTag}
	if err := e.emitEntriesInto(mapping, spelled, nil); err != nil {
		return nil, false, err
	}
	return mapping, len(mapping.Content) > 0, nil
}

// emitSequence renders an array position's items in index order, skipping
// items that hold nothing filled.
func (e *draftEntry) emitSequence(spelled string) (*yaml.Node, bool, error) {
	sequence := &yaml.Node{Kind: yaml.SequenceNode, Tag: yamlSeqTag}
	for index, item := range e.items {
		rendered, filled, err := item.emitNode(fmt.Sprintf("%s[%d]", spelled, index))
		if err != nil {
			return nil, false, err
		}
		if filled {
			sequence.Content = append(sequence.Content, rendered)
		}
	}
	return sequence, len(sequence.Content) > 0, nil
}

// valueNode encodes one filled Value. The Draft's normalized spellings carry
// straight through: bool, int64, and float64 emit unquoted; a string is
// quoted only when its plain form would resolve as another type ("80" stays
// a string, 80 stays a number). A raw-YAML graft encodes its whole parsed
// tree, map keys in the encoder's deterministic sorted order — the Draft
// stores the parsed value, so content splices verbatim while spelling
// normalizes.
func valueNode(value *Value, spelled string) (*yaml.Node, error) {
	node := &yaml.Node{}
	if err := node.Encode(value.Data); err != nil {
		return nil, fmt.Errorf("emitting the Manifest: rendering the value at %s: %w", describeDraftPosition(spelled), err)
	}
	return node, nil
}

// stringScalar spells one string — a mapping key or an identity value — as a
// scalar node the encoder quotes only when YAML requires.
func stringScalar(text string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: yamlStringTag, Value: text}
}

// renderManifest encodes the assembled document with two-space indent — the
// conventional Manifest spelling — ending in a single trailing newline.
func renderManifest(document *yaml.Node) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("emitting the Manifest: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("emitting the Manifest: %w", err)
	}
	return buffer.Bytes(), nil
}
