package tui_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/tui"
)

// pressKey drives the Session shell's Update with one synthetic key
// message — the state-first pattern (DESIGN.md — Testing): assert on the
// returned model and command, never on rendered frames.
func pressKey(model tui.Model, key tea.KeyMsg) (tea.Model, tea.Cmd) {
	GinkgoHelper()
	return model.Update(key)
}

var _ = Describe("the Session shell", func() {
	When("the Session's live index is resolved", func() {
		It("renders how many API group versions the cluster serves", func() {
			model := tui.New(42)

			Expect(model.View()).To(ContainSubstring("connected: 42 API group versions"))
		})

		It("starts with no initial command — the index is resolved before launch", func() {
			Expect(tui.New(2).Init()).To(BeNil())
		})
	})

	When("the Draft is empty (M0 has no Draft yet)", func() {
		DescribeTable(
			"the exit grammar quits immediately with no prompt",
			func(key tea.KeyMsg) {
				updated, cmd := pressKey(tui.New(3), key)

				Expect(updated).To(Equal(tui.New(3)),
					"quitting must not mutate the Session shell's state")
				Expect(cmd).NotTo(BeNil())
				Expect(cmd()).To(Equal(tea.QuitMsg{}),
					"the empty-Draft rule: quit immediately, never a prompt")
			},
			Entry("q — the empty-Draft rule",
				tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}),
			Entry("Ctrl-c — the conventional escape hatch",
				tea.KeyMsg{Type: tea.KeyCtrlC}),
		)

		It("stays in the Session on keys outside the exit grammar", func() {
			updated, cmd := pressKey(tui.New(3), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})

			Expect(updated).To(Equal(tui.New(3)))
			Expect(cmd).To(BeNil())
		})
	})
})
