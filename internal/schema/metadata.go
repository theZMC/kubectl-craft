package schema

import (
	"encoding/json"
	"fmt"
	"strconv"

	"k8s.io/kube-openapi/pkg/validation/spec"
)

// The Kubernetes schema extensions node metadata gives first-class treatment.
const (
	// intOrStringExtension marks a field that accepts an integer or a
	// string (e.g. an absolute count or a percentage); built-in Type
	// Schemas spell the same flavor as the "int-or-string" format.
	intOrStringExtension = "x-kubernetes-int-or-string"
	// preserveUnknownFieldsExtension marks a subtree the Type Schema is
	// deliberately blind to: values pass through unvalidated, so composing
	// routes it to the raw-YAML escape hatch.
	preserveUnknownFieldsExtension = "x-kubernetes-preserve-unknown-fields"
	// validationsExtension carries CEL rules. They are surfaced as
	// constraint text for the detail pane only; evaluation stays
	// server-side, where Validate submits the Manifest (ADR-0004).
	validationsExtension = "x-kubernetes-validations"
)

// Metadata is everything a Node's position in the Type Schema says about the
// field itself — the per-node facts the compose view's detail pane and
// value-entry widgets are driven by. It is pure display-and-input data:
// nothing here validates a value (Validate is server-side dry-run).
type Metadata struct {
	// Type is the display type: one of string, integer, number, boolean,
	// object, or an element-typed array spelling ([]string, []object, ...).
	// A field accepting an integer or a string is its own flavor,
	// int-or-string — never "object". Map-shaped objects display as
	// object; their value structure is the node's children's business.
	Type string
	// Description is the field's documentation. When an allOf-wrapped
	// $ref carries its own description (the common Kubernetes spelling),
	// that outer, field-specific text wins over the referenced component
	// schema's generic one.
	Description string
	// Enum lists the admissible values verbatim when the Type Schema
	// declares an enum; empty otherwise.
	Enum []string
	// Constraints renders the declared constraints as display text, one
	// entry each — format, pattern, numeric bounds, length and item
	// bounds, multipleOf — followed by any CEL rules
	// (x-kubernetes-validations) the schema spells. Display-only: the
	// server, not the TUI, enforces them.
	Constraints []string
	// Default is the schema-declared default value, exactly as the
	// OpenAPI v3 Document spells it; nil when the schema declares none.
	Default any
	// Required reports declared requiredness: whether the parent object's
	// required list names this field. The root of the field tree is never
	// Required. Contextual requiredness is computed over this input, not
	// here.
	Required bool
	// SchemaBlind marks a subtree the Type Schema says nothing about — an
	// x-kubernetes-preserve-unknown-fields object or a plain untyped
	// object with no declared structure. Composing routes a schema-blind
	// node to the raw-YAML escape hatch instead of schema-guided entry.
	SchemaBlind bool
}

// Metadata reads the node's per-field metadata from its Type Schema fragment,
// resolving $refs now — at inspection, like expansion — so a broken $ref
// errors here instead of at tree construction. Facets spelled on an
// allOf-wrapped $ref (description, default) take precedence over the resolved
// component schema's own.
func (n *Node) Metadata() (Metadata, error) {
	chain, err := n.resolveChain(n.schema)
	if err != nil {
		return Metadata{}, err
	}

	displayType, err := n.displayType(chain, map[*spec.Schema]bool{})
	if err != nil {
		return Metadata{}, err
	}
	constraints, err := n.constraintTexts(chain)
	if err != nil {
		return Metadata{}, err
	}

	return Metadata{
		Type:        displayType,
		Description: firstDescription(chain),
		Enum:        renderEnum(firstEnum(chain)),
		Constraints: constraints,
		Default:     firstDefault(chain),
		Required:    n.required,
		SchemaBlind: isSchemaBlind(chain),
	}, nil
}

// displayType renders the resolved fragment's display type. Arrays are
// element-typed by resolving the item schema ([]string, []object, ...), with
// visited guarding the pathological self-referential array; int-or-string is
// recognized before anything else so it never reads as an object or a bare
// anyOf.
func (n *Node) displayType(chain []*spec.Schema, visited map[*spec.Schema]bool) (string, error) {
	if isIntOrString(chain) {
		return "int-or-string", nil
	}

	concrete := concreteSchema(chain)
	switch typeName(concrete) {
	case "array":
		item := itemSchema(concrete)
		if item == nil || visited[concrete] {
			return "array", nil
		}
		visited[concrete] = true
		itemChain, err := n.resolveChain(item)
		if err != nil {
			return "", err
		}
		element, err := n.displayType(itemChain, visited)
		if err != nil {
			return "", err
		}
		return "[]" + element, nil
	case "":
		// Untyped fragments — object-shaped (properties or a map's
		// value schema) or entirely free-form — display as object.
		return "object", nil
	default:
		return typeName(concrete), nil
	}
}

// constraintTexts renders the chain's declared constraints as display text:
// the keyword constraints first, then any CEL rules.
func (n *Node) constraintTexts(chain []*spec.Schema) ([]string, error) {
	texts := keywordConstraintTexts(chain)
	celTexts, err := n.celConstraintTexts(chain)
	if err != nil {
		return nil, err
	}
	return append(texts, celTexts...), nil
}

// keywordConstraintTexts renders the schema-keyword constraints — format,
// pattern, numeric bounds, length and item bounds, multipleOf — each
// resolving outermost-first along the chain like every other facet.
func keywordConstraintTexts(chain []*spec.Schema) []string {
	var texts []string
	appendText := func(name, value string) {
		texts = append(texts, name+": "+value)
	}

	// The int-or-string format is the type flavor, not a constraint.
	if format := firstNonEmpty(chain, func(s *spec.Schema) string { return s.Format }); format != "" && !isIntOrString(chain) {
		appendText("format", format)
	}
	if pattern := firstNonEmpty(chain, func(s *spec.Schema) string { return s.Pattern }); pattern != "" {
		appendText("pattern", pattern)
	}
	if owner := firstSchema(chain, func(s *spec.Schema) bool { return s.Minimum != nil }); owner != nil {
		appendText("minimum", renderNumber(*owner.Minimum, owner.ExclusiveMinimum))
	}
	if owner := firstSchema(chain, func(s *spec.Schema) bool { return s.Maximum != nil }); owner != nil {
		appendText("maximum", renderNumber(*owner.Maximum, owner.ExclusiveMaximum))
	}
	if owner := firstSchema(chain, func(s *spec.Schema) bool { return s.MultipleOf != nil }); owner != nil {
		appendText("multipleOf", renderNumber(*owner.MultipleOf, false))
	}
	for _, bound := range []struct {
		name  string
		value func(*spec.Schema) *int64
	}{
		{"minLength", func(s *spec.Schema) *int64 { return s.MinLength }},
		{"maxLength", func(s *spec.Schema) *int64 { return s.MaxLength }},
		{"minItems", func(s *spec.Schema) *int64 { return s.MinItems }},
		{"maxItems", func(s *spec.Schema) *int64 { return s.MaxItems }},
		{"minProperties", func(s *spec.Schema) *int64 { return s.MinProperties }},
		{"maxProperties", func(s *spec.Schema) *int64 { return s.MaxProperties }},
	} {
		if owner := firstSchema(chain, func(s *spec.Schema) bool { return bound.value(s) != nil }); owner != nil {
			appendText(bound.name, strconv.FormatInt(*bound.value(owner), 10))
		}
	}
	return texts
}

// celConstraintTexts renders the chain's CEL rules as constraint text,
// collected from every schema in the chain — wrapper and resolved target
// alike — in chain order. Display-only: evaluation stays server-side.
func (n *Node) celConstraintTexts(chain []*spec.Schema) ([]string, error) {
	var texts []string
	for _, schema := range chain {
		rules, err := celRules(schema)
		if err != nil {
			return nil, fmt.Errorf("reading node metadata at %s: %w", n.describe(), err)
		}
		for _, rule := range rules {
			text := "rule: " + rule.Rule
			if rule.Message != "" {
				text += " — " + rule.Message
			}
			texts = append(texts, text)
		}
	}
	return texts, nil
}

// celRule is one x-kubernetes-validations entry, as far as the detail pane
// cares: the CEL expression and its human-readable failure message.
type celRule struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// celRules decodes the schema's x-kubernetes-validations extension; a schema
// without the extension carries no rules.
func celRules(schema *spec.Schema) ([]celRule, error) {
	if _, tagged := schema.Extensions[validationsExtension]; !tagged {
		return nil, nil
	}
	var rules []celRule
	if err := schema.Extensions.GetObject(validationsExtension, &rules); err != nil {
		return nil, fmt.Errorf("decoding the %s extension: %w", validationsExtension, err)
	}
	return rules, nil
}

// isIntOrString recognizes both spellings of the int-or-string flavor: the
// x-kubernetes-int-or-string extension CRD-published schemas carry, and the
// "int-or-string" format built-in Type Schemas carry. Either may sit on an
// allOf wrapper or on the resolved target, so the whole chain is consulted.
func isIntOrString(chain []*spec.Schema) bool {
	for _, schema := range chain {
		if flagged, _ := schema.Extensions.GetBool(intOrStringExtension); flagged {
			return true
		}
		if schema.Format == "int-or-string" {
			return true
		}
	}
	return false
}

// isSchemaBlind reports whether the Type Schema says nothing about the
// subtree: x-kubernetes-preserve-unknown-fields anywhere in the chain, or a
// resolved object (typed or untyped) declaring no properties, no map value
// schema, no items, and no value set — nothing schema-guided entry could
// work with.
func isSchemaBlind(chain []*spec.Schema) bool {
	for _, schema := range chain {
		if flagged, _ := schema.Extensions.GetBool(preserveUnknownFieldsExtension); flagged {
			return true
		}
	}
	if isIntOrString(chain) {
		return false
	}

	concrete := concreteSchema(chain)
	if name := typeName(concrete); name != "object" && name != "" {
		return false
	}
	return len(concrete.Properties) == 0 &&
		concrete.AdditionalProperties == nil &&
		itemSchema(concrete) == nil &&
		len(concrete.Enum) == 0 &&
		len(concrete.OneOf) == 0 &&
		len(concrete.AnyOf) == 0
}

// concreteSchema is the resolved end of a resolution chain — the fragment
// $refs and allOf wrappers ultimately land on.
func concreteSchema(chain []*spec.Schema) *spec.Schema {
	return chain[len(chain)-1]
}

// typeName is the fragment's declared JSON type, empty when untyped.
func typeName(schema *spec.Schema) string {
	if len(schema.Type) == 0 {
		return ""
	}
	return schema.Type[0]
}

// firstSchema finds the outermost schema in the chain satisfying the
// predicate; outermost-first is how every wrapper-vs-target facet resolves.
func firstSchema(chain []*spec.Schema, present func(*spec.Schema) bool) *spec.Schema {
	for _, schema := range chain {
		if present(schema) {
			return schema
		}
	}
	return nil
}

// firstNonEmpty resolves a string facet outermost-first along the chain.
func firstNonEmpty(chain []*spec.Schema, value func(*spec.Schema) string) string {
	if owner := firstSchema(chain, func(s *spec.Schema) bool { return value(s) != "" }); owner != nil {
		return value(owner)
	}
	return ""
}

// firstDescription resolves the description outermost-first, so an
// allOf-wrapped $ref's field-specific text wins over the component schema's.
func firstDescription(chain []*spec.Schema) string {
	return firstNonEmpty(chain, func(s *spec.Schema) string { return s.Description })
}

// firstDefault resolves the declared default outermost-first; nil when no
// schema in the chain declares one.
func firstDefault(chain []*spec.Schema) any {
	if owner := firstSchema(chain, func(s *spec.Schema) bool { return s.Default != nil }); owner != nil {
		return owner.Default
	}
	return nil
}

// firstEnum resolves the enum values outermost-first.
func firstEnum(chain []*spec.Schema) []any {
	if owner := firstSchema(chain, func(s *spec.Schema) bool { return len(s.Enum) > 0 }); owner != nil {
		return owner.Enum
	}
	return nil
}

// renderEnum spells enum values for display and selection: strings verbatim
// (they are the values a Draft carries), anything else in its JSON spelling.
func renderEnum(values []any) []string {
	if len(values) == 0 {
		return nil
	}
	rendered := make([]string, 0, len(values))
	for _, value := range values {
		rendered = append(rendered, renderEnumValue(value))
	}
	return rendered
}

// renderEnumValue spells one value the way a Draft carries it: strings
// verbatim, anything else in its JSON spelling — the spelling enum membership
// is compared in, both at display and at set time.
func renderEnumValue(value any) string {
	if text, isString := value.(string); isString {
		return text
	}
	if encoded, err := json.Marshal(value); err == nil {
		return string(encoded)
	}
	return fmt.Sprint(value)
}

// renderNumber spells a numeric bound without exponent noise, marking
// exclusive bounds explicitly.
func renderNumber(value float64, exclusive bool) string {
	text := strconv.FormatFloat(value, 'f', -1, 64)
	if exclusive {
		text += " (exclusive)"
	}
	return text
}
