// Command capture-status-fixtures (re)generates the checked-in corpus of
// recorded dry-run Status payloads that backs the hermetic Validate specs
// (internal/validate/testdata), following the raw OpenAPI corpus precedent
// (hack/capture-fixtures).
//
// It boots the same k3s image the integration suite uses, installs the
// sample CRD manifests from internal/schema/testdata/crds (the corpus CRDs
// carry the CEL rules), registers an always-deny ValidatingWebhookConfiguration
// backed by an in-process HTTPS admission server (reachable from the cluster
// through testcontainers' host port access), POSTs each deliberately-invalid
// scenario Manifest with ?dryRun=All, and writes every failure Status to its
// fixture file as raw response bytes — never pretty-printed or re-marshalled,
// so the corpus records exactly what a real API server said.
//
// It is a plain Go program under hack/ (run explicitly via
// `mise run fixtures:capture-status`, from the repo root) rather than a
// spec, so the fast hermetic loop (`ginkgo --label-filter='!integration'
// ./...`) never touches Docker.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/thezmc/kubectl-craft/test/cluster"
)

const (
	// crdsDir holds the sample CRD manifests installed into the capture
	// cluster (shared with the OpenAPI corpus capture — the Gadget carries
	// the CEL rules); fixturesDir receives the recorded Status payloads.
	// Both are repo-root-relative: the mise task runs from the repo root.
	crdsDir     = "internal/schema/testdata/crds"
	fixturesDir = "internal/validate/testdata"

	// k3sManifestsDir is k3s's auto-apply directory: its deploy controller
	// watches it and applies whatever lands there. The sample CRDs are
	// copied in after start — not via k3s.WithManifest — because the image
	// declares VOLUME /var/lib/rancher/k3s, so files staged before start
	// are shadowed by the anonymous volume when the container starts.
	k3sManifestsDir = "/var/lib/rancher/k3s/server/manifests/"

	// captureTimeout and capturePoll bound each scenario's wait for its
	// failure Status: the Gadget scenario needs the installed CRD to be
	// served, and the denial scenario needs the webhook registration to
	// propagate to the admission chain.
	captureTimeout = 3 * time.Minute
	capturePoll    = 2 * time.Second
)

// statusScenario is one deliberately-invalid dry-run POST and the fixture
// file its raw failure Status is recorded to.
type statusScenario struct {
	// fixture is the file name written under internal/validate/testdata.
	fixture string
	// path is the resource collection the Manifest is POSTed to.
	path string
	// manifest is the JSON Manifest body, invalid on purpose.
	manifest string
	// expect is a substring the failure Status must carry before it is
	// recorded, so a scenario cannot capture the wrong failure (a 404
	// while the CRD is still registering, a create that was admitted
	// before the webhook engaged).
	expect string
}

// scenarios declares the recorded corpus (issue #52's minimum): a
// required-field violation, a CEL rule violation from the corpus CRDs, a
// type/enum violation with an indexed path (plus a map-key path in the same
// Status), and a webhook denial.
var scenarios = []statusScenario{
	{
		// apps/v1 Deployment with no spec.selector: the server answers
		// with FieldValueRequired causes on dotted paths.
		fixture: "deployment_required.json",
		path:    "/apis/apps/v1/namespaces/default/deployments",
		manifest: `{"apiVersion":"apps/v1","kind":"Deployment",` +
			`"metadata":{"name":"craft-capture-required"},` +
			`"spec":{"replicas":1,"template":{"metadata":{"labels":{"app":"craft"}},` +
			`"spec":{"containers":[{"name":"app","image":"nginx"}]}}}}`,
		expect: "Required value",
	},
	{
		// craft.example.com/v1 Gadget violating both of the corpus CRD's
		// CEL rules: the object-level rule (minReplicas > maxReplicas)
		// and the scalar-level profile rule.
		fixture: "gadget_cel.json",
		path:    "/apis/craft.example.com/v1/namespaces/default/gadgets",
		manifest: `{"apiVersion":"craft.example.com/v1","kind":"Gadget",` +
			`"metadata":{"name":"craft-capture-cel"},` +
			`"spec":{"minReplicas":5,"maxReplicas":1,"profile":"turbo"}}`,
		expect: "minReplicas must not exceed maxReplicas",
	},
	{
		// v1 Pod with an unsupported imagePullPolicy (an enum violation on
		// an indexed path) and a bogus resource name (a map-key path,
		// spec.containers[0].resources.limits[bogus] in the server's
		// unquoted spelling).
		fixture: "pod_enum.json",
		path:    "/api/v1/namespaces/default/pods",
		manifest: `{"apiVersion":"v1","kind":"Pod",` +
			`"metadata":{"name":"craft-capture-enum"},` +
			`"spec":{"containers":[{"name":"app","image":"nginx",` +
			`"imagePullPolicy":"Sometimes",` +
			`"resources":{"limits":{"bogus":"1"}}}]}}`,
		expect: "Unsupported value",
	},
	{
		// v1 ConfigMap labeled to match the always-deny webhook: the
		// server answers with the webhook's freeform denial — no causes,
		// no field paths, the unmappable shape.
		fixture: "configmap_webhook_denial.json",
		path:    "/api/v1/namespaces/default/configmaps",
		manifest: `{"apiVersion":"v1","kind":"ConfigMap",` +
			`"metadata":{"name":"craft-capture-denied",` +
			`"labels":{"craft.example.com/deny":"true"}},` +
			`"data":{"purpose":"webhook-denial capture"}}`,
		expect: "denied the request",
	},
}

func main() {
	log.SetFlags(0)

	// Ctrl-C (or a supervisor's TERM) cancels the context, which fails the
	// in-flight POST or Eventually poll and unwinds through run's deferred
	// teardown — the container is terminated, never leaked (with ryuk
	// disabled — the podman workaround — nothing else would clean it up).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		log.Fatalf("capture-status-fixtures: %v", err)
	}
}

// captureFailure carries a gomega assertion failure out of the Eventually
// poll as a panic, so the deferred container teardown still runs before the
// failure surfaces as an ordinary error.
type captureFailure struct{ message string }

func run(ctx context.Context) (err error) {
	defer recoverCaptureFailure(&err)

	g := gomega.NewGomega(func(message string, _ ...int) {
		panic(captureFailure{message: message})
	})

	manifests, err := sampleCRDManifests()
	if err != nil {
		return err
	}

	// The always-deny admission server starts before the cluster boots:
	// host port access must know the port at container-create time.
	webhook, err := startDenyWebhook()
	if err != nil {
		return err
	}
	defer webhook.close()

	// k3s.Run hands back a non-nil container even on error, precisely so a
	// partial boot can be terminated: register the teardown before checking
	// the error (with ryuk disabled — the podman workaround — nothing else
	// would clean up).
	container, bootErr := bootCluster(ctx, webhook.port)
	if container != nil {
		defer terminateCluster(ctx, container)
	}
	if bootErr != nil {
		return bootErr
	}

	if installErr := installSampleCRDs(ctx, container, manifests); installErr != nil {
		return installErr
	}

	client, err := clusterClient(ctx, container)
	if err != nil {
		return err
	}

	if registerErr := registerDenyWebhook(ctx, client, webhook); registerErr != nil {
		return registerErr
	}

	return captureScenarios(ctx, g, client)
}

// recoverCaptureFailure converts a gomega captureFailure panic into the
// named return error, so the deferred container teardown has already run by
// the time the failure is reported; every other panic keeps panicking.
func recoverCaptureFailure(err *error) {
	r := recover()
	if r == nil {
		return
	}
	failure, ok := r.(captureFailure)
	if !ok {
		panic(r)
	}
	*err = errors.New(failure.message)
}

// terminateCluster tears the k3s container down on every exit path,
// surviving an already-canceled context.
func terminateCluster(ctx context.Context, container *k3s.K3sContainer) {
	if err := container.Terminate(context.WithoutCancel(ctx)); err != nil {
		log.Printf("capture-status-fixtures: terminating the k3s container: %v", err)
	}
}

// sampleCRDManifests lists the checked-in sample CRD manifests to install
// into the capture cluster.
func sampleCRDManifests() ([]string, error) {
	pattern := filepath.Join(crdsDir, "*.yaml")
	manifests, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("globbing the sample CRD manifests (%s): %w", pattern, err)
	}
	if len(manifests) == 0 {
		return nil, fmt.Errorf(
			"no sample CRD manifests under %s: run from the repo root (mise run fixtures:capture-status)", crdsDir,
		)
	}
	return manifests, nil
}

// bootCluster starts the k3s container with the integration suite's
// noise-reduction args, plus host port access so the cluster's admission
// chain can reach the in-process deny webhook at
// host.testcontainers.internal.
func bootCluster(ctx context.Context, webhookPort int) (*k3s.K3sContainer, error) {
	container, err := k3s.Run(
		// The image pin is shared with the integration suite
		// (test/cluster), so the recorded Statuses and the live specs
		// describe one cluster version.
		ctx, cluster.K3sImage,
		testcontainers.WithCmdArgs(
			"--disable=traefik",
			"--disable=metrics-server",
			"--disable-helm-controller",
		),
		testcontainers.WithHostPortAccess(webhookPort),
	)
	if err != nil {
		return container, fmt.Errorf("booting the k3s container: %w", err)
	}
	return container, nil
}

// installSampleCRDs copies the sample CRD manifests into the running
// container's auto-apply directory; k3s's deploy controller notices and
// applies them, and the Gadget scenario's Eventually absorbs the
// propagation delay.
func installSampleCRDs(ctx context.Context, container *k3s.K3sContainer, manifests []string) error {
	for _, manifest := range manifests {
		target := k3sManifestsDir + filepath.Base(manifest)
		if err := container.CopyFileToContainer(ctx, manifest, target, 0o644); err != nil {
			return fmt.Errorf("installing the sample CRD manifest %s: %w", manifest, err)
		}
	}
	return nil
}

// apiClient is the thin doorway the capture run POSTs through: the
// kubeconfig's authenticated HTTP client and the API server's base URL.
type apiClient struct {
	http *http.Client
	host string
}

// clusterClient builds the authenticated API client from the container's
// kubeconfig.
func clusterClient(ctx context.Context, container *k3s.K3sContainer) (*apiClient, error) {
	kubeconfig, err := container.GetKubeConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the k3s kubeconfig: %w", err)
	}

	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("building the REST config from the kubeconfig: %w", err)
	}

	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("building the HTTP client from the REST config: %w", err)
	}
	return &apiClient{http: httpClient, host: strings.TrimSuffix(cfg.Host, "/")}, nil
}

// post POSTs one JSON body to an API path and returns the response code and
// raw body bytes.
func (c *apiClient) post(ctx context.Context, path, query, body string) (int, []byte, error) {
	url := c.host + path + "?" + query
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("building the POST to %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("POSTing to %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // a failed close of a fully-read response body loses nothing

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("reading the response from %s: %w", url, err)
	}
	return resp.StatusCode, raw, nil
}

// registerDenyWebhook creates the always-deny ValidatingWebhookConfiguration,
// scoped by objectSelector to the capture's own labeled ConfigMap so k3s's
// machinery is never caught in the deny path (failurePolicy is Fail).
func registerDenyWebhook(ctx context.Context, client *apiClient, webhook *denyWebhook) error {
	code, body, err := client.post(ctx,
		"/apis/admissionregistration.k8s.io/v1/validatingwebhookconfigurations", "",
		webhook.configuration())
	if err != nil {
		return err
	}
	if code != http.StatusCreated {
		return fmt.Errorf("registering the deny webhook: HTTP %d: %s", code, body)
	}
	return nil
}

// captureScenarios records every scenario's failure Status, polling each
// dry-run POST until the server answers with the expected failure (the CRD
// scenario needs the Gadget CRD served; the denial scenario needs the
// webhook registration active). Polling goes through gomega Eventually —
// never sleeps.
func captureScenarios(ctx context.Context, g gomega.Gomega, client *apiClient) error {
	for _, scenario := range scenarios {
		var raw []byte
		g.Eventually(func(g gomega.Gomega) {
			code, body, err := client.post(ctx, scenario.path, "dryRun=All", scenario.manifest)
			g.Expect(err).NotTo(gomega.HaveOccurred())
			g.Expect(code).To(gomega.BeNumerically(">=", 400),
				"%s must be rejected, got HTTP %d: %s", scenario.fixture, code, body)
			g.Expect(kindOf(body)).To(gomega.Equal("Status"),
				"%s must be answered with a Status body, got: %s", scenario.fixture, body)
			g.Expect(string(body)).To(gomega.ContainSubstring(scenario.expect),
				"%s must record the intended failure", scenario.fixture)
			raw = body
		}).WithContext(ctx).WithTimeout(captureTimeout).WithPolling(capturePoll).Should(gomega.Succeed())

		if err := writeFixture(scenario.fixture, raw); err != nil {
			return err
		}
	}
	return nil
}

// kindOf reads the kind of one API response body; a body that is not a JSON
// object reads as no kind at all.
func kindOf(body []byte) string {
	var typed struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(body, &typed); err != nil {
		return ""
	}
	return typed.Kind
}

// writeFixture writes one recorded Status to its fixture file — exactly as
// served, never re-marshalled, so the hermetic sweep maps what a real API
// server said.
func writeFixture(fixture string, raw []byte) error {
	if !json.Valid(raw) {
		return fmt.Errorf("recording %s: the response is not valid JSON", fixture)
	}
	if err := os.MkdirAll(fixturesDir, 0o755); err != nil {
		return fmt.Errorf("creating the fixture directory %s: %w", fixturesDir, err)
	}
	target := filepath.Join(fixturesDir, fixture)
	if err := os.WriteFile(target, raw, 0o644); err != nil {
		return fmt.Errorf("writing the fixture %s: %w", target, err)
	}
	log.Printf("recorded %s (%d bytes)", target, len(raw))
	return nil
}

// configuration spells the always-deny ValidatingWebhookConfiguration as the
// JSON body to POST: URL-based clientConfig pointing back at the in-process
// admission server through testcontainers' host gateway, trusting its
// self-signed certificate.
func (w *denyWebhook) configuration() string {
	caBundle := base64.StdEncoding.EncodeToString(w.certificatePEM)
	url := fmt.Sprintf("https://%s:%d/", testcontainers.HostInternal, w.port)
	config := map[string]any{
		"apiVersion": "admissionregistration.k8s.io/v1",
		"kind":       "ValidatingWebhookConfiguration",
		"metadata":   map[string]any{"name": "deny.craft.example.com"},
		"webhooks": []map[string]any{{
			"name":                    "deny.craft.example.com",
			"admissionReviewVersions": []string{"v1"},
			"sideEffects":             "None",
			"failurePolicy":           "Fail",
			"timeoutSeconds":          10,
			"clientConfig":            map[string]any{"url": url, "caBundle": caBundle},
			"rules": []map[string]any{{
				"apiGroups":   []string{""},
				"apiVersions": []string{"v1"},
				"operations":  []string{"CREATE"},
				"resources":   []string{"configmaps"},
				"scope":       "Namespaced",
			}},
			"objectSelector": map[string]any{
				"matchLabels": map[string]string{"craft.example.com/deny": "true"},
			},
		}},
	}
	body, err := json.Marshal(config)
	if err != nil {
		panic(err) // the literal above always marshals
	}
	return string(body)
}
