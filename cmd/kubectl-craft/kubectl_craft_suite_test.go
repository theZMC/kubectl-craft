package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestKubectlCraft(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "KubectlCraft Suite")
}

var _ = Describe("the kubectl-craft binary", func() {
	Context("before a Session can Compose Manifests", func() {
		It("prints a walking-skeleton placeholder", func() {
			Expect(placeholder()).To(ContainSubstring("kubectl-craft"))
			Expect(placeholder()).To(ContainSubstring("Type Schemas"))
		})
	})
})
