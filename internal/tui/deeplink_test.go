package tui_test

import (
	"context"
	"errors"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/internal/tui"
)

// deepLinkedShell builds the Session shell launched with a resolved deep
// link over the fixture corpus, the way runSession hands one over after
// the positional arg resolves.
func deepLinkedShell(kind data.Kind, fieldPath string) tui.Model {
	return tui.New(
		context.Background(),
		browsableKinds(),
		corpusFetcher(),
		corpusIndex(),
		&stubValidator{outcome: data.Clean{}},
		"",
		&tui.DeepLink{Kind: kind, FieldPath: fieldPath},
	)
}

// launchDeepLinked drives a deep-linked shell through its Init command —
// the linked Kind's lazy group-document fetch — the way the Bubble Tea
// runtime would.
func launchDeepLinked(kind data.Kind, fieldPath string) tui.Model {
	GinkgoHelper()
	model := deepLinkedShell(kind, fieldPath)
	fetch := model.Init()
	Expect(fetch).NotTo(BeNil(), "a deep-linked Session must start by fetching the linked Kind's group document")
	model, _ = press(model, fetch())
	return model
}

var _ = Describe("the deep-linked Session entry", func() {
	When("the launch arg deep-links a Kind without a Field Path", func() {
		It("skips the picker: the shell starts in the loading state for the linked Kind", func() {
			model := deepLinkedShell(kindNamed("Deployment", "v1"), "")

			Expect(model.FetchingDocument()).To(BeTrue(),
				"the deep link opens the linked Kind directly — no picker frame first")
			selected, ok := model.SelectedKind()
			Expect(ok).To(BeTrue())
			Expect(selected).To(Equal(kindNamed("Deployment", "v1")))
			_, highlighted := model.HighlightedKind()
			Expect(highlighted).To(BeFalse(), "the picker is not the open view")
		})

		It("opens the compose view at the root once the group document lands", func() {
			model := launchDeepLinked(kindNamed("Deployment", "v1"), "")

			Expect(model.ComposeOpen()).To(BeTrue())
			Expect(model.Breadcrumb()).To(Equal("apps/v1 Deployment"))
			Expect(model.FocusedFieldPath()).To(BeEmpty(), "a kind-only deep link lands at the root")
			_, noticed := model.Notice()
			Expect(noticed).To(BeFalse())
		})

		It("returns to the Kind picker on Esc like any other compose view", func() {
			model := launchDeepLinked(kindNamed("Deployment", "v1"), "")

			model, back := press(model, tea.KeyMsg{Type: tea.KeyEsc})
			Expect(back).NotTo(BeNil())
			model, _ = press(model, back())

			Expect(model.ComposeOpen()).To(BeFalse())
			highlighted, ok := model.HighlightedKind()
			Expect(ok).To(BeTrue(), "the picker is browsable after leaving a deep-linked compose view")
			Expect(highlighted).To(Equal(kindNamed("ConfigMap", "v1")),
				"the picker opens on the full Kind list, nothing pre-filtered")
		})
	})

	When("the deep link carries a Field Path", func() {
		It("defers the landing until the document lands, then expands the ancestors and focuses the path", func() {
			model := launchDeepLinked(kindNamed("Deployment", "v1"), "spec.strategy")

			Expect(model.ComposeOpen()).To(BeTrue())
			Expect(model.FocusedFieldPath()).To(Equal("spec.strategy"),
				"the focus lands on the linked Field Path via the search landing rule")
			Expect(model.Breadcrumb()).To(Equal("apps/v1 Deployment › spec.strategy"))
			Expect(model.VisibleFieldPaths()).To(ContainElements("spec.replicas", "spec.strategy"),
				"the landing expands the path's ancestors, so its siblings are visible too")
			_, noticed := model.Notice()
			Expect(noticed).To(BeFalse())
		})

		It("lands an array crossing on the collection node — a fresh Draft instantiates nothing", func() {
			model := launchDeepLinked(kindNamed("Deployment", "v1"), "spec.template.spec.containers.name")

			Expect(model.ComposeOpen()).To(BeTrue())
			Expect(model.FocusedFieldPath()).To(Equal("spec.template.spec.containers"),
				"with no instantiated items, the landing rule stops on the collection node")
		})
	})

	When("the deep-linked Field Path doesn't exist in the Type Schema", func() {
		It("opens the Kind at the root with a non-fatal notice naming the path", func() {
			model := launchDeepLinked(kindNamed("Deployment", "v1"), "spec.bogus")

			Expect(model.ComposeOpen()).To(BeTrue(), "a bad path never blocks the Kind from opening")
			Expect(model.FocusedFieldPath()).To(BeEmpty(), "the view opens at the root instead")

			notice, noticed := model.Notice()
			Expect(noticed).To(BeTrue())
			Expect(notice).To(ContainSubstring("spec.bogus"), "the notice names the path that doesn't exist")
			Expect(model.View()).To(ContainSubstring("spec.bogus"),
				"the notice renders in the TUI, with the Session still running")
		})

		It("clears the notice on the first key press, restoring the hint bar", func() {
			model := launchDeepLinked(kindNamed("Deployment", "v1"), "spec.bogus")

			model, _ = press(model, keyRune('j'))

			_, noticed := model.Notice()
			Expect(noticed).To(BeFalse(), "any key acknowledges the notice — browsing has started")
			Expect(model.View()).To(ContainSubstring("? help"), "the contextual hint bar returns")
			Expect(model.FocusedFieldPath()).To(Equal("apiVersion"),
				"the acknowledging key still navigates — the notice consumes nothing")
		})
	})

	When("the deep-linked Kind's group document fetch fails", func() {
		It("lands in the in-TUI error state with Esc back to the picker", func() {
			fetcher := &stubFetcher{failWith: errors.New("the cluster hung up mid-fetch")}
			model := tui.New(
				context.Background(),
				browsableKinds(),
				fetcher,
				corpusIndex(),
				&stubValidator{outcome: data.Clean{}},
				"",
				&tui.DeepLink{Kind: kindNamed("Deployment", "v1")},
			)
			fetch := model.Init()
			Expect(fetch).NotTo(BeNil())
			model, _ = press(model, fetch())

			message, failed := model.ComposeError()
			Expect(failed).To(BeTrue())
			Expect(message).To(ContainSubstring("the cluster hung up mid-fetch"))

			model, _ = press(model, tea.KeyMsg{Type: tea.KeyEsc})
			_, highlighted := model.HighlightedKind()
			Expect(highlighted).To(BeTrue(), "the picker is the way out of a failed deep link")
		})
	})
})
