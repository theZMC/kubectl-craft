package tui_test

import (
	"context"
	"image/color"
	"io"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/charmbracelet/x/vt"
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

// goldenSession pairs the running golden program with a virtual terminal
// fed by everything the program has written so far. Bubble Tea v2's diff
// renderer writes partial-line updates — cursor jumps can split any awaited
// substring across writes — so the raw byte stream is not scannable: the
// quiescence gates read the emulated screen instead, where every fragment
// has landed at its real position.
type goldenSession struct {
	*teatest.TestModel

	// output is the one incremental reader over the program's output;
	// screen accumulates everything it has ever written.
	output io.Reader
	screen *vt.Emulator
}

// newGoldenSession wraps a started teatest program in the virtual terminal
// its quiescence gates read. The emulator answers terminal queries — the
// shell's Init-time background query among them — into its response pipe,
// and those synchronous writes block the parse until something reads them:
// a drain goroutine discards the responses (the program under test takes
// its input from teatest, never from here), and the spec's cleanup closes
// the pipe so the drain ends with the spec.
func newGoldenSession(session *teatest.TestModel) *goldenSession {
	screen := vt.NewEmulator(goldenWidth, goldenHeight)
	go func() { _, _ = io.Copy(io.Discard, screen) }()
	DeferCleanup(screen.Close)

	return &goldenSession{
		TestModel: session,
		output:    session.Output(),
		screen:    screen,
	}
}

// screenText drains the program's newly written bytes into the virtual
// terminal and returns the visible screen as plain text.
func (s *goldenSession) screenText() string {
	_, _ = io.Copy(s.screen, s.output)
	return s.screen.String()
}

// startGoldenSession runs the Session shell as a real tea.Program on
// teatest's in-memory terminal at the pinned frame size, over the same
// fixture corpus the state-first specs use. teatest builds the program on
// in-memory buffers itself, so the openTTY seam never opens. Deterministic
// frames need a stable color profile — the program otherwise sniffs the
// environment, so CI and a local terminal could pin different bytes; NoTTY
// is lipgloss v2's spelling of v1's suite-wide Ascii profile, stripping
// every style from the program's output before any sentinel is scanned.
func startGoldenSession(link *tui.DeepLink) *goldenSession {
	GinkgoHelper()
	shell := tui.New(context.Background(), browsableKinds(), corpusFetcher(), corpusIndex(),
		&stubValidator{outcome: data.Clean{}}, "", link)
	return newGoldenSession(teatest.NewTestModel(GinkgoTB(), shell,
		teatest.WithInitialTermSize(goldenWidth, goldenHeight),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.NoTTY))))
}

// awaitRender blocks until the emulated screen shows the sentinel: the
// quiescence gate that drives past loading states — a lazy group-document
// fetch resolves off the Update loop, so keys sent before its landing
// would fall into the loading state's grammar instead of the view the
// frame pins. The gate polls the screen, never the raw stream (an async
// wait, so it goes through Eventually — the house rule).
func awaitRender(session *goldenSession, sentinel string) {
	GinkgoHelper()
	Eventually(session.screenText).WithTimeout(frameTimeout).
		Should(ContainSubstring(sentinel), "the frame never quiesced on the sentinel")
}

// finalFrame ends the Session and renders the final frame from the final
// model: the quiesced view at the pinned size, free of the terminal's
// cursor-movement noise. Quit ends the program without another Update, so
// the frame is exactly the state the spec drove to. lipgloss v2 styles
// always emit ANSI — downsampling moved to the program's output layer —
// so the harness strips the styling here, exactly what the v1 suite's
// Ascii profile did before any spec rendered anything.
func finalFrame(session *goldenSession) string {
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
func startColoredSession(background color.Color) *goldenSession {
	GinkgoHelper()
	invalid := data.Invalid{Status: statusWithCause("spec.nickname",
		"Invalid value: \"ratchet\": nickname is already taken")}
	shell := tui.New(context.Background(), browsableKinds(), corpusFetcher(), corpusIndex(),
		&stubValidator{outcome: invalid}, "", &tui.DeepLink{Kind: kindNamed("Gadget", "v1")})
	session := teatest.NewTestModel(GinkgoTB(), shell,
		teatest.WithInitialTermSize(goldenWidth, goldenHeight),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.TrueColor)))
	session.Send(tea.BackgroundColorMsg{Color: background})
	return newGoldenSession(session)
}

// startColoredOverlaySession runs the Session shell like startColoredSession
// but with a clean Validator: the colored overlay goldens pin a floating
// overlay over the Muted() panes, not a Validate outcome, so the frame stays
// about the compositor — the Structure box border and title over the tree's
// faint Meta backdrop, once per palette half.
func startColoredOverlaySession(background color.Color) *goldenSession {
	GinkgoHelper()
	shell := tui.New(context.Background(), browsableKinds(), corpusFetcher(), corpusIndex(),
		&stubValidator{outcome: data.Clean{}}, "", &tui.DeepLink{Kind: kindNamed("Gadget", "v1")})
	session := teatest.NewTestModel(GinkgoTB(), shell,
		teatest.WithInitialTermSize(goldenWidth, goldenHeight),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.TrueColor)))
	session.Send(tea.BackgroundColorMsg{Color: background})
	return newGoldenSession(session)
}

// composeExitMenuOverlay drives the colored Session to the exit menu floating
// over the muted compose view: a value confirms into the Draft so `q` opens
// the three-way menu, and the menu composites as a Structure-bordered box
// over the panes rendered through the theme's Muted() variant. The awaits
// gate only the async fetch boundary; every key in between transitions
// synchronously in Update.
func composeExitMenuOverlay(session *goldenSession) {
	GinkgoHelper()
	awaitRender(session, "apiVersion") // the deep link's fetch has landed on the compose view

	searchLand(session, "nickname")
	confirmFocusedLeaf(session, "ratchet")
	session.Send(keyRune('q'))
	awaitRender(session, "Emit & quit") // the exit menu has floated open
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
func composeMixedState(session *goldenSession) {
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
func finalColoredFrame(session *goldenSession) string {
	GinkgoHelper()
	Expect(session.Quit()).To(Succeed())
	shell, isShell := session.FinalModel(GinkgoTB(), teatest.WithFinalTimeout(frameTimeout)).(tui.Model)
	Expect(isShell).To(BeTrue(), "the Session shell must be the program's final model")
	return shell.View().Content
}

// searchLand lands the focus on a Field Path through the / field-search
// overlay — the deterministic way to reach a node without counting rows.
func searchLand(session *goldenSession, query string) {
	session.Send(keyRune('/'))
	session.Type(query)
	session.Send(enterKey)
}

// confirmFocusedLeaf opens the focused leaf's value widget, types the
// value, and confirms it into the Draft.
func confirmFocusedLeaf(session *goldenSession, value string) {
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

var _ = Describe("the floating overlay golden frames", func() {
	// The floating-overlay tripwire (#77): one open overlay composited over
	// the Muted() panes, pinned with its ANSI styling at truecolor, once per
	// palette half. The ASCII exit_menu golden proves the box's glyphs and
	// layout; these prove the paint — the Structure border and title over the
	// tree's faint Meta backdrop — so a background that fails to mute, or a
	// border that loses Structure, fails byte-for-byte.
	When("the exit menu floats over the muted compose view on dark", func() {
		It("pins the Structure-bordered box over the faint panes on the dark palette", func() {
			session := startColoredOverlaySession(lipgloss.Color("#1e1e1e"))
			composeExitMenuOverlay(session)

			Expect(finalColoredFrame(session)).To(
				matchers.MatchGoldenFrame("testdata/golden/overlay_exit_truecolor_dark.golden"),
			)
		})
	})

	When("the exit menu floats over the muted compose view on light", func() {
		It("pins the Structure-bordered box over the faint panes on the light palette", func() {
			session := startColoredOverlaySession(lipgloss.Color("#ffffff"))
			composeExitMenuOverlay(session)

			Expect(finalColoredFrame(session)).To(
				matchers.MatchGoldenFrame("testdata/golden/overlay_exit_truecolor_light.golden"),
			)
		})
	})
})
