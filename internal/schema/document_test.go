package schema_test

import (
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/schema"
)

// parseFixture parses one checked-in group document from the fixture corpus.
func parseFixture(name string) *schema.Document {
	GinkgoHelper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	Expect(err).NotTo(HaveOccurred())
	doc, err := schema.ParseDocument(raw)
	Expect(err).NotTo(HaveOccurred())
	return doc
}

func gvk(group, version, kind string) schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: group, Version: version, Kind: kind}
}

var _ = Describe("the OpenAPI v3 Document", func() {
	When("parsed from the raw group-document bytes the data layer fetches", func() {
		DescribeTable(
			"it enumerates the Kinds the group document defines",
			func(fixture string, definedKinds []schema.GroupVersionKind, helperSchemaNames []string) {
				doc := parseFixture(fixture)

				kinds := doc.Kinds()

				for _, defined := range definedKinds {
					Expect(kinds).To(ContainElement(defined))
				}
				kindNames := make([]string, 0, len(kinds))
				for _, k := range kinds {
					kindNames = append(kindNames, k.Kind)
				}
				for _, helper := range helperSchemaNames {
					Expect(kindNames).NotTo(ContainElement(helper),
						"untagged helper schemas are not Kinds")
				}
				Expect(kindNames).NotTo(ContainElement(HaveSuffix("List")),
					"List wrappers are not composable Kinds")
			},
			Entry(
				"apps/v1 defines the workload Kinds",
				"apis_apps_v1.json",
				[]schema.GroupVersionKind{
					gvk("apps", "v1", "ControllerRevision"),
					gvk("apps", "v1", "DaemonSet"),
					gvk("apps", "v1", "Deployment"),
					gvk("apps", "v1", "ReplicaSet"),
					gvk("apps", "v1", "StatefulSet"),
				},
				[]string{"DeploymentSpec", "PodTemplateSpec", "ObjectMeta"},
			),
			Entry(
				"apiextensions.k8s.io/v1 defines CustomResourceDefinition",
				"apis_apiextensions.k8s.io_v1.json",
				[]schema.GroupVersionKind{
					gvk("apiextensions.k8s.io", "v1", "CustomResourceDefinition"),
				},
				[]string{"JSONSchemaProps", "CustomResourceDefinitionSpec", "ObjectMeta"},
			),
			Entry(
				"craft.example.com/v1 defines both sample Kinds",
				"apis_craft.example.com_v1.json",
				[]schema.GroupVersionKind{
					gvk("craft.example.com", "v1", "Gadget"),
					gvk("craft.example.com", "v1", "Widget"),
				},
				[]string{"ObjectMeta", "ManagedFieldsEntry"},
			),
			Entry(
				"craft.example.com/v2 serves only the multi-version Widget",
				"apis_craft.example.com_v2.json",
				[]schema.GroupVersionKind{
					gvk("craft.example.com", "v2", "Widget"),
				},
				[]string{"Gadget", "ObjectMeta"},
			),
		)

		DescribeTable(
			"it resolves a Kind to its root Type Schema",
			func(fixture string, kind schema.GroupVersionKind, pinnedDescription string) {
				doc := parseFixture(fixture)

				root, err := doc.RootSchema(kind)

				Expect(err).NotTo(HaveOccurred())
				Expect(root).NotTo(BeNil())
				Expect(root.Description).To(ContainSubstring(pinnedDescription),
					"the root must be the component schema tagged with the Kind's GVK")
				Expect(root.Properties).To(HaveKey("spec"))
			},
			Entry("apps/v1 Deployment",
				"apis_apps_v1.json",
				gvk("apps", "v1", "Deployment"),
				"Deployment enables declarative updates"),
			Entry("apiextensions.k8s.io/v1 CustomResourceDefinition",
				"apis_apiextensions.k8s.io_v1.json",
				gvk("apiextensions.k8s.io", "v1", "CustomResourceDefinition"),
				"CustomResourceDefinition represents a resource"),
			Entry("craft.example.com/v1 Widget — the older served version",
				"apis_craft.example.com_v1.json",
				gvk("craft.example.com", "v1", "Widget"),
				"Widget (v1)"),
			Entry("craft.example.com/v2 Widget — the Preferred Version",
				"apis_craft.example.com_v2.json",
				gvk("craft.example.com", "v2", "Widget"),
				"Widget (v2)"),
			Entry("craft.example.com/v1 Gadget — the CEL and int-or-string sample",
				"apis_craft.example.com_v1.json",
				gvk("craft.example.com", "v1", "Gadget"),
				"Gadget exercises int-or-string"),
		)
	})

	When("a Kind is not defined by the group document", func() {
		It("yields a clear error naming the missing Type Schema", func() {
			doc := parseFixture("apis_craft.example.com_v1.json")

			_, err := doc.RootSchema(gvk("craft.example.com", "v1", "Doohickey"))

			Expect(err).To(MatchError(ContainSubstring("Type Schema")))
			Expect(err).To(MatchError(ContainSubstring("Kind")))
			Expect(err).To(MatchError(ContainSubstring("craft.example.com/v1 Doohickey")))
		})

		It("does not resolve a List wrapper as a composable Kind", func() {
			doc := parseFixture("apis_apps_v1.json")

			_, err := doc.RootSchema(gvk("apps", "v1", "DeploymentList"))

			Expect(err).To(MatchError(ContainSubstring("no Type Schema for Kind apps/v1 DeploymentList")))
		})
	})

	When("the group-document bytes are malformed", func() {
		DescribeTable(
			"parsing yields a wrapped error and never panics",
			func(raw []byte) {
				doc, err := schema.ParseDocument(raw)

				Expect(err).To(HaveOccurred())
				Expect(errors.Unwrap(err)).To(HaveOccurred(),
					"the decode failure must stay wrapped for callers to inspect")
				Expect(doc).To(BeNil())
			},
			Entry("bytes that are not JSON",
				[]byte("not an OpenAPI v3 Document")),
			Entry("a JSON document of the wrong shape",
				[]byte(`["not", "a", "group", "document"]`)),
			Entry("a truncated group document",
				[]byte(`{"openapi":"3.0.0","components":{"schemas":{"com.example`)),
			Entry("a component schema with an undecodable x-kubernetes-group-version-kind tag",
				[]byte(`{"openapi":"3.0.0","info":{"title":"Kubernetes","version":"1.36"},`+
					`"components":{"schemas":{"com.example.craft.v1.Bad":`+
					`{"type":"object","x-kubernetes-group-version-kind":"not-a-gvk-list"}}}}`)),
		)
	})
})
