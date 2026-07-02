package matchers_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/matchers"
)

var _ = Describe("the BeMissingRequired matcher", func() {
	When("a Draft's missing required Field Paths are matched", func() {
		It("succeeds on exactly the expected Field Paths, in tree order", func() {
			Expect([]string{"spec", "spec.size"}).To(matchers.BeMissingRequired("spec", "spec.size"))
		})

		It("fails when the Field Paths are out of tree order", func() {
			Expect([]string{"spec.size", "spec"}).NotTo(matchers.BeMissingRequired("spec", "spec.size"))
		})

		It("fails when a required Field Path is missing that was not expected", func() {
			Expect([]string{"spec", "spec.size"}).NotTo(matchers.BeMissingRequired("spec"))
		})

		When("nothing required is expected to be missing", func() {
			It("succeeds on a nil result", func() {
				Expect([]string(nil)).To(matchers.BeMissingRequired())
			})

			It("succeeds on an empty result", func() {
				Expect([]string{}).To(matchers.BeMissingRequired())
			})

			It("fails when something required is missing", func() {
				Expect([]string{"spec"}).NotTo(matchers.BeMissingRequired())
			})
		})

		It("rejects an actual that is not the computed []string of Field Paths", func() {
			match, err := matchers.BeMissingRequired("spec").Match(42)

			Expect(match).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring(
				"BeMissingRequired matches the []string of missing required Draft-level Field Paths",
			)))
		})
	})
})
