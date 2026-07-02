package matchers_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/matchers"
	"github.com/thezmc/kubectl-craft/internal/schema"
)

// widgetDraft composes a Draft against a minimal synthetic Widget Type Schema.
func widgetDraft() *schema.Draft {
	GinkgoHelper()
	raw := `{"openapi":"3.0.0","info":{"title":"Kubernetes","version":"1.36"},` +
		`"components":{"schemas":{"com.example.craft.v1.Widget":{"type":"object",` +
		`"x-kubernetes-group-version-kind":[{"group":"craft.example.com","version":"v1","kind":"Widget"}],` +
		`"properties":{"spec":{"type":"object","properties":{"size":{"type":"integer"}}}}}}}}`
	doc, err := schema.ParseDocument([]byte(raw))
	Expect(err).NotTo(HaveOccurred())
	kind := schema.GroupVersionKind{Group: "craft.example.com", Version: "v1", Kind: "Widget"}
	root, err := doc.FieldTree(kind)
	Expect(err).NotTo(HaveOccurred())
	return schema.NewDraft(root, kind)
}

var _ = Describe("the HaveValueAt matcher", func() {
	When("the Draft holds a filled value at the Field Path", func() {
		It("succeeds on presence alone and on exact data", func() {
			draft := widgetDraft()
			Expect(draft.Set("spec.size", 3)).To(Succeed())

			Expect(draft).To(matchers.HaveValueAt("spec.size"))
			Expect(draft).To(matchers.HaveValueAt("spec.size", int64(3)))
		})

		It("fails when the data differs", func() {
			draft := widgetDraft()
			Expect(draft.Set("spec.size", 3)).To(Succeed())

			Expect(draft).NotTo(matchers.HaveValueAt("spec.size", int64(4)))
		})
	})

	When("nothing is filled at the Field Path", func() {
		It("fails", func() {
			Expect(widgetDraft()).NotTo(matchers.HaveValueAt("spec.size"))
		})
	})

	When("the actual value is not a Draft", func() {
		It("errors instead of guessing", func() {
			success, err := matchers.HaveValueAt("spec.size").Match("not a Draft")

			Expect(success).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring("HaveValueAt matches a *schema.Draft")))
		})
	})

	When("more than one expected data value is given", func() {
		It("errors instead of guessing", func() {
			_, err := matchers.HaveValueAt("spec.size", 1, 2).Match(widgetDraft())

			Expect(err).To(MatchError(ContainSubstring("at most one expected data value")))
		})
	})
})
