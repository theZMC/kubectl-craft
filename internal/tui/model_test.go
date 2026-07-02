package tui_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/internal/tui"
)

// browsableKinds is the spec fixture Kind list, shaped exactly as
// discovery hands it over: deterministic (group, version, kind) order,
// with a multi-version Kind (HorizontalPodAutoscaler, Preferred Version
// autoscaling/v2) and a CRD short name (gz) that is not a subsequence of
// its kind name, so short-name matching is provable on its own.
func browsableKinds() []data.Kind {
	return []data.Kind{
		{
			GVK:              schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"},
			GroupVersionPath: "api/v1",
			Plural:           "configmaps",
			ShortNames:       []string{"cm"},
			Preferred:        true,
		},
		{
			GVK:              schema.GroupVersionKind{Version: "v1", Kind: "Pod"},
			GroupVersionPath: "api/v1",
			Plural:           "pods",
			ShortNames:       []string{"po"},
			Preferred:        true,
		},
		{
			GVK:              schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
			GroupVersionPath: "apis/apps/v1",
			Plural:           "deployments",
			ShortNames:       []string{"deploy"},
			Preferred:        true,
		},
		{
			GVK:              schema.GroupVersionKind{Group: "autoscaling", Version: "v1", Kind: "HorizontalPodAutoscaler"},
			GroupVersionPath: "apis/autoscaling/v1",
			Plural:           "horizontalpodautoscalers",
			ShortNames:       []string{"hpa"},
		},
		{
			GVK:              schema.GroupVersionKind{Group: "autoscaling", Version: "v2", Kind: "HorizontalPodAutoscaler"},
			GroupVersionPath: "apis/autoscaling/v2",
			Plural:           "horizontalpodautoscalers",
			ShortNames:       []string{"hpa"},
			Preferred:        true,
		},
		{
			GVK:              schema.GroupVersionKind{Group: "craft.example.com", Version: "v1", Kind: "Gadget"},
			GroupVersionPath: "apis/craft.example.com/v1",
			Plural:           "gadgets",
			ShortNames:       []string{"gz"},
			Preferred:        true,
		},
	}
}

// kindNamed returns the fixture Kind with the given kind name and version.
func kindNamed(kind, version string) data.Kind {
	GinkgoHelper()
	for _, candidate := range browsableKinds() {
		if candidate.GVK.Kind == kind && candidate.GVK.Version == version {
			return candidate
		}
	}
	Fail("no fixture Kind named " + kind + " at " + version)
	return data.Kind{}
}

// press drives the Session shell's Update with synthetic messages — the
// state-first pattern (DESIGN.md — Testing): assert on the returned model
// and command, never on rendered frames. It returns the model after every
// message and the command from the last one.
func press(model tui.Model, msgs ...tea.Msg) (tui.Model, tea.Cmd) {
	GinkgoHelper()
	var cmd tea.Cmd
	for _, msg := range msgs {
		var updated tea.Model
		updated, cmd = model.Update(msg)
		model = updated.(tui.Model)
	}
	return model, cmd
}

// typeFilter presses one printable key per rune, the way a user types a
// type-to-filter query.
func typeFilter(model tui.Model, query string) tui.Model {
	GinkgoHelper()
	for _, r := range query {
		model, _ = press(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return model
}

// kindNames projects matched Kinds onto their kind names for readable
// list assertions.
func kindNames(kinds []data.Kind) []string {
	names := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names = append(names, kind.GVK.Kind)
	}
	return names
}

var _ = Describe("the Session shell", func() {
	When("the Session opens on the Kind picker", func() {
		It("starts with no initial command — Kinds are discovered before launch", func() {
			Expect(newShell().Init()).To(BeNil())
		})

		It("lists every browsable Kind with each Kind's versions together, Preferred Version first", func() {
			model := newShell()

			Expect(kindNames(model.MatchedKinds())).To(Equal([]string{
				"ConfigMap", "Pod",
				"Deployment",
				"HorizontalPodAutoscaler", "HorizontalPodAutoscaler",
				"Gadget",
			}))

			hpaRows := model.MatchedKinds()[3:5]
			Expect(hpaRows[0].Preferred).To(BeTrue(),
				"the Preferred Version row must lead its Kind's versions — it is the default representative")
			Expect(hpaRows[0].GVK.Version).To(Equal("v2"))
			Expect(hpaRows[1].GVK.Version).To(Equal("v1"),
				"non-preferred versions must remain listed and reachable")
		})

		It("highlights the first row and renders group/version as row metadata", func() {
			model := newShell()

			highlighted, ok := model.HighlightedKind()
			Expect(ok).To(BeTrue())
			Expect(highlighted).To(Equal(kindNamed("ConfigMap", "v1")))

			Expect(model.View()).To(ContainSubstring("Deployment"))
			Expect(model.View()).To(ContainSubstring("apps/v1"))
			Expect(model.View()).To(ContainSubstring("v1 (cm)"),
				"the core group's metadata is the bare version with the short names")
		})
	})

	When("printable keys type into the filter", func() {
		It("narrows immediately, fuzzy-matching on the kind name", func() {
			model := typeFilter(newShell(), "dply")

			Expect(model.Filter()).To(Equal("dply"))
			Expect(kindNames(model.MatchedKinds())).To(Equal([]string{"Deployment"}))
		})

		It("matches short names, so the filter reaches a Kind the way the deep-link arg would", func() {
			model := typeFilter(newShell(), "gz")

			Expect(kindNames(model.MatchedKinds())).To(Equal([]string{"Gadget"}),
				"gz is not a subsequence of Gadget — only the short name can match")
		})

		It("re-anchors the selection on the narrowed list's first row", func() {
			model, _ := press(newShell(), tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyDown})
			model = typeFilter(model, "hpa")

			highlighted, ok := model.HighlightedKind()
			Expect(ok).To(BeTrue())
			Expect(highlighted).To(Equal(kindNamed("HorizontalPodAutoscaler", "v2")),
				"filtering lands on the Preferred Version row first")
		})

		It("re-widens one keystroke at a time as backspace erases the filter", func() {
			model := typeFilter(newShell(), "gz")

			model, _ = press(model, tea.KeyMsg{Type: tea.KeyBackspace})
			Expect(model.Filter()).To(Equal("g"))
			Expect(kindNames(model.MatchedKinds())).To(ContainElements("Gadget", "ConfigMap"),
				"g fuzzy-matches more than the short name did")

			model, _ = press(model, tea.KeyMsg{Type: tea.KeyBackspace})
			Expect(model.Filter()).To(BeEmpty())
			Expect(model.MatchedKinds()).To(HaveLen(len(browsableKinds())))
		})

		It("keeps a no-match filter harmless: Enter selects nothing and the view stays up", func() {
			model := typeFilter(newShell(), "zzz")

			Expect(model.MatchedKinds()).To(BeEmpty())
			_, ok := model.HighlightedKind()
			Expect(ok).To(BeFalse())

			model, cmd := press(model, tea.KeyMsg{Type: tea.KeyEnter})
			Expect(cmd).To(BeNil())
			_, selected := model.SelectedKind()
			Expect(selected).To(BeFalse())
			Expect(model.View()).To(ContainSubstring("no Kinds match"))
		})
	})

	When("the selection moves through the Kind list", func() {
		DescribeTable(
			"movement keys slide the highlight and clamp at the edges",
			func(down, up tea.KeyMsg) {
				model := newShell()
				rowCount := len(model.MatchedKinds())

				model, _ = press(model, up)
				highlighted, _ := model.HighlightedKind()
				Expect(highlighted).To(Equal(kindNamed("ConfigMap", "v1")),
					"moving up from the first row must clamp, not wrap")

				for range rowCount + 2 {
					model, _ = press(model, down)
				}
				highlighted, _ = model.HighlightedKind()
				Expect(highlighted).To(Equal(kindNamed("Gadget", "v1")),
					"moving down past the last row must clamp, not wrap")

				model, _ = press(model, up)
				highlighted, _ = model.HighlightedKind()
				Expect(highlighted.GVK.Kind).To(Equal("HorizontalPodAutoscaler"))
			},
			Entry("arrow keys",
				tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyUp}),
			Entry("Ctrl-j/k",
				tea.KeyMsg{Type: tea.KeyCtrlJ}, tea.KeyMsg{Type: tea.KeyCtrlK}),
		)
	})

	When("Enter selects the highlighted Kind", func() {
		It("emits the typed handoff carrying the GVK and group-version path", func() {
			model := typeFilter(newShell(), "deploy")

			model, cmd := press(model, tea.KeyMsg{Type: tea.KeyEnter})
			Expect(cmd).NotTo(BeNil())

			msg := cmd()
			Expect(msg).To(Equal(tui.KindSelectedMsg{Kind: kindNamed("Deployment", "v1")}),
				"the handoff must carry the selected Kind's GVK and group-version path")

			_, selected := model.SelectedKind()
			Expect(selected).To(BeFalse(),
				"the transition happens when the shell consumes the message, not on the key itself")
		})

		It("consumes the handoff by lazily fetching the Kind's group document, then opens the compose view", func() {
			model := typeFilter(newShell(), "deploy")
			model, cmd := press(model, tea.KeyMsg{Type: tea.KeyEnter})

			model, fetch := press(model, cmd())

			selected, ok := model.SelectedKind()
			Expect(ok).To(BeTrue())
			Expect(selected).To(Equal(kindNamed("Deployment", "v1")))
			Expect(model.FetchingDocument()).To(BeTrue(),
				"the group document fetch runs as a command, so the shell shows a loading state meanwhile")
			Expect(fetch).NotTo(BeNil())

			model, _ = press(model, fetch())
			Expect(model.ComposeOpen()).To(BeTrue(),
				"the fetched group document opens the compose view on the selected Kind")
		})

		DescribeTable(
			"the compose view keeps the empty-Draft exit grammar",
			func(key tea.KeyMsg) {
				model := composeDeployment()

				_, quit := press(model, key)
				Expect(quit).NotTo(BeNil())
				Expect(quit()).To(Equal(tea.QuitMsg{}),
					"the Draft is still always empty in M2, so quitting needs no prompt")
			},
			Entry("q — the empty-Draft rule",
				tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}),
			Entry("Ctrl-c — the conventional escape hatch",
				tea.KeyMsg{Type: tea.KeyCtrlC}),
		)
	})

	When("Esc dismisses the picker", func() {
		It("clears first, then dismisses: an active filter absorbs the first Esc", func() {
			model := typeFilter(newShell(), "hpa")

			model, cmd := press(model, tea.KeyMsg{Type: tea.KeyEsc})
			Expect(cmd).To(BeNil(),
				"Esc with an active filter clears it and must not quit")
			Expect(model.Filter()).To(BeEmpty())
			Expect(model.MatchedKinds()).To(HaveLen(len(browsableKinds())),
				"clearing the filter re-widens to the full Kind list")

			_, quit := press(model, tea.KeyMsg{Type: tea.KeyEsc})
			Expect(quit).NotTo(BeNil())
			Expect(quit()).To(Equal(tea.QuitMsg{}),
				"Esc on an empty filter quits — an empty Session has no Draft, so no confirmation")
		})

		It("lets Ctrl-c quit immediately even while a filter is active", func() {
			model := typeFilter(newShell(), "hpa")

			_, quit := press(model, tea.KeyMsg{Type: tea.KeyCtrlC})
			Expect(quit).NotTo(BeNil())
			Expect(quit()).To(Equal(tea.QuitMsg{}))
		})

		It("treats q as a filter key, not an exit verb — the picker is a search surface", func() {
			model, cmd := press(newShell(), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})

			Expect(cmd).To(BeNil())
			Expect(model.Filter()).To(Equal("q"))
		})
	})

	When("the terminal window sizes the picker's viewport", func() {
		It("scrolls the list with the selection", func() {
			model, _ := press(newShell(), tea.WindowSizeMsg{Width: 60, Height: 4})

			Expect(model.View()).To(ContainSubstring("ConfigMap"))
			Expect(model.View()).NotTo(ContainSubstring("Gadget"),
				"rows beyond the three visible lines start scrolled out")

			for range len(browsableKinds()) {
				model, _ = press(model, tea.KeyMsg{Type: tea.KeyDown})
			}

			Expect(model.View()).To(ContainSubstring("Gadget"),
				"the viewport must follow the selection to the bottom")
			Expect(model.View()).NotTo(ContainSubstring("ConfigMap"),
				"the top rows scroll out as the selection descends")

			for range len(browsableKinds()) {
				model, _ = press(model, tea.KeyMsg{Type: tea.KeyUp})
			}

			Expect(model.View()).To(ContainSubstring("ConfigMap"),
				"the viewport must follow the selection back to the top")
		})

		DescribeTable(
			"tiny terminals never crash the picker",
			func(size tea.WindowSizeMsg) {
				model, _ := press(newShell(), size)

				Expect(model.View()).To(ContainSubstring(">"),
					"the filter prompt is the last thing to give up")

				model, _ = press(model, tea.KeyMsg{Type: tea.KeyDown})
				Expect(model.View()).NotTo(BeEmpty())
			},
			Entry("one row tall", tea.WindowSizeMsg{Width: 20, Height: 1}),
			Entry("two rows tall", tea.WindowSizeMsg{Width: 20, Height: 2}),
			Entry("zero-sized", tea.WindowSizeMsg{Width: 0, Height: 0}),
		)
	})
})
