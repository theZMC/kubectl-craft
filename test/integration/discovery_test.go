package integration_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/thezmc/kubectl-craft/internal/data"
)

// sessionKindLister builds the Session's discovery seam from the broadcast
// kubeconfig, the same way `kubectl craft` builds it from the resolved
// context's REST config.
func sessionKindLister() data.KindLister {
	GinkgoHelper()

	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	Expect(err).NotTo(HaveOccurred())

	lister, err := data.NewKindLister(cfg)
	Expect(err).NotTo(HaveOccurred())

	return lister
}

// discoverKinds polls live discovery until the cluster answers with a
// non-empty Kind list.
func discoverKinds(ctx SpecContext, lister data.KindLister) []data.Kind {
	GinkgoHelper()

	var kinds []data.Kind
	Eventually(func(g Gomega) {
		var err error
		kinds, err = data.DiscoverKinds(lister)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(kinds).NotTo(BeEmpty())
	}).WithContext(ctx).Should(Succeed())

	return kinds
}

// kindsByGVK indexes the Kind list for membership assertions: k3s ships
// bundled extras, so specs check that Kinds are present (or absent), never
// that the list is exactly anything.
func kindsByGVK(kinds []data.Kind) map[schema.GroupVersionKind]data.Kind {
	byGVK := make(map[schema.GroupVersionKind]data.Kind, len(kinds))
	for _, kind := range kinds {
		byGVK[kind.GVK] = kind
	}
	return byGVK
}

var _ = Describe("Kind discovery against a live k3s cluster", func() {
	It("lists the browsable Kinds with Document paths, short names, and Preferred Version marking", func(ctx SpecContext) {
		kinds := discoverKinds(ctx, sessionKindLister())
		byGVK := kindsByGVK(kinds)

		deploymentGVK := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
		Expect(byGVK).To(HaveKey(deploymentGVK),
			"Deployment must be in the browsable Kind list")
		deployment := byGVK[deploymentGVK]
		Expect(deployment.GroupVersionPath).To(Equal("apis/apps/v1"),
			"a named group Kind's Document path must use the apis/<group>/<version> shape")
		Expect(deployment.ShortNames).To(ContainElement("deploy"),
			"the deep-link arg must be able to resolve the deploy short name")
		Expect(deployment.Preferred).To(BeTrue(),
			"apps/v1 must carry the group's Preferred Version marking")
		Expect(deployment.Namespaced).To(BeTrue(),
			"Deployment must surface as namespaced, so Validate POSTs it under a namespace segment")

		namespaceGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"}
		Expect(byGVK).To(HaveKey(namespaceGVK))
		Expect(byGVK[namespaceGVK].Namespaced).To(BeFalse(),
			"Namespace must surface as cluster-scoped, so Validate POSTs it without a namespace segment")

		crdGVK := schema.GroupVersionKind{
			Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition",
		}
		Expect(byGVK).To(HaveKey(crdGVK))
		Expect(byGVK[crdGVK].Namespaced).To(BeFalse(),
			"CustomResourceDefinition must surface as cluster-scoped")

		configMapGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
		Expect(byGVK).To(HaveKey(configMapGVK),
			"ConfigMap must be in the browsable Kind list")
		Expect(byGVK[configMapGVK].GroupVersionPath).To(Equal("api/v1"),
			"a core Kind's Document path must use the api/<version> shape")

		Expect(kinds).NotTo(ContainElement(HaveField("GVK.Kind", "ComponentStatus")),
			"read-only virtual kinds must be excluded: a Manifest is meaningless for them")
		Expect(kinds).NotTo(ContainElement(HaveField("GVK.Kind", "TokenReview")),
			"request-shaped kinds must be excluded even though they serve create")
		Expect(kinds).NotTo(ContainElement(HaveField("GVK.Kind", "SubjectAccessReview")),
			"request-shaped kinds must be excluded even though they serve create")
	}, NodeTimeout(defaultSpecTimeout))
})

const kindProbeCRDName = "kindprobes.discovery.craft.example.com"

// kindProbeCRD is a two-version probe CRD: v2 carries the higher version
// priority, so the cluster must report it as the group's Preferred Version.
func kindProbeCRD() *unstructured.Unstructured {
	version := func(name string, storage bool) map[string]any {
		return map[string]any{
			"name":    name,
			"served":  true,
			"storage": storage,
			"schema": map[string]any{"openAPIV3Schema": map[string]any{
				"type": "object",
			}},
		}
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": kindProbeCRDName},
		"spec": map[string]any{
			"group": "discovery.craft.example.com",
			"names": map[string]any{
				"plural":     "kindprobes",
				"singular":   "kindprobe",
				"kind":       "KindProbe",
				"listKind":   "KindProbeList",
				"shortNames": []any{"kp"},
			},
			"scope":    "Namespaced",
			"versions": []any{version("v1", false), version("v2", true)},
		},
	}}
}

// The spec installs a CRD, so it is decorated Serial: the Kind list it
// changes is shared by every parallel process (DESIGN.md — Testing: mutating
// specs are Serial).
var _ = Describe("Kind discovery of a freshly installed CRD", Serial, func() {
	It("sees the new Kinds live, create-capable, with short names and Preferred Version marking", func(ctx SpecContext) {
		cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
		Expect(err).NotTo(HaveOccurred())

		dynamicClient, err := dynamic.NewForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())
		crds := dynamicClient.Resource(crdGVR)

		// The lister is built before the CRD exists: the same lister must
		// see the new Kinds afterwards, proving discovery is asked live per
		// call and never cached (DESIGN.md — Data layer).
		lister := sessionKindLister()

		probeGVK := func(version string) schema.GroupVersionKind {
			return schema.GroupVersionKind{Group: "discovery.craft.example.com", Version: version, Kind: "KindProbe"}
		}

		By("confirming the probe Kind is not browsable yet")
		Expect(kindsByGVK(discoverKinds(ctx, lister))).NotTo(HaveKey(probeGVK("v2")))

		By("installing the two-version probe CRD")
		_, err = crds.Create(ctx, kindProbeCRD(), metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) {
			Expect(crds.Delete(ctx, kindProbeCRDName, metav1.DeleteOptions{})).To(Succeed())
		})

		By("waiting for the same lister to see both served versions live")
		var byGVK map[schema.GroupVersionKind]data.Kind
		Eventually(func(g Gomega) {
			kinds, discoverErr := data.DiscoverKinds(lister)
			g.Expect(discoverErr).NotTo(HaveOccurred())

			byGVK = kindsByGVK(kinds)
			g.Expect(byGVK).To(HaveKey(probeGVK("v1")))
			g.Expect(byGVK).To(HaveKey(probeGVK("v2")))
		}).WithContext(ctx).Should(Succeed())

		v1, v2 := byGVK[probeGVK("v1")], byGVK[probeGVK("v2")]
		Expect(v1.GroupVersionPath).To(Equal("apis/discovery.craft.example.com/v1"))
		Expect(v2.GroupVersionPath).To(Equal("apis/discovery.craft.example.com/v2"))
		Expect(v1.ShortNames).To(ContainElement("kp"),
			"the CRD's short name must be browsable for the deep-link arg")
		Expect(v2.Preferred).To(BeTrue(),
			"the higher-priority served version must carry the Preferred Version marking")
		Expect(v1.Preferred).To(BeFalse(),
			"only the group's Preferred Version may carry the marking")
	}, NodeTimeout(defaultSpecTimeout))
})
