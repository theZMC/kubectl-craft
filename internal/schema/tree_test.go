package schema_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/thezmc/kubectl-craft/internal/schema"
)

// growFieldTree grows the field tree for a Kind from a fixture's root Type Schema.
func growFieldTree(fixture string, kind schema.GroupVersionKind) *schema.Node {
	GinkgoHelper()
	root, err := parseFixture(fixture).FieldTree(kind)
	Expect(err).NotTo(HaveOccurred())
	return root
}

// walkFieldPath resolves a schema-level Field Path one dotted segment at a time.
func walkFieldPath(root *schema.Node, path string) *schema.Node {
	GinkgoHelper()
	node := root
	for _, segment := range strings.Split(path, ".") {
		child, err := node.Child(segment)
		Expect(err).NotTo(HaveOccurred())
		node = child
	}
	return node
}

// childFieldPaths expands a node and collects its children's Field Paths.
func childFieldPaths(node *schema.Node) []string {
	GinkgoHelper()
	children, err := node.Children()
	Expect(err).NotTo(HaveOccurred())
	paths := make([]string, 0, len(children))
	for _, child := range children {
		paths = append(paths, child.FieldPath())
	}
	return paths
}

var _ = Describe("the field tree", func() {
	When("grown from a Kind's root Type Schema", func() {
		DescribeTable(
			"each node materializes at its schema-level Field Path",
			func(fixture string, kind schema.GroupVersionKind, path string) {
				root := growFieldTree(fixture, kind)

				node := walkFieldPath(root, path)

				Expect(node.FieldPath()).To(Equal(path))
			},
			Entry("apps/v1 Deployment reaches a container's image through the containers array",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				"spec.template.spec.containers.image"),
			Entry("apps/v1 Deployment reaches the pod template's labels map",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				"spec.template.metadata.labels"),
			Entry("apps/v1 Deployment reaches the selector through its allOf-wrapped $ref",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				"spec.selector.matchLabels"),
			Entry("apiextensions.k8s.io/v1 CustomResourceDefinition reaches a version's validation schema",
				"apis_apiextensions.k8s.io_v1.json", gvk("apiextensions.k8s.io", "v1", "CustomResourceDefinition"),
				"spec.versions.schema.openAPIV3Schema"),
			Entry("craft.example.com/v1 Widget spells paint",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Widget"),
				"spec.paint"),
			Entry("craft.example.com/v2 Widget respells it color",
				"apis_craft.example.com_v2.json", gvk("craft.example.com", "v2", "Widget"),
				"spec.color"),
			Entry("craft.example.com/v2 Widget adds finish",
				"apis_craft.example.com_v2.json", gvk("craft.example.com", "v2", "Widget"),
				"spec.finish"),
			Entry("craft.example.com/v1 Gadget reaches the int-or-string field",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				"spec.maxUnavailable"),
			Entry("craft.example.com/v1 Gadget reaches its metadata name",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				"metadata.name"),
		)

		It("yields an error for a Kind the OpenAPI v3 Document does not define", func() {
			doc := parseFixture("apis_craft.example.com_v1.json")

			_, err := doc.FieldTree(gvk("craft.example.com", "v1", "Doohickey"))

			Expect(err).To(MatchError(ContainSubstring("no Type Schema for Kind craft.example.com/v1 Doohickey")))
		})
	})

	When("a node is expanded", func() {
		DescribeTable(
			"each structural spelling surfaces the expected children",
			func(fixture string, kind schema.GroupVersionKind, path string, childPaths types.GomegaMatcher) {
				root := growFieldTree(fixture, kind)
				node := root
				if path != "" {
					node = walkFieldPath(root, path)
				}

				Expect(childFieldPaths(node)).To(childPaths)
			},
			Entry("object properties become child Nodes at the root",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"), "",
				ContainElements("apiVersion", "kind", "metadata", "spec")),
			Entry("an allOf-wrapped single $ref resolves transparently to its object properties",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"), "spec",
				ContainElements("spec.replicas", "spec.selector", "spec.template")),
			Entry("an array exposes its item schema as a single child sharing the Field Path",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"), "spec.template.spec.containers",
				ConsistOf("spec.template.spec.containers")),
			Entry("a map-shaped object exposes its value schema as a single child sharing the Field Path",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"), "spec.template.metadata.labels",
				ConsistOf("spec.template.metadata.labels")),
			Entry("a scalar leaf yields no children",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"), "spec.replicas",
				BeEmpty()),
			Entry("an int-or-string leaf yields no children",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"), "spec.maxUnavailable",
				BeEmpty()),
			Entry("a preserve-unknown-fields object names no children",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"), "spec.tuning",
				BeEmpty()),
		)

		It("resolves the named field through an array's item schema", func() {
			root := growFieldTree("apis_apps_v1.json", gvk("apps", "v1", "Deployment"))
			containers := walkFieldPath(root, "spec.template.spec.containers")

			items, err := containers.Children()
			Expect(err).NotTo(HaveOccurred())
			Expect(items).To(HaveLen(1))
			viaItem, err := items[0].Child("image")
			Expect(err).NotTo(HaveOccurred())
			viaArray, err := containers.Child("image")
			Expect(err).NotTo(HaveOccurred())

			Expect(viaItem.FieldPath()).To(Equal("spec.template.spec.containers.image"))
			Expect(viaArray.FieldPath()).To(Equal(viaItem.FieldPath()),
				"dots address schema-defined fields straight through items")
		})

		It("yields a clear error for a field the Type Schema does not define", func() {
			root := growFieldTree("apis_apps_v1.json", gvk("apps", "v1", "Deployment"))
			spec := walkFieldPath(root, "spec")

			_, err := spec.Child("flavor")

			Expect(err).To(MatchError(ContainSubstring(`no field "flavor"`)))
			Expect(err).To(MatchError(ContainSubstring(`Field Path "spec"`)))
		})
	})

	When("a Type Schema references itself", func() {
		DescribeTable(
			"expanding the JSONSchemaProps cycle terminates at every step",
			func(segment string, depth int) {
				root := growFieldTree("apis_apiextensions.k8s.io_v1.json",
					gvk("apiextensions.k8s.io", "v1", "CustomResourceDefinition"))
				node := walkFieldPath(root, "spec.versions.schema.openAPIV3Schema")

				for range depth {
					child, err := node.Child(segment)
					Expect(err).NotTo(HaveOccurred())
					node = child
				}

				Expect(node.FieldPath()).To(Equal(
					"spec.versions.schema.openAPIV3Schema" + strings.Repeat("."+segment, depth),
				))
				next, err := node.Child(segment)
				Expect(err).NotTo(HaveOccurred())
				Expect(next.FieldPath()).To(Equal(node.FieldPath()+"."+segment),
					"the cyclic node stays expandable, one cheap step at a time")
			},
			Entry("through the properties map of JSONSchemaProps", "properties", 64),
			Entry("through the not schema of JSONSchemaProps", "not", 64),
			Entry("through the allOf array of JSONSchemaProps", "allOf", 64),
		)
	})

	When("a $ref cannot be resolved to a concrete Type Schema", func() {
		brokenDocument := func(specFragment string) *schema.Document {
			GinkgoHelper()
			raw := `{"openapi":"3.0.0","info":{"title":"Kubernetes","version":"1.36"},` +
				`"components":{"schemas":{` +
				`"com.example.craft.v1.Broken":{"type":"object",` +
				`"x-kubernetes-group-version-kind":[{"group":"craft.example.com","version":"v1","kind":"Broken"}],` +
				`"properties":{"spec":` + specFragment + `}},` +
				`"com.example.craft.v1.Ouroboros":{"$ref":"#/components/schemas/com.example.craft.v1.Snake"},` +
				`"com.example.craft.v1.Snake":{"$ref":"#/components/schemas/com.example.craft.v1.Ouroboros"}}}}`
			doc, err := schema.ParseDocument([]byte(raw))
			Expect(err).NotTo(HaveOccurred())
			return doc
		}

		DescribeTable(
			"the tree still grows lazily and only the expansion errors",
			func(specFragment, complaint string) {
				doc := brokenDocument(specFragment)

				root, err := doc.FieldTree(gvk("craft.example.com", "v1", "Broken"))
				Expect(err).NotTo(HaveOccurred(),
					"growing the tree resolves nothing — $refs are chased at expansion")
				children, err := root.Children()
				Expect(err).NotTo(HaveOccurred(),
					"the broken $ref is only spelled, not chased, when spec materializes")
				Expect(children).To(HaveLen(1))

				_, err = children[0].Children()

				Expect(err).To(MatchError(ContainSubstring(complaint)))
				Expect(err).To(MatchError(ContainSubstring(`Field Path "spec"`)))
			},
			Entry("a $ref naming a component schema the OpenAPI v3 Document does not define",
				`{"$ref":"#/components/schemas/com.example.craft.v1.Missing"}`,
				"names a component schema this OpenAPI v3 Document does not define"),
			Entry("a $ref pointing outside the document's component schemas",
				`{"$ref":"https://example.com/elsewhere.json#/Widget"}`,
				"points outside this OpenAPI v3 Document's component schemas"),
			Entry("a $ref chain that cycles without reaching a concrete Type Schema",
				`{"$ref":"#/components/schemas/com.example.craft.v1.Ouroboros"}`,
				"cycles without reaching a concrete Type Schema"),
		)
	})
})
