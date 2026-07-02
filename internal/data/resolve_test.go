package data_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/thezmc/kubectl-craft/internal/data"
)

// resolvableKinds is the hermetic stand-in for a discovered Kind list: a
// core Kind, a named-group Kind, a multi-version Kind whose Preferred
// Version is not the first listed, and one kind name served by two groups
// so ambiguity is provable.
func resolvableKinds() []data.Kind {
	return []data.Kind{
		{
			GVK:              schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"},
			GroupVersionPath: "api/v1",
			Plural:           "configmaps",
			ShortNames:       []string{"cm"},
			Preferred:        true,
		},
		{
			GVK:              schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
			GroupVersionPath: "apis/apps/v1",
			Plural:           "deployments",
			ShortNames:       []string{"deploy"},
			Preferred:        true,
		},
		{
			GVK:              schema.GroupVersionKind{Group: "autoscaling", Version: "v1", Kind: "HorizontalPodAutoscaler"},
			GroupVersionPath: "apis/autoscaling/v1",
			Plural:           "horizontalpodautoscalers",
			ShortNames:       []string{"hpa"},
		},
		{
			GVK:              schema.GroupVersionKind{Group: "autoscaling", Version: "v2", Kind: "HorizontalPodAutoscaler"},
			GroupVersionPath: "apis/autoscaling/v2",
			Plural:           "horizontalpodautoscalers",
			ShortNames:       []string{"hpa"},
			Preferred:        true,
		},
		{
			GVK:              schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
			GroupVersionPath: "apis/networking.k8s.io/v1",
			Plural:           "ingresses",
			ShortNames:       []string{"ing"},
			Preferred:        true,
		},
		{
			GVK:              schema.GroupVersionKind{Group: "extensions", Version: "v1beta1", Kind: "Ingress"},
			GroupVersionPath: "apis/extensions/v1beta1",
			Plural:           "ingresses",
			ShortNames:       []string{"ing"},
			Preferred:        true,
		},
	}
}

var _ = Describe("resolving a deep-link kind token", func() {
	When("the token names a Kind with kubectl explain's tolerance", func() {
		DescribeTable(
			"the Kind name, plural, and short names all resolve, case-insensitively",
			func(token string) {
				kind, err := data.ResolveKindToken(resolvableKinds(), token)

				Expect(err).NotTo(HaveOccurred())
				Expect(kind.GVK).To(Equal(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}))
			},
			Entry("the Kind name", "Deployment"),
			Entry("the Kind name lowercased", "deployment"),
			Entry("the Kind name uppercased", "DEPLOYMENT"),
			Entry("the plural", "deployments"),
			Entry("a short name", "deploy"),
			Entry("a short name uppercased", "DEPLOY"),
		)

		It("resolves a core-group Kind the same way", func() {
			kind, err := data.ResolveKindToken(resolvableKinds(), "cm")

			Expect(err).NotTo(HaveOccurred())
			Expect(kind.GVK).To(Equal(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}))
		})
	})

	When("the Kind is served at multiple versions", func() {
		It("lands on the Preferred Version, wherever it sits in the list", func() {
			kind, err := data.ResolveKindToken(resolvableKinds(), "hpa")

			Expect(err).NotTo(HaveOccurred())
			Expect(kind.GVK.Version).To(Equal("v2"),
				"the deep link must land on the Preferred Version, not the first discovered version")
			Expect(kind.Preferred).To(BeTrue())
		})
	})

	When("no browsable Kind matches the token", func() {
		It("errors naming the token, so the pre-flight failure reads back what was typed", func() {
			_, err := data.ResolveKindToken(resolvableKinds(), "gizmo")

			Expect(err).To(MatchError(ContainSubstring(`unknown kind "gizmo"`)))
		})

		It("treats an empty token as unknown instead of matching anything", func() {
			_, err := data.ResolveKindToken(resolvableKinds(), "")

			Expect(err).To(MatchError(ContainSubstring(`unknown kind ""`)))
		})
	})

	When("the token matches Kinds in more than one group", func() {
		It("errors naming every candidate instead of guessing a group", func() {
			_, err := data.ResolveKindToken(resolvableKinds(), "ingress")

			Expect(err).To(MatchError(ContainSubstring(`ambiguous kind "ingress"`)))
			Expect(err).To(MatchError(ContainSubstring("Ingress.networking.k8s.io")))
			Expect(err).To(MatchError(ContainSubstring("Ingress.extensions")))
		})
	})
})
