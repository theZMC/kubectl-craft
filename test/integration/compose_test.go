package integration_test

import (
	tea "charm.land/bubbletea/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/internal/tui"
)

// discoverSessionKinds lists the live cluster's browsable Kinds the way
// runSession does before the shell starts.
func discoverSessionKinds() []data.Kind {
	GinkgoHelper()

	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	Expect(err).NotTo(HaveOccurred())

	lister, err := data.NewKindLister(cfg)
	Expect(err).NotTo(HaveOccurred())

	var kinds []data.Kind
	Eventually(func(g Gomega) {
		kinds, err = data.DiscoverKinds(lister)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(kinds).NotTo(BeEmpty())
	}).Should(Succeed())

	return kinds
}

var _ = Describe("the Session shell against a live k3s cluster", func() {
	It("opens the compose view over the live apps/v1 Deployment Type Schema", func(ctx SpecContext) {
		client := sessionClient()
		index := fetchIndex(ctx, client)
		kinds := discoverSessionKinds()

		var deployment data.Kind
		found := false
		for _, kind := range kinds {
			if kind.GVK.Group == "apps" && kind.GVK.Version == "v1" && kind.GVK.Kind == "Deployment" {
				deployment, found = kind, true
				break
			}
		}
		Expect(found).To(BeTrue(), "discovery must offer apps/v1 Deployment")

		model := tui.New(ctx, kinds, client, index, client, "default", nil)

		updated, cmd := model.Update(tui.KindSelectedMsg{Kind: deployment})
		model = updated.(tui.Model)
		Expect(model.FetchingDocument()).To(BeTrue(),
			"the group document fetch runs lazily, as a command, on the Kind's first open")
		Expect(cmd).NotTo(BeNil())

		updated, _ = model.Update(cmd()) // the live fetch and parse
		model = updated.(tui.Model)

		Expect(model.ComposeOpen()).To(BeTrue(),
			"the live group document must open the read-only compose view")
		Expect(model.Breadcrumb()).To(Equal("apps/v1 Deployment"))
		Expect(model.VisibleFieldPaths()).To(ContainElements("metadata", "spec", "status"))

		// Walk to spec (bounded, so a regression fails instead of spinning)
		// and expand it: the live tree browses lazily.
		for range 512 {
			if model.FocusedFieldPath() == "spec" {
				break
			}
			updated, _ = model.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
			model = updated.(tui.Model)
		}
		Expect(model.FocusedFieldPath()).To(Equal("spec"))
		updated, _ = model.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
		model = updated.(tui.Model)

		Expect(model.VisibleFieldPaths()).To(ContainElements("spec.replicas", "spec.selector", "spec.template"))
		Expect(model.Breadcrumb()).To(Equal("apps/v1 Deployment › spec"))
	}, NodeTimeout(defaultSpecTimeout))
})
