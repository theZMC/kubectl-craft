package integration_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/thezmc/kubectl-craft/internal/data"
)

// crdGVR addresses CustomResourceDefinitions through the dynamic client so
// the spec can install and change a CRD without a new client dependency.
var crdGVR = schema.GroupVersionResource{
	Group:    "apiextensions.k8s.io",
	Version:  "v1",
	Resource: "customresourcedefinitions",
}

const (
	cacheProbeCRDName   = "cacheprobes.cache.craft.example.com"
	cacheProbeGroupPath = "apis/cache.craft.example.com/v1"

	// cacheProbeCachedGlob is the sanitized cache filename pattern for the
	// probe group, pinning the on-disk layout end-to-end (the host_port
	// segment is a wildcard because the k3s port is dynamic).
	cacheProbeCachedGlob = "apis_cache.craft.example.com_v1@*.json"
)

// cacheProbeCRD builds the probe CRD with the given spec properties; the
// staleness spec changes the properties to move the group's server content
// hash.
func cacheProbeCRD(specProperties map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": cacheProbeCRDName},
		"spec": map[string]any{
			"group": "cache.craft.example.com",
			"names": map[string]any{
				"plural":   "cacheprobes",
				"singular": "cacheprobe",
				"kind":     "CacheProbe",
				"listKind": "CacheProbeList",
			},
			"scope": "Namespaced",
			"versions": []any{map[string]any{
				"name":    "v1",
				"served":  true,
				"storage": true,
				"schema": map[string]any{"openAPIV3Schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"spec": map[string]any{
							"type":       "object",
							"properties": specProperties,
						},
					},
				}},
			}},
		},
	}}
}

// The spec installs and then changes a CRD, so it is decorated Serial: the
// live index it moves is shared by every parallel process (DESIGN.md —
// Testing: mutating specs are Serial).
var _ = Describe("the hash-validated disk cache against a live k3s cluster", Serial, func() {
	It("replaces the cached OpenAPI v3 Document when a CRD change moves the group's content hash", func(ctx SpecContext) {
		cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
		Expect(err).NotTo(HaveOccurred())

		dynamicClient, err := dynamic.NewForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())
		crds := dynamicClient.Resource(crdGVR)

		By("installing the probe CRD")
		_, err = crds.Create(ctx, cacheProbeCRD(map[string]any{
			"size": map[string]any{"type": "integer"},
		}), metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) {
			Expect(crds.Delete(ctx, cacheProbeCRDName, metav1.DeleteOptions{})).To(Succeed())
		})

		// The cache learns its cluster from the Session's rest.Config.Host,
		// the same value production wiring would hand it.
		cacheRoot := GinkgoT().TempDir()
		cache := data.NewCache(sessionClient(), cacheRoot, cfg.Host)

		By("waiting for the live index to serve the probe group with a content hash")
		firstHash := eventuallyContentHash(ctx, cache, cacheProbeGroupPath, "")

		By("fetching the group's Document through the cache")
		document, err := cache.FetchGroupDocument(ctx, cacheProbeGroupPath, firstHash)
		Expect(err).NotTo(HaveOccurred())
		Expect(document).NotTo(BeEmpty())
		Expect(cachedProbeFiles(cacheRoot)).To(HaveLen(1),
			"the fetched Document must land on disk at the group's content hash")

		By("changing the CRD so the group's content hash moves")
		Eventually(func(g Gomega) {
			live, getErr := crds.Get(ctx, cacheProbeCRDName, metav1.GetOptions{})
			g.Expect(getErr).NotTo(HaveOccurred())

			changed := cacheProbeCRD(map[string]any{
				"size":  map[string]any{"type": "integer"},
				"color": map[string]any{"type": "string"},
			})
			changed.SetResourceVersion(live.GetResourceVersion())
			_, updateErr := crds.Update(ctx, changed, metav1.UpdateOptions{})
			g.Expect(updateErr).NotTo(HaveOccurred())
		}).WithContext(ctx).Should(Succeed())

		By("waiting for the live index to serve the moved content hash")
		secondHash := eventuallyContentHash(ctx, cache, cacheProbeGroupPath, firstHash)

		By("refetching through the cache at the moved hash")
		document, err = cache.FetchGroupDocument(ctx, cacheProbeGroupPath, secondHash)
		Expect(err).NotTo(HaveOccurred())
		Expect(document).NotTo(BeEmpty())

		files := cachedProbeFiles(cacheRoot)
		Expect(files).To(HaveLen(1),
			"the superseded sibling must be gone — replace-on-refetch eviction")
		Expect(filepath.Base(files[0])).To(HaveSuffix("@"+secondHash+".json"),
			"the surviving file must carry the moved content hash")
	}, NodeTimeout(defaultSpecTimeout))
})

// eventuallyContentHash polls the live index until the group is served with
// a non-empty content hash different from previousHash, and returns it.
func eventuallyContentHash(ctx SpecContext, fetcher data.Fetcher, groupPath, previousHash string) string {
	GinkgoHelper()

	var hash string
	Eventually(func(g Gomega) {
		groups, err := fetcher.FetchIndex(ctx)
		g.Expect(err).NotTo(HaveOccurred())

		byPath := groupsByPath(groups)
		g.Expect(byPath).To(HaveKey(groupPath))
		hash = byPath[groupPath].ContentHash
		g.Expect(hash).NotTo(BeEmpty(),
			"the group's index entry must carry a server content hash")
		g.Expect(hash).NotTo(Equal(previousHash),
			"the live index must reflect the changed CRD with a moved content hash")
	}).WithContext(ctx).Should(Succeed())

	return hash
}

// cachedProbeFiles lists the probe group's cached files under the temp cache
// root, across the dynamic host_port directory.
func cachedProbeFiles(cacheRoot string) []string {
	GinkgoHelper()

	files, err := filepath.Glob(filepath.Join(cacheRoot, "v1", "*", cacheProbeCachedGlob))
	Expect(err).NotTo(HaveOccurred())
	return files
}
