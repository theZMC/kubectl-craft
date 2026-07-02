package tui_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/tui"
)

var (
	enterKey     = tea.KeyMsg{Type: tea.KeyEnter}
	escKey       = tea.KeyMsg{Type: tea.KeyEsc}
	spaceKey     = tea.KeyMsg{Type: tea.KeySpace}
	downKey      = tea.KeyMsg{Type: tea.KeyDown}
	backspaceKey = tea.KeyMsg{Type: tea.KeyBackspace}
)

// widen keeps the panes' lines unwrapped so substring assertions on the
// rendered view stay honest.
func widen(model tui.Model) tui.Model {
	GinkgoHelper()
	model, _ = press(model, tea.WindowSizeMsg{Width: 200, Height: 50})
	return model
}

// composeGadget opens the compose view on craft.example.com/v1 Gadget with
// spec expanded — the corpus Kind carrying the numeric, pattern, and
// int-or-string shapes the widgets are checked against.
func composeGadget() tui.Model {
	GinkgoHelper()
	return expandField(widen(openKind(newShell(), kindNamed("Gadget", "v1"))), "spec")
}

// openWidget focuses a leaf and opens its value widget.
func openWidget(model tui.Model, fieldPath string) tui.Model {
	GinkgoHelper()
	model = focusField(model, fieldPath)
	model, _ = press(model, enterKey)
	Expect(model.Editing()).To(BeTrue(), "Enter on the %s leaf must open its value widget", fieldPath)
	return model
}

// draftValue reads the value the Draft holds at a Draft-level Field Path,
// failing the spec when nothing is filled there.
func draftValue(model tui.Model, fieldPath string) any {
	GinkgoHelper()
	value, filled := model.DraftValueAt(fieldPath)
	Expect(filled).To(BeTrue(), "the Draft must hold a value at %s", fieldPath)
	return value
}

// confirmLeaf opens a leaf's widget, types the given text, and confirms it
// into the Draft.
func confirmLeaf(model tui.Model, fieldPath, text string) tui.Model {
	GinkgoHelper()
	model = openWidget(model, fieldPath)
	model = typeFilter(model, text)
	model, _ = press(model, enterKey)
	Expect(model.Editing()).To(BeFalse(), "confirming %q at %s must close the widget", text, fieldPath)
	return model
}

var _ = Describe("edit mode", func() {
	When("Enter opens a leaf's value widget", func() {
		It("keeps navigate keys inert while editing — and q types instead of quitting", func() {
			model := openWidget(composeGadget(), "spec.nickname")

			model, cmd := press(model, keyRune('j'), downKey, keyRune('G'), keyRune('q'), keyRune('/'), keyRune('?'))

			Expect(cmd).To(BeNil(), "q must not quit while a text widget is open")
			Expect(model.Editing()).To(BeTrue())
			Expect(model.FocusedFieldPath()).To(Equal("spec.nickname"),
				"the tree focus must not move while the widget is open")
			Expect(model.SearchOpen()).To(BeFalse(), "/ types into the widget instead of opening the search overlay")
			Expect(model.HelpOpen()).To(BeFalse(), "? types into the widget instead of opening the help overlay")
		})

		It("cancels on Esc without mutating the Draft", func() {
			model := openWidget(composeGadget(), "spec.nickname")
			model = typeFilter(model, "ratchet")

			model, _ = press(model, escKey)

			Expect(model.Editing()).To(BeFalse())
			_, filled := model.DraftValueAt("spec.nickname")
			Expect(filled).To(BeFalse(), "Esc cancels back to navigate mode with the Draft untouched")
			Expect(model.ComposeOpen()).To(BeTrue())
		})

		It("leaves a hint on a schema-blind leaf — raw YAML composes it, not a typed widget", func() {
			model := focusField(composeGadget(), "spec.tuning")

			model, _ = press(model, enterKey)

			Expect(model.Editing()).To(BeFalse())
			notice, showing := model.Notice()
			Expect(showing).To(BeTrue())
			Expect(notice).To(ContainSubstring("raw YAML"),
				"the raw-YAML escape hatch is the schema-blind node's editor, and it isn't wired yet")
		})

		It("leaves a hint on an uninstantiated [items] placeholder row — a on the collection adds the first item", func() {
			model := expandField(composeGadget(), "spec.gears")
			model, _ = press(model, keyRune('j')) // the [items] row shares its parent's Field Path

			model, _ = press(model, enterKey)

			Expect(model.Editing()).To(BeFalse())
			Expect(model.FocusedFieldPath()).To(Equal("spec.gears"))
			notice, showing := model.Notice()
			Expect(showing).To(BeTrue(), "an inert Enter must say why, not just do nothing")
			Expect(notice).To(ContainSubstring("press a on the collection"),
				"the placeholder subtree is structure browsing — instantiating goes through the mutation verbs")
		})
	})

	When("the text widget composes a string", func() {
		It("confirms the typed value into the Draft and shows it on the row", func() {
			model := confirmLeaf(composeGadget(), "spec.nickname", "ratchet")

			Expect(draftValue(model, "spec.nickname")).To(Equal("ratchet"))
			Expect(model.View()).To(ContainSubstring("nickname: ratchet"),
				"a set leaf renders its value on the tree row")
		})

		It("rejects a pattern violation inline, committing nothing", func() {
			model := openWidget(composeGadget(), "spec.nickname")
			model = typeFilter(model, "Bad!")

			model, _ = press(model, enterKey)

			Expect(model.Editing()).To(BeTrue(), "a rejected confirm keeps the widget open")
			Expect(model.View()).To(ContainSubstring("does not match the Type Schema's pattern"))
			_, filled := model.DraftValueAt("spec.nickname")
			Expect(filled).To(BeFalse(), "a rejected value never reaches the Draft")
		})

		It("confirms an empty buffer as the explicit empty string — unsetting stays the d verb's business", func() {
			model := widen(openKind(newShell(), widgetKind()))
			model = expandField(model, "spec")
			model = openWidget(model, "spec.paint")

			model, _ = press(model, enterKey)

			Expect(model.Editing()).To(BeFalse())
			Expect(draftValue(model, "spec.paint")).To(Equal(""),
				"an empty string is a legitimate Manifest value; sparse semantics remove entries only via unset")
		})

		It("reopens holding the value the Draft already has", func() {
			model := confirmLeaf(composeGadget(), "spec.nickname", "ratchet")

			model, _ = press(model, enterKey, backspaceKey)
			model = typeFilter(model, "s")
			model, _ = press(model, enterKey)

			Expect(draftValue(model, "spec.nickname")).To(Equal("ratches"),
				"the widget prefills with the set value, so editing amends instead of retyping")
		})
	})

	When("the numeric widget validates on confirm", func() {
		It("confirms an integer in its canonical spelling", func() {
			model := confirmLeaf(composeGadget(), "spec.teeth", "12")

			Expect(draftValue(model, "spec.teeth")).To(Equal(int64(12)))
			Expect(model.View()).To(ContainSubstring("teeth: 12"))
		})

		It("rejects what does not parse before the Draft is ever asked", func() {
			model := openWidget(composeGadget(), "spec.teeth")
			model = typeFilter(model, "dozen")

			model, _ = press(model, enterKey)

			Expect(model.Editing()).To(BeTrue())
			Expect(model.View()).To(ContainSubstring("is not an integer"))
			_, filled := model.DraftValueAt("spec.teeth")
			Expect(filled).To(BeFalse())
		})

		It("renders a multipleOf rejection inline and recovers on the next confirm", func() {
			model := openWidget(composeGadget(), "spec.teeth")
			model = typeFilter(model, "7")

			model, _ = press(model, enterKey)
			Expect(model.Editing()).To(BeTrue())
			Expect(model.View()).To(ContainSubstring("not a multiple of"))

			model, _ = press(model, backspaceKey)
			model = typeFilter(model, "8")
			model, _ = press(model, enterKey)

			Expect(model.Editing()).To(BeFalse())
			Expect(draftValue(model, "spec.teeth")).To(Equal(int64(8)))
		})

		It("holds a number to its bounds", func() {
			model := openWidget(composeGadget(), "spec.efficiency")
			model = typeFilter(model, "1.5")

			model, _ = press(model, enterKey)

			Expect(model.Editing()).To(BeTrue())
			Expect(model.View()).To(ContainSubstring("above the Type Schema's maximum"))

			model, _ = press(model, backspaceKey, backspaceKey, backspaceKey)
			model = typeFilter(model, "0.5")
			model, _ = press(model, enterKey)

			Expect(draftValue(model, "spec.efficiency")).To(Equal(0.5))
		})
	})

	When("the toggle widget composes a boolean", func() {
		It("flips on space and confirms the toggle's state", func() {
			model := expandField(widen(composeDeployment()), "spec")
			model = openWidget(model, "spec.paused")

			model, _ = press(model, spaceKey, enterKey)

			Expect(model.Editing()).To(BeFalse())
			Expect(draftValue(model, "spec.paused")).To(BeTrue())
			Expect(model.View()).To(ContainSubstring("paused: true"))
		})
	})

	When("the enum select is constrained to the Type Schema's values", func() {
		palette := func() tui.Model {
			GinkgoHelper()
			kind := composeKind("craft.example.com", "v3", "Palette", "apis/craft.example.com/v3")
			return expandField(widen(openKind(newShell(), kind)), "spec")
		}

		It("chooses with the arrows and confirms the highlighted value", func() {
			model := openWidget(palette(), "spec.shade")

			model, _ = press(model, downKey, enterKey)

			Expect(draftValue(model, "spec.shade")).To(Equal("green"))
			Expect(model.View()).To(ContainSubstring("shade: green"))
		})

		It("keeps printable keys inert — nothing outside the enum can be spelled", func() {
			model := openWidget(palette(), "spec.shade")

			model, _ = press(model, keyRune('x'), keyRune('j'), enterKey)

			Expect(draftValue(model, "spec.shade")).To(Equal("red"),
				"the select stays on its first value: typing must not move or edit it")
		})
	})

	When("the int-or-string text widget accepts both spellings", func() {
		It("confirms a percentage as the string spelling", func() {
			model := confirmLeaf(composeGadget(), "spec.maxUnavailable", "25%")

			Expect(draftValue(model, "spec.maxUnavailable")).To(Equal("25%"))
		})

		It("confirms a bare count as the integer spelling", func() {
			model := confirmLeaf(composeGadget(), "spec.maxUnavailable", "3")

			Expect(draftValue(model, "spec.maxUnavailable")).To(Equal(int64(3)))
		})
	})

	When("the tree renders Draft state", func() {
		It("shows an unset field's schema default as a dimmed placeholder until a value replaces it", func() {
			model := composeGadget()

			Expect(model.View()).To(ContainSubstring("profile: balanced"),
				"the schema default renders as a placeholder on the unset row")
			_, filled := model.DraftValueAt("spec.profile")
			Expect(filled).To(BeFalse(), "a placeholder is not a value: the Draft stays sparse")

			model = confirmLeaf(model, "spec.profile", "economy")

			Expect(model.View()).To(ContainSubstring("profile: economy"))
			Expect(model.View()).NotTo(ContainSubstring("profile: balanced"),
				"a set value takes the row over from the placeholder")
		})
	})

	When("the completeness status line tracks the Draft", func() {
		It("counts the required chain down as confirmed values fill it", func() {
			model := widen(openKind(newShell(), widgetKind()))

			Expect(model.MissingRequiredFieldPaths()).To(Equal([]string{"spec", "spec.size"}))
			Expect(model.View()).To(ContainSubstring("2 required fields missing"))

			model = expandField(model, "spec")
			model = confirmLeaf(model, "spec.size", "5")

			Expect(draftValue(model, "spec.size")).To(Equal(int64(5)))
			Expect(model.MissingRequiredFieldPaths()).To(BeEmpty(),
				"setting spec.size instantiates spec implicitly, completing the chain")
			Expect(model.View()).To(ContainSubstring("no required fields missing"))
			Expect(model.View()).NotTo(ContainSubstring("✱"),
				"the required-but-unset markers clear with the chain")
		})

		It("surfaces contextual requiredness as instantiating uncovers it", func() {
			model := composeGadget()
			Expect(model.View()).To(ContainSubstring("no required fields missing"),
				"Gadget's root requires nothing while the Draft is empty")

			model = confirmLeaf(model, "spec.nickname", "ratchet")

			Expect(model.MissingRequiredFieldPaths()).To(Equal([]string{"spec.maxReplicas", "spec.minReplicas"}),
				"instantiating spec makes its required fields missing — contextual requiredness")
			Expect(model.View()).To(ContainSubstring("2 required fields missing"))
			Expect(model.View()).To(ContainSubstring("maxReplicas ✱"))

			model, _ = press(model, keyRune('g'))
			model = confirmLeaf(model, "spec.minReplicas", "1")
			Expect(model.View()).To(ContainSubstring("1 required field missing"))

			model, _ = press(model, keyRune('g'))
			model = confirmLeaf(model, "spec.maxReplicas", "3")
			Expect(model.MissingRequiredFieldPaths()).To(BeEmpty())
			Expect(model.View()).To(ContainSubstring("no required fields missing"))
		})
	})

	When("Esc returns to the picker over a non-empty Draft", func() {
		It("warns before discarding, keeps composing on cancel, and discards on confirm", func() {
			model := widen(openKind(newShell(), widgetKind()))
			model = expandField(model, "spec")
			model = confirmLeaf(model, "spec.size", "5")

			model, cmd := press(model, escKey)
			Expect(cmd).To(BeNil(), "a non-empty Draft must not return to the picker unconfirmed")
			Expect(model.ConfirmingDiscard()).To(BeTrue())
			Expect(model.View()).To(ContainSubstring("discard the Draft"))

			model, _ = press(model, keyRune('j'))
			Expect(model.ConfirmingDiscard()).To(BeTrue(), "only Enter and Esc answer the confirm")

			model, _ = press(model, escKey)
			Expect(model.ConfirmingDiscard()).To(BeFalse())
			Expect(model.ComposeOpen()).To(BeTrue())
			Expect(draftValue(model, "spec.size")).To(Equal(int64(5)),
				"cancelling the confirm keeps the Draft intact")

			model, _ = press(model, escKey)
			model, back := press(model, enterKey)
			Expect(back).NotTo(BeNil())
			model, _ = press(model, back())
			_, selected := model.SelectedKind()
			Expect(selected).To(BeFalse(), "confirming returns to the Kind picker")

			model, reopen := press(model, tui.KindSelectedMsg{Kind: widgetKind()})
			Expect(reopen).To(BeNil(), "the group document stays memoized")
			Expect(model.ComposeOpen()).To(BeTrue())
			_, filled := model.DraftValueAt("spec.size")
			Expect(filled).To(BeFalse(), "the discarded Draft is gone: reopening composes afresh")
		})
	})

	When("q quits over a non-empty Draft", func() {
		It("warns before discarding — the interim guard until the exit ramp's three-way prompt", func() {
			model := widen(openKind(newShell(), widgetKind()))
			model = expandField(model, "spec")
			model = confirmLeaf(model, "spec.size", "5")

			model, cmd := press(model, keyRune('q'))
			Expect(cmd).To(BeNil(), "a non-empty Draft must not be discarded by a bare keypress")
			Expect(model.ConfirmingDiscard()).To(BeTrue())
			Expect(model.View()).To(ContainSubstring("discard the Draft and quit?"))

			model, _ = press(model, escKey)
			Expect(model.ConfirmingDiscard()).To(BeFalse())
			Expect(model.ComposeOpen()).To(BeTrue())
			Expect(draftValue(model, "spec.size")).To(Equal(int64(5)),
				"cancelling the confirm keeps composing with the Draft intact")

			model, _ = press(model, keyRune('q'))
			_, quit := press(model, enterKey)
			Expect(quit).NotTo(BeNil())
			Expect(quit()).To(Equal(tea.QuitMsg{}), "confirming quits the Session")
		})
	})
})
