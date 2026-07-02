package integration_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/thezmc/kubectl-craft/internal/data"
)

// sessionClient builds the Session's data client from the broadcast
// kubeconfig, the same way `kubectl craft` builds it from the resolved
// context's REST config.
func sessionClient() *data.Client {
	GinkgoHelper()

	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	Expect(err).NotTo(HaveOccurred())

	client, err := data.NewClient(cfg)
	Expect(err).NotTo(HaveOccurred())

	return client
}

// fetchIndex polls the live /openapi/v3 index until the cluster serves it.
func fetchIndex(ctx SpecContext, client *data.Client) []data.GroupVersion {
	GinkgoHelper()

	var groups []data.GroupVersion
	Eventually(func(g Gomega) {
		var err error
		groups, err = client.FetchIndex(ctx)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(groups).NotTo(BeEmpty())
	}).WithContext(ctx).Should(Succeed())

	return groups
}

// groupsByPath indexes the live index entries for membership assertions:
// k3s ships bundled extras, so specs check that groups are present, never
// that the list is exactly anything.
func groupsByPath(groups []data.GroupVersion) map[string]data.GroupVersion {
	byPath := make(map[string]data.GroupVersion, len(groups))
	for _, gv := range groups {
		byPath[gv.Path] = gv
	}
	return byPath
}

var _ = Describe("the Session's OpenAPI v3 client against a live k3s cluster", func() {
	var client *data.Client

	BeforeEach(func() {
		client = sessionClient()
	})

	It("fetches the live /openapi/v3 index with per-group content hashes", func(ctx SpecContext) {
		byPath := groupsByPath(fetchIndex(ctx, client))

		Expect(byPath).To(HaveKey("api/v1"),
			"the core group must be in the live index")
		Expect(byPath).To(HaveKey("apis/apps/v1"),
			"the apps group must be in the live index")

		Expect(byPath["api/v1"].ContentHash).NotTo(BeEmpty(),
			"the core group's index entry must carry a server content hash")
		Expect(byPath["apis/apps/v1"].ContentHash).NotTo(BeEmpty(),
			"the apps group's index entry must carry a server content hash")
	}, NodeTimeout(defaultSpecTimeout))

	It("fetches the apps/v1 OpenAPI v3 Document at its content hash as raw bytes", func(ctx SpecContext) {
		byPath := groupsByPath(fetchIndex(ctx, client))
		appsV1, ok := byPath["apis/apps/v1"]
		Expect(ok).To(BeTrue(), "the apps group must be in the live index")
		Expect(appsV1.ContentHash).NotTo(BeEmpty(),
			"the fetch must be addressed at a real server content hash, never degrade to an unhashed fetch")

		document, err := client.FetchGroupDocument(ctx, appsV1.Path, appsV1.ContentHash)
		Expect(err).NotTo(HaveOccurred())
		Expect(document).NotTo(BeEmpty(),
			"the OpenAPI v3 Document that sources every apps/v1 Type Schema must arrive as raw bytes")
	}, NodeTimeout(defaultSpecTimeout))
})
