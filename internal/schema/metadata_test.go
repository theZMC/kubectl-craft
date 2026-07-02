package schema_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/thezmc/kubectl-craft/internal/schema"
)

// nodeMetadata walks a schema-level Field Path (the root when empty) and
// reads the node's metadata.
func nodeMetadata(fixture string, kind schema.GroupVersionKind, path string) schema.Metadata {
	GinkgoHelper()
	node := growFieldTree(fixture, kind)
	if path != "" {
		node = walkFieldPath(node, path)
	}
	metadata, err := node.Metadata()
	Expect(err).NotTo(HaveOccurred())
	return metadata
}

// deploymentMetadata reads node metadata off the apps/v1 Deployment Type Schema.
func deploymentMetadata(path string) schema.Metadata {
	GinkgoHelper()
	return nodeMetadata("apis_apps_v1.json", gvk("apps", "v1", "Deployment"), path)
}

// gadgetMetadata reads node metadata off the craft.example.com/v1 Gadget Type Schema.
func gadgetMetadata(path string) schema.Metadata {
	GinkgoHelper()
	return nodeMetadata("apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"), path)
}

var _ = Describe("node metadata", func() {
	When("a node's display type is read", func() {
		DescribeTable(
			"each Type Schema flavor renders its display type",
			func(read func(string) schema.Metadata, path, displayType string) {
				Expect(read(path).Type).To(Equal(displayType))
			},
			Entry("the root of the field tree is an object",
				deploymentMetadata, "", "object"),
			Entry("a string leaf", deploymentMetadata, "metadata.name", "string"),
			Entry("an integer leaf", deploymentMetadata, "spec.replicas", "integer"),
			Entry("a boolean leaf", deploymentMetadata, "spec.paused", "boolean"),
			Entry("a number leaf", gadgetMetadata, "spec.efficiency", "number"),
			Entry("an array is element-typed through its item schema's $ref",
				deploymentMetadata, "spec.template.spec.containers", "[]object"),
			Entry("an array of strings is element-typed as []string",
				deploymentMetadata, "spec.template.spec.containers.args", "[]string"),
			Entry("a map-shaped object displays as object",
				deploymentMetadata, "spec.template.metadata.labels", "object"),
			Entry("a built-in int-or-string field is its own flavor, never object",
				deploymentMetadata, "spec.strategy.rollingUpdate.maxSurge", "int-or-string"),
			Entry("a CRD-published int-or-string field is the same flavor",
				gadgetMetadata, "spec.maxUnavailable", "int-or-string"),
			Entry("a preserve-unknown-fields subtree still displays as object",
				gadgetMetadata, "spec.tuning", "object"),
		)
	})

	When("a node's description is read", func() {
		DescribeTable(
			"the field's documentation surfaces, wrapper text winning over the referenced component's",
			func(read func(string) schema.Metadata, path string, description types.GomegaMatcher) {
				Expect(read(path).Description).To(description)
			},
			Entry("the root carries the Kind's own description",
				gadgetMetadata, "",
				Equal("Gadget exercises int-or-string, preserve-unknown-fields, and CEL validation shapes.")),
			Entry("a plain field carries its schema description",
				gadgetMetadata, "spec.tuning",
				Equal("Free-form tuning knobs passed through untouched; the schema deliberately names no fields here.")),
			Entry("an allOf-wrapped $ref keeps the wrapper's field-specific description",
				deploymentMetadata, "spec",
				Equal("Specification of the desired behavior of the Deployment.")),
			Entry("the wrapper on metadata wins over ObjectMeta's generic description",
				deploymentMetadata, "metadata",
				HavePrefix("Standard object's metadata.")),
		)
	})

	When("a node's enum values are read", func() {
		DescribeTable(
			"declared enums list their admissible values verbatim",
			func(read func(string) schema.Metadata, path string, enum types.GomegaMatcher) {
				Expect(read(path).Enum).To(enum)
			},
			Entry("the Deployment strategy type names its two strategies",
				deploymentMetadata, "spec.strategy.type",
				Equal([]string{"Recreate", "RollingUpdate"})),
			Entry("a container's imagePullPolicy names its three policies",
				deploymentMetadata, "spec.template.spec.containers.imagePullPolicy",
				Equal([]string{"Always", "IfNotPresent", "Never"})),
			Entry("a field without an enum lists nothing",
				deploymentMetadata, "spec.replicas",
				BeEmpty()),
		)
	})

	When("a node's constraints are read", func() {
		DescribeTable(
			"declared constraints render as display text",
			func(read func(string) schema.Metadata, path string, constraints types.GomegaMatcher) {
				Expect(read(path).Constraints).To(constraints)
			},
			Entry("a format surfaces as a constraint",
				deploymentMetadata, "spec.replicas",
				ConsistOf("format: int32")),
			Entry("pattern and length bounds surface together",
				gadgetMetadata, "spec.nickname",
				ConsistOf("pattern: ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$", "minLength: 1", "maxLength: 63")),
			Entry("numeric bounds surface, exclusive bounds marked",
				gadgetMetadata, "spec.efficiency",
				ConsistOf("minimum: 0 (exclusive)", "maximum: 1")),
			Entry("item bounds surface on an array",
				gadgetMetadata, "spec.gears",
				ConsistOf("minItems: 1", "maxItems: 5")),
			Entry("multipleOf surfaces on an integer",
				gadgetMetadata, "spec.teeth",
				ConsistOf("multipleOf: 2")),
			Entry("a CEL rule on a scalar surfaces as constraint text with its message",
				gadgetMetadata, "spec.profile",
				ConsistOf("rule: self in ['economy', 'balanced', 'performance'] — profile must be economy, balanced, or performance")),
			Entry("a CEL rule on an object surfaces the cross-field invariant",
				gadgetMetadata, "spec",
				ConsistOf("rule: self.minReplicas <= self.maxReplicas — minReplicas must not exceed maxReplicas")),
			Entry("the int-or-string format is the type flavor, not a constraint",
				deploymentMetadata, "spec.strategy.rollingUpdate.maxSurge",
				BeEmpty()),
			Entry("an unconstrained field declares nothing",
				deploymentMetadata, "spec.paused",
				BeEmpty()),
		)
	})

	When("a node's default is read", func() {
		DescribeTable(
			"the schema-declared default surfaces exactly as spelled",
			func(read func(string) schema.Metadata, path string, defaultValue types.GomegaMatcher) {
				Expect(read(path).Default).To(defaultValue)
			},
			Entry("a CRD-declared string default",
				gadgetMetadata, "spec.profile",
				Equal("balanced")),
			Entry("a built-in string default",
				deploymentMetadata, "spec.template.spec.containers.ports.protocol",
				Equal("TCP")),
			Entry("a built-in numeric default",
				deploymentMetadata, "spec.template.spec.containers.ports.containerPort",
				BeEquivalentTo(0)),
			Entry("a default spelled on the allOf wrapper, not the referenced component",
				deploymentMetadata, "spec",
				Equal(map[string]any{})),
			Entry("a field without a default carries nil",
				gadgetMetadata, "spec.minReplicas",
				BeNil()),
		)
	})

	When("a node's declared requiredness is read", func() {
		DescribeTable(
			"a node knows whether its parent object's required list names it",
			func(read func(string) schema.Metadata, path string, required bool) {
				Expect(read(path).Required).To(Equal(required))
			},
			Entry("the root of the field tree is never required",
				gadgetMetadata, "", false),
			Entry("a required CRD field", gadgetMetadata, "spec.minReplicas", true),
			Entry("its required sibling", gadgetMetadata, "spec.maxReplicas", true),
			Entry("an optional CRD field", gadgetMetadata, "spec.profile", false),
			Entry("a required built-in field", deploymentMetadata, "spec.selector", true),
			Entry("an optional built-in field", deploymentMetadata, "spec.replicas", false),
			Entry("requiredness resolves through an array's item schema",
				deploymentMetadata, "spec.template.spec.containers.name", true),
			Entry("its optional sibling through the same item schema",
				deploymentMetadata, "spec.template.spec.containers.image", false),
		)
	})

	When("a node's schema-blind flag is read", func() {
		DescribeTable(
			"subtrees the Type Schema says nothing about are flagged for the raw-YAML escape hatch",
			func(read func(string) schema.Metadata, path string, schemaBlind bool) {
				Expect(read(path).SchemaBlind).To(Equal(schemaBlind))
			},
			Entry("an x-kubernetes-preserve-unknown-fields subtree is schema-blind",
				gadgetMetadata, "spec.tuning", true),
			Entry("an untyped object with no declared structure is schema-blind",
				deploymentMetadata, "metadata.managedFields.fieldsV1", true),
			Entry("an object with declared properties is not",
				gadgetMetadata, "spec", false),
			Entry("a map-shaped object is guided by its value schema, so it is not",
				deploymentMetadata, "spec.template.metadata.labels", false),
			Entry("an int-or-string field is a typed flavor, not schema-blind",
				gadgetMetadata, "spec.maxUnavailable", false),
			Entry("a scalar leaf is not", deploymentMetadata, "metadata.name", false),
		)
	})

	When("the node's $ref cannot be resolved", func() {
		It("errors at inspection, naming the position, like expansion does", func() {
			raw := `{"openapi":"3.0.0","info":{"title":"Kubernetes","version":"1.36"},` +
				`"components":{"schemas":{` +
				`"com.example.craft.v1.Broken":{"type":"object",` +
				`"x-kubernetes-group-version-kind":[{"group":"craft.example.com","version":"v1","kind":"Broken"}],` +
				`"properties":{"spec":{"$ref":"#/components/schemas/com.example.craft.v1.Missing"}}}}}}`
			doc, err := schema.ParseDocument([]byte(raw))
			Expect(err).NotTo(HaveOccurred())
			root, err := doc.FieldTree(gvk("craft.example.com", "v1", "Broken"))
			Expect(err).NotTo(HaveOccurred())
			spec, err := root.Child("spec")
			Expect(err).NotTo(HaveOccurred())

			_, err = spec.Metadata()

			Expect(err).To(MatchError(ContainSubstring("names a component schema this OpenAPI v3 Document does not define")))
			Expect(err).To(MatchError(ContainSubstring(`Field Path "spec"`)))
		})
	})
})
