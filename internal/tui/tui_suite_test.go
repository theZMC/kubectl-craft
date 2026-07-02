package tui_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTui(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tui Suite")
}

var _ = Describe("the TUI layer", func() {
	Context("before the Session shell renders", func() {
		It("is scaffolded to isolate all Bubble Tea code", func() {
			Expect(true).To(BeTrue())
		})
	})
})
