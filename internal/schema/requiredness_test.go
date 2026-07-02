package schema_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/matchers"
	"github.com/thezmc/kubectl-craft/internal/schema"
)

// missingRequired computes the currently-missing required Draft-level Field
// Paths for a Kind, given the Draft's instantiated Draft-level Field Paths.
func missingRequired(fixture string, kind schema.GroupVersionKind, instantiated []string) []string {
	GinkgoHelper()
	root := growFieldTree(fixture, kind)
	missing, err := root.MissingRequired(instantiated)
	Expect(err).NotTo(HaveOccurred())
	return missing
}

// syntheticDocument parses an inline OpenAPI v3 Document spelling shapes the
// captured fixture corpus does not carry.
func syntheticDocument(componentSchemas string) *schema.Document {
	GinkgoHelper()
	raw := `{"openapi":"3.0.0","info":{"title":"Kubernetes","version":"1.36"},` +
		`"components":{"schemas":{` + componentSchemas + `}}}`
	doc, err := schema.ParseDocument([]byte(raw))
	Expect(err).NotTo(HaveOccurred())
	return doc
}

var _ = Describe("contextual requiredness", func() {
	When("a Draft instantiates Field Paths against a Kind's field tree", func() {
		DescribeTable(
			"the currently-missing required Field Paths follow what the Draft has instantiated",
			func(fixture string, kind schema.GroupVersionKind, instantiated []string, missing ...string) {
				Expect(missingRequired(fixture, kind, instantiated)).To(matchers.BeMissingRequired(missing...))
			},

			// apps/v1 Deployment — nothing required at the root, so
			// requiredness surfaces only as the Draft digs in.
			Entry("an empty apps/v1 Deployment Draft misses nothing: its root Type Schema declares no required fields",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				nil),
			Entry("instantiating spec makes the DeploymentSpec required fields missing, in tree order",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				[]string{"spec"},
				"spec.selector", "spec.template"),
			Entry("instantiating a deep Field Path instantiates every position along it",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				[]string{"spec.template.spec"},
				"spec.selector", "spec.template.spec.containers"),
			Entry("containers[0].name is missing only once a container item exists",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				[]string{"spec.template.spec.containers[0]"},
				"spec.selector", "spec.template.spec.containers[0].name"),
			Entry("each instantiated item binds the item schema's required fields separately, in index order",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				[]string{"spec.template.spec.containers[0].name", "spec.template.spec.containers[1]"},
				"spec.selector", "spec.template.spec.containers[1].name"),
			Entry("a satisfied apps/v1 Deployment Draft misses nothing",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				[]string{"spec.selector", "spec.template.spec.containers[0].name"}),

			// craft.example.com Widget — spec is required at the root, so
			// the root-level required chain is missing immediately.
			Entry("an empty craft.example.com/v1 Widget Draft misses its root-level required chain immediately",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Widget"),
				nil,
				"spec", "spec.size"),
			Entry("instantiating the Widget spec leaves its required leaf missing",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Widget"),
				[]string{"spec"},
				"spec.size"),
			Entry("instantiating only an optional Widget field still leaves the required leaf missing",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Widget"),
				[]string{"spec.paint"},
				"spec.size"),
			Entry("a satisfied craft.example.com/v1 Widget Draft misses nothing",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Widget"),
				[]string{"spec.size"}),
			Entry("an empty craft.example.com/v2 Widget Draft misses the same root-level required chain",
				"apis_craft.example.com_v2.json", gvk("craft.example.com", "v2", "Widget"),
				nil,
				"spec", "spec.size"),
			Entry("a satisfied craft.example.com/v2 Widget Draft misses nothing",
				"apis_craft.example.com_v2.json", gvk("craft.example.com", "v2", "Widget"),
				[]string{"spec.color", "spec.finish", "spec.size"}),

			// craft.example.com/v1 Gadget — required fields live one level
			// down, plus the preserve-unknown-fields escape hatch.
			Entry("an empty craft.example.com/v1 Gadget Draft misses nothing: its root Type Schema declares no required fields",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				nil),
			Entry("instantiating the Gadget spec makes both replica bounds missing, in tree order",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				[]string{"spec"},
				"spec.maxReplicas", "spec.minReplicas"),
			Entry("a partially-instantiated Gadget Draft misses only the unset required leaf",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				[]string{"spec.maxReplicas"},
				"spec.minReplicas"),
			Entry("a satisfied craft.example.com/v1 Gadget Draft misses nothing",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				[]string{"spec.maxReplicas", "spec.minReplicas"}),
			Entry("raw YAML grafted beneath a preserve-unknown-fields subtree tracks nothing required",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				[]string{"spec.maxReplicas", "spec.minReplicas", "spec.tuning.knobs"}),

			// apiextensions.k8s.io/v1 CustomResourceDefinition — a real
			// multi-level root chain, and the JSONSchemaProps $ref cycle.
			Entry("an empty CustomResourceDefinition Draft misses the whole root-level required chain, stopping at the versions array",
				"apis_apiextensions.k8s.io_v1.json", gvk("apiextensions.k8s.io", "v1", "CustomResourceDefinition"),
				nil,
				"spec", "spec.group", "spec.names", "spec.names.kind", "spec.names.plural", "spec.scope", "spec.versions"),
			Entry("instantiating a version item binds its required fields while the rest of the chain stays missing",
				"apis_apiextensions.k8s.io_v1.json", gvk("apiextensions.k8s.io", "v1", "CustomResourceDefinition"),
				[]string{"spec.versions[0]"},
				"spec.group", "spec.names", "spec.names.kind", "spec.names.plural", "spec.scope",
				"spec.versions[0].name", "spec.versions[0].served", "spec.versions[0].storage"),
			Entry("a Draft deep inside the JSONSchemaProps $ref cycle terminates without materializing the tree",
				"apis_apiextensions.k8s.io_v1.json", gvk("apiextensions.k8s.io", "v1", "CustomResourceDefinition"),
				[]string{`spec.versions[0].schema.openAPIV3Schema.properties["replicas"].properties["limit"]`},
				"spec.group", "spec.names", "spec.names.kind", "spec.names.plural", "spec.scope",
				"spec.versions[0].name", "spec.versions[0].served", "spec.versions[0].storage"),
		)

		It("terminates deep JSONSchemaProps chains one instantiated position at a time", func() {
			root := growFieldTree("apis_apiextensions.k8s.io_v1.json",
				gvk("apiextensions.k8s.io", "v1", "CustomResourceDefinition"))
			deep := "spec.versions[0].schema.openAPIV3Schema" +
				strings.Repeat(`.properties["nested"]`, 64)

			missing, err := root.MissingRequired([]string{deep})

			Expect(err).NotTo(HaveOccurred())
			Expect(missing).To(matchers.BeMissingRequired(
				"spec.group", "spec.names", "spec.names.kind", "spec.names.plural", "spec.scope",
				"spec.versions[0].name", "spec.versions[0].served", "spec.versions[0].storage",
			))
		})
	})

	When("a map value's Type Schema declares required fields", func() {
		rackTree := func() *schema.Node {
			GinkgoHelper()
			doc := syntheticDocument(
				`"com.example.craft.v1.Rack":{"type":"object",` +
					`"x-kubernetes-group-version-kind":[{"group":"craft.example.com","version":"v1","kind":"Rack"}],` +
					`"properties":{"slots":{"type":"object",` +
					`"additionalProperties":{"$ref":"#/components/schemas/com.example.craft.v1.Slot"}}}},` +
					`"com.example.craft.v1.Slot":{"type":"object","required":["width"],` +
					`"properties":{"width":{"type":"integer"}}}`,
			)
			root, err := doc.FieldTree(gvk("craft.example.com", "v1", "Rack"))
			Expect(err).NotTo(HaveOccurred())
			return root
		}

		DescribeTable(
			"required fields bind per instantiated key, keys in lexical order",
			func(instantiated []string, missing ...string) {
				computed, err := rackTree().MissingRequired(instantiated)
				Expect(err).NotTo(HaveOccurred())
				Expect(computed).To(matchers.BeMissingRequired(missing...))
			},
			Entry("a map with no instantiated keys misses nothing beneath it",
				[]string{"slots"}),
			Entry("instantiating a key makes the value schema's required leaf missing",
				[]string{`slots["front"]`},
				`slots["front"].width`),
			Entry("each key binds separately and satisfied keys drop out",
				[]string{`slots["rear"]`, `slots["front"].width`},
				`slots["rear"].width`),
		)
	})

	When("a required chain cycles through its own Type Schema", func() {
		It("terminates, reporting each Field Path once per schema on the chain", func() {
			doc := syntheticDocument(
				`"com.example.craft.v1.Loop":{"type":"object",` +
					`"x-kubernetes-group-version-kind":[{"group":"craft.example.com","version":"v1","kind":"Loop"}],` +
					`"required":["again"],` +
					`"properties":{"again":{"$ref":"#/components/schemas/com.example.craft.v1.Loop"}}}`,
			)
			root, err := doc.FieldTree(gvk("craft.example.com", "v1", "Loop"))
			Expect(err).NotTo(HaveOccurred())

			missing, err := root.MissingRequired(nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(missing).To(matchers.BeMissingRequired("again", "again.again"))
		})
	})

	When("the Draft names positions the Type Schema cannot place", func() {
		DescribeTable(
			"the computation yields a clear error instead of guessing",
			func(instantiated, complaint string) {
				root := growFieldTree("apis_apps_v1.json", gvk("apps", "v1", "Deployment"))

				_, err := root.MissingRequired([]string{instantiated})

				Expect(err).To(MatchError(ContainSubstring(complaint)))
			},
			Entry("a field the Type Schema does not define",
				"spec.flavor", `no field "flavor"`),
			Entry("a selector on a plain object",
				"spec[0]", "is not an array or a map-shaped object"),
			Entry("a field addressed directly under an array",
				"spec.template.spec.containers.name", "is an array"),
			Entry("a map key selector on an array",
				`spec.template.spec.containers["web"]`, "is an array"),
			Entry("an index selector on a map-shaped object",
				"spec.template.metadata.labels[0]", "is a map-shaped object"),
		)
	})

	When("an instantiated Draft-level Field Path is malformed", func() {
		DescribeTable(
			"the path is rejected with the malformation named",
			func(path, complaint string) {
				root := growFieldTree("apis_apps_v1.json", gvk("apps", "v1", "Deployment"))

				_, err := root.MissingRequired([]string{path})

				Expect(err).To(MatchError(ContainSubstring("malformed Draft-level Field Path")))
				Expect(err).To(MatchError(ContainSubstring(complaint)))
			},
			Entry("the empty path", "", "expected a field name"),
			Entry("a doubled dot", "spec..size", "expected a field name"),
			Entry("a path starting with a selector", "[0]", "expected a field name"),
			Entry("an unclosed selector", "spec[0", "missing its closing ']'"),
			Entry("a selector that is neither an index nor a quoted key", "spec[zero]",
				"neither an item index nor a quoted map key"),
			Entry("a bare word after a selector", `spec.containers[0]name`, "expected '.' or '['"),
		)
	})
})
