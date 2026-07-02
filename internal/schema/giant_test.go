package schema_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/schema"
	"github.com/thezmc/kubectl-craft/test/giantcrd"
)

// giantGVK identifies the giant Kind at one served version.
func giantGVK(version string) schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: giantcrd.Group, Version: version, Kind: giantcrd.Kind}
}

// giantRoot grows the giant's field tree at one version from the checked-in
// fixture.
func giantRoot(version string) *schema.Node {
	GinkgoHelper()
	root, err := parseFixture(giantcrd.FixtureName(version)).FieldTree(giantGVK(version))
	Expect(err).NotTo(HaveOccurred())
	return root
}

var _ = Describe("the giant fixture", func() {
	When("the checked-in documents are compared against their generator", func() {
		It("matches test/giantcrd's deterministic output byte-for-byte", func() {
			// The giant is generated, not captured: this drift check is
			// what keeps the checked-in bytes honest without a cluster —
			// regenerate with `mise run fixtures:generate-giant`.
			for _, version := range giantcrd.Versions {
				generated, err := giantcrd.Document(version)
				Expect(err).NotTo(HaveOccurred())

				checkedIn, err := os.ReadFile(filepath.Join("testdata", giantcrd.FixtureName(version)))
				Expect(err).NotTo(HaveOccurred(), "the giant fixture %s must be checked in", giantcrd.FixtureName(version))
				Expect(checkedIn).To(Equal(generated),
					"the checked-in %s drifted from test/giantcrd — run `mise run fixtures:generate-giant`",
					giantcrd.FixtureName(version))
			}
		})
	})

	When("the v1 giant's Field Paths are enumerated", func() {
		It("spells at least 10,000 schema-addressable Field Paths — the perf pass's scale floor", func() {
			paths := giantRoot("v1").FieldPaths()
			Expect(len(paths)).To(BeNumerically(">=", 10_000),
				"the giant must stay an order of magnitude past real captured CRDs (~2k nodes)")
		})
	})
})
