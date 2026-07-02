package tui_test

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/internal/tui"
)

// widgetV1 and widgetV2 are the two-version Widget pair as discovery would
// list it: the same (group, kind) served at v1 and v2, the Preferred Version
// v2 — the corpus Kind captured for the version carry-over.
func widgetV1() data.Kind {
	kind := composeKind("craft.example.com", "v1", "Widget", "apis/craft.example.com/v1")
	kind.Preferred = false
	kind.Plural = "widgets"
	return kind
}

func widgetV2() data.Kind {
	kind := composeKind("craft.example.com", "v2", "Widget", "apis/craft.example.com/v2")
	kind.Plural = "widgets"
	return kind
}

// newVersionedShell builds the Session shell over a Kind list carrying both
// Widget versions, around the given Fetcher.
func newVersionedShell(fetcher data.Fetcher) tui.Model {
	kinds := append(browsableKinds(), widgetV1(), widgetV2())
	return tui.New(context.Background(), kinds, fetcher, corpusIndex(), nil)
}

// composeWidgetV1 opens the compose view on craft.example.com/v1 Widget over
// a shell that knows both served versions.
func composeWidgetV1(fetcher data.Fetcher) tui.Model {
	GinkgoHelper()
	return widen(openKind(newVersionedShell(fetcher), widgetV1()))
}

// requestSwitch opens `V`'s version list and selects the highlighted served
// version, returning the model and the switch-request command.
func requestSwitch(model tui.Model) (tui.Model, tea.Cmd) {
	GinkgoHelper()
	model, _ = press(model, keyRune('V'))
	Expect(model.VersionListOpen()).To(BeTrue(), "V must open the served-version list")
	model, cmd := press(model, enterKey)
	Expect(cmd).NotTo(BeNil(), "Enter must ask the shell to switch")
	return model, cmd
}

// switchWidgetToV2 drives a full switch request through the shell: the
// version list, the switch request, and the target group document's lazy
// fetch, returning the model once the carry-over resolved.
func switchWidgetToV2(model tui.Model) tui.Model {
	GinkgoHelper()
	model, cmd := requestSwitch(model)
	model, fetch := press(model, cmd())
	Expect(fetch).NotTo(BeNil(), "an unparsed target group document must fetch lazily, as a command")
	model, _ = press(model, fetch())
	return model
}

var _ = Describe("the version switch", func() {
	When("V opens the served-version list in navigate mode", func() {
		It("lists the open Kind's other served versions from the discovery data on the shell", func() {
			model := composeWidgetV1(corpusFetcher())

			model, cmd := press(model, keyRune('V'))

			Expect(cmd).To(BeNil())
			Expect(model.VersionListOpen()).To(BeTrue())
			Expect(model.VersionOptions()).To(Equal([]data.Kind{widgetV2()}),
				"the list holds every served version but the open one")
			highlighted, ok := model.HighlightedVersion()
			Expect(ok).To(BeTrue())
			Expect(highlighted).To(Equal(widgetV2()))
			Expect(model.View()).To(ContainSubstring("(Preferred Version)"),
				"the Preferred Version is marked as row metadata")
		})

		It("filters under the type-to-filter grammar, and Esc clears then dismisses", func() {
			model := composeWidgetV1(corpusFetcher())
			model, _ = press(model, keyRune('V'))

			model = typeFilter(model, "v9")
			Expect(model.VersionFilter()).To(Equal("v9"))
			Expect(model.VersionOptions()).To(BeEmpty())
			_, ok := model.HighlightedVersion()
			Expect(ok).To(BeFalse())

			model, cmd := press(model, enterKey)
			Expect(cmd).To(BeNil(), "Enter with nothing highlighted selects nothing")
			Expect(model.VersionListOpen()).To(BeTrue())

			model, _ = press(model, escKey)
			Expect(model.VersionFilter()).To(BeEmpty(), "the first Esc clears the filter")
			Expect(model.VersionListOpen()).To(BeTrue())

			model, _ = press(model, escKey)
			Expect(model.VersionListOpen()).To(BeFalse(), "Esc on an empty filter dismisses back to navigate mode")
			Expect(model.ComposeOpen()).To(BeTrue())
		})

		It("notices instead of opening the list on a Kind served at one version", func() {
			model := widen(openKind(newShell(), kindNamed("Gadget", "v1")))

			model, _ = press(model, keyRune('V'))

			Expect(model.VersionListOpen()).To(BeFalse())
			notice, showing := model.Notice()
			Expect(showing).To(BeTrue())
			Expect(notice).To(ContainSubstring("only served version"))
		})

		It("stays a search-surface key everywhere else: V types into the open search overlay", func() {
			model := composeWidgetV1(corpusFetcher())
			model, _ = press(model, keyRune('/'))

			model, _ = press(model, keyRune('V'))

			Expect(model.VersionListOpen()).To(BeFalse(), "a search surface has no command letters")
			Expect(model.SearchFilter()).To(Equal("V"))
		})
	})

	When("the carry-over is clean — nothing would drop", func() {
		It("switches immediately: tree rebuilt, carried value readable at the same path, breadcrumb re-rooted", func() {
			fetcher := corpusFetcher()
			model := composeWidgetV1(fetcher)
			model = expandField(model, "spec")
			model = confirmLeaf(model, "spec.size", "5")

			model = switchWidgetToV2(model)

			Expect(model.ComposeOpen()).To(BeTrue())
			Expect(model.ConfirmingVersionSwitch()).To(BeFalse(), "a clean carry-over needs no confirmation")
			Expect(model.Breadcrumb()).To(HavePrefix("craft.example.com/v2 Widget"),
				"the breadcrumb root updates to the target version")
			Expect(draftValue(model, "spec.size")).To(Equal(int64(5)),
				"the carried value stays readable at the same Draft-level Field Path")
			Expect(model.MissingRequiredFieldPaths()).To(BeEmpty(),
				"completeness recomputes over the carried Draft — v2's required chain is satisfied")
			Expect(model.FocusedFieldPath()).To(Equal("spec.size"),
				"a focused position the target version still defines keeps the focus")
			Expect(fetcher.fetches).To(Equal([]fetchRecord{
				{groupPath: "apis/craft.example.com/v1", contentHash: "CRAFT1HASH"},
				{groupPath: "apis/craft.example.com/v2", contentHash: "CRAFT2HASH"},
			}), "the switch fetches the target version's group document through the existing lazy path")
		})

		It("shows the loading state for the target version while its group document travels", func() {
			model := composeWidgetV1(corpusFetcher())
			model, cmd := requestSwitch(model)

			model, fetch := press(model, cmd())

			Expect(fetch).NotTo(BeNil())
			Expect(model.SwitchingVersion()).To(BeTrue())
			Expect(model.FetchingDocument()).To(BeTrue())
			Expect(model.Breadcrumb()).To(Equal("craft.example.com/v2 Widget"))
			Expect(model.View()).To(ContainSubstring("fetching the apis/craft.example.com/v2"))
			Expect(model.View()).To(ContainSubstring("esc/q cancel the version switch"),
				"the switch's loading state documents cancelling, not quitting")
		})

		It("resolves an already-parsed target group document without another fetch", func() {
			fetcher := corpusFetcher()
			model := switchWidgetToV2(composeWidgetV1(fetcher))
			Expect(fetcher.fetches).To(HaveLen(2))

			model, cmd := requestSwitch(model)
			model, next := press(model, cmd())

			Expect(next).To(BeNil(), "v1's group document parsed once already — the switch resolves from the memo")
			Expect(model.ComposeOpen()).To(BeTrue())
			Expect(model.Breadcrumb()).To(HavePrefix("craft.example.com/v1 Widget"))
			Expect(fetcher.fetches).To(HaveLen(2), "a group document fetches once per Session")
		})
	})

	When("values would drop, the confirmation renders the drop report first", func() {
		// composedForDrop fills the shared spelling and the v1-only one, so
		// switching to v2 must carry spec.size and report spec.paint.
		composedForDrop := func(fetcher data.Fetcher) tui.Model {
			GinkgoHelper()
			model := composeWidgetV1(fetcher)
			model = expandField(model, "spec")
			model = confirmLeaf(model, "spec.paint", "red")
			return confirmLeaf(model, "spec.size", "5")
		}

		It("renders the ordered drop report — paths and reasons — before switching", func() {
			model := switchWidgetToV2(composedForDrop(corpusFetcher()))

			Expect(model.ConfirmingVersionSwitch()).To(BeTrue())
			report := model.DropReport()
			Expect(report).To(HaveLen(1))
			Expect(report[0].Path).To(Equal("spec.paint"))
			Expect(report[0].Reason).To(ContainSubstring(`no field "paint"`))
			Expect(model.Breadcrumb()).To(HavePrefix("craft.example.com/v1 Widget"),
				"nothing switches until the report is accepted")
			Expect(model.View()).To(ContainSubstring("would drop these Draft-level Field Paths"))
			Expect(model.View()).To(ContainSubstring("spec.paint — "))
			Expect(model.View()).To(ContainSubstring("drop 1 Field Path and switch to craft.example.com/v2 Widget?"))
		})

		It("Enter accepts the drops and commits the switch", func() {
			model := switchWidgetToV2(composedForDrop(corpusFetcher()))

			model, cmd := press(model, enterKey)
			Expect(cmd).NotTo(BeNil(), "accepting travels to the shell as a typed message")
			model, _ = press(model, cmd())

			Expect(model.ConfirmingVersionSwitch()).To(BeFalse())
			Expect(model.Breadcrumb()).To(HavePrefix("craft.example.com/v2 Widget"))
			Expect(draftValue(model, "spec.size")).To(Equal(int64(5)))
			_, filled := model.DraftValueAt("spec.paint")
			Expect(filled).To(BeFalse(), "the dropped value is gone from the carried Draft")
		})

		It("Esc cancels: composing keeps the current version untouched", func() {
			model := switchWidgetToV2(composedForDrop(corpusFetcher()))

			model, _ = press(model, escKey)

			Expect(model.ConfirmingVersionSwitch()).To(BeFalse())
			Expect(model.ComposeOpen()).To(BeTrue())
			Expect(model.Breadcrumb()).To(HavePrefix("craft.example.com/v1 Widget"))
			Expect(draftValue(model, "spec.paint")).To(Equal("red"))
			Expect(draftValue(model, "spec.size")).To(Equal(int64(5)))
		})

		It("keeps other keys inert while the confirm is open", func() {
			model := switchWidgetToV2(composedForDrop(corpusFetcher()))
			focused := model.FocusedFieldPath()

			model, cmd := press(model, keyRune('j'), keyRune('q'), keyRune('V'))

			Expect(cmd).To(BeNil())
			Expect(model.ConfirmingVersionSwitch()).To(BeTrue())
			Expect(model.FocusedFieldPath()).To(Equal(focused))
		})
	})

	When("the focused position no longer exists in the target version", func() {
		It("degrades gracefully: the focus falls back to the root of the field tree", func() {
			model := composeWidgetV1(corpusFetcher())
			model = expandField(model, "spec")
			model = confirmLeaf(model, "spec.paint", "red")
			Expect(model.FocusedFieldPath()).To(Equal("spec.paint"))

			model = switchWidgetToV2(model)
			Expect(model.ConfirmingVersionSwitch()).To(BeTrue())
			model, cmd := press(model, enterKey)
			model, _ = press(model, cmd())

			Expect(model.ComposeOpen()).To(BeTrue())
			Expect(model.FocusedFieldPath()).To(BeEmpty(), "spec.paint has no v2 spelling — the focus lands at the root")
			Expect(model.Breadcrumb()).To(Equal("craft.example.com/v2 Widget"))
			Expect(model.MissingRequiredFieldPaths()).To(ConsistOf("spec", "spec.size"),
				"the only value dropped, so completeness recomputes over an empty Draft")
		})
	})

	When("the switch's group document never lands", func() {
		It("returns to composing with a non-fatal notice when the fetch fails — the Draft survives", func() {
			fetcher := corpusFetcher()
			delete(fetcher.documents, "apis/craft.example.com/v2")
			model := composeWidgetV1(fetcher)
			model = expandField(model, "spec")
			model = confirmLeaf(model, "spec.size", "5")

			model = switchWidgetToV2(model)

			Expect(model.ComposeOpen()).To(BeTrue())
			Expect(model.SwitchingVersion()).To(BeFalse())
			notice, showing := model.Notice()
			Expect(showing).To(BeTrue())
			Expect(notice).To(ContainSubstring("switching to craft.example.com/v2 Widget failed"))
			Expect(model.Breadcrumb()).To(HavePrefix("craft.example.com/v1 Widget"))
			Expect(draftValue(model, "spec.size")).To(Equal(int64(5)))
		})

		It("Esc cancels the in-flight switch, and the stale fetch only memoizes", func() {
			model := composeWidgetV1(corpusFetcher())
			model = expandField(model, "spec")
			model = confirmLeaf(model, "spec.size", "5")
			model, cmd := requestSwitch(model)
			model, fetch := press(model, cmd())
			Expect(model.SwitchingVersion()).To(BeTrue())

			model, _ = press(model, escKey)

			Expect(model.SwitchingVersion()).To(BeFalse())
			Expect(model.ComposeOpen()).To(BeTrue(), "cancelling returns to composing, Draft intact")
			Expect(model.Breadcrumb()).To(HavePrefix("craft.example.com/v1 Widget"))
			Expect(draftValue(model, "spec.size")).To(Equal(int64(5)))

			model, _ = press(model, fetch())
			Expect(model.ComposeOpen()).To(BeTrue(), "the stale fetch lands in the memo and nothing more")
			Expect(model.Breadcrumb()).To(HavePrefix("craft.example.com/v1 Widget"))
		})
	})

	When("the hint bar and help document the verb", func() {
		It("spells V in navigate mode's hint bar and the full-map help", func() {
			model := composeWidgetV1(corpusFetcher())
			Expect(model.View()).To(ContainSubstring("V version"))

			model, _ = press(model, keyRune('?'))
			Expect(model.HelpOpen()).To(BeTrue())
			Expect(model.View()).To(ContainSubstring("switch the open Kind's version"))
		})
	})
})
