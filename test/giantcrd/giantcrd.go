// Package giantcrd deterministically generates the giant fixture: a
// pathological OpenAPI v3 group document on the order of real-world giant
// CRDs' shapes but an order of magnitude past their size, backing the
// huge-CRD perf pass (MILESTONES.md — M5).
//
// Why generated rather than captured: the biggest real CRDs measure far
// below the scale the perf budgets need — prometheuses.monitoring.coreos.com
// at prometheus-operator v0.92.1 spells ~2,000 typed schema nodes in 830KB
// of YAML, scrapeconfigs ~2,100 — so no single captured Kind reaches the
// ≥10k schema-addressable nodes the perf pass regresses against, and
// capturing a whole multi-CRD group would add several megabytes of fixture
// bytes for a corpus still short of that bar. The generator concentrates
// the pathologies instead: deep nesting (a 40-level required spine), wide
// sibling fans (10 sectors × 50 units × 20 fields — 10,000 leaf fields),
// big enums, arrays, map-shaped objects, int-or-string, and a schema-blind
// subtree, in one Kind the fixture corpus sweeps like any captured
// document.
//
// The output is deterministic — the checked-in fixture bytes in
// internal/schema/testdata are exactly Document's output, and a spec pins
// that — so `mise run fixtures:generate-giant` regenerates them verbatim.
package giantcrd

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// Group and Kind identify the giant Kind exactly as the generated
	// documents' x-kubernetes-group-version-kind extensions tag it.
	Group = "giant.example.com"
	Kind  = "Giant"

	// spineDepth is how deep the required spine nests — past any real
	// CRD's nesting, so depth-dependent walks (assured required chains,
	// deep landings) are measured at pathological depth.
	spineDepth = 40

	// v1Sectors/v2Sectors × unitsPerSector × fieldsPerUnit size the wide
	// fan: v1 carries 10 × 50 × 20 = 10,000 leaf fields, which is what
	// lifts the document past 10k schema-addressable nodes; v2 keeps the
	// first five sectors, so a version switch drops half the grid.
	v1Sectors      = 10
	v2Sectors      = 5
	unitsPerSector = 50
	fieldsPerUnit  = 20

	// modeValues sizes spec.mode's enum — the big-enum pathology.
	modeValues = 100
)

// Versions are the served versions the generator spells, v1 the giant and
// v2 the smaller target a version switch carries the Draft onto.
var Versions = []string{"v1", "v2"}

// GroupVersionPath spells the group-version path the live /openapi/v3 index
// would name for one version, e.g. "apis/giant.example.com/v1".
func GroupVersionPath(version string) string {
	return "apis/" + Group + "/" + version
}

// FixtureName spells the checked-in fixture filename for one version, in
// the capture tooling's naming convention (path separators to underscores).
func FixtureName(version string) string {
	return "apis_" + Group + "_" + version + ".json"
}

// DeepSpinePath is the giant's deepest schema-level Field Path at v1 —
// spec.spine.next…next.anchor, spineDepth levels down — the position the
// perf pass lands on, fills, and watches drop on a version switch (v2
// renames the deepest scalar to beacon).
func DeepSpinePath() string {
	return "spec.spine" + strings.Repeat(".next", spineDepth-1) + ".anchor"
}

// Document generates one version's group document as raw bytes, exactly as
// checked in. Marshaling goes through encoding/json's sorted map keys, so
// the bytes are deterministic. An unknown version errors.
func Document(version string) ([]byte, error) {
	root, err := rootSchema(version)
	if err != nil {
		return nil, err
	}
	componentName := fmt.Sprintf("com.example.giant.%s.%s", version, Kind)
	raw, err := json.Marshal(object{
		"openapi": "3.0.0",
		"info":    object{"title": "Kubernetes", "version": "1.36"},
		"components": object{"schemas": object{
			componentName: root,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling the giant %s group document: %w", version, err)
	}
	return raw, nil
}

// object is one JSON object of the generated document; encoding/json
// marshals its keys sorted, which is what keeps Document deterministic.
type object = map[string]any

// rootSchema grows one version's root Type Schema: the Kind identity
// fields, a small metadata object, and the spec carrying every pathology.
func rootSchema(version string) (object, error) {
	spec, err := specSchema(version)
	if err != nil {
		return nil, err
	}
	return object{
		"type":        "object",
		"description": "Giant is the synthetic giant Kind of the huge-CRD perf pass.",
		"x-kubernetes-group-version-kind": []object{
			{"group": Group, "kind": Kind, "version": version},
		},
		"required": []string{"spec"},
		"properties": object{
			"apiVersion": object{"type": "string", "description": "APIVersion defines the versioned schema of this representation of an object."},
			"kind":       object{"type": "string", "description": "Kind is a string value representing the REST resource this object represents."},
			"metadata":   metadataSchema(),
			"spec":       spec,
		},
	}, nil
}

// metadataSchema is a small inline stand-in for ObjectMeta: enough fields
// for identity and the label/annotation maps, nothing more.
func metadataSchema() object {
	return object{
		"type":        "object",
		"description": "Standard object metadata.",
		"properties": object{
			"name":        object{"type": "string", "description": "Name must be unique within a namespace."},
			"namespace":   object{"type": "string", "description": "Namespace defines the space within which each name must be unique."},
			"labels":      object{"type": "object", "additionalProperties": object{"type": "string"}, "description": "Map of string keys and values that can be used to organize and categorize objects."},
			"annotations": object{"type": "object", "additionalProperties": object{"type": "string"}, "description": "Annotations is an unstructured key value map stored with a resource."},
		},
	}
}

// specSchema is the spec object carrying every pathology the perf pass
// measures: the wide grid, the deep spine, the big enum, arrays, maps,
// int-or-string, and a schema-blind subtree. v2 narrows the grid, retypes
// one unit field, and renames the spine's deepest scalar, so a version
// switch has real drops to report.
func specSchema(version string) (object, error) {
	sectors, err := sectorCount(version)
	if err != nil {
		return nil, err
	}
	return object{
		"type":        "object",
		"description": "GiantSpec concentrates the pathologies real giant CRDs spread out.",
		"required":    []string{"mode", "spine"},
		"x-kubernetes-validations": []object{
			{"rule": "has(self.mode)", "message": "mode must be set"},
		},
		"properties": object{
			"mode":       enumSchema("mode", modeValues),
			"grade":      enumSchema("grade", 25),
			"spine":      spineSchema(version, 0),
			"grid":       gridSchema(version, sectors),
			"chain":      chainSchema(),
			"matrix":     matrixSchema(),
			"racks":      racksSchema(),
			"labelsAt":   object{"type": "object", "additionalProperties": object{"type": "string"}, "description": "labelsAt is a scalar-valued map-shaped object."},
			"portOrName": portOrNameSchema(),
			"freeform":   object{"type": "object", "x-kubernetes-preserve-unknown-fields": true, "description": "freeform is the schema-blind subtree; composing routes it to raw YAML."},
		},
	}, nil
}

// sectorCount is how many grid sectors one version carries; v2 dropping
// half the grid is what gives the version switch a big drop report.
func sectorCount(version string) (int, error) {
	switch version {
	case "v1":
		return v1Sectors, nil
	case "v2":
		return v2Sectors, nil
	default:
		return 0, fmt.Errorf("the giant fixture serves versions %v, not %q", Versions, version)
	}
}

// enumSchema is one big-enum string field: name-000 … name-NNN.
func enumSchema(name string, count int) object {
	values := make([]string, 0, count)
	for index := range count {
		values = append(values, fmt.Sprintf("%s-%03d", name, index))
	}
	return object{
		"type":        "string",
		"description": fmt.Sprintf("%s selects one of %d admissible values.", name, count),
		"enum":        values,
	}
}

// spineSchema nests spineDepth required levels: every level requires the
// next, so an empty Draft's assured required chain runs the spine's full
// depth. The deepest level carries one scalar — anchor at v1, renamed to
// beacon at v2, so a deep value drops on a version switch.
func spineSchema(version string, level int) object {
	if level == spineDepth-1 {
		scalar := "anchor"
		if version == "v2" {
			scalar = "beacon"
		}
		return object{
			"type":        "object",
			"description": fmt.Sprintf("Level %02d is the spine's deepest position.", level),
			"required":    []string{scalar},
			"properties": object{
				scalar: object{"type": "string", "description": "The spine's deepest scalar."},
			},
		}
	}
	return object{
		"type":        "object",
		"description": fmt.Sprintf("Level %02d of the required spine.", level),
		"required":    []string{"next"},
		"properties": object{
			"next": spineSchema(version, level+1),
			"mark": object{"type": "string", "description": fmt.Sprintf("mark annotates level %02d.", level)},
		},
	}
}

// gridSchema is the wide fan: sectors × units × fields, every leaf typed
// and described like a controller-gen giant would spell it. f16 is an
// integer at v1 and a string at v2 — the type-family drop.
func gridSchema(version string, sectors int) object {
	sectorProperties := object{}
	for sector := range sectors {
		sectorProperties[fmt.Sprintf("sector%02d", sector)] = sectorSchema(version, sector)
	}
	return object{
		"type":        "object",
		"description": "grid is the wide sibling fan lifting the document past 10k schema-addressable nodes.",
		"properties":  sectorProperties,
	}
}

// sectorSchema is one grid sector: unitsPerSector sibling units.
func sectorSchema(version string, sector int) object {
	unitProperties := object{}
	for unit := range unitsPerSector {
		unitProperties[fmt.Sprintf("unit%02d", unit)] = unitSchema(version, sector, unit)
	}
	return object{
		"type":        "object",
		"description": fmt.Sprintf("Sector %02d fans out %d sibling units.", sector, unitsPerSector),
		"properties":  unitProperties,
	}
}

// unitSchema is one grid unit: fieldsPerUnit scalar leaves with the small
// type variety a real giant carries.
func unitSchema(version string, sector, unit int) object {
	fields := object{}
	for field := range fieldsPerUnit {
		fields[fmt.Sprintf("f%02d", field)] = fieldSchema(version, sector, unit, field)
	}
	return object{
		"type":        "object",
		"description": fmt.Sprintf("Unit %02d of sector %02d.", unit, sector),
		"properties":  fields,
	}
}

// fieldSchema is one grid leaf. Most are strings; f16 is an integer at v1
// and a string at v2 (the carry-over type drop), f17 a boolean, f18 a
// number, and f19 a bounded string, so widgets and constraints both appear
// across the fan.
func fieldSchema(version string, sector, unit, field int) object {
	description := fmt.Sprintf("Field f%02d of unit %02d in sector %02d.", field, unit, sector)
	switch field {
	case 16:
		leafType := "integer"
		if version == "v2" {
			leafType = "string"
		}
		return object{"type": leafType, "description": description}
	case 17:
		return object{"type": "boolean", "description": description}
	case 18:
		return object{"type": "number", "description": description}
	case 19:
		return object{"type": "string", "description": description, "maxLength": int64(64)}
	default:
		return object{"type": "string", "description": description}
	}
}

// chainSchema is the array-of-objects pathology: items with a required
// field, so instantiating an item at depth moves the missing-required set.
func chainSchema() object {
	return object{
		"type":        "array",
		"description": "chain is an array of object items.",
		"items": object{
			"type":     "object",
			"required": []string{"name"},
			"properties": object{
				"name":   object{"type": "string", "description": "name identifies the link."},
				"weight": object{"type": "integer", "description": "weight orders the link."},
				"tags":   object{"type": "array", "items": object{"type": "string"}, "description": "tags label the link."},
			},
		},
	}
}

// matrixSchema is the nested-array pathology: [][]string.
func matrixSchema() object {
	return object{
		"type":        "array",
		"description": "matrix is an array of arrays of strings.",
		"items":       object{"type": "array", "items": object{"type": "string"}},
	}
}

// racksSchema is the map-crossing pathology: a map-shaped object whose
// values carry schema-defined fields, so search landings cross a key.
func racksSchema() object {
	return object{
		"type":        "object",
		"description": "racks is a map-shaped object with structured values.",
		"additionalProperties": object{
			"type": "object",
			"properties": object{
				"label": object{"type": "string", "description": "label names the rack."},
			},
		},
	}
}

// portOrNameSchema is the int-or-string pathology, spelled the way
// CRD-published schemas spell it.
func portOrNameSchema() object {
	return object{
		"description":                "portOrName accepts a port number or a port name.",
		"anyOf":                      []object{{"type": "integer"}, {"type": "string"}},
		"x-kubernetes-int-or-string": true,
	}
}
