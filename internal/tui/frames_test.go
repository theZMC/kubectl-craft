package tui_test

import (
	"bytes"
	"context"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/internal/matchers"
	"github.com/thezmc/kubectl-craft/internal/tui"
)

// The golden frames pin the Session shell's signature surfaces as rendered
// frames, driven through a real tea.Program on teatest's in-memory
// terminal — no PTY, so they run in the fast loop. The state-first specs
// assert semantics; these catch what those can't see: layout drift, style
// bleed, truncation, marker glyph collisions. Regenerate deliberately with
// KUBECTL_CRAFT_UPDATE_GOLDEN=1 and eyeball the diff (CONTRIBUTING.md —
// Golden frames).
const (
	// goldenWidth × goldenHeight is the pinned terminal size. 100×30 fits
	// the corpus Kinds' field trees and both compose panes side by side
	// without truncation, stays within an ordinary terminal window, and —
	// deterministic frames need fixed dims — every golden Session gets
	// exactly this size, CI and local alike.
	goldenWidth  = 100
	goldenHeight = 30

	// frameTimeout bounds every wait on the real program's rendering;
	// teatest's one-second default is too tight for a busy CI runner.
	frameTimeout = 10 * time.Second
)

var _ = BeforeSuite(func() {
	// Deterministic frames also need a stable color profile: lipgloss
	// otherwise sniffs the environment at first render, so CI and a local
	// terminal could pin different bytes. Ascii strips every style to
	// plain text before any spec renders anything.
	lipgloss.SetColorProfile(termenv.Ascii)
})

// startGoldenSession runs the Session shell as a real tea.Program on
// teatest's in-memory terminal at the pinned frame size, over the same
// fixture corpus the state-first specs use. teatest builds the program on
// in-memory buffers itself, so the openTTY seam never opens.
func startGoldenSession(link *tui.DeepLink) *teatest.TestModel {
	GinkgoHelper()
	shell := tui.New(context.Background(), browsableKinds(), corpusFetcher(), corpusIndex(),
		&stubValidator{outcome: data.Clean{}}, "", link)
	return teatest.NewTestModel(GinkgoTB(), shell, teatest.WithInitialTermSize(goldenWidth, goldenHeight))
}

// awaitRender blocks until the program's output carries the sentinel: the
// quiescence gate that drives past loading states — a lazy group-document
// fetch resolves off the Update loop, so keys sent before its landing
// would fall into the loading state's grammar instead of the view the
// frame pins.
func awaitRender(session *teatest.TestModel, sentinel string) {
	GinkgoHelper()
	teatest.WaitFor(GinkgoTB(), session.Output(), func(rendered []byte) bool {
		return bytes.Contains(rendered, []byte(sentinel))
	}, teatest.WithDuration(frameTimeout))
}

// finalFrame ends the Session and renders the final frame from the final
// model: the quiesced view at the pinned size, free of the terminal's
// cursor-movement noise. Quit ends the program without another Update, so
// the frame is exactly the state the spec drove to.
func finalFrame(session *teatest.TestModel) string {
	GinkgoHelper()
	Expect(session.Quit()).To(Succeed())
	shell, isShell := session.FinalModel(GinkgoTB(), teatest.WithFinalTimeout(frameTimeout)).(tui.Model)
	Expect(isShell).To(BeTrue(), "the Session shell must be the program's final model")
	return shell.View()
}

// searchLand lands the focus on a Field Path through the / field-search
// overlay — the deterministic way to reach a node without counting rows.
func searchLand(session *teatest.TestModel, query string) {
	session.Send(keyRune('/'))
	session.Type(query)
	session.Send(enterKey)
}

// confirmFocusedLeaf opens the focused leaf's value widget, types the
// value, and confirms it into the Draft.
func confirmFocusedLeaf(session *teatest.TestModel, value string) {
	session.Send(enterKey)
	session.Type(value)
	session.Send(enterKey)
}

var _ = Describe("the golden frames", func() {
	When("the Session opens on the Kind picker", func() {
		It("pins the browsable Kind list with the first row highlighted", func() {
			session := startGoldenSession(nil)
			awaitRender(session, "Gadget")

			Expect(finalFrame(session)).To(matchers.MatchGoldenFrame("testdata/golden/picker.golden"))
		})

		It("pins the type-to-filter narrowing with a filter typed", func() {
			session := startGoldenSession(nil)
			session.Type("hpa")
			awaitRender(session, "> hpa")

			Expect(finalFrame(session)).To(matchers.MatchGoldenFrame("testdata/golden/picker_filtered.golden"))
		})
	})

	When("the compose view holds a mix of Draft state", func() {
		It("pins a set value, dimmed placeholders, required markers, and the status line", func() {
			session := startGoldenSession(nil)
			session.Type("gz")
			session.Send(enterKey)
			awaitRender(session, "apiVersion") // the field tree's first row: the lazy fetch has landed

			searchLand(session, "nickname")
			confirmFocusedLeaf(session, "ratchet")
			awaitRender(session, "nickname: ratchet")

			Expect(finalFrame(session)).To(matchers.MatchGoldenFrame("testdata/golden/compose_gadget.golden"))
		})
	})

	When("q opens the exit menu over a non-empty Draft", func() {
		It("pins the three-way menu over the composed Widget", func() {
			session := startGoldenSession(&tui.DeepLink{Kind: widgetKind()})
			awaitRender(session, "apiVersion") // the deep link's fetch has landed on the compose view

			searchLand(session, "size")
			confirmFocusedLeaf(session, "5")
			awaitRender(session, "size: 5")
			session.Send(keyRune('q'))
			awaitRender(session, "Emit & quit")

			Expect(finalFrame(session)).To(matchers.MatchGoldenFrame("testdata/golden/exit_menu.golden"))
		})
	})
})
