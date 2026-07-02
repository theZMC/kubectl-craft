package data_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/thezmc/kubectl-craft/internal/data"
)

// deploymentKind is the namespaced Kind the dry-run specs POST: apps/v1
// Deployment, shaped exactly as discovery would shape it.
func deploymentKind() data.Kind {
	return data.Kind{
		GVK:              schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
		GroupVersionPath: "apis/apps/v1",
		Plural:           "deployments",
		Namespaced:       true,
	}
}

// namespaceKind is the cluster-scoped Kind of the scope specs: core v1
// Namespace, which must POST without any namespace segment.
func namespaceKind() data.Kind {
	return data.Kind{
		GVK:              schema.GroupVersionKind{Version: "v1", Kind: "Namespace"},
		GroupVersionPath: "api/v1",
		Plural:           "namespaces",
		Namespaced:       false,
	}
}

// recordedRequest captures what one dry-run POST put on the wire, so specs
// can pin the endpoint-construction contract.
type recordedRequest struct {
	method      string
	path        string
	query       string
	contentType string
	body        []byte
}

// recordingValidateServer answers every request with the given status code
// and body, recording what arrived.
func recordingValidateServer(code int, body string) (*httptest.Server, *recordedRequest) {
	GinkgoHelper()
	recorded := &recordedRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded.method = r.Method
		recorded.path = r.URL.Path
		recorded.query = r.URL.RawQuery
		recorded.contentType = r.Header.Get("Content-Type")
		requestBody, err := io.ReadAll(r.Body)
		Expect(err).NotTo(HaveOccurred())
		recorded.body = requestBody

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	DeferCleanup(server.Close)
	return server, recorded
}

// statusBody spells a metav1.Status response the way the API server does.
func statusBody(status metav1.Status) string {
	GinkgoHelper()
	status.Kind = "Status"
	status.APIVersion = "v1"
	raw, err := json.Marshal(status)
	Expect(err).NotTo(HaveOccurred())
	return string(raw)
}

var _ = Describe("the Session's Validate client", func() {
	manifest := []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n")

	When("the server accepts the dry-run", func() {
		It("answers Clean and POSTs the Manifest bytes verbatim to the namespaced endpoint with dryRun=All", func(ctx SpecContext) {
			server, recorded := recordingValidateServer(http.StatusCreated,
				`{"kind":"Deployment","apiVersion":"apps/v1"}`)

			outcome := newSessionClient(server.URL).Validate(ctx, deploymentKind(), manifest, "workshop")

			Expect(outcome).To(Equal(data.Clean{}))
			Expect(recorded.method).To(Equal(http.MethodPost),
				"Validate is a bare create POST — compose builds a new Manifest, never a patch")
			Expect(recorded.path).To(Equal("/apis/apps/v1/namespaces/workshop/deployments"),
				"a namespaced Kind POSTs under the resolved namespace segment")
			Expect(recorded.query).To(Equal("dryRun=All&fieldValidation=Strict"),
				"dryRun=All keeps the POST persistence-free; fieldValidation=Strict makes unknown "+
					"fields fail instead of merely warning — a lying Clean breaks the safety net")
			Expect(recorded.body).To(Equal(manifest),
				"the Manifest bytes must travel verbatim — Validate checks exactly what would be Emitted")
			Expect(recorded.contentType).To(Equal("application/yaml"),
				"the Emitted Manifest is YAML and the API server decodes it natively")
		})
	})

	When("the Kind is cluster-scoped", func() {
		It("POSTs without a namespace segment, ignoring any resolved namespace", func(ctx SpecContext) {
			server, recorded := recordingValidateServer(http.StatusCreated,
				`{"kind":"Namespace","apiVersion":"v1"}`)
			clusterScoped := []byte("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: workshop\n")

			outcome := newSessionClient(server.URL).Validate(ctx, namespaceKind(), clusterScoped, "workshop")

			Expect(outcome).To(Equal(data.Clean{}))
			Expect(recorded.path).To(Equal("/api/v1/namespaces"),
				"a cluster-scoped Kind's endpoint carries no namespace segment")
			Expect(recorded.query).To(Equal("dryRun=All&fieldValidation=Strict"))
		})
	})

	When("the server rejects the Manifest with a validation Status", func() {
		It("answers Invalid carrying the raw metav1.Status for internal/validate to map", func(ctx SpecContext) {
			rejection := metav1.Status{
				Status:  metav1.StatusFailure,
				Message: `Deployment.apps "web" is invalid: spec.selector: Required value`,
				Reason:  metav1.StatusReasonInvalid,
				Code:    http.StatusUnprocessableEntity,
				Details: &metav1.StatusDetails{
					Kind: "Deployment",
					Causes: []metav1.StatusCause{{
						Type:    metav1.CauseTypeFieldValueRequired,
						Message: "Required value",
						Field:   "spec.selector",
					}},
				},
			}
			server, _ := recordingValidateServer(http.StatusUnprocessableEntity, statusBody(rejection))

			outcome := newSessionClient(server.URL).Validate(ctx, deploymentKind(), manifest, "workshop")

			invalid, isInvalid := outcome.(data.Invalid)
			Expect(isInvalid).To(BeTrue(), "a validation Status is the Manifest's problem, never Unavailable")
			Expect(invalid.Status.Reason).To(Equal(metav1.StatusReasonInvalid))
			Expect(invalid.Status.Details.Causes).To(HaveLen(1),
				"the raw Status travels out uninterpreted — mapping causes is internal/validate's job")
			Expect(invalid.Status.Details.Causes[0].Field).To(Equal("spec.selector"))
		})
	})

	When("the Manifest carries an unknown field under strict field validation", func() {
		It("answers Invalid — fieldValidation=Strict turns the typo into a rejection instead of a silent warning", func(ctx SpecContext) {
			rejection := metav1.Status{
				Status:  metav1.StatusFailure,
				Message: `strict decoding error: unknown field "spec.replcias"`,
				Reason:  metav1.StatusReasonBadRequest,
				Code:    http.StatusBadRequest,
			}
			server, _ := recordingValidateServer(http.StatusBadRequest, statusBody(rejection))
			typoed := []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  replcias: 3\n")

			outcome := newSessionClient(server.URL).Validate(ctx, deploymentKind(), typoed, "workshop")

			invalid, isInvalid := outcome.(data.Invalid)
			Expect(isInvalid).To(BeTrue(),
				"a strict-decoding rejection is the Manifest's problem — a lying Clean would break the safety net")
			Expect(invalid.Status.Message).To(ContainSubstring(`unknown field "spec.replcias"`))
		})
	})

	When("the server refuses the dry-run with an RBAC 403", func() {
		It("answers Unavailable with the server's own words — never a manifest error", func(ctx SpecContext) {
			refusal := metav1.Status{
				Status:  metav1.StatusFailure,
				Message: `deployments.apps is forbidden: User "craft-nobody" cannot create resource "deployments"`,
				Reason:  metav1.StatusReasonForbidden,
				Code:    http.StatusForbidden,
			}
			server, _ := recordingValidateServer(http.StatusForbidden, statusBody(refusal))

			outcome := newSessionClient(server.URL).Validate(ctx, deploymentKind(), manifest, "workshop")

			unavailable, isUnavailable := outcome.(data.Unavailable)
			Expect(isUnavailable).To(BeTrue(),
				"an RBAC refusal says nothing about the Manifest, even though it arrives Status-shaped")
			Expect(unavailable.Reason).To(ContainSubstring("403"))
			Expect(unavailable.Reason).To(ContainSubstring("forbidden"),
				"the reason must carry the server's own words for the results pane")
		})
	})

	When("the server throttles the dry-run with a 429", func() {
		It("answers Unavailable — a throttled cluster is never the Manifest's fault", func(ctx SpecContext) {
			throttled := metav1.Status{
				Status:  metav1.StatusFailure,
				Message: "too many requests, please try again later",
				Reason:  metav1.StatusReasonTooManyRequests,
				Code:    http.StatusTooManyRequests,
			}
			server, _ := recordingValidateServer(http.StatusTooManyRequests, statusBody(throttled))

			outcome := newSessionClient(server.URL).Validate(ctx, deploymentKind(), manifest, "workshop")

			unavailable, isUnavailable := outcome.(data.Unavailable)
			Expect(isUnavailable).To(BeTrue(),
				"API Priority & Fairness throttling arrives Status-shaped but says nothing about the Manifest")
			Expect(unavailable.Reason).To(ContainSubstring("429"))
			Expect(unavailable.Reason).To(ContainSubstring("too many requests"))
		})
	})

	When("the network fails", func() {
		It("answers Unavailable with a clear kubectl-like connection reason", func(ctx SpecContext) {
			server := httptest.NewServer(http.NotFoundHandler())
			dead := server.URL
			server.Close()

			outcome := newSessionClient(dead).Validate(ctx, deploymentKind(), manifest, "workshop")

			unavailable, isUnavailable := outcome.(data.Unavailable)
			Expect(isUnavailable).To(BeTrue())
			Expect(unavailable.Reason).To(ContainSubstring("unable to connect to the server"))
		})
	})

	When("the server fails with a non-Status 500", func() {
		It("answers Unavailable — a failing cluster is never the Manifest's fault", func(ctx SpecContext) {
			server, _ := recordingValidateServer(http.StatusInternalServerError, "boom: not a Status body")

			outcome := newSessionClient(server.URL).Validate(ctx, deploymentKind(), manifest, "workshop")

			unavailable, isUnavailable := outcome.(data.Unavailable)
			Expect(isUnavailable).To(BeTrue())
			Expect(unavailable.Reason).To(ContainSubstring("500"))
		})
	})

	When("the server fails with a Status-shaped 500", func() {
		It("answers Unavailable carrying the server's message — an unexpected 5xx is not a validation verdict", func(ctx SpecContext) {
			failure := metav1.Status{
				Status:  metav1.StatusFailure,
				Message: "Internal error occurred: failed calling webhook",
				Reason:  metav1.StatusReasonInternalError,
				Code:    http.StatusInternalServerError,
			}
			server, _ := recordingValidateServer(http.StatusInternalServerError, statusBody(failure))

			outcome := newSessionClient(server.URL).Validate(ctx, deploymentKind(), manifest, "workshop")

			unavailable, isUnavailable := outcome.(data.Unavailable)
			Expect(isUnavailable).To(BeTrue())
			Expect(unavailable.Reason).To(ContainSubstring("failed calling webhook"))
		})
	})

	When("the server answers a 4xx without a Status body", func() {
		It("answers Unavailable — a non-Status error was not a validation verdict", func(ctx SpecContext) {
			server, _ := recordingValidateServer(http.StatusBadRequest, "an HTML error page, say")

			outcome := newSessionClient(server.URL).Validate(ctx, deploymentKind(), manifest, "workshop")

			unavailable, isUnavailable := outcome.(data.Unavailable)
			Expect(isUnavailable).To(BeTrue())
			Expect(unavailable.Reason).To(ContainSubstring("400"))
		})
	})

	When("a namespaced Kind has no namespace at all", func() {
		It("answers Unavailable naming the fix, without any request leaving", func(ctx SpecContext) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				requests++
			}))
			DeferCleanup(server.Close)

			outcome := newSessionClient(server.URL).Validate(ctx, deploymentKind(), manifest, "")

			unavailable, isUnavailable := outcome.(data.Unavailable)
			Expect(isUnavailable).To(BeTrue(),
				"the server's 404 for the malformed POST would read as a manifest error — refuse client-side instead")
			Expect(unavailable.Reason).To(ContainSubstring("Deployment is namespaced"))
			Expect(unavailable.Reason).To(ContainSubstring("metadata.namespace"))
			Expect(requests).To(BeZero(), "no request may leave for an unaddressable POST")
		})
	})

	When("the resolved namespace is not even a DNS label", func() {
		It("answers Unavailable naming the bad namespace, without any request leaving", func(ctx SpecContext) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				requests++
			}))
			DeferCleanup(server.Close)

			outcome := newSessionClient(server.URL).Validate(ctx, deploymentKind(), manifest, "kube/../system")

			unavailable, isUnavailable := outcome.(data.Unavailable)
			Expect(isUnavailable).To(BeTrue(),
				"joined raw into the path, the namespace would rewrite the request into a misleading 404")
			Expect(unavailable.Reason).To(ContainSubstring(`"kube/../system"`))
			Expect(unavailable.Reason).To(ContainSubstring("not a valid namespace name"))
			Expect(requests).To(BeZero(), "no request may leave for an unaddressable POST")
		})
	})
})

var _ = Describe("resolving a Manifest's namespace, like kubectl", func() {
	When("the Draft sets metadata.namespace", func() {
		It("wins over the Session default", func() {
			manifest := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: workshop\n")

			Expect(data.ResolveNamespace(manifest, "default")).To(Equal("workshop"))
		})
	})

	When("the Draft leaves metadata.namespace unset", func() {
		It("falls back to the Session default resolved at launch", func() {
			manifest := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n")

			Expect(data.ResolveNamespace(manifest, "team-default")).To(Equal("team-default"))
		})
	})

	When("the Manifest cannot even be parsed", func() {
		It("falls back to the Session default — the dry-run itself will say what is wrong", func() {
			Expect(data.ResolveNamespace([]byte("\t: not yaml"), "default")).To(Equal("default"))
		})
	})
})
