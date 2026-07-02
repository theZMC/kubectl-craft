package tui_test

import (
	"errors"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/tui"
)

var (
	upKey    = tea.KeyMsg{Type: tea.KeyUp}
	ctrlDKey = tea.KeyMsg{Type: tea.KeyCtrlD}
	ctrlCKey = tea.KeyMsg{Type: tea.KeyCtrlC}
)

// composedWidget opens craft.example.com/v1 Widget and fills spec.size, so
// the Draft is non-empty and the exit ramp has something to emit.
func composedWidget() tui.Model {
	GinkgoHelper()
	model := widen(openKind(newShell(), widgetKind()))
	model = expandField(model, "spec")
	return confirmLeaf(model, "spec.size", "5")
}

// widgetManifest is the Manifest composedWidget's Draft Emits: identity
// keys leading, every other mapping sorted lexically, sparse beneath.
const widgetManifest = "apiVersion: craft.example.com/v1\nkind: Widget\nspec:\n  size: 5\n"

// identityManifest is the identity-only Manifest an empty Widget Draft
// Emits — the sparse-emission contract's floor.
const identityManifest = "apiVersion: craft.example.com/v1\nkind: Widget\n"

// highlightedExitOption reads the exit menu's highlighted ramp, failing the
// spec when the menu is not open.
func highlightedExitOption(model tui.Model) string {
	GinkgoHelper()
	option, open := model.HighlightedExitOption()
	Expect(open).To(BeTrue(), "the exit menu must be open")
	return option
}

// emitThroughShell relays the emit ramp's command through the shell the way
// the Bubble Tea runtime would — the typed message carrying the Manifest
// bytes lands in Update, which records them and quits — and returns the
// model that recorded the emission after insisting the Session quit.
func emitThroughShell(model tui.Model, cmd tea.Cmd) tui.Model {
	GinkgoHelper()
	Expect(cmd).NotTo(BeNil(), "the emit ramp must end the Session through a command")
	model, quit := press(model, cmd())
	Expect(quit).NotTo(BeNil(), "recording the emission must quit the Session")
	Expect(quit()).To(Equal(tea.QuitMsg{}))
	return model
}

// emittedManifest reads the Manifest bytes the Session recorded, failing
// the spec when it never emitted.
func emittedManifest(model tui.Model) string {
	GinkgoHelper()
	manifest, emitted := model.EmittedManifest()
	Expect(emitted).To(BeTrue(), "the Session must have recorded an emission")
	return string(manifest)
}

// expectNothingEmitted insists the Session recorded no emission — the
// discard ramps' shared postcondition.
func expectNothingEmitted(model tui.Model) {
	GinkgoHelper()
	_, emitted := model.EmittedManifest()
	Expect(emitted).To(BeFalse(), "a discard ramp must record no Manifest")
}

var _ = Describe("the exit ramp", func() {
	When("q is pressed in navigate mode over a non-empty Draft", func() {
		It("opens the three-way exit menu with Emit & quit highlighted, the Draft intact", func() {
			model := composedWidget()

			model, cmd := press(model, keyRune('q'))

			Expect(cmd).To(BeNil(), "a non-empty Draft must not be discarded by a bare keypress")
			Expect(model.ExitMenuOpen()).To(BeTrue())
			Expect(highlightedExitOption(model)).To(Equal("Emit & quit"),
				"the ramp composing exists for leads the menu")
			Expect(draftValue(model, "spec.size")).To(Equal(int64(5)),
				"opening the menu must not touch the Draft")
		})

		It("renders the menu and the completeness status line together", func() {
			model, _ := press(composedWidget(), keyRune('q'))

			view := model.View()
			Expect(view).To(ContainSubstring("Emit & quit"))
			Expect(view).To(ContainSubstring("Discard & quit"))
			Expect(view).To(ContainSubstring("Cancel"))
			Expect(view).To(ContainSubstring("no required fields missing"),
				"the status line and the menu coexist — what would I emit, and how complete is it")
			Expect(view).To(ContainSubstring("enter confirm · esc cancel"),
				"the hint bar speaks the menu's grammar while it is open")
		})

		It("moves the highlight with ↑/↓, clamping at the menu's edges", func() {
			model, _ := press(composedWidget(), keyRune('q'))

			model, _ = press(model, upKey)
			Expect(highlightedExitOption(model)).To(Equal("Emit & quit"),
				"↑ clamps at the first ramp")

			model, _ = press(model, downKey)
			Expect(highlightedExitOption(model)).To(Equal("Discard & quit"))

			model, _ = press(model, downKey)
			Expect(highlightedExitOption(model)).To(Equal("Cancel"))

			model, _ = press(model, downKey)
			Expect(highlightedExitOption(model)).To(Equal("Cancel"),
				"↓ clamps at the last ramp")

			model, _ = press(model, upKey)
			Expect(highlightedExitOption(model)).To(Equal("Discard & quit"))
		})

		It("emits the Manifest and quits on Emit & quit", func() {
			model, _ := press(composedWidget(), keyRune('q'))

			model, cmd := press(model, enterKey)
			model = emitThroughShell(model, cmd)

			Expect(emittedManifest(model)).To(Equal(widgetManifest),
				"the recorded bytes are the Draft's pure emission — identity keys leading, sparse beneath")
		})

		It("quits without emitting on Discard & quit", func() {
			model, _ := press(composedWidget(), keyRune('q'))

			model, _ = press(model, downKey)
			model, quit := press(model, enterKey)

			Expect(quit).NotTo(BeNil())
			Expect(quit()).To(Equal(tea.QuitMsg{}))
			expectNothingEmitted(model)
		})

		It("keeps composing on Cancel, the Draft intact", func() {
			model, _ := press(composedWidget(), keyRune('q'))

			model, _ = press(model, downKey, downKey)
			model, cmd := press(model, enterKey)

			Expect(cmd).To(BeNil())
			Expect(model.ExitMenuOpen()).To(BeFalse())
			Expect(model.ComposeOpen()).To(BeTrue())
			Expect(draftValue(model, "spec.size")).To(Equal(int64(5)))
			expectNothingEmitted(model)
		})

		It("cancels on Esc exactly like the Cancel ramp", func() {
			model, _ := press(composedWidget(), keyRune('q'))

			model, cmd := press(model, escKey)

			Expect(cmd).To(BeNil())
			Expect(model.ExitMenuOpen()).To(BeFalse())
			Expect(model.ComposeOpen()).To(BeTrue())
			Expect(draftValue(model, "spec.size")).To(Equal(int64(5)))
		})

		It("still discard-quits immediately on Ctrl-c — the escape hatch reaches through the menu", func() {
			model, _ := press(composedWidget(), keyRune('q'))

			model, quit := press(model, ctrlCKey)

			Expect(quit).NotTo(BeNil())
			Expect(quit()).To(Equal(tea.QuitMsg{}))
			expectNothingEmitted(model)
		})
	})

	When("q is pressed in navigate mode over an empty Draft", func() {
		It("quits immediately with nothing emitted — no menu over nothing to lose", func() {
			model := widen(openKind(newShell(), widgetKind()))

			model, quit := press(model, keyRune('q'))

			Expect(quit).NotTo(BeNil())
			Expect(quit()).To(Equal(tea.QuitMsg{}))
			Expect(model.ExitMenuOpen()).To(BeFalse())
			expectNothingEmitted(model)
		})
	})

	When("Ctrl-d emit-&-quits directly from navigate mode", func() {
		It("emits the Draft's Manifest without a menu", func() {
			model := composedWidget()

			model, cmd := press(model, ctrlDKey)
			Expect(model.ExitMenuOpen()).To(BeFalse(), "the EOF idiom skips the menu")
			model = emitThroughShell(model, cmd)

			Expect(emittedManifest(model)).To(Equal(widgetManifest))
		})

		It("emits the identity-only Manifest on an empty Draft — the sparse-emission floor", func() {
			model := widen(openKind(newShell(), widgetKind()))

			model, cmd := press(model, ctrlDKey)
			model = emitThroughShell(model, cmd)

			Expect(emittedManifest(model)).To(Equal(identityManifest),
				"an empty Draft still emits apiVersion and kind — never an empty document")
		})

		It("stays literal in edit mode — the widget owns the key", func() {
			model := openWidget(composedWidget(), "spec.size")

			model, cmd := press(model, ctrlDKey)

			Expect(cmd).To(BeNil(), "Ctrl-d must not emit or quit while a value widget is open")
			Expect(model.Editing()).To(BeTrue())
			expectNothingEmitted(model)
		})
	})

	When("q is pressed inside the compose view's overlays", func() {
		It("types into the field-search filter instead of opening the menu", func() {
			model := composedWidget()

			model, _ = press(model, keyRune('/'))
			model, cmd := press(model, keyRune('q'))

			Expect(cmd).To(BeNil())
			Expect(model.SearchOpen()).To(BeTrue())
			Expect(model.SearchFilter()).To(Equal("q"), "q is a filter character inside a search surface")
			Expect(model.ExitMenuOpen()).To(BeFalse())
		})
	})

	When("the Session quits from the picker", func() {
		It("emits nothing — there is no Draft to emit", func() {
			model := widen(newShell())

			model, quit := press(model, escKey)

			Expect(quit).NotTo(BeNil())
			Expect(quit()).To(Equal(tea.QuitMsg{}))
			expectNothingEmitted(model)
		})
	})

	When("Esc returns to the picker mid-compose", func() {
		It("discards through the confirm without ever emitting", func() {
			model := composedWidget()

			model, _ = press(model, escKey)
			Expect(model.ConfirmingDiscard()).To(BeTrue(),
				"the Esc-to-picker discard confirm survives the exit ramp unchanged")
			model, back := press(model, enterKey)
			Expect(back).NotTo(BeNil())
			model, _ = press(model, back())

			Expect(model.ComposeOpen()).To(BeFalse())
			expectNothingEmitted(model)
		})
	})

	When("the help overlay documents the exit ramp", func() {
		It("names q's three-way menu and Ctrl-d's direct emit", func() {
			model, _ := press(composedWidget(), keyRune('?'))

			view := model.View()
			Expect(view).To(ContainSubstring("Emit & quit / Discard & quit / Cancel"))
			Expect(view).To(ContainSubstring("ctrl+d"))
		})
	})

	When("the emission itself fails", func() {
		// The fixture corpus cannot make a real Draft.Emit fail, so the
		// notice's load-bearing wording pins through the export_test seam:
		// what failed, the emission's own words, and that nothing was lost.
		It("pins the non-fatal emit-failure notice's wording", func() {
			notice := tui.EmitFailureNotice(errors.New("rendering the Manifest: boom"))

			Expect(notice).To(Equal(
				"emitting the Manifest failed: rendering the Manifest: boom — the Draft is intact",
			))
		})
	})
})
