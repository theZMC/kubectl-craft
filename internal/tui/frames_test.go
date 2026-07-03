package tui_test

import (
	"bytes"
	"context"
	"image/color"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"
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

// startGoldenSession runs the Session shell as a real tea.Program on
// teatest's in-memory terminal at the pinned frame size, over the same
// fixture corpus the state-first specs use. teatest builds the program on
// in-memory buffers itself, so the openTTY seam never opens. Deterministic
// frames need a stable color profile — the program otherwise sniffs the
// environment, so CI and a local terminal could pin different bytes; NoTTY
// is lipgloss v2's spelling of v1's suite-wide Ascii profile, stripping
// every style from the program's output before any sentinel is scanned.
func startGoldenSession(link *tui.DeepLink) *teatest.TestModel {
	GinkgoHelper()
	shell := tui.New(context.Background(), browsableKinds(), corpusFetcher(), corpusIndex(),
		&stubValidator{outcome: data.Clean{}}, "", link)
	return teatest.NewTestModel(GinkgoTB(), shell,
		teatest.WithInitialTermSize(goldenWidth, goldenHeight),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.NoTTY)))
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
// the frame is exactly the state the spec drove to. lipgloss v2 styles
// always emit ANSI — downsampling moved to the program's output layer —
// so the harness strips the styling here, exactly what the v1 suite's
// Ascii profile did before any spec rendered anything.
func finalFrame(session *teatest.TestModel) string {
	GinkgoHelper()
	Expect(session.Quit()).To(Succeed())
	shell, isShell := session.FinalModel(GinkgoTB(), teatest.WithFinalTimeout(frameTimeout)).(tui.Model)
	Expect(isShell).To(BeTrue(), "the Session shell must be the program's final model")
	return ansi.Strip(shell.View().Content)
}

// startColoredSession runs the Session shell exactly like startGoldenSession
// but keeps the styling: the color profile pins to TrueColor and the queried
// terminal background is answered by injecting the given color as a
// tea.BackgroundColorMsg — the same message a real terminal's answer arrives
// as (the ThemeOf seam's discipline), so dark and light pin deterministic
// bytes. The Validator answers Invalid with one finding on spec.nickname, so
// the mixed-state frame carries a mapped Validate marker.
func startColoredSession(background color.Color) *teatest.TestModel {
	GinkgoHelper()
	invalid := data.Invalid{Status: statusWithCause("spec.nickname",
		"Invalid value: \"ratchet\": nickname is already taken")}
	shell := tui.New(context.Background(), browsableKinds(), corpusFetcher(), corpusIndex(),
		&stubValidator{outcome: invalid}, "", &tui.DeepLink{Kind: kindNamed("Gadget", "v1")})
	session := teatest.NewTestModel(GinkgoTB(), shell,
		teatest.WithInitialTermSize(goldenWidth, goldenHeight),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.TrueColor)))
	session.Send(tea.BackgroundColorMsg{Color: background})
	return session
}

// composeMixedState drives the colored Session to the one mixed-state frame
// the colored goldens pin: a set value (Set), missing required markers
// (NeedsFixing), a mapped Validate finding — stale, still NeedsFixing —
// the required-to-Validate gate flag (Ask), and a pending unset confirm
// (Ask) all visible together, under the Structure breadcrumb and focused
// row with the Meta default placeholders alongside. A notice is
// deliberately not in the frame: it shares the footer line the pending
// confirm pins, and it renders unpainted by design. The awaits gate only
// the async boundaries — the lazy fetch and the dry-run flight; every key
// in between transitions synchronously in Update.
func composeMixedState(session *teatest.TestModel) {
	GinkgoHelper()
	awaitRender(session, "apiVersion") // the deep link's fetch has landed on the compose view

	searchLand(session, "nickname")
	confirmFocusedLeaf(session, "ratchet")

	// v runs the required-to-Validate gate first: Gadget is namespaced and
	// the Session resolves no default namespace, so the gate prompts for
	// metadata.name and then metadata.namespace before the dry-run flies.
	session.Send(keyRune('v'))
	session.Type("demo")
	session.Send(enterKey)
	session.Type("default")
	session.Send(enterKey)
	awaitRender(session, "Validate finding") // the Invalid outcome has landed

	// Unsetting metadata.namespace — its own value, no confirm — brings the
	// Ask-colored gate flag back to the status line and marks the finding
	// stale: both of the status line's prompt-side tokens in one frame.
	searchLand(session, "metadata.namespace")
	session.Send(keyRune('d'))

	// h steps from the nickname leaf to its parent, and d over spec — one
	// filled value beneath it — opens the destructive confirm: the pending
	// prompt the frame pins.
	searchLand(session, "nickname")
	session.Send(keyRune('h'))
	session.Send(keyRune('d'))
	awaitRender(session, "discard 1 value under spec")
}

// finalColoredFrame ends the Session like finalFrame but keeps the ANSI
// styling: the colored goldens pin the escape sequences themselves, so a
// wrong token mapping — a set value rendering in the error color — fails
// mechanically instead of shipping silently.
func finalColoredFrame(session *teatest.TestModel) string {
	GinkgoHelper()
	Expect(session.Quit()).To(Succeed())
	shell, isShell := session.FinalModel(GinkgoTB(), teatest.WithFinalTimeout(frameTimeout)).(tui.Model)
	Expect(isShell).To(BeTrue(), "the Session shell must be the program's final model")
	return shell.View().Content
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

var _ = Describe("the colored golden frames", func() {
	// The color-regression tripwire (ADR-0007): one mixed-state compose
	// frame pinned with its ANSI styling at truecolor, once per palette
	// half. The ASCII goldens prove the glyphs; these prove the token
	// mapping — every semantic accent in one frame, so a wrong mapping
	// fails byte-for-byte.
	When("the queried terminal background answers dark", func() {
		It("pins the mixed-state compose frame on the dark palette halves", func() {
			session := startColoredSession(lipgloss.Color("#1e1e1e"))
			composeMixedState(session)

			Expect(finalColoredFrame(session)).To(
				matchers.MatchGoldenFrame("testdata/golden/compose_mixed_truecolor_dark.golden"),
			)
		})
	})

	When("the queried terminal background answers light", func() {
		It("pins the mixed-state compose frame on the light palette halves", func() {
			session := startColoredSession(lipgloss.Color("#ffffff"))
			composeMixedState(session)

			Expect(finalColoredFrame(session)).To(
				matchers.MatchGoldenFrame("testdata/golden/compose_mixed_truecolor_light.golden"),
			)
		})
	})
})
