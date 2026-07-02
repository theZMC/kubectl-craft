package schema_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/schema"
	"github.com/thezmc/kubectl-craft/test/giantcrd"
)

// The perf budgets are regression tripwires, not marketing. An Apple M4
// measures the giant cold at parse ~14ms, root window ~5µs, full Field
// Path walk ~4ms, carry-over ~0.3ms (the benchmarks below are the precise
// instrument) — every budget sits 50×+ above that, because shared CI
// runners under load are slow and noisy, while a hot path regressing to a
// different complexity class over 10k+ nodes still blows through. The
// specs carry Label("perf"): they run in the fast loop by design (each
// costs milliseconds), and the label is the escape hatch
// (`ginkgo --label-filter='!perf'`) if a runner ever proves too noisy.
const (
	// parseBudget bounds ParseDocument + root resolution over the giant:
	// the one-time cost of opening a Kind from a fetched group document
	// (measured ~14ms).
	parseBudget = time.Second

	// expansionBudget bounds the compose view's first render window: the
	// root's children plus one level of lookahead — what must resolve
	// before the first frame (measured ~5µs; the laziness contract keeps
	// it independent of total tree size).
	expansionBudget = 100 * time.Millisecond

	// fieldPathsBudget bounds the full FieldPaths() walk — the `/` field
	// search's candidate enumeration, paid once per compose view
	// (measured ~4ms).
	fieldPathsBudget = 500 * time.Millisecond

	// carryOverBudget bounds a version switch's carry-over of a
	// populated Draft across the giant's field trees (measured ~0.3ms
	// for a 500-value Draft).
	carryOverBudget = 500 * time.Millisecond
)

// giantFixtureBytes reads one checked-in giant document for the benchmarks.
func giantFixtureBytes(tb testing.TB, version string) []byte {
	tb.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", giantcrd.FixtureName(version)))
	if err != nil {
		tb.Fatal(err)
	}
	return raw
}

// populateGiantDraft fills a Draft the way a long composing session would:
// one leaf per unit across the whole grid (500 values), the spine's deepest
// anchor, and a few instantiated chain items — the Draft a version switch
// then carries across the giant.
func populateGiantDraft(draft *schema.Draft) error {
	for sector := range 10 {
		for unit := range 50 {
			path := fmt.Sprintf("spec.grid.sector%02d.unit%02d.f00", sector, unit)
			if err := draft.Set(path, "filled"); err != nil {
				return fmt.Errorf("filling %s: %w", path, err)
			}
		}
	}
	if err := draft.Set(giantcrd.DeepSpinePath(), "bottom"); err != nil {
		return fmt.Errorf("filling the spine's anchor: %w", err)
	}
	for index := range 5 {
		if _, err := draft.AppendItem("spec.chain"); err != nil {
			return fmt.Errorf("appending a chain item: %w", err)
		}
		if err := draft.Set(fmt.Sprintf("spec.chain[%d].name", index), "link"); err != nil {
			return fmt.Errorf("naming chain item %d: %w", index, err)
		}
	}
	return nil
}

// expandRootWindow resolves the compose view's opening window: the root's
// children and one level of lookahead, exactly what newCompose materializes
// before the first frame.
func expandRootWindow(root *schema.Node) error {
	children, err := root.Children()
	if err != nil {
		return fmt.Errorf("expanding the giant's root: %w", err)
	}
	for _, child := range children {
		if _, err := child.Children(); err != nil {
			return fmt.Errorf("looking ahead past %s: %w", child.FieldPath(), err)
		}
	}
	return nil
}

var _ = Describe("the huge-CRD perf pass over the compose core", Label("perf"), func() {
	When("the giant group document is parsed and its Kind root resolved", func() {
		It("stays inside the parse budget", func() {
			raw, err := os.ReadFile(filepath.Join("testdata", giantcrd.FixtureName("v1")))
			Expect(err).NotTo(HaveOccurred())

			start := time.Now()
			doc, err := schema.ParseDocument(raw)
			Expect(err).NotTo(HaveOccurred())
			_, err = doc.RootSchema(giantGVK("v1"))
			Expect(err).NotTo(HaveOccurred())
			Expect(time.Since(start)).To(BeNumerically("<", parseBudget),
				"ParseDocument + root resolution over the giant blew its budget")
		})
	})

	When("the giant's field tree materializes its first render window", func() {
		It("resolves the root's children and their lookahead inside the expansion budget", func() {
			root := giantRoot("v1")

			start := time.Now()
			Expect(expandRootWindow(root)).To(Succeed())
			Expect(time.Since(start)).To(BeNumerically("<", expansionBudget),
				"the compose view's opening window over the giant blew its budget")
		})
	})

	When("the giant's Field Paths are enumerated for the search index", func() {
		It("walks all ten-thousand-plus candidates inside the walk budget", func() {
			root := giantRoot("v1")

			start := time.Now()
			paths := root.FieldPaths()
			Expect(time.Since(start)).To(BeNumerically("<", fieldPathsBudget),
				"the full FieldPaths() walk over the giant blew its budget")
			Expect(len(paths)).To(BeNumerically(">=", 10_000))
		})
	})

	When("a populated Draft carries over to the giant's next version", func() {
		It("partitions every value inside the carry-over budget, drop report included", func() {
			draft := schema.NewDraft(giantRoot("v1"), giantGVK("v1"))
			Expect(populateGiantDraft(draft)).To(Succeed())
			target := giantRoot("v2")

			start := time.Now()
			carried, drops := draft.CarryOver(target, giantGVK("v2"))
			Expect(time.Since(start)).To(BeNumerically("<", carryOverBudget),
				"carry-over across the giant blew its budget")

			Expect(carried).NotTo(BeNil())
			Expect(drops).NotTo(BeEmpty(),
				"v2 drops half the grid and renames the spine's anchor — the report must say so")
		})
	})
})

// The benchmarks are the precise instrument behind the budgeted specs:
// run them with `go test -bench=Giant -run='^$' ./internal/schema` when
// tuning a hot path or re-deriving a budget.

func BenchmarkParseGiantDocument(b *testing.B) {
	raw := giantFixtureBytes(b, "v1")
	b.ResetTimer()
	for range b.N {
		doc, err := schema.ParseDocument(raw)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := doc.RootSchema(schema.GroupVersionKind{Group: giantcrd.Group, Version: "v1", Kind: giantcrd.Kind}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGiantFirstExpansion(b *testing.B) {
	doc, err := schema.ParseDocument(giantFixtureBytes(b, "v1"))
	if err != nil {
		b.Fatal(err)
	}
	gvk := schema.GroupVersionKind{Group: giantcrd.Group, Version: "v1", Kind: giantcrd.Kind}
	b.ResetTimer()
	for range b.N {
		root, err := doc.FieldTree(gvk)
		if err != nil {
			b.Fatal(err)
		}
		if err := expandRootWindow(root); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGiantFieldPaths(b *testing.B) {
	doc, err := schema.ParseDocument(giantFixtureBytes(b, "v1"))
	if err != nil {
		b.Fatal(err)
	}
	gvk := schema.GroupVersionKind{Group: giantcrd.Group, Version: "v1", Kind: giantcrd.Kind}
	root, err := doc.FieldTree(gvk)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		if paths := root.FieldPaths(); len(paths) < 10_000 {
			b.Fatalf("the giant enumerated only %d Field Paths", len(paths))
		}
	}
}

func BenchmarkGiantCarryOver(b *testing.B) {
	source, err := schema.ParseDocument(giantFixtureBytes(b, "v1"))
	if err != nil {
		b.Fatal(err)
	}
	target, err := schema.ParseDocument(giantFixtureBytes(b, "v2"))
	if err != nil {
		b.Fatal(err)
	}
	sourceGVK := schema.GroupVersionKind{Group: giantcrd.Group, Version: "v1", Kind: giantcrd.Kind}
	targetGVK := schema.GroupVersionKind{Group: giantcrd.Group, Version: "v2", Kind: giantcrd.Kind}
	sourceRoot, err := source.FieldTree(sourceGVK)
	if err != nil {
		b.Fatal(err)
	}
	targetRoot, err := target.FieldTree(targetGVK)
	if err != nil {
		b.Fatal(err)
	}
	draft := schema.NewDraft(sourceRoot, sourceGVK)
	if err := populateGiantDraft(draft); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		if carried, _ := draft.CarryOver(targetRoot, targetGVK); carried == nil {
			b.Fatal("carry-over returned no Draft")
		}
	}
}
