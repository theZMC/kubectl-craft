package tui_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/tui"
)

// composeContainers opens apps/v1 Deployment and walks the tree down to the
// spec.template.spec.containers array node — the object-item collection the
// mutation verbs are checked against.
func composeContainers() tui.Model {
	GinkgoHelper()
	model := expandField(widen(composeDeployment()), "spec")
	model = expandField(model, "spec.template")
	model = expandField(model, "spec.template.spec")
	return focusField(model, "spec.template.spec.containers")
}

// appendItem presses `a` on the focused collection node and insists the
// focus moved into an instantiated item row.
func appendItem(model tui.Model, wantDraftPath string) tui.Model {
	GinkgoHelper()
	model, _ = press(model, keyRune('a'))
	Expect(model.FocusedDraftPath()).To(Equal(wantDraftPath),
		"a must append the item and move the focus into it")
	return model
}

// addMapKey presses `a` on the focused map-shaped node, types the key into
// the inline prompt, and confirms it.
func addMapKey(model tui.Model, key string) tui.Model {
	GinkgoHelper()
	model, _ = press(model, keyRune('a'))
	Expect(model.PromptingForKey()).To(BeTrue(), "a on a map-shaped node must prompt for the key")
	model = typeFilter(model, key)
	model, _ = press(model, enterKey)
	Expect(model.PromptingForKey()).To(BeFalse(), "confirming %q must close the key prompt", key)
	return model
}

var _ = Describe("the mutation verbs", func() {
	When("a appends an item on an array node", func() {
		It("instantiates [0] as a real tree row, focuses it, and flags its required fields", func() {
			model := composeContainers()

			model = appendItem(model, "spec.template.spec.containers[0]")

			Expect(model.Breadcrumb()).To(Equal("apps/v1 Deployment › spec.template.spec.containers[0]"),
				"the breadcrumb spells the Draft-level bracket path")
			Expect(model.View()).To(ContainSubstring("containers[0]"))
			Expect(model.View()).To(ContainSubstring("[items]"),
				"the schema-level placeholder row remains for structure browsing")
			Expect(model.MissingRequiredFieldPaths()).To(Equal([]string{
				"spec.selector", "spec.template.spec.containers[0].name",
			}), "adding containers[0] makes its required name contextual — flagged and counted")
			Expect(model.View()).To(ContainSubstring("2 required fields missing"))
		})

		It("appends the next index on the collection node while earlier items keep their state", func() {
			model := appendItem(composeContainers(), "spec.template.spec.containers[0]")
			model, _ = press(model, keyRune('l')) // expand containers[0]

			model, _ = press(model, keyRune('k')) // back up to the collection node
			model = appendItem(model, "spec.template.spec.containers[1]")

			view := model.View()
			Expect(view).To(ContainSubstring("containers[1]"))
			Expect(model.VisibleFieldPaths()).To(ContainElement("spec.template.spec.containers.image"),
				"containers[0] stays expanded across the append — items are rows of their own")
		})

		It("edits an instantiated item's leaf exactly like an ordinary leaf", func() {
			model := appendItem(composeContainers(), "spec.template.spec.containers[0]")
			model, _ = press(model, keyRune('l')) // expand containers[0]

			model = focusField(model, "spec.template.spec.containers.image")
			Expect(model.FocusedDraftPath()).To(Equal("spec.template.spec.containers[0].image"))
			model = confirmLeaf(model, "spec.template.spec.containers.image", "nginx")

			Expect(draftValue(model, "spec.template.spec.containers[0].image")).To(Equal("nginx"))
			Expect(model.View()).To(ContainSubstring("image: nginx"))
		})

		It("appends a scalar array's item as an editable leaf row", func() {
			model := focusField(composeGadget(), "spec.gears")

			model = appendItem(model, "spec.gears[0]")
			model, _ = press(model, enterKey)
			Expect(model.Editing()).To(BeTrue(), "an instantiated scalar item opens its widget like any leaf")
			model = typeFilter(model, "high")
			model, _ = press(model, enterKey)

			Expect(draftValue(model, "spec.gears[0]")).To(Equal("high"))
			Expect(model.View()).To(ContainSubstring("gears[0]: high"))
		})

		It("is a no-op with a hint-bar flash on a non-collection node", func() {
			model := focusField(composeGadget(), "spec.teeth")

			model, _ = press(model, keyRune('a'))

			Expect(model.PromptingForKey()).To(BeFalse())
			Expect(model.FocusedDraftPath()).To(Equal("spec.teeth"), "the focus stays put")
			notice, showing := model.Notice()
			Expect(showing).To(BeTrue(), "an inert a says why — not an error state")
			Expect(notice).To(ContainSubstring("not an array or a map-shaped object"))
		})

		It("is a no-op with a hint on a collection inside an uninstantiated placeholder subtree", func() {
			model := expandField(composeContainers(), "spec.template.spec.containers")
			model, _ = press(model, keyRune('j')) // the [items] placeholder row
			model, _ = press(model, keyRune('l')) // expand the item schema's fields
			model = focusField(model, "spec.template.spec.containers.env")

			model, _ = press(model, keyRune('a'))

			notice, showing := model.Notice()
			Expect(showing).To(BeTrue())
			Expect(notice).To(ContainSubstring("uninstantiated collection"),
				"nothing under a placeholder is Draft-addressable — a works on instantiated positions")
		})
	})

	When("a prompts inline for a map key", func() {
		It("confirms the typed key into a real row and focuses it", func() {
			model := expandField(widen(composeDeployment()), "metadata")
			model = focusField(model, "metadata.labels")

			model = addMapKey(model, "app")

			Expect(model.FocusedDraftPath()).To(Equal(`metadata.labels["app"]`))
			Expect(model.Breadcrumb()).To(Equal(`apps/v1 Deployment › metadata.labels["app"]`))
			Expect(model.View()).To(ContainSubstring(`labels["app"]`))
			Expect(model.View()).To(ContainSubstring("[value]"),
				"the schema-level placeholder row remains for structure browsing")

			model, _ = press(model, enterKey)
			Expect(model.Editing()).To(BeTrue(), "an instantiated key's leaf opens its widget like any leaf")
			model = typeFilter(model, "web")
			model, _ = press(model, enterKey)
			Expect(draftValue(model, `metadata.labels["app"]`)).To(Equal("web"))
		})

		It("cancels the prompt on Esc without touching the Draft", func() {
			model := expandField(widen(composeDeployment()), "metadata")
			model = focusField(model, "metadata.labels")
			model, _ = press(model, keyRune('a'))
			model = typeFilter(model, "app")

			model, _ = press(model, escKey)

			Expect(model.PromptingForKey()).To(BeFalse())
			Expect(model.View()).NotTo(ContainSubstring(`labels["app"]`))
			Expect(model.FocusedDraftPath()).To(Equal("metadata.labels"), "the focus stays on the collection")
		})

		It("rejects a duplicate key inline and keeps the prompt open", func() {
			model := expandField(widen(composeDeployment()), "metadata")
			model = focusField(model, "metadata.labels")
			model = addMapKey(model, "app")
			model, _ = press(model, keyRune('k')) // back up to the collection node

			model, _ = press(model, keyRune('a'))
			model = typeFilter(model, "app")
			model, _ = press(model, enterKey)

			Expect(model.PromptingForKey()).To(BeTrue(), "a rejected key keeps the prompt open")
			Expect(model.View()).To(ContainSubstring("already holds"),
				"the rejection renders inline, naming the duplicate")

			model, _ = press(model, escKey)
			Expect(model.PromptingForKey()).To(BeFalse(), "Esc cancels out of the rejection")
		})

		It("keeps navigate keys typing into the prompt — a search-surface grammar, not commands", func() {
			model := expandField(widen(composeDeployment()), "metadata")
			model = focusField(model, "metadata.labels")
			model, _ = press(model, keyRune('a'))

			model, cmd := press(model, keyRune('q'), keyRune('j'))

			Expect(cmd).To(BeNil(), "q must not quit while the key prompt is open")
			Expect(model.PromptingForKey()).To(BeTrue())
			Expect(model.FocusedDraftPath()).To(Equal("metadata.labels"), "j must not move the tree")
		})
	})

	When("d unsets what the Draft holds", func() {
		It("unsets a set scalar instantly — back to the dimmed placeholder, no confirm", func() {
			model := confirmLeaf(composeGadget(), "spec.profile", "economy")
			model = focusField(model, "spec.profile")

			model, _ = press(model, keyRune('d'))

			Expect(model.ConfirmingUnset()).To(BeFalse(), "a set scalar acts instantly")
			_, filled := model.DraftValueAt("spec.profile")
			Expect(filled).To(BeFalse(), "unset removes the entry — sparse semantics, never set-to-empty")
			Expect(model.View()).To(ContainSubstring("profile: balanced"),
				"the schema default returns as the dimmed placeholder")
		})

		It("confirms a subtree with the Draft-reported discard count, cancelling on n", func() {
			model := appendItem(composeContainers(), "spec.template.spec.containers[0]")
			model, _ = press(model, keyRune('l'))
			model = confirmLeaf(model, "spec.template.spec.containers.image", "nginx")
			model = confirmLeaf(model, "spec.template.spec.containers.name", "app")
			model, _ = press(model, keyRune('g')) // focusField only walks down
			model = focusField(model, "spec")

			model, _ = press(model, keyRune('d'))

			Expect(model.ConfirmingUnset()).To(BeTrue())
			Expect(model.View()).To(ContainSubstring("discard 2 values under spec?"),
				"the confirm carries the discard count from the Draft layer")

			model, _ = press(model, keyRune('n'))
			Expect(model.ConfirmingUnset()).To(BeFalse())
			Expect(draftValue(model, "spec.template.spec.containers[0].name")).To(Equal("app"),
				"n cancels with the Draft intact")
		})

		It("discards the subtree on y, collapsing it back to uninstantiated structure", func() {
			model := appendItem(composeContainers(), "spec.template.spec.containers[0]")
			model, _ = press(model, keyRune('l'))
			model = confirmLeaf(model, "spec.template.spec.containers.name", "app")
			model, _ = press(model, keyRune('g')) // focusField only walks down
			model = focusField(model, "spec")

			model, _ = press(model, keyRune('d'), keyRune('y'))

			Expect(model.ConfirmingUnset()).To(BeFalse())
			_, filled := model.DraftValueAt("spec.template.spec.containers[0].name")
			Expect(filled).To(BeFalse())
			Expect(model.View()).NotTo(ContainSubstring("containers[0]"),
				"the instantiated rows go with the values")
			Expect(model.MissingRequiredFieldPaths()).To(BeEmpty(),
				"de-instantiating clears the contextual required chain")
		})

		It("removes an item row and renumbers its later siblings per the Draft contract", func() {
			model := appendItem(composeContainers(), "spec.template.spec.containers[0]")
			model, _ = press(model, keyRune('l'))
			model = confirmLeaf(model, "spec.template.spec.containers.name", "one")
			model, _ = press(model, keyRune('g')) // focusField only walks down
			model = focusField(model, "spec.template.spec.containers")
			model = appendItem(model, "spec.template.spec.containers[1]")
			model, _ = press(model, keyRune('l'))
			model = confirmLeaf(model, "spec.template.spec.containers.name", "two")

			model, _ = press(model, keyRune('g'))
			model = focusField(model, "spec.template.spec.containers")
			model, _ = press(model, keyRune('j')) // onto containers[0]
			Expect(model.FocusedDraftPath()).To(Equal("spec.template.spec.containers[0]"))
			model, _ = press(model, keyRune('d'))
			Expect(model.View()).To(ContainSubstring("discard 1 value under spec.template.spec.containers[0]?"))
			model, _ = press(model, enterKey)

			Expect(draftValue(model, "spec.template.spec.containers[0].name")).To(Equal("two"),
				"the removed item's old path now addresses its successor — the renumbering contract")
			Expect(model.View()).To(ContainSubstring("containers[0]"))
			Expect(model.View()).NotTo(ContainSubstring("containers[1]"),
				"the tree renumbers with the Draft")
			Expect(model.FocusedDraftPath()).To(Equal("spec.template.spec.containers[0]"),
				"the breadcrumb and focus land on the renumbered successor")
		})

		It("removes an instantiated-but-empty item instantly and clears its required flags", func() {
			model := appendItem(composeContainers(), "spec.template.spec.containers[0]")
			Expect(model.MissingRequiredFieldPaths()).To(ContainElement("spec.template.spec.containers[0].name"))

			model, _ = press(model, keyRune('d'))

			Expect(model.ConfirmingUnset()).To(BeFalse(), "an empty item discards nothing, so no confirm")
			Expect(model.View()).NotTo(ContainSubstring("containers[0]"))
			Expect(model.MissingRequiredFieldPaths()).NotTo(ContainElement("spec.template.spec.containers[0].name"),
				"removing the item clears its contextual requiredness")
		})

		It("is a no-op on a node the Draft holds nothing at", func() {
			model := focusField(composeGadget(), "spec.profile")
			before := model.View()

			model, _ = press(model, keyRune('d'))

			Expect(model.ConfirmingUnset()).To(BeFalse())
			Expect(model.View()).To(Equal(before), "d on an unset node does nothing at all")
		})
	})

	When("the hint bar and help advertise the mutation verbs contextually", func() {
		It("offers a append item on an array node and a add key on a map node", func() {
			model := composeContainers()
			Expect(model.View()).To(ContainSubstring("a append item"))
			Expect(model.View()).NotTo(ContainSubstring("a add key"))

			model, _ = press(model, keyRune('g')) // focusField only walks down
			model = focusField(expandField(model, "spec.template.metadata"), "spec.template.metadata.labels")
			Expect(model.View()).To(ContainSubstring("a add key"))
			Expect(model.View()).NotTo(ContainSubstring("a append item"))
		})

		It("offers d unset only where the Draft holds something", func() {
			model := composeGadget()
			model = focusField(model, "spec.profile")
			Expect(model.View()).NotTo(ContainSubstring("d unset"),
				"nothing is set at spec.profile yet — d would be a no-op")

			model = confirmLeaf(model, "spec.profile", "economy")
			Expect(model.View()).To(ContainSubstring("d unset"))
		})

		It("documents a and d in the ? help overlay", func() {
			model, _ := press(composeDeployment(), keyRune('?'))

			Expect(model.View()).To(ContainSubstring("append an item on an array node"))
			Expect(model.View()).To(ContainSubstring("unset the focused value"))
		})
	})
})
