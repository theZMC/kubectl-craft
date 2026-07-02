package integration_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/internal/tui"
)

var _ = Describe("the deep-link entry against a live k3s cluster", func() {
	It("resolves deploy through live discovery and reaches the compose entry state", func(ctx SpecContext) {
		client := sessionClient()
		index := fetchIndex(ctx, client)
		kinds := discoverSessionKinds()

		kind, err := data.ResolveKindToken(kinds, "deploy")
		Expect(err).NotTo(HaveOccurred(),
			"the live cluster's discovery must resolve the deploy short name")
		Expect(kind.GVK).To(Equal(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}))
		Expect(kind.Preferred).To(BeTrue(), "the deep link lands on the Preferred Version")

		model := tui.New(ctx, kinds, client, index, client, "default",
			&tui.DeepLink{Kind: kind, FieldPath: "spec.strategy"})

		fetch := model.Init()
		Expect(fetch).NotTo(BeNil(),
			"a deep-linked Session starts by fetching the linked Kind's group document")
		updated, _ := model.Update(fetch()) // the live fetch and parse
		model = updated.(tui.Model)

		Expect(model.ComposeOpen()).To(BeTrue(),
			"the deep link must reach the compose view without a picker frame")
		Expect(model.Breadcrumb()).To(Equal("apps/v1 Deployment › spec.strategy"))
		Expect(model.FocusedFieldPath()).To(Equal("spec.strategy"),
			"the launch arg's Field Path lands via the search landing rule")
		Expect(model.VisibleFieldPaths()).To(ContainElements("spec.replicas", "spec.strategy"),
			"the landing expands the path's ancestors in the live tree")
	}, NodeTimeout(defaultSpecTimeout))
})
