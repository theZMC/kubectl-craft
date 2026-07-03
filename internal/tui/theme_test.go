package tui_test

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/tui"
)

// backgroundAnswered resolves the shell's palette by driving Update with
// the terminal's background answer — the same tea.BackgroundColorMsg the
// program's Init-time query resolves through (ADR-0007: the program's own
// I/O, never environment sniffing), which is what lets specs pin dark and
// light deterministically.
func backgroundAnswered(background string) tui.Theme {
	GinkgoHelper()
	model, _ := press(newShell(), tea.BackgroundColorMsg{Color: lipgloss.Color(background)})
	return tui.ThemeOf(model)
}

// tokenStyles projects the five tokens onto their resolved styles, keyed by
// the token table's names (DESIGN.md — Palette).
func tokenStyles(th tui.Theme) map[string]lipgloss.Style {
	return map[string]lipgloss.Style{
		"NeedsFixing": th.NeedsFixing(),
		"Set":         th.Set(),
		"Ask":         th.Ask(),
		"Structure":   th.Structure(),
		"Meta":        th.Meta(),
	}
}

// expectColoredTokens asserts the four colored tokens resolved to the given
// halves of their pairs, in token-table order: NeedsFixing, Set, Ask,
// Structure.
func expectColoredTokens(th tui.Theme, needsFixing, set, ask, structure string) {
	GinkgoHelper()
	Expect(th.NeedsFixing().GetForeground()).To(Equal(lipgloss.Color(needsFixing)))
	Expect(th.Set().GetForeground()).To(Equal(lipgloss.Color(set)))
	Expect(th.Ask().GetForeground()).To(Equal(lipgloss.Color(ask)))
	Expect(th.Structure().GetForeground()).To(Equal(lipgloss.Color(structure)))
}

var _ = Describe("the theme layer", func() {
	When("the terminal has not answered the background query", func() {
		It("resolves every pair to its dark half — the deterministic dark-first default", func() {
			expectColoredTokens(tui.ThemeOf(newShell()),
				"#e06c75", "#98c379", "#e5c07b", "#61afef")
		})
	})

	When("the queried background answers dark", func() {
		It("keeps every pair on its dark half", func() {
			expectColoredTokens(backgroundAnswered("#1e1e1e"),
				"#e06c75", "#98c379", "#e5c07b", "#61afef")
		})
	})

	When("the queried background answers light", func() {
		It("flips every pair to its light half", func() {
			expectColoredTokens(backgroundAnswered("#ffffff"),
				"#c94f4f", "#4e9a06", "#b58900", "#2472c8")
		})

		It("re-resolves back to dark when a later answer darkens", func() {
			model, _ := press(newShell(),
				tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")},
				tea.BackgroundColorMsg{Color: lipgloss.Color("#000000")})

			expectColoredTokens(tui.ThemeOf(model),
				"#e06c75", "#98c379", "#e5c07b", "#61afef")
		})
	})

	It("renders Meta as neutral faint — no owned hue, so metadata never competes with meaning", func() {
		meta := tui.ThemeOf(newShell()).Meta()

		Expect(meta.GetFaint()).To(BeTrue())
		Expect(meta.GetForeground()).To(Equal(lipgloss.NoColor{}))
		Expect(meta.GetBold()).To(BeFalse())
	})

	It("carries the focused row's bold emphasis on Structure alone", func() {
		theme := tui.ThemeOf(newShell())

		Expect(theme.Structure().GetBold()).To(BeTrue())
		Expect(theme.NeedsFixing().GetBold()).To(BeFalse())
		Expect(theme.Set().GetBold()).To(BeFalse())
		Expect(theme.Ask().GetBold()).To(BeFalse())
	})

	When("the theme is muted", func() {
		It("resolves every token to the Meta treatment", func() {
			muted := tui.ThemeOf(newShell()).Muted()

			for name, style := range tokenStyles(muted) {
				Expect(style.GetFaint()).To(BeTrue(), "%s must dim to the Meta treatment", name)
				Expect(style.GetForeground()).To(Equal(lipgloss.NoColor{}), "%s must carry no hue", name)
				Expect(style.GetBold()).To(BeFalse(), "%s must carry no emphasis", name)
			}
		})
	})

	When("the theme resolves the chrome bar variant", func() {
		It("adds the bar's reverse treatment to every token, meanings intact", func() {
			bar := tui.ThemeOf(newShell()).Bar()

			for name, style := range tokenStyles(bar) {
				Expect(style.GetReverse()).To(BeTrue(), "%s must ride the chrome bar's reverse treatment", name)
			}
			expectColoredTokens(bar, "#e06c75", "#98c379", "#e5c07b", "#61afef")
		})

		It("keeps hint text on the Meta treatment", func() {
			meta := tui.ThemeOf(newShell()).Bar().Meta()

			Expect(meta.GetFaint()).To(BeTrue(), "hints stay Meta on the bar")
			Expect(meta.GetForeground()).To(Equal(lipgloss.NoColor{}))
		})

		It("renders the bar's own surface as attribute-only chrome — no hue, so no sixth meaning", func() {
			chrome := tui.ThemeOf(newShell()).Bar().Chrome()

			Expect(chrome.GetReverse()).To(BeTrue())
			Expect(chrome.GetForeground()).To(Equal(lipgloss.NoColor{}), "chrome owns no hue (ADR-0007)")
			Expect(chrome.GetFaint()).To(BeFalse(), "the bar's surface never dims what rides it")
			Expect(chrome.GetBold()).To(BeFalse())
		})

		It("carries no chrome off the bar", func() {
			Expect(tui.ThemeOf(newShell()).Chrome().GetReverse()).To(BeFalse(),
				"chrome exists only where the bar does")
		})
	})

	When("the queried background answers while a view is open", func() {
		It("re-paints the open compose view without changing a single glyph", func() {
			dark := composeDeployment()
			light, _ := press(dark, tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})

			Expect(render(light)).To(Equal(render(dark)),
				"the visible surface is color only — glyphs, layout, and wording stay put")
			Expect(light.View().Content).NotTo(Equal(dark.View().Content),
				"the open view re-resolves onto the light halves")
		})

		It("re-paints the Kind picker without changing a single glyph", func() {
			dark := newShell()
			light, _ := press(dark, tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})

			Expect(render(light)).To(Equal(render(dark)),
				"the visible surface is color only — glyphs, layout, and wording stay put")
			Expect(light.View().Content).NotTo(Equal(dark.View().Content),
				"the open view re-resolves onto the light halves")
		})
	})
})
