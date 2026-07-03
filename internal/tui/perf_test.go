package tui_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/internal/tui"
	"github.com/thezmc/kubectl-craft/test/giantcrd"
)

// The compose view's perf budgets over the giant fixture, mirroring
// internal/schema's: regression tripwires with 25×+ headroom over what an
// Apple M4 measures cold (compose open ~37ms, `/` first open ~10ms, a
// search keystroke ~3ms steady-state, a confirmed mutation at depth
// ~0.5ms, the whole version switch ~23ms — the keystroke benchmark below
// is the precise instrument), so shared CI runners pass under load while
// a complexity-class regression over 10k+ nodes still trips.
// Label("perf") keeps them addressable: they run in the fast loop (the
// whole set costs about a second), and `ginkgo --label-filter='!perf'`
// excludes them if a runner proves noisy.
const (
	// composeOpenBudget bounds opening the compose view on the giant:
	// parsing the fetched group document, growing the field tree, the
	// root's first expansion window, and the first rendered frame.
	composeOpenBudget = time.Second

	// searchOpenBudget bounds the `/` overlay's first open: the one-time
	// FieldPaths() enumeration over 10k+ candidates plus the first
	// rendered frame of the unfiltered list.
	searchOpenBudget = 500 * time.Millisecond

	// searchKeystrokeBudget bounds one search keystroke against the
	// built index — a filter re-rank over 10k+ candidates plus the
	// rendered frame. 16ms is the subjective-instant bar; the budget is
	// CI headroom, and the benchmark is where the real number lives.
	searchKeystrokeBudget = 250 * time.Millisecond

	// mutationBudget bounds a confirmed Draft mutation at depth: the
	// Set, the completeness recompute, the row rebuild, and the frame.
	mutationBudget = 250 * time.Millisecond

	// versionSwitchBudget bounds the whole version switch across the
	// giant: the carry-over, the drop confirm, and the rebuilt compose
	// view landing back on the focused Field Path.
	versionSwitchBudget = time.Second
)

// giantKind is the giant Kind's picker row at one served version; v1 — the
// 10k-node giant — is the Preferred Version.
func giantKind(version string) data.Kind {
	return data.Kind{
		GVK:              schema.GroupVersionKind{Group: giantcrd.Group, Version: version, Kind: giantcrd.Kind},
		GroupVersionPath: giantcrd.GroupVersionPath(version),
		Plural:           "giants",
		Namespaced:       true,
		Preferred:        version == "v1",
	}
}

// readGiantFixture reads one checked-in giant document without ginkgo, so
// the benchmark can share the shell setup with the specs.
func readGiantFixture(version string) []byte {
	raw, err := os.ReadFile(filepath.Join("..", "schema", "testdata", giantcrd.FixtureName(version)))
	if err != nil {
		panic(err)
	}
	return raw
}

// newGiantShell builds the Session shell over the giant Kind's two served
// versions, the stub Fetcher serving the checked-in giant documents.
func newGiantShell() tui.Model {
	fetcher := &stubFetcher{documents: map[string][]byte{
		giantcrd.GroupVersionPath("v1"): readGiantFixture("v1"),
		giantcrd.GroupVersionPath("v2"): readGiantFixture("v2"),
	}}
	index := []data.GroupVersion{
		{Path: giantcrd.GroupVersionPath("v1"), ContentHash: "GIANT1HASH"},
		{Path: giantcrd.GroupVersionPath("v2"), ContentHash: "GIANT2HASH"},
	}
	return tui.New(context.Background(), []data.Kind{giantKind("v1"), giantKind("v2")}, fetcher, index,
		&stubValidator{outcome: data.Clean{}}, "", nil)
}

// landOnGiantPath jumps to one schema-level Field Path through the `/`
// search overlay, typing the spelled path as the filter — its contiguous
// match ranks first — and selecting it under the landing rule.
func landOnGiantPath(model tui.Model, fieldPath string) tui.Model {
	GinkgoHelper()
	model, _ = press(model, keyRune('/'))
	Expect(model.SearchOpen()).To(BeTrue())
	model = typeFilter(model, fieldPath)
	match, ok := model.HighlightedSearchMatch()
	Expect(ok).To(BeTrue())
	Expect(match.FieldPath).To(Equal(fieldPath))
	model, _ = press(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	Expect(model.FocusedFieldPath()).To(Equal(fieldPath))
	return model
}

// fillFocusedLeaf opens the focused leaf's value widget, types the value,
// and confirms it into the Draft.
func fillFocusedLeaf(model tui.Model, value string) tui.Model {
	GinkgoHelper()
	model, _ = press(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	Expect(model.Editing()).To(BeTrue())
	model = typeFilter(model, value)
	model, _ = press(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	Expect(model.Editing()).To(BeFalse(), "the typed value must confirm cleanly")
	return model
}

// pressAndRender drives one key press and renders its frame — the whole
// latency one keystroke costs the Session — returning the elapsed time.
func pressAndRender(model tui.Model, msg tea.Msg) (tui.Model, time.Duration) {
	start := time.Now()
	model, _ = press(model, msg)
	_ = model.View()
	return model, time.Since(start)
}

var _ = Describe("the huge-CRD perf pass over the compose view", Label("perf"), func() {
	When("the compose view opens on the giant", func() {
		It("parses the group document, grows the tree, and renders the first frame inside the budget", func() {
			model := newGiantShell()

			start := time.Now()
			model = openKind(model, giantKind("v1"))
			_ = model.View()
			Expect(time.Since(start)).To(BeNumerically("<", composeOpenBudget),
				"opening the compose view on the giant blew its budget")
		})
	})

	When("the `/` field search opens over the giant for the first time", func() {
		It("enumerates all 10k+ candidates and renders inside the budget", func() {
			model := openKind(newGiantShell(), giantKind("v1"))

			var opened time.Duration
			model, opened = pressAndRender(model, keyRune('/'))
			Expect(model.SearchOpen()).To(BeTrue())
			Expect(opened).To(BeNumerically("<", searchOpenBudget),
				"the search overlay's first open over the giant blew its budget")
			Expect(len(model.SearchMatches())).To(BeNumerically(">=", 10_000))
		})
	})

	When("search keystrokes filter the giant's built index", func() {
		It("re-ranks and renders every keystroke inside the budget", func() {
			model := openKind(newGiantShell(), giantKind("v1"))
			model, _ = press(model, keyRune('/'))

			worst := time.Duration(0)
			for _, r := range "sector07.unit03.f0" {
				var took time.Duration
				model, took = pressAndRender(model, keyRune(r))
				worst = max(worst, took)
			}
			Expect(model.SearchMatches()).NotTo(BeEmpty())
			Expect(worst).To(BeNumerically("<", searchKeystrokeBudget),
				"a search keystroke against the giant's index blew its budget")
		})
	})

	When("a value confirms into the Draft deep in the giant", func() {
		It("sets, recomputes completeness, rebuilds the rows, and renders inside the budget", func() {
			model := openKind(newGiantShell(), giantKind("v1"))
			model = landOnGiantPath(model, "spec.grid.sector07.unit03.f04")
			model, _ = press(model, tea.KeyPressMsg{Code: tea.KeyEnter})
			Expect(model.Editing()).To(BeTrue())
			model = typeFilter(model, "fast")

			var confirmed time.Duration
			model, confirmed = pressAndRender(model, tea.KeyPressMsg{Code: tea.KeyEnter})
			Expect(confirmed).To(BeNumerically("<", mutationBudget),
				"a confirmed mutation at depth blew its budget")

			value, filled := model.DraftValueAt("spec.grid.sector07.unit03.f04")
			Expect(filled).To(BeTrue())
			Expect(value).To(Equal("fast"))
		})
	})

	When("the giant switches versions with a populated Draft", func() {
		It("carries over, reports the drops, and rebuilds the view inside the budget", func() {
			model := openKind(newGiantShell(), giantKind("v1"))
			// A kept leaf, a dropped sector's leaf, and the deep anchor
			// v2 renames: the carry-over has work in both directions.
			model = fillFocusedLeaf(landOnGiantPath(model, "spec.grid.sector02.unit00.f00"), "kept")
			model = fillFocusedLeaf(landOnGiantPath(model, "spec.grid.sector07.unit00.f00"), "dropped")
			model = fillFocusedLeaf(landOnGiantPath(model, giantcrd.DeepSpinePath()), "bottom")

			start := time.Now()
			model, _ = press(model, keyRune('V'))
			Expect(model.VersionListOpen()).To(BeTrue())
			model, cmd := press(model, tea.KeyPressMsg{Code: tea.KeyEnter})
			Expect(cmd).NotTo(BeNil(), "selecting the version must request the switch")
			model, cmd = press(model, cmd())
			Expect(cmd).NotTo(BeNil(), "the target group document must fetch lazily")
			model, _ = press(model, cmd())

			Expect(model.ConfirmingVersionSwitch()).To(BeTrue(),
				"dropping half the grid must open the drop confirm")
			Expect(model.DropReport()).NotTo(BeEmpty())

			model, cmd = press(model, tea.KeyPressMsg{Code: tea.KeyEnter})
			Expect(cmd).NotTo(BeNil(), "accepting the drop report must commit the switch")
			model, _ = press(model, cmd())
			_ = model.View()
			Expect(time.Since(start)).To(BeNumerically("<", versionSwitchBudget),
				"the version switch across the giant blew its budget")

			Expect(model.Breadcrumb()).To(ContainSubstring("giant.example.com/v2"))
			value, filled := model.DraftValueAt("spec.grid.sector02.unit00.f00")
			Expect(filled).To(BeTrue(), "a value the target version still spells must carry")
			Expect(value).To(Equal("kept"))
			_, filled = model.DraftValueAt("spec.grid.sector07.unit00.f00")
			Expect(filled).To(BeFalse(), "a value in a dropped sector must not carry")
		})
	})
})

var _ = Describe("the compose view's render window over the giant", func() {
	// The render path must only materialize visible rows: the tree pane
	// slices the flattened row list to the viewport before any row
	// renders, so a giant with thousands of expandable rows costs one
	// window per frame. This pins that contract behaviorally.
	When("the tree scrolls a window over a deep expansion", func() {
		It("renders only the rows the viewport shows", func() {
			model := openKind(newGiantShell(), giantKind("v1"))
			model, _ = press(model, tea.WindowSizeMsg{Width: 120, Height: 24})
			model = landOnGiantPath(model, giantcrd.DeepSpinePath())

			view := render(model)
			Expect(strings.Count(view, "\n")).To(BeNumerically("<=", 24),
				"a 24-line terminal must never receive more than 24 lines")
			Expect(view).To(ContainSubstring("anchor"),
				"the landing keeps the focused deep row inside the window")

			model, _ = press(model, keyRune('g'))
			view = render(model)
			Expect(view).To(ContainSubstring("spine"),
				"the top of the tree scrolls back into the window")
			Expect(view).NotTo(ContainSubstring("anchor"),
				"a row scrolled out of the window must not render")
		})
	})
})

// pressBench drives the shell's Update outside a running spec — the ginkgo
// press helper is off-limits to benchmarks — dropping the commands.
func pressBench(model tui.Model, msgs ...tea.Msg) tui.Model {
	for _, msg := range msgs {
		updated, _ := model.Update(msg)
		model = updated.(tui.Model)
	}
	return model
}

// BenchmarkGiantSearchKeystroke is the precise keypress-latency instrument
// behind searchKeystrokeBudget — and the recorded stand-in for the manual
// keypress smoke: one steady-state search keystroke (filter re-rank over
// the giant's 10k+ candidates plus the rendered frame) per iteration. Run
// it with `go test -bench=Keystroke -run='^$' ./internal/tui`.
func BenchmarkGiantSearchKeystroke(b *testing.B) {
	model := newGiantShell()
	updated, fetch := model.Update(tui.KindSelectedMsg{Kind: giantKind("v1")})
	model = updated.(tui.Model)
	if fetch == nil {
		b.Fatal("an unparsed group must fetch lazily, as a command")
	}
	model = pressBench(model, fetch(), tea.WindowSizeMsg{Width: 120, Height: 40}, keyRune('/'))
	if !model.SearchOpen() {
		b.Fatal("the `/` search overlay must open over the giant")
	}
	for _, r := range "sector07.unit03.f" {
		model = pressBench(model, keyRune(r))
	}

	keys := []tea.Msg{keyRune('0'), tea.KeyPressMsg{Code: tea.KeyBackspace}}
	b.ResetTimer()
	for iteration := range b.N {
		model = pressBench(model, keys[iteration%2])
		_ = model.View()
	}
}
