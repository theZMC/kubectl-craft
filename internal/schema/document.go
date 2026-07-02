package schema

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"k8s.io/kube-openapi/pkg/spec3"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

// gvkExtension is the component-schema extension the API server uses to tag
// which Kinds a component schema defines. Component schemas without it are
// helper schemas (ObjectMeta, *Spec, JSONSchemaProps, ...), not Kinds.
const gvkExtension = "x-kubernetes-group-version-kind"

// GroupVersionKind identifies a Kind — the unit of browsing and composing —
// exactly as carried by the x-kubernetes-group-version-kind extension.
type GroupVersionKind struct {
	Group   string `json:"group"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
}

// String renders the Kind in glossary form, e.g. "apps/v1 Deployment"; the
// core group renders bare, e.g. "v1 Pod".
func (gvk GroupVersionKind) String() string {
	if gvk.Group == "" {
		return gvk.Version + " " + gvk.Kind
	}
	return gvk.Group + "/" + gvk.Version + " " + gvk.Kind
}

// Document is one parsed OpenAPI v3 group document — the authoritative
// source of every Type Schema its group serves. It is built from the raw
// group-document bytes the data layer fetches, and it answers the two
// questions everything downstream asks: which Kinds does this document
// define, and what is the root Type Schema of a given Kind?
type Document struct {
	openAPI *spec3.OpenAPI
	roots   map[GroupVersionKind]*spec.Schema
}

// ParseDocument parses raw group-document bytes (as returned by the data
// layer's Fetcher) into a Document, indexing every component schema tagged
// as a Kind. Undecodable bytes yield a wrapped error, never a panic.
func ParseDocument(raw []byte) (*Document, error) {
	var openAPI spec3.OpenAPI
	if err := json.Unmarshal(raw, &openAPI); err != nil {
		return nil, fmt.Errorf("parsing the OpenAPI v3 Document: %w", err)
	}

	doc := &Document{openAPI: &openAPI, roots: map[GroupVersionKind]*spec.Schema{}}
	if openAPI.Components == nil {
		return doc, nil
	}

	// Component names are walked in sorted order so that a GVK tagged on
	// more than one component resolves deterministically (first name wins).
	for _, name := range slices.Sorted(maps.Keys(openAPI.Components.Schemas)) {
		component := openAPI.Components.Schemas[name]
		if component == nil {
			continue
		}

		gvks, err := taggedKinds(component)
		if err != nil {
			return nil, fmt.Errorf("parsing the OpenAPI v3 Document: component schema %q: %w", name, err)
		}
		for _, gvk := range gvks {
			if isListWrapper(gvk) {
				continue
			}
			if _, taken := doc.roots[gvk]; taken {
				continue
			}
			doc.roots[gvk] = component
		}
	}

	return doc, nil
}

// Kinds enumerates the Kinds the group document defines, sorted by group,
// version, then kind. Non-Kind component schemas never appear: List wrappers
// are dropped and untagged helper schemas carry no GVK to enumerate.
//
// Shared machinery kinds the server tags into every group document
// (DeleteOptions, Status, WatchEvent, ...) are enumerated honestly;
// narrowing to the Kinds a picker should offer is discovery's job
// (DESIGN.md: kind list from discovery), not this document's.
func (d *Document) Kinds() []GroupVersionKind {
	kinds := slices.Collect(maps.Keys(d.roots))
	slices.SortFunc(kinds, func(a, b GroupVersionKind) int {
		if c := strings.Compare(a.Group, b.Group); c != 0 {
			return c
		}
		if c := strings.Compare(a.Version, b.Version); c != 0 {
			return c
		}
		return strings.Compare(a.Kind, b.Kind)
	})
	return kinds
}

// RootSchema resolves a Kind to its root component Type Schema — the schema
// every field tree grows from. A Kind the document does not define yields a
// clear error naming the missing Type Schema.
func (d *Document) RootSchema(gvk GroupVersionKind) (*spec.Schema, error) {
	root, ok := d.roots[gvk]
	if !ok {
		return nil, fmt.Errorf("this OpenAPI v3 Document defines no Type Schema for Kind %s", gvk)
	}
	return root, nil
}

// taggedKinds decodes the component schema's x-kubernetes-group-version-kind
// extension; a component without the tag defines no Kinds.
func taggedKinds(component *spec.Schema) ([]GroupVersionKind, error) {
	tag, tagged := component.Extensions[gvkExtension]
	if !tagged {
		return nil, nil
	}

	encoded, err := json.Marshal(tag)
	if err != nil {
		return nil, fmt.Errorf("re-encoding the %s extension: %w", gvkExtension, err)
	}
	var gvks []GroupVersionKind
	if err := json.Unmarshal(encoded, &gvks); err != nil {
		return nil, fmt.Errorf("decoding the %s extension: %w", gvkExtension, err)
	}
	return gvks, nil
}

// isListWrapper reports whether the GVK names a collection wrapper
// (DeploymentList, WidgetList, ...) rather than a composable Kind.
// Kubernetes API conventions reserve the "List" suffix for list kinds.
func isListWrapper(gvk GroupVersionKind) bool {
	return strings.HasSuffix(gvk.Kind, "List")
}
