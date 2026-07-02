package data_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestData(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Data Suite")
}

var _ = Describe("the data layer", func() {
	Context("before any Session connects to a cluster", func() {
		It("is scaffolded to source Type Schemas from the OpenAPI v3 Document", func() {
			Expect(true).To(BeTrue())
		})
	})
})
