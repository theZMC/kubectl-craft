package schema_test

import (
	"go/build"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSchema(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Schema Suite")
}

var _ = Describe("the compose core", func() {
	Context("as the pure home of Type Schema, Draft, and emission logic", func() {
		It("imports no Bubble Tea packages", func() {
			pkg, err := build.ImportDir(".", 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(pkg.Imports).NotTo(ContainElement(ContainSubstring("charmbracelet")))
		})
	})
})
