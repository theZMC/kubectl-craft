package tui_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/tui"
)

var ctrlSKey = tea.KeyMsg{Type: tea.KeyCtrlS}

// tuningGraft is the graft the raw-YAML specs compose at Gadget's
// spec.tuning — the corpus's canonical preserve-unknown-fields fixture —
// spelled as its parsed value.
func tuningGraft() map[string]any {
	return map[string]any{"knobs": map[string]any{"gain": 3}}
}

// graftTuning opens the raw-YAML text area on spec.tuning, types the
// two-line tuningGraft subtree, and confirms it into the Draft.
func graftTuning(model tui.Model) tui.Model {
	GinkgoHelper()
	model = openWidget(model, "spec.tuning")
	model = typeFilter(model, "knobs:")
	model, _ = press(model, enterKey)
	model = typeFilter(model, "  gain: 3")
	model, _ = press(model, ctrlSKey)
	Expect(model.Editing()).To(BeFalse(), "confirming the raw YAML must close the text area")
	return model
}

var _ = Describe("the raw-YAML escape hatch", func() {
	When("Enter opens the text area on a schema-blind leaf", func() {
		It("opens edit mode with the escape hatch's own grammar", func() {
			model := focusField(composeGadget(), "spec.tuning")

			model, cmd := press(model, enterKey)

			Expect(cmd).To(BeNil())
			Expect(model.Editing()).To(BeTrue(),
				"the raw-YAML text area is the schema-blind leaf's value widget")
			Expect(model.View()).To(ContainSubstring("composing raw YAML"))
			Expect(model.View()).To(ContainSubstring("ctrl+s confirm"),
				"the hint bar documents the multiline confirm — enter types newlines here")
		})

		It("keeps navigate keys typing and Enter breaking lines — q types instead of quitting", func() {
			model := openWidget(composeGadget(), "spec.tuning")

			model, cmd := press(model, keyRune('q'), enterKey, keyRune('j'))

			Expect(cmd).To(BeNil(), "q must not quit while the text area is open")
			Expect(model.Editing()).To(BeTrue(), "enter breaks the line instead of confirming")
			Expect(model.FocusedFieldPath()).To(Equal("spec.tuning"), "j must not move the tree")
		})

		It("cancels on Esc without touching the Draft", func() {
			model := openWidget(composeGadget(), "spec.tuning")
			model = typeFilter(model, "knobs: {}")

			model, _ = press(model, escKey)

			Expect(model.Editing()).To(BeFalse())
			_, filled := model.DraftValueAt("spec.tuning")
			Expect(filled).To(BeFalse(), "Esc cancels back to navigate mode with the Draft untouched")
		})

		It("grafts the parsed subtree into the Draft on confirm", func() {
			model := graftTuning(composeGadget())

			Expect(draftValue(model, "spec.tuning")).To(Equal(tuningGraft()),
				"the confirmed buffer parses and grafts at the leaf's Draft-level Field Path")
		})

		It("renders a parse rejection inline, committing nothing", func() {
			model := openWidget(composeGadget(), "spec.tuning")
			model = typeFilter(model, "knobs: [")

			model, _ = press(model, ctrlSKey)

			Expect(model.Editing()).To(BeTrue(), "a rejected confirm keeps the text area open")
			Expect(model.View()).To(ContainSubstring("parsing the raw YAML grafted"))
			_, filled := model.DraftValueAt("spec.tuning")
			Expect(filled).To(BeFalse(), "malformed YAML never reaches the Draft")
		})

		It("rejects an empty buffer inline — unsetting stays the d verb's business", func() {
			model := openWidget(composeGadget(), "spec.tuning")

			model, _ = press(model, ctrlSKey)

			Expect(model.Editing()).To(BeTrue())
			Expect(model.View()).To(ContainSubstring("holds no value"),
				"a graft holding nothing is a rejection, not an implicit unset")
		})

		It("reopens holding the grafted YAML in its canonical spelling", func() {
			model := graftTuning(composeGadget())

			model = openWidget(model, "spec.tuning")

			view := model.View()
			Expect(view).To(ContainSubstring("knobs:"))
			Expect(view).To(ContainSubstring("gain: 3"),
				"the text area prefills with the graft, so editing amends instead of retyping")
		})
	})

	When("a grafted subtree renders distinctly", func() {
		It("sizes the graft on the tree row instead of spelling it inline", func() {
			model := graftTuning(composeGadget())

			Expect(model.View()).To(ContainSubstring("tuning: raw YAML (2 lines)"),
				"a graft is opaque to the Type Schema — the row says how much sits there")
		})

		It("shows the graft's summary and its YAML in the detail pane", func() {
			model := focusField(graftTuning(composeGadget()), "spec.tuning")

			view := model.View()
			Expect(view).To(ContainSubstring("grafted: raw YAML (2 lines)"))
			Expect(view).To(ContainSubstring("gain: 3"),
				"the detail pane shows what was grafted, not just that something was")
		})

		It("offers the escape hatch's verbs on the schema-blind row's hint bar", func() {
			model := focusField(composeGadget(), "spec.tuning")

			Expect(model.View()).To(ContainSubstring("e $EDITOR"),
				"the hint bar advertises the pop-out where it serves the focused row")
		})
	})

	When("d unsets a grafted subtree", func() {
		It("confirms with the graft's distinct rendering before discarding", func() {
			model := focusField(graftTuning(composeGadget()), "spec.tuning")

			model, _ = press(model, keyRune('d'))

			Expect(model.ConfirmingUnset()).To(BeTrue(),
				"a graft is a whole subtree behind one entry, so d confirms first")
			Expect(model.View()).To(ContainSubstring(
				"discard the raw YAML (2 lines) grafted at spec.tuning?",
			))

			model, _ = press(model, keyRune('n'))
			Expect(model.ConfirmingUnset()).To(BeFalse())
			Expect(draftValue(model, "spec.tuning")).To(Equal(tuningGraft()),
				"n cancels with the graft intact")
		})

		It("discards the graft on y, back to the schema-blind placeholder", func() {
			model := focusField(graftTuning(composeGadget()), "spec.tuning")

			model, _ = press(model, keyRune('d'), keyRune('y'))

			Expect(model.ConfirmingUnset()).To(BeFalse())
			_, filled := model.DraftValueAt("spec.tuning")
			Expect(filled).To(BeFalse(), "unset removes the graft — sparse semantics")
			Expect(model.View()).NotTo(ContainSubstring("raw YAML (2 lines)"))
		})
	})
})
