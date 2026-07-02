package schema_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/schema"
)

var _ = Describe("field-search candidate enumeration", func() {
	When("every corpus Kind enumerates its schema-level Field Paths", func() {
		describeCorpusTable(
			"enumeration is finite, deterministic, and well-formed for every Kind",
			func(fixture string) {
				doc := parseFixture(fixture)
				for _, kind := range corpusKinds(doc, fixture) {
					root, err := doc.FieldTree(kind)
					Expect(err).NotTo(HaveOccurred())

					paths := root.FieldPaths()

					Expect(paths).NotTo(BeEmpty(),
						"Kind %s: a browsable Kind must offer search candidates", kind)
					seen := map[string]bool{}
					for _, path := range paths {
						Expect(path).NotTo(BeEmpty(),
							"Kind %s: a candidate must name a position — the root is not a candidate", kind)
						Expect(path).NotTo(ContainSubstring("["),
							"Kind %s: candidates are schema-level Field Paths, dots only — %q", kind, path)
						Expect(seen[path]).To(BeFalse(),
							"Kind %s: %q enumerated twice", kind, path)
						if dot := strings.LastIndex(path, "."); dot >= 0 {
							Expect(seen[path[:dot]]).To(BeTrue(),
								"Kind %s: %q must follow its parent in tree order", kind, path)
						}
						seen[path] = true
					}
					Expect(root.FieldPaths()).To(Equal(paths),
						"Kind %s: enumeration must be deterministic", kind)
				}
			},
		)
	})

	When("the candidates are read back for pinned shapes", func() {
		It("lists fields through arrays, maps, and allOf-wrapped $refs at their dotted Field Paths", func() {
			root := growFieldTree("apis_apps_v1.json", gvk("apps", "v1", "Deployment"))

			Expect(root.FieldPaths()).To(ContainElements(
				"spec.template.spec.containers.imagePullPolicy",
				"spec.selector.matchLabels",
				"spec.template.metadata.labels",
			), "dots address schema-defined fields straight through items and keys")
		})

		It("enumerates a subtree from any node, siblings sorted by field name", func() {
			root := growFieldTree("apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Widget"))

			spec := walkFieldPath(root, "spec")

			Expect(spec.FieldPaths()).To(Equal([]string{"spec.paint", "spec.size"}))
		})

		It("visits each Type Schema once per chain, so the JSONSchemaProps cycle yields finite candidates", func() {
			root := growFieldTree("apis_apiextensions.k8s.io_v1.json",
				gvk("apiextensions.k8s.io", "v1", "CustomResourceDefinition"))

			paths := root.FieldPaths()

			Expect(paths).To(ContainElements(
				"spec.versions.schema.openAPIV3Schema.properties",
				"spec.versions.schema.openAPIV3Schema.not",
			), "the cycle's re-entry fields are themselves candidates")
			Expect(paths).NotTo(ContainElement("spec.versions.schema.openAPIV3Schema.not.not"),
				"a Type Schema already on the chain is not entered again")
			Expect(paths).NotTo(ContainElement("spec.versions.schema.openAPIV3Schema.properties.properties"),
				"the lap beneath the cycle's re-entry is never materialized")
		})

		It("stops at a schema-blind subtree — nothing beneath it is a candidate", func() {
			root := growFieldTree("apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"))

			paths := root.FieldPaths()

			Expect(paths).To(ContainElement("spec.tuning"))
			for _, path := range paths {
				Expect(path).NotTo(HavePrefix("spec.tuning."),
					"the Type Schema says nothing beneath spec.tuning, so search offers nothing there")
			}
		})

		It("keeps enumeration total over a dangling $ref: the field is listed, its subtree is not", func() {
			raw := `{"openapi":"3.0.0","info":{"title":"Kubernetes","version":"1.36"},` +
				`"components":{"schemas":{"com.example.craft.v4.Phantom":{"type":"object",` +
				`"x-kubernetes-group-version-kind":[{"group":"craft.example.com","kind":"Phantom","version":"v4"}],` +
				`"properties":{"spec":{"type":"object","properties":{` +
				`"ghost":{"$ref":"#/components/schemas/com.example.craft.v4.Missing"},` +
				`"solid":{"type":"string"}}}}}}}}`
			doc, err := schema.ParseDocument([]byte(raw))
			Expect(err).NotTo(HaveOccurred())
			root, err := doc.FieldTree(gvk("craft.example.com", "v4", "Phantom"))
			Expect(err).NotTo(HaveOccurred())

			paths := root.FieldPaths()

			Expect(paths).To(ContainElements("spec.ghost", "spec.solid"),
				"the dangling $ref field itself stays findable — its error surfaces at expansion")
			for _, path := range paths {
				Expect(path).NotTo(HavePrefix("spec.ghost."),
					"nothing beneath an unresolvable $ref can be enumerated")
			}
		})
	})
})
