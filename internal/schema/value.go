package schema

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"k8s.io/kube-openapi/pkg/validation/spec"
)

// TypeRawYAML is the Value.Type a raw-YAML graft carries — the one value
// flavor that is opaque to the Type Schema. Every other Value.Type is the
// node's display type (Metadata().Type): boolean, string, integer, number,
// or int-or-string.
const TypeRawYAML = "raw-YAML"

// Value is one filled entry of a Draft: the data itself and the schema type
// it was checked against, so emission round-trips without guessing.
type Value struct {
	// Type is the schema display type the value was checked against —
	// "boolean", "string", "integer", "number", "int-or-string" — or
	// TypeRawYAML for a raw-YAML graft.
	Type string
	// Data is the value, normalized by type: bool, string, int64 for an
	// integer, float64 for a number; an int-or-string carries int64 or
	// string, whichever spelling was set; a raw-YAML graft carries the
	// opaque parsed YAML tree.
	Data any
}

// checkScalar validates one scalar value against the node's Type Schema —
// the schema-local, client-side checks of DESIGN.md Flow §6: the declared
// type (int-or-string in both spellings), enum membership, pattern, numeric
// bounds and multipleOf, and length bounds — resolving the same constraint
// facets Metadata renders. Cross-field logic (CEL rules) stays server-side
// Validate's business (ADR-0004). A valid value comes back normalized,
// carrying its schema type.
func (n *Node) checkScalar(draftPath string, value any) (Value, error) {
	chain, err := n.resolveChain(n.schema)
	if err != nil {
		return Value{}, err
	}
	if isSchemaBlind(chain) {
		return Value{}, blindPositionError(draftPath)
	}
	displayType, err := n.displayType(chain, map[*spec.Schema]bool{})
	if err != nil {
		return Value{}, err
	}
	data, err := typedData(chain, displayType, value, draftPath)
	if err != nil {
		return Value{}, err
	}
	if err := checkConstraints(chain, data, draftPath); err != nil {
		return Value{}, err
	}
	return Value{Type: displayType, Data: data}, nil
}

// typedData normalizes the value to the display type's canonical Go spelling,
// rejecting values of any other type — and rejecting structural positions
// outright: objects, arrays, and map-shaped objects take entries, not a
// scalar value.
func typedData(chain []*spec.Schema, displayType string, value any, draftPath string) (any, error) {
	switch displayType {
	case "boolean", "string", "integer", "number":
		return scalarData(displayType, value, draftPath)
	case "int-or-string":
		return intOrStringData(value, draftPath)
	default:
		return nil, structureError(chain, displayType, draftPath)
	}
}

// scalarData normalizes one single-typed scalar: bool, string, int64, or
// float64. A value of the wrong type is rejected in domain language.
func scalarData(displayType string, value any, draftPath string) (any, error) {
	if data, matches := coerce(displayType, value); matches {
		return data, nil
	}
	return nil, fmt.Errorf("the Type Schema types %s as %s, so a Draft cannot fill it with %s",
		describeDraftPosition(draftPath), displayType, spellValue(value))
}

// coerce matches a Go value against one single-typed display type, yielding
// the canonical spelling: integers normalize to int64 (an integral float
// counts), numbers to float64.
func coerce(displayType string, value any) (any, bool) {
	switch displayType {
	case "boolean":
		data, isBool := value.(bool)
		return data, isBool
	case "string":
		data, isString := value.(string)
		return data, isString
	case "integer":
		data, isIntegral := asInt64(value)
		return data, isIntegral
	default: // number
		data, isNumeric := asFloat64(value)
		return data, isNumeric
	}
}

// intOrStringData normalizes an int-or-string value: both spellings are
// admissible — an integer (any integral spelling, kept as int64) or a string.
func intOrStringData(value any, draftPath string) (any, error) {
	if data, isIntegral := asInt64(value); isIntegral {
		return data, nil
	}
	if data, isString := value.(string); isString {
		return data, nil
	}
	return nil, fmt.Errorf("the Type Schema types %s as int-or-string — an integer or a string, either spelling — so a Draft cannot fill it with %s",
		describeDraftPosition(draftPath), spellValue(value))
}

// structureError explains why a structural position takes no scalar value,
// naming the mutation that does apply there.
func structureError(chain []*spec.Schema, displayType, draftPath string) error {
	resolved := concreteSchema(chain)
	if itemSchema(resolved) != nil {
		return fmt.Errorf("%s is an array: a Draft appends items to it and fills each item, not the array itself",
			describeDraftPosition(draftPath))
	}
	if valueSchema(resolved) != nil {
		return fmt.Errorf("%s is a map-shaped object: a Draft adds keys to it and fills each value, not the map itself",
			describeDraftPosition(draftPath))
	}
	return fmt.Errorf("%s holds structure (%s): a Draft fills values at its schema-defined fields",
		describeDraftPosition(draftPath), displayType)
}

// checkConstraints applies the schema-local constraints Metadata renders —
// enum membership for every type, pattern and length bounds for strings,
// numeric bounds and multipleOf for numbers — to an already-typed value.
func checkConstraints(chain []*spec.Schema, data any, draftPath string) error {
	if err := checkEnum(chain, data, draftPath); err != nil {
		return err
	}
	if text, isString := data.(string); isString {
		return checkString(chain, text, draftPath)
	}
	if number, isNumeric := asFloat64(data); isNumeric {
		return checkNumeric(chain, number, draftPath)
	}
	return nil
}

// checkEnum insists an enum-constrained value is among the admissible ones,
// compared in the spelling a Draft carries (renderEnumValue).
func checkEnum(chain []*spec.Schema, data any, draftPath string) error {
	admissible := renderEnum(firstEnum(chain))
	if len(admissible) == 0 || slices.Contains(admissible, renderEnumValue(data)) {
		return nil
	}
	return fmt.Errorf("the Type Schema admits only %s at %s, not %s",
		strings.Join(admissible, ", "), describeDraftPosition(draftPath), spellValue(data))
}

// checkString applies the string constraints: length bounds counted in
// characters, then the declared pattern. A pattern Go cannot compile is
// skipped — checks are best-effort schema-local, and server-side Validate is
// the safety net.
func checkString(chain []*spec.Schema, text, draftPath string) error {
	length := int64(utf8.RuneCountInString(text))
	if owner := firstSchema(chain, func(s *spec.Schema) bool { return s.MinLength != nil }); owner != nil && length < *owner.MinLength {
		return fmt.Errorf("%s is shorter than the Type Schema's minLength %d at %s",
			spellValue(text), *owner.MinLength, describeDraftPosition(draftPath))
	}
	if owner := firstSchema(chain, func(s *spec.Schema) bool { return s.MaxLength != nil }); owner != nil && length > *owner.MaxLength {
		return fmt.Errorf("%s is longer than the Type Schema's maxLength %d at %s",
			spellValue(text), *owner.MaxLength, describeDraftPosition(draftPath))
	}
	pattern := firstNonEmpty(chain, func(s *spec.Schema) string { return s.Pattern })
	if pattern == "" {
		return nil
	}
	expression, err := regexp.Compile(pattern)
	if err != nil {
		return nil //nolint:nilerr // an uncompilable pattern is server-side Validate's business
	}
	if !expression.MatchString(text) {
		return fmt.Errorf("%s does not match the Type Schema's pattern %q at %s",
			spellValue(text), pattern, describeDraftPosition(draftPath))
	}
	return nil
}

// checkNumeric applies the numeric bounds — minimum and maximum with their
// exclusive flags, then multipleOf.
func checkNumeric(chain []*spec.Schema, number float64, draftPath string) error {
	if err := checkMinimum(chain, number, draftPath); err != nil {
		return err
	}
	if err := checkMaximum(chain, number, draftPath); err != nil {
		return err
	}
	return checkMultipleOf(chain, number, draftPath)
}

func checkMinimum(chain []*spec.Schema, number float64, draftPath string) error {
	owner := firstSchema(chain, func(s *spec.Schema) bool { return s.Minimum != nil })
	if owner == nil || number > *owner.Minimum || (number == *owner.Minimum && !owner.ExclusiveMinimum) {
		return nil
	}
	return fmt.Errorf("%s is below the Type Schema's minimum %s at %s",
		renderNumber(number, false), renderNumber(*owner.Minimum, owner.ExclusiveMinimum), describeDraftPosition(draftPath))
}

func checkMaximum(chain []*spec.Schema, number float64, draftPath string) error {
	owner := firstSchema(chain, func(s *spec.Schema) bool { return s.Maximum != nil })
	if owner == nil || number < *owner.Maximum || (number == *owner.Maximum && !owner.ExclusiveMaximum) {
		return nil
	}
	return fmt.Errorf("%s is above the Type Schema's maximum %s at %s",
		renderNumber(number, false), renderNumber(*owner.Maximum, owner.ExclusiveMaximum), describeDraftPosition(draftPath))
}

func checkMultipleOf(chain []*spec.Schema, number float64, draftPath string) error {
	owner := firstSchema(chain, func(s *spec.Schema) bool { return s.MultipleOf != nil })
	if owner == nil || math.Mod(number, *owner.MultipleOf) == 0 {
		return nil
	}
	return fmt.Errorf("%s is not a multiple of %s at %s",
		renderNumber(number, false), renderNumber(*owner.MultipleOf, false), describeDraftPosition(draftPath))
}

// asInt64 normalizes any integral spelling — Go integer kinds, or a float
// carrying a whole number — to int64.
func asInt64(value any) (int64, bool) {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflected.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if reflected.Uint() <= math.MaxInt64 {
			return int64(reflected.Uint()), true
		}
	case reflect.Float32, reflect.Float64:
		number := reflected.Float()
		if number == math.Trunc(number) && number >= math.MinInt64 && number <= math.MaxInt64 {
			return int64(number), true
		}
	}
	return 0, false
}

// asFloat64 normalizes any numeric spelling to float64.
func asFloat64(value any) (float64, bool) {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(reflected.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(reflected.Uint()), true
	case reflect.Float32, reflect.Float64:
		return reflected.Float(), true
	default:
		return 0, false
	}
}

// spellValue spells a value for a rejection message: strings quoted so the
// spelling difference reads clearly, everything else as Go prints it.
func spellValue(value any) string {
	if text, isString := value.(string); isString {
		return strconv.Quote(text)
	}
	if value == nil {
		return "nothing"
	}
	return fmt.Sprint(value)
}
