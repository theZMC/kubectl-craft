package data_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"

	"github.com/thezmc/kubectl-craft/internal/data"
)

// newSessionClient builds a Client the way a Session would, pointed at the
// given server host.
func newSessionClient(host string) *data.Client {
	GinkgoHelper()
	client, err := data.NewClient(&rest.Config{Host: host})
	Expect(err).NotTo(HaveOccurred())
	return client
}

var _ = Describe("the Session's OpenAPI v3 client", func() {
	When("the cluster serves OpenAPI v3 Documents", func() {
		var (
			client        *data.Client
			appsDocument  []byte
			requestedHash string
		)

		BeforeEach(func() {
			appsDocument = []byte(`{"openapi":"3.0.0","info":{"title":"Kubernetes"},"components":{"schemas":{}}}`)
			requestedHash = ""

			mux := http.NewServeMux()
			mux.HandleFunc("/openapi/v3", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"paths": map[string]any{
						"api/v1": map[string]any{
							"serverRelativeURL": "/openapi/v3/api/v1?hash=CORE1HASH",
						},
						"apis/apps/v1": map[string]any{
							"serverRelativeURL": "/openapi/v3/apis/apps/v1?hash=APPS1HASH",
						},
					},
				})
			})
			mux.HandleFunc("/openapi/v3/apis/apps/v1", func(w http.ResponseWriter, r *http.Request) {
				requestedHash = r.URL.Query().Get("hash")
				_, _ = w.Write(appsDocument)
			})

			server := httptest.NewServer(mux)
			DeferCleanup(server.Close)
			client = newSessionClient(server.URL)
		})

		It("fetches the live index and exposes each group version with its server content hash", func(ctx SpecContext) {
			groups, err := client.FetchIndex(ctx)

			Expect(err).NotTo(HaveOccurred())
			Expect(groups).To(Equal([]data.GroupVersion{
				{Path: "api/v1", ContentHash: "CORE1HASH"},
				{Path: "apis/apps/v1", ContentHash: "APPS1HASH"},
			}))
		})

		It("fetches a group's OpenAPI v3 Document by group and content hash as raw response bytes", func(ctx SpecContext) {
			body, err := client.FetchGroupDocument(ctx, "apis/apps/v1", "APPS1HASH")

			Expect(err).NotTo(HaveOccurred())
			Expect(body).To(Equal(appsDocument), "the bytes backing every Type Schema must stay raw")
			Expect(requestedHash).To(Equal("APPS1HASH"), "the fetch must address the server content hash")
		})
	})

	When("the cluster does not serve /openapi/v3", func() {
		It("refuses the Session with the minimum-version error, gated on capability alone", func(ctx SpecContext) {
			server := httptest.NewServer(http.NotFoundHandler())
			DeferCleanup(server.Close)
			client := newSessionClient(server.URL)

			_, err := client.FetchIndex(ctx)

			Expect(err).To(MatchError(data.ErrOpenAPIV3NotServed))
			Expect(err.Error()).To(ContainSubstring("OpenAPI v3"))
			Expect(err.Error()).To(ContainSubstring("1.24"))
		})
	})

	When("the cluster is unreachable", func() {
		It("hard-fails the Session with a clear kubectl-like connection error", func(ctx SpecContext) {
			server := httptest.NewServer(http.NotFoundHandler())
			unreachable := server.URL
			server.Close()
			client := newSessionClient(unreachable)

			_, err := client.FetchIndex(ctx)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unable to connect to the server"))
		})
	})
})
