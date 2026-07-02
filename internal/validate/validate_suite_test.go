package validate_test

import (
	"go/build"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestValidate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Validate Suite")
}

var _ = Describe("the Validate core", func() {
	Context("as the pure home of Status-to-findings mapping", func() {
		It("imports no Bubble Tea packages", func() {
			pkg, err := build.ImportDir(".", 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(pkg.Imports).NotTo(ContainElement(ContainSubstring("charmbracelet")))
		})
	})
})
