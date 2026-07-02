package schema_test

import (
	"path/filepath"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/schema"
)

// sweepDepth bounds the corpus walk: deep enough to pass the JSONSchemaProps
// cycle re-entry (openAPIV3Schema.properties.properties materializes at eight
// expansions) with room for another lap, while keeping the sweep in the fast
// loop.
const sweepDepth = 10

// scalarDisplayTypes are the display types that promise a value, not
// structure: a node spelling one of these must expand to no children.
var scalarDisplayTypes = []string{"string", "integer", "number", "boolean", "int-or-string"}

// displayTypes is every base display type the metadata contract admits; an
// element-typed array spelling ([]string, [][]object, ...) reduces to one of
// these. A display type outside this set is not resolvable.
var displayTypes = append([]string{"object", "array"}, scalarDisplayTypes...)

// corpusFixtures enumerates the checked-in group documents dynamically, at
// spec-construction time, so a newly captured fixture is swept with no code
// change.
func corpusFixtures() []string {
	matches, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil {
		panic(err) // the pattern is fixed, and only a malformed pattern errors
	}
	fixtures := make([]string, 0, len(matches))
	for _, match := range matches {
		fixtures = append(fixtures, filepath.Base(match))
	}
	return fixtures
}

// describeCorpusTable declares a DescribeTable with one Entry per corpus
// fixture, so every table sweeps whatever documents are checked in.
func describeCorpusTable(description string, body func(fixture string)) {
	args := []any{body}
	for _, fixture := range corpusFixtures() {
		args = append(args, Entry(fixture, fixture))
	}
	DescribeTable(description, args...)
}

// corpusKinds enumerates the Kinds one fixture defines, insisting the
// document defines at least one — a fixture the sweep silently skips would
// hollow out the corpus guarantee.
func corpusKinds(doc *schema.Document, fixture string) []schema.GroupVersionKind {
	GinkgoHelper()
	kinds := doc.Kinds()
	Expect(kinds).NotTo(BeEmpty(), "fixture %s defines no Kinds — nothing swept", fixture)
	return kinds
}

// sweep is everything walking one Kind's field tree observed: the display
// type at every schema-level Field Path visited, and which of those positions
// are schema-blind.
type sweep struct {
	kind        schema.GroupVersionKind
	types       map[string]string
	schemaBlind map[string]bool
}

// sweepKind builds the Kind's field tree and walks it to sweepDepth,
// asserting the per-node invariants at every position it reaches.
func sweepKind(doc *schema.Document, kind schema.GroupVersionKind) *sweep {
	GinkgoHelper()
	root, err := doc.FieldTree(kind)
	Expect(err).NotTo(HaveOccurred(), "Kind %s must grow a field tree from its root Type Schema", kind)
	s := &sweep{kind: kind, types: map[string]string{}, schemaBlind: map[string]bool{}}
	s.walk(root, 0)
	return s
}

// walk asserts one node's invariants — a resolvable display type, children
// consistent with the node's kind, well-formed schema-level Field Paths one
// dotted segment at a time — and descends to the bounded depth.
func (s *sweep) walk(node *schema.Node, depth int) {
	GinkgoHelper()
	path := node.FieldPath()

	metadata, err := node.Metadata()
	Expect(err).NotTo(HaveOccurred(), "Kind %s: reading metadata at %q", s.kind, path)
	Expect(baseDisplayType(metadata.Type)).To(BeElementOf(displayTypes),
		"Kind %s: the display type at %q must resolve", s.kind, path)
	if _, seen := s.types[path]; !seen {
		s.types[path] = metadata.Type
	}
	if metadata.SchemaBlind {
		s.schemaBlind[path] = true
	}

	if depth == sweepDepth {
		return
	}
	children, err := node.Children()
	Expect(err).NotTo(HaveOccurred(), "Kind %s: expanding %q", s.kind, path)
	if slices.Contains(scalarDisplayTypes, metadata.Type) {
		Expect(children).To(BeEmpty(),
			"Kind %s: the %s node at %q promises a value, not structure", s.kind, metadata.Type, path)
	}

	var dotted []string
	for _, child := range children {
		childPath := child.FieldPath()
		if childPath == path {
			// An array's item Node or a map's value Node shares its
			// parent's Field Path; only those structures surface one.
			Expect(metadata.Type).To(Or(Equal("object"), Equal("array"), HavePrefix("[]")),
				"Kind %s: only an array or a map-shaped object shares its Field Path with a child, not the %s at %q",
				s.kind, metadata.Type, path)
		} else {
			segment, extends := strings.CutPrefix(childPath, joinFieldPath(path, ""))
			Expect(extends).To(BeTrue(),
				"Kind %s: child %q must extend its parent's Field Path %q", s.kind, childPath, path)
			Expect(segment).NotTo(BeEmpty(),
				"Kind %s: a Field Path segment under %q must name a field", s.kind, path)
			Expect(segment).NotTo(ContainSubstring("."),
				"Kind %s: %q must extend %q by exactly one dotted segment", s.kind, childPath, path)
			Expect(metadata.Type).To(Equal("object"),
				"Kind %s: only an object surfaces schema-defined fields, not the %s at %q",
				s.kind, metadata.Type, path)
			dotted = append(dotted, childPath)
		}
		s.walk(child, depth+1)
	}
	Expect(slices.IsSorted(dotted)).To(BeTrue(),
		"Kind %s: the children of %q must expand sorted by field name", s.kind, path)
}

// baseDisplayType reduces an element-typed array spelling to its base display
// type: []string to string, [][]object to object, anything else to itself.
func baseDisplayType(displayType string) string {
	for strings.HasPrefix(displayType, "[]") {
		displayType = strings.TrimPrefix(displayType, "[]")
	}
	return displayType
}

// joinFieldPath extends a schema-level Field Path by one dotted segment; at
// the root the segment stands alone.
func joinFieldPath(path, segment string) string {
	if path == "" {
		return segment
	}
	return path + "." + segment
}

var _ = Describe("the fixture corpus", func() {
	When("every checked-in group document is swept end-to-end", func() {
		describeCorpusTable(
			"every corpus fixture builds a correct Type Schema tree",
			func(fixture string) {
				doc := parseFixture(fixture)
				for _, kind := range corpusKinds(doc, fixture) {
					sweepKind(doc, kind)
				}
			},
		)

		describeCorpusTable(
			"contextual requiredness over an empty Draft terminates deterministically for every Kind",
			func(fixture string) {
				doc := parseFixture(fixture)
				for _, kind := range corpusKinds(doc, fixture) {
					root, err := doc.FieldTree(kind)
					Expect(err).NotTo(HaveOccurred())

					missing, err := root.MissingRequired(nil)
					Expect(err).NotTo(HaveOccurred(),
						"Kind %s: contextual requiredness must terminate over an empty Draft", kind)
					for _, fieldPath := range missing {
						Expect(fieldPath).NotTo(BeEmpty(),
							"Kind %s: a missing required Field Path must name a position", kind)
						Expect(fieldPath).NotTo(ContainSubstring("["),
							"Kind %s: an empty Draft instantiates no items or keys, so %q cannot be missing",
							kind, fieldPath)
					}

					again, err := root.MissingRequired(nil)
					Expect(err).NotTo(HaveOccurred())
					Expect(again).To(Equal(missing),
						"Kind %s: contextual requiredness must be deterministic", kind)
				}
			},
		)

		It("sweeps a non-empty corpus", func() {
			// The tables above enumerate testdata/*.json at construction; an
			// empty glob would generate no entries and pass vacuously.
			Expect(corpusFixtures()).NotTo(BeEmpty())
		})
	})

	When("the corpus shapes M1 exists for are read back from the sweep", func() {
		DescribeTable(
			"the sweep observes each pinned shape, not merely survives it",
			func(fixture string, kind schema.GroupVersionKind, path, displayType string, schemaBlind bool) {
				doc := parseFixture(fixture)

				s := sweepKind(doc, kind)

				Expect(s.types).To(HaveKeyWithValue(path, displayType))
				Expect(s.schemaBlind[path]).To(Equal(schemaBlind))
				if schemaBlind {
					for walked := range s.types {
						Expect(walked).NotTo(HavePrefix(path+"."),
							"the Type Schema is blind beneath %q, so the sweep finds no fields there", path)
					}
				}
			},
			Entry("the JSONSchemaProps cycle re-enters through its properties map",
				"apis_apiextensions.k8s.io_v1.json",
				gvk("apiextensions.k8s.io", "v1", "CustomResourceDefinition"),
				"spec.versions.schema.openAPIV3Schema.properties.properties",
				"object", false),
			Entry("the JSONSchemaProps cycle re-enters through not, twice over",
				"apis_apiextensions.k8s.io_v1.json",
				gvk("apiextensions.k8s.io", "v1", "CustomResourceDefinition"),
				"spec.versions.schema.openAPIV3Schema.not.not.not",
				"object", false),
			Entry("the craft.example.com int-or-string flag is its own type flavor",
				"apis_craft.example.com_v1.json",
				gvk("craft.example.com", "v1", "Gadget"),
				"spec.maxUnavailable",
				"int-or-string", false),
			Entry("the craft.example.com preserve-unknown-fields subtree is schema-blind",
				"apis_craft.example.com_v1.json",
				gvk("craft.example.com", "v1", "Gadget"),
				"spec.tuning",
				"object", true),
		)
	})
})
