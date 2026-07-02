package schema

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"k8s.io/kube-openapi/pkg/validation/spec"
)

// componentRefPrefix is where every intra-document $ref points: the group
// document's own component schemas. Refs pointing anywhere else cannot be
// resolved from this Document.
const componentRefPrefix = "#/components/schemas/"

// Node is one position in a Kind's field tree — the navigable structure the
// compose view walks. A Node holds its Type Schema fragment exactly as the
// OpenAPI v3 Document spells it, $refs and all; nothing is resolved until the
// node is expanded. That laziness is what makes the tree cycle-safe: growing
// it from a root Type Schema materializes only the root, and a
// self-referential schema (JSONSchemaProps) simply yields another expandable
// Node at every step instead of recursing.
type Node struct {
	doc      *Document
	path     string       // schema-level Field Path; empty at the root
	schema   *spec.Schema // as written in the document, $refs unresolved
	required bool         // the parent object's required list names this field
}

// FieldTree grows the field tree for a Kind from its root Type Schema. Only
// the root Node is materialized here; every deeper Node appears lazily on
// expansion via Children or Child.
func (d *Document) FieldTree(gvk GroupVersionKind) (*Node, error) {
	root, err := d.RootSchema(gvk)
	if err != nil {
		return nil, err
	}
	return &Node{doc: d, schema: root}, nil
}

// FieldPath is the node's schema-level Field Path — dots only, e.g.
// "spec.template.spec.containers". The root's Field Path is empty, and an
// array's item Node (or a map's value Node) shares its parent's Field Path:
// dots address schema-defined fields, never individual items or keys.
func (n *Node) FieldPath() string { return n.path }

// Children materializes the node's child Nodes, resolving $refs now — at
// expansion, not construction. Object properties become one child each,
// sorted by name; an array's item schema and a map-shaped object's value
// schema (additionalProperties) each surface as a single child sharing this
// node's Field Path. A leaf yields no children.
func (n *Node) Children() ([]*Node, error) {
	resolved, err := n.resolve(n.schema)
	if err != nil {
		return nil, err
	}

	var children []*Node
	for _, name := range slices.Sorted(maps.Keys(resolved.Properties)) {
		property := resolved.Properties[name]
		children = append(children, &Node{
			doc:      n.doc,
			path:     n.childPath(name),
			schema:   &property,
			required: slices.Contains(resolved.Required, name),
		})
	}
	if item := itemSchema(resolved); item != nil {
		children = append(children, &Node{doc: n.doc, path: n.path, schema: item})
	}
	if value := valueSchema(resolved); value != nil {
		children = append(children, &Node{doc: n.doc, path: n.path, schema: value})
	}
	return children, nil
}

// Child resolves one schema-level Field Path segment: the named field among
// this node's object properties, looked up through an array's item schema or
// a map's value schema when needed — dots address schema-defined fields
// straight through items and keys. A segment the Type Schema does not define
// yields a clear error naming the position.
func (n *Node) Child(name string) (*Node, error) {
	current := n.schema
	visited := map[*spec.Schema]bool{}
	for {
		resolved, err := n.resolve(current)
		if err != nil {
			return nil, err
		}
		if visited[resolved] {
			break // a pure items/value cycle defines no such field
		}
		visited[resolved] = true

		if property, defined := resolved.Properties[name]; defined {
			return &Node{
				doc:      n.doc,
				path:     n.childPath(name),
				schema:   &property,
				required: slices.Contains(resolved.Required, name),
			}, nil
		}
		next := itemSchema(resolved)
		if next == nil {
			next = valueSchema(resolved)
		}
		if next == nil {
			break
		}
		current = next
	}
	return nil, fmt.Errorf("the Type Schema defines no field %q at %s", name, n.describe())
}

// resolve chases $refs and the allOf-wrapped single-$ref spelling Kubernetes
// uses for typed references, returning the concrete Type Schema fragment.
// Resolution happens here — at expansion — never at tree construction, and a
// $ref chain that never lands on a concrete schema errors instead of looping.
func (n *Node) resolve(schema *spec.Schema) (*spec.Schema, error) {
	chain, err := n.resolveChain(schema)
	if err != nil {
		return nil, err
	}
	return concreteSchema(chain), nil
}

// resolveChain resolves like resolve but keeps every schema visited along the
// way, outermost-first: the fragment as written, any allOf wrappers, and the
// concrete Type Schema fragment last. Metadata reads facets off the whole
// chain, because a wrapper's description or default overrides the resolved
// component schema's own.
func (n *Node) resolveChain(schema *spec.Schema) ([]*spec.Schema, error) {
	chain := []*spec.Schema{schema}
	chased := map[string]bool{}
	for {
		if ref := schema.Ref.String(); ref != "" {
			target, err := n.doc.componentSchema(ref, chased)
			if err != nil {
				return nil, fmt.Errorf("expanding %s: %w", n.describe(), err)
			}
			schema = target
			chain = append(chain, schema)
			continue
		}
		if wrapped := allOfWrappedRef(schema); wrapped != nil {
			schema = wrapped
			chain = append(chain, schema)
			continue
		}
		return chain, nil
	}
}

// componentSchema resolves one intra-document $ref to its component schema,
// tracking the chased names so a $ref chain that cycles without ever
// reaching a concrete Type Schema is reported rather than followed forever.
func (d *Document) componentSchema(ref string, chased map[string]bool) (*spec.Schema, error) {
	name, isComponent := strings.CutPrefix(ref, componentRefPrefix)
	if !isComponent {
		return nil, fmt.Errorf("$ref %q points outside this OpenAPI v3 Document's component schemas", ref)
	}
	if chased[name] {
		return nil, fmt.Errorf("the $ref chain through %q cycles without reaching a concrete Type Schema", name)
	}
	chased[name] = true

	if d.openAPI.Components != nil {
		if target := d.openAPI.Components.Schemas[name]; target != nil {
			return target, nil
		}
	}
	return nil, fmt.Errorf("$ref %q names a component schema this OpenAPI v3 Document does not define", ref)
}

// childPath extends the node's schema-level Field Path by one dotted segment.
func (n *Node) childPath(name string) string {
	if n.path == "" {
		return name
	}
	return n.path + "." + name
}

// describe names the node's position for error messages.
func (n *Node) describe() string {
	if n.path == "" {
		return "the root of the field tree"
	}
	return fmt.Sprintf("Field Path %q", n.path)
}

// itemSchema is the array's item Type Schema, when the fragment is an array.
func itemSchema(schema *spec.Schema) *spec.Schema {
	if schema.Items == nil {
		return nil
	}
	return schema.Items.Schema
}

// valueSchema is the map value Type Schema, when the fragment is a map-shaped
// object (additionalProperties carrying a schema rather than a bare boolean).
func valueSchema(schema *spec.Schema) *spec.Schema {
	if schema.AdditionalProperties == nil {
		return nil
	}
	return schema.AdditionalProperties.Schema
}

// allOfWrappedRef recognizes the common Kubernetes spelling
// {"allOf":[{"$ref":…}]} — often alongside description or default — and
// returns the wrapped schema so it resolves transparently. A schema carrying
// structure of its own is not a wrapper.
func allOfWrappedRef(schema *spec.Schema) *spec.Schema {
	if len(schema.AllOf) != 1 {
		return nil
	}
	if len(schema.Properties) > 0 || schema.Items != nil || schema.AdditionalProperties != nil {
		return nil
	}
	if schema.AllOf[0].Ref.String() == "" {
		return nil
	}
	return &schema.AllOf[0]
}
