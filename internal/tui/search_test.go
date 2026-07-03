package tui_test

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/tui"
)

// openSearch opens the `/` field-search overlay on an open compose view.
func openSearch(model tui.Model) tui.Model {
	GinkgoHelper()
	model, _ = press(model, keyRune('/'))
	Expect(model.SearchOpen()).To(BeTrue(), "/ must open the search overlay from navigate mode")
	return model
}

// matchPaths projects the overlay's matches onto their Field Paths for
// readable list assertions.
func matchPaths(matches []tui.SearchMatch) []string {
	paths := make([]string, 0, len(matches))
	for _, match := range matches {
		paths = append(paths, match.FieldPath)
	}
	return paths
}

// matchedRunes spells the runes a match highlighted, in match order.
func matchedRunes(match tui.SearchMatch) string {
	runes := []rune(match.FieldPath)
	spelled := make([]rune, 0, len(match.Matched))
	for _, index := range match.Matched {
		spelled = append(spelled, runes[index])
	}
	return string(spelled)
}

// jumpTo filters the open overlay to the given query, asserts the expected
// match is highlighted, and selects it with Enter.
func jumpTo(model tui.Model, query, expected string) tui.Model {
	GinkgoHelper()
	model = typeFilter(model, query)
	highlighted, ok := model.HighlightedSearchMatch()
	Expect(ok).To(BeTrue())
	Expect(highlighted.FieldPath).To(Equal(expected),
		"the ranking must put the expected match first for %q", query)
	model, _ = press(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	Expect(model.SearchOpen()).To(BeFalse(), "Enter selects the match and closes the overlay")
	return model
}

var _ = Describe("the / field-search overlay", func() {
	When("/ opens the overlay from navigate mode", func() {
		It("opens over the panes with the fixed SCHEMA scope and its own hint line", func() {
			model := openSearch(composeDeployment())

			Expect(model.SearchFilter()).To(BeEmpty())
			Expect(render(model)).To(ContainSubstring("SCHEMA"),
				"the single M2 scope is spelled out — the DRAFT scope's Tab toggle slots in beside it later")
			Expect(render(model)).To(ContainSubstring("esc clear/dismiss"),
				"the overlay carries its own hint line")
			Expect(render(model)).NotTo(ContainSubstring("esc Kind picker"),
				"the navigate-mode hint bar yields to the overlay's")
		})

		It("advertises / in the compose view's hint bar", func() {
			Expect(render(composeDeployment())).To(ContainSubstring("/ search"))
		})

		It("offers the Kind's schema-level Field Paths, not the visible rows", func() {
			paths := matchPaths(openSearch(composeDeployment()).SearchMatches())

			Expect(paths).To(ContainElements("apiVersion", "spec.template.spec.containers.image"),
				"candidates come from the Type Schema, expanded or not")
			Expect(paths).NotTo(ContainElement(""), "the root is not a candidate")
		})

		It("treats q as a filter key, not an exit verb — the overlay is a search surface", func() {
			model, cmd := press(openSearch(composeDeployment()), keyRune('q'))

			Expect(cmd).To(BeNil())
			Expect(model.SearchOpen()).To(BeTrue())
			Expect(model.SearchFilter()).To(Equal("q"))
		})

		It("still quits immediately on Ctrl-c — the conventional escape hatch reaches through the overlay", func() {
			_, quit := press(openSearch(composeDeployment()), tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})

			Expect(quit).NotTo(BeNil())
			Expect(quit()).To(Equal(tea.QuitMsg{}))
		})
	})

	When("typing filters the candidates", func() {
		It("fuzzy-matches over the full dotted path and reports the matched runes for highlighting", func() {
			model := typeFilter(openSearch(composeDeployment()), "imgplcy")

			matches := model.SearchMatches()

			Expect(matchPaths(matches)).To(ContainElement("spec.template.spec.containers.imagePullPolicy"))
			for _, match := range matches {
				Expect(strings.ToLower(matchedRunes(match))).To(Equal("imgplcy"),
					"%q must highlight exactly the filter's runes, in order", match.FieldPath)
			}
		})

		It("ranks tighter, shorter matches first", func() {
			model := typeFilter(openSearch(composeDeployment()), "replicas")

			paths := matchPaths(model.SearchMatches())

			Expect(paths).To(ContainElements("spec.replicas", "status.replicas"))
			Expect(paths[0]).To(Equal("spec.replicas"),
				"equally tight matches rank by Field Path length")
		})

		It("re-widens one keystroke at a time as backspace erases the filter", func() {
			model := typeFilter(openSearch(composeDeployment()), "imgplcy")
			narrowed := len(model.SearchMatches())

			model, _ = press(model, tea.KeyPressMsg{Code: tea.KeyBackspace})

			Expect(model.SearchFilter()).To(Equal("imgplc"))
			Expect(len(model.SearchMatches())).To(BeNumerically(">=", narrowed))
		})

		It("keeps a no-match filter harmless: Enter selects nothing and the overlay stays up", func() {
			model := typeFilter(openSearch(composeDeployment()), "zzzz")

			Expect(model.SearchMatches()).To(BeEmpty())
			Expect(render(model)).To(ContainSubstring("no Field Paths match"))

			model, cmd := press(model, tea.KeyPressMsg{Code: tea.KeyEnter})
			Expect(cmd).To(BeNil())
			Expect(model.SearchOpen()).To(BeTrue())
			Expect(model.ComposeOpen()).To(BeTrue())
		})
	})

	When("the selection moves through the matches", func() {
		DescribeTable(
			"movement keys slide the selection and clamp at the edges",
			func(down, up tea.KeyPressMsg) {
				model := openSearch(composeDeployment())

				model, _ = press(model, up)
				highlighted, _ := model.HighlightedSearchMatch()
				Expect(highlighted.FieldPath).To(Equal("apiVersion"),
					"moving up from the first match must clamp, not wrap")

				model, _ = press(model, down, down)
				highlighted, _ = model.HighlightedSearchMatch()
				Expect(highlighted.FieldPath).To(Equal("metadata"),
					"an empty filter lists the candidates in tree order")
			},
			Entry("arrow keys", tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyUp}),
			Entry("Ctrl-j/k", tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}),
		)

		It("scrolls the match list with the selection in a short terminal", func() {
			model, _ := press(composeDeployment(), tea.WindowSizeMsg{Width: 80, Height: 8})
			model = openSearch(model)

			for range 10 {
				model, _ = press(model, tea.KeyPressMsg{Code: tea.KeyDown})
			}

			highlighted, ok := model.HighlightedSearchMatch()
			Expect(ok).To(BeTrue())
			Expect(render(model)).To(ContainSubstring(highlighted.FieldPath),
				"the viewport must follow the selection")
		})
	})

	When("Enter jumps the tree under the landing rule", func() {
		It("expands every ancestor and focuses a match under no collection", func() {
			model := jumpTo(openSearch(composeDeployment()), "spec.strategy.type", "spec.strategy.type")

			Expect(model.FocusedFieldPath()).To(Equal("spec.strategy.type"))
			Expect(model.Breadcrumb()).To(Equal("apps/v1 Deployment › spec.strategy.type"),
				"the breadcrumb reflects the landing")
			Expect(model.VisibleFieldPaths()).To(ContainElements("spec.replicas", "spec.strategy.rollingUpdate"),
				"every ancestor along the path is expanded")
		})

		It("lands on the collection node when the path crosses an array the Draft holds no items at", func() {
			model := jumpTo(openSearch(composeDeployment()),
				"containers.image", "spec.template.spec.containers.image")

			Expect(model.FocusedFieldPath()).To(Equal("spec.template.spec.containers"),
				"crossing an array with nothing instantiated lands on the collection node — where `a` adds the first item")
			Expect(model.Breadcrumb()).To(Equal("apps/v1 Deployment › spec.template.spec.containers"))

			shared := 0
			for _, path := range model.VisibleFieldPaths() {
				if path == "spec.template.spec.containers" {
					shared++
				}
			}
			Expect(shared).To(Equal(1),
				"the collection node itself is the landing: its [items] row stays unexpanded")
		})

		It("lands inside the first instantiated item when the path crosses an array with items", func() {
			model := focusField(widen(composeDeployment()), "spec")
			model = expandField(model, "spec")
			model = expandField(model, "spec.template")
			model = expandField(model, "spec.template.spec")
			model = focusField(model, "spec.template.spec.containers")
			model, _ = press(model, keyRune('a')) // focus moves into containers[0]
			model, _ = press(model, keyRune('k'), keyRune('a'))
			Expect(model.FocusedDraftPath()).To(Equal("spec.template.spec.containers[1]"))

			model = jumpTo(openSearch(model), "containers.image", "spec.template.spec.containers.image")

			Expect(model.FocusedFieldPath()).To(Equal("spec.template.spec.containers.image"))
			Expect(model.FocusedDraftPath()).To(Equal("spec.template.spec.containers[0].image"),
				"a crossing with instantiated entries lands on the first instantiated item, not the placeholder")
			Expect(model.Breadcrumb()).To(Equal("apps/v1 Deployment › spec.template.spec.containers[0].image"))
		})

		It("lands on the map node when the path crosses a map-shaped object the Draft holds no keys at", func() {
			rack := composeKind("craft.example.com", "v5", "Rack", "apis/craft.example.com/v5")
			model := jumpTo(openSearch(openKind(newShell(), rack)), "slots.label", "spec.slots.label")

			Expect(model.FocusedFieldPath()).To(Equal("spec.slots"),
				"a map with no instantiated keys is the same landing branch as an empty array")
		})

		It("lands inside the first instantiated key — sorted order — when the map holds entries", func() {
			rack := composeKind("craft.example.com", "v5", "Rack", "apis/craft.example.com/v5")
			model := expandField(widen(openKind(newShell(), rack)), "spec")
			model = focusField(model, "spec.slots")
			model = addMapKey(model, "zulu")      // focus moves into slots["zulu"]
			model, _ = press(model, keyRune('k')) // back up to the collection node
			model = addMapKey(model, "alpha")

			model = jumpTo(openSearch(model), "slots.label", "spec.slots.label")

			Expect(model.FocusedDraftPath()).To(Equal(`spec.slots["alpha"].label`),
				"the first instantiated key is the first in the Draft's sorted key order")
		})

		It("lands on the first collection crossed, not the deepest reachable row", func() {
			model := jumpTo(openSearch(openKind(newShell(), crdKind())),
				"openapiv3schema.properties", "spec.versions.schema.openAPIV3Schema.properties")

			Expect(model.FocusedFieldPath()).To(Equal("spec.versions"),
				"spec.versions is the first array the path crosses, and the Draft holds no items there")
		})

		It("resets the completed search on the next open, keeping the candidates", func() {
			model := jumpTo(openSearch(composeDeployment()), "spec.strategy.type", "spec.strategy.type")

			model = openSearch(model)

			Expect(model.SearchFilter()).To(BeEmpty(), "selecting a match completed that search")
			highlighted, ok := model.HighlightedSearchMatch()
			Expect(ok).To(BeTrue())
			Expect(highlighted.FieldPath).To(Equal("apiVersion"))
		})
	})

	When("a self-referential Type Schema is searched", func() {
		It("enumerates finite candidates through the JSONSchemaProps cycle — each Type Schema once per chain", func() {
			model := openSearch(openKind(newShell(), crdKind()))

			paths := matchPaths(model.SearchMatches())

			Expect(paths).To(ContainElement("spec.versions.schema.openAPIV3Schema.properties"),
				"the cycle's re-entry field is itself a candidate")
			Expect(paths).NotTo(ContainElement("spec.versions.schema.openAPIV3Schema.properties.properties"),
				"the lap beneath the re-entry is never materialized, so enumeration cannot hang")
		})
	})

	When("Esc clears then dismisses", func() {
		It("absorbs the first Esc into clearing the filter; the second returns to navigate mode", func() {
			model := typeFilter(openSearch(composeDeployment()), "img")

			model, _ = press(model, tea.KeyPressMsg{Code: tea.KeyEsc})
			Expect(model.SearchOpen()).To(BeTrue(),
				"Esc with an active filter clears it and keeps the overlay open")
			Expect(model.SearchFilter()).To(BeEmpty())

			model, cmd := press(model, tea.KeyPressMsg{Code: tea.KeyEsc})
			Expect(cmd).To(BeNil(), "dismissing the overlay must not leave the compose view")
			Expect(model.SearchOpen()).To(BeFalse())
			Expect(model.ComposeOpen()).To(BeTrue())
			Expect(model.FocusedFieldPath()).To(BeEmpty(), "dismissal leaves the focus where it was")
		})
	})
})
