package schema

import (
	"fmt"
	"maps"
	"slices"
	"strconv"

	"gopkg.in/yaml.v3"
)

// parseGraft parses the raw YAML a Draft grafts at a schema-blind position
// into the opaque value it stores: maps keyed by string (a Manifest's map
// keys are strings — anything else is rejected), slices, and scalars, exactly
// as spelled. Malformed YAML and YAML holding no value at all are rejected in
// domain language.
func parseGraft(draftPath, rawYAML string) (any, error) {
	var parsed any
	if err := yaml.Unmarshal([]byte(rawYAML), &parsed); err != nil {
		return nil, fmt.Errorf("parsing the raw YAML grafted at %s: %w", describeDraftPosition(draftPath), err)
	}
	if parsed == nil {
		return nil, fmt.Errorf("the raw YAML grafted at %s holds no value: unset the graft instead", describeDraftPosition(draftPath))
	}
	return normalizeGraft(draftPath, parsed)
}

// normalizeGraft normalizes the parsed YAML tree to string-keyed maps all the
// way down, so the graft's leaf paths can be spelled as quoted key selectors.
func normalizeGraft(draftPath string, parsed any) (any, error) {
	switch value := parsed.(type) {
	case map[string]any:
		for key, child := range value {
			normalized, err := normalizeGraft(draftPath, child)
			if err != nil {
				return nil, err
			}
			value[key] = normalized
		}
		return value, nil
	case map[any]any:
		return normalizeGraftKeys(draftPath, value)
	case []any:
		for index, child := range value {
			normalized, err := normalizeGraft(draftPath, child)
			if err != nil {
				return nil, err
			}
			value[index] = normalized
		}
		return value, nil
	default:
		return parsed, nil
	}
}

// normalizeGraftKeys converts a mapping whose keys the YAML parser did not
// already narrow to strings, rejecting any key that is not one.
func normalizeGraftKeys(draftPath string, value map[any]any) (any, error) {
	normalized := make(map[string]any, len(value))
	for key, child := range value {
		text, isString := key.(string)
		if !isString {
			return nil, fmt.Errorf("the raw YAML grafted at %s uses %v as a map key: a Manifest's map keys are strings",
				describeDraftPosition(draftPath), key)
		}
		normalizedChild, err := normalizeGraft(draftPath, child)
		if err != nil {
			return nil, err
		}
		normalized[text] = normalizedChild
	}
	return normalized, nil
}

// graftLeafPaths spells the instantiated Draft-level Field Paths a graft
// contributes: every leaf of the opaque parsed value, map keys as quoted key
// selectors, list items as index selectors — the exact grammar MissingRequired
// accepts. An empty map or list is itself a leaf, as is a scalar.
func graftLeafPaths(base string, data any, paths *[]string) {
	switch value := data.(type) {
	case map[string]any:
		if len(value) > 0 {
			for _, key := range slices.Sorted(maps.Keys(value)) {
				graftLeafPaths(base+"["+strconv.Quote(key)+"]", value[key], paths)
			}
			return
		}
	case []any:
		if len(value) > 0 {
			for index, item := range value {
				graftLeafPaths(fmt.Sprintf("%s[%d]", base, index), item, paths)
			}
			return
		}
	}
	*paths = append(*paths, base)
}
