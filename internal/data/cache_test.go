package data_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/data"
)

// countingFetcher stubs the live Client behind the Fetcher seam, counting
// every call so specs can pin the warm-start guarantee: a hit must never
// touch the live cluster.
type countingFetcher struct {
	groups   []data.GroupVersion
	indexErr error

	document    []byte
	documentErr error

	indexCalls    int
	documentCalls int
}

func (f *countingFetcher) FetchIndex(context.Context) ([]data.GroupVersion, error) {
	f.indexCalls++
	return f.groups, f.indexErr
}

func (f *countingFetcher) FetchGroupDocument(_ context.Context, _, _ string) ([]byte, error) {
	f.documentCalls++
	return f.document, f.documentErr
}

var _ = Describe("the hash-validated disk cache behind the Fetcher seam", func() {
	const (
		serverHost  = "https://127.0.0.1:6443"
		groupPath   = "apis/apps/v1"
		contentHash = "APPS1HASH"
	)

	var (
		root         string
		clusterDir   string
		documentPath string
		liveDocument []byte
		inner        *countingFetcher
		cache        *data.Cache
	)

	// mustWriteCached seeds the cache directory the way a previous Session's
	// write would have left it.
	mustWriteCached := func(name string, body []byte) {
		GinkgoHelper()
		Expect(os.MkdirAll(clusterDir, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(clusterDir, name), body, 0o644)).To(Succeed())
	}

	// cachedFileNames lists what the cache left on disk for the cluster.
	cachedFileNames := func() []string {
		GinkgoHelper()
		entries, err := os.ReadDir(clusterDir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		Expect(err).NotTo(HaveOccurred())
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		return names
	}

	BeforeEach(func() {
		root = GinkgoT().TempDir()
		// The layout under root is pinned verbatim (DESIGN.md — Data
		// layer): v1/<sanitized host_port>/<sanitized group-version>@<hash>.json.
		clusterDir = filepath.Join(root, "v1", "127.0.0.1_6443")
		documentPath = filepath.Join(clusterDir, "apis_apps_v1@APPS1HASH.json")

		liveDocument = []byte(`{"openapi":"3.0.0","components":{"schemas":{}}}`)
		inner = &countingFetcher{
			groups: []data.GroupVersion{
				{Path: groupPath, ContentHash: contentHash},
			},
			document: liveDocument,
		}
		cache = data.NewCache(inner, root, serverHost)
	})

	When("the group's OpenAPI v3 Document is already cached at the live content hash", func() {
		var cachedDocument []byte

		BeforeEach(func() {
			// Distinct bytes prove the Session was served from disk, not live.
			cachedDocument = []byte(`{"openapi":"3.0.0","info":{"title":"cached"}}`)
			mustWriteCached("apis_apps_v1@APPS1HASH.json", cachedDocument)
		})

		It("serves the raw bytes from disk without touching the live cluster", func(ctx SpecContext) {
			body, err := cache.FetchGroupDocument(ctx, groupPath, contentHash)

			Expect(err).NotTo(HaveOccurred())
			Expect(body).To(Equal(cachedDocument), "a hit must serve the cached raw response bytes")
			Expect(inner.documentCalls).To(BeZero(),
				"the warm-start guarantee: a hit never touches the live Fetcher")
		})
	})

	When("the group's OpenAPI v3 Document is not yet cached", func() {
		It("fetches through the live Fetcher and returns the raw response bytes", func(ctx SpecContext) {
			body, err := cache.FetchGroupDocument(ctx, groupPath, contentHash)

			Expect(err).NotTo(HaveOccurred())
			Expect(body).To(Equal(liveDocument))
			Expect(inner.documentCalls).To(Equal(1))
		})

		It("writes the raw response bytes at <group-version>@<content hash>.json under the sanitized cluster directory", func(ctx SpecContext) {
			_, err := cache.FetchGroupDocument(ctx, groupPath, contentHash)

			Expect(err).NotTo(HaveOccurred())
			Expect(os.ReadFile(documentPath)).To(Equal(liveDocument),
				"the cached file must be the raw response bytes — no cache-format versioning")
			Expect(cachedFileNames()).To(ConsistOf("apis_apps_v1@APPS1HASH.json"),
				"the atomic write must leave no temp files behind")
		})

		It("deletes superseded <group-version>@* siblings after the write", func(ctx SpecContext) {
			mustWriteCached("apis_apps_v1@STALEHASH.json", []byte(`{"stale":true}`))
			mustWriteCached("api_v1@CORE1HASH.json", []byte(`{"other":"group"}`))

			_, err := cache.FetchGroupDocument(ctx, groupPath, contentHash)

			Expect(err).NotTo(HaveOccurred())
			Expect(cachedFileNames()).To(ConsistOf(
				"apis_apps_v1@APPS1HASH.json",
				"api_v1@CORE1HASH.json",
			), "eviction is self-cleaning replace-on-refetch and must never touch other groups")
		})
	})

	When("the cached file is corrupt", func() {
		BeforeEach(func() {
			mustWriteCached("apis_apps_v1@APPS1HASH.json", []byte(`{"truncated":`))
		})

		It("treats it as a miss, falls through to live, and replaces the file", func(ctx SpecContext) {
			body, err := cache.FetchGroupDocument(ctx, groupPath, contentHash)

			Expect(err).NotTo(HaveOccurred())
			Expect(body).To(Equal(liveDocument))
			Expect(inner.documentCalls).To(Equal(1))
			Expect(os.ReadFile(documentPath)).To(Equal(liveDocument),
				"the refetch must replace the corrupt file")
		})
	})

	When("the live index entry carries no content hash", func() {
		It("bypasses the cache: fetches through live and writes nothing", func(ctx SpecContext) {
			body, err := cache.FetchGroupDocument(ctx, groupPath, "")

			Expect(err).NotTo(HaveOccurred())
			Expect(body).To(Equal(liveDocument))
			Expect(inner.documentCalls).To(Equal(1))
			Expect(cachedFileNames()).To(BeEmpty(),
				"an unkeyed document must never land on disk")
		})
	})

	When("the live fetch fails", func() {
		BeforeEach(func() {
			inner.document = nil
			inner.documentErr = errors.New("unable to connect to the server")
		})

		It("propagates the failure and writes nothing — the cache is a speedup, never a fallback", func(ctx SpecContext) {
			_, err := cache.FetchGroupDocument(ctx, groupPath, contentHash)

			Expect(err).To(MatchError(inner.documentErr))
			Expect(cachedFileNames()).To(BeEmpty())
		})
	})

	Describe("the live /openapi/v3 index", func() {
		It("is always fetched live and never cached — it is what keeps the cache honest", func(ctx SpecContext) {
			for range 2 {
				groups, err := cache.FetchIndex(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(groups).To(Equal(inner.groups))
			}

			Expect(inner.indexCalls).To(Equal(2),
				"every FetchIndex must delegate to the live Fetcher")
			Expect(cachedFileNames()).To(BeEmpty(),
				"discovery of the index must never land on disk")
		})

		It("propagates a live index failure unchanged", func(ctx SpecContext) {
			inner.indexErr = data.ErrOpenAPIV3NotServed

			_, err := cache.FetchIndex(ctx)

			Expect(err).To(MatchError(data.ErrOpenAPIV3NotServed))
		})
	})
})
