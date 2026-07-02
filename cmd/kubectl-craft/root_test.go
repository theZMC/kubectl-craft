package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/internal/tui"
)

// noShell is the sessionShell for specs that never reach a launch.
func noShell(context.Context, []data.Kind, data.Fetcher, []data.GroupVersion, *tui.DeepLink) error {
	return nil
}

// launch records everything one Session shell launch received: the
// discovered Kind list, the Fetcher sourcing group documents, the live
// /openapi/v3 index, and the resolved deep link (nil for a bare launch).
type launch struct {
	kinds   []data.Kind
	fetcher data.Fetcher
	index   []data.GroupVersion
	link    *tui.DeepLink
}

// writeKubeconfig writes a kubeconfig whose fixed context points at the
// given server, the way a Session binds to one cluster at invocation.
func writeKubeconfig(server string) string {
	GinkgoHelper()
	contents := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
  name: fake
contexts:
- context:
    cluster: fake
    user: fake
  name: fake
current-context: fake
users:
- name: fake
  user: {}
`, server)
	path := filepath.Join(GinkgoT().TempDir(), "kubeconfig")
	Expect(os.WriteFile(path, []byte(contents), 0o600)).To(Succeed())
	return path
}

// captureStdout swaps the real os.Stdout for a pipe around run and returns
// everything the process wrote to it. The command path must write nothing:
// stdout carries nothing but the Emitted Manifest, so any reintroduced
// os.Stdout printing — a resurrected banner, a stray debug line — shows up
// here. (Whether tui.Run itself binds the program to /dev/tty is proven
// end-to-end; teatest coverage is M5.)
func captureStdout(run func() error) (string, error) {
	GinkgoHelper()
	original := os.Stdout
	read, write, pipeErr := os.Pipe()
	Expect(pipeErr).NotTo(HaveOccurred())
	os.Stdout = write
	defer func() { os.Stdout = original }()

	runErr := run()

	os.Stdout = original
	Expect(write.Close()).To(Succeed())
	captured, readErr := io.ReadAll(read)
	Expect(readErr).NotTo(HaveOccurred())
	Expect(read.Close()).To(Succeed())
	return string(captured), runErr
}

// serveJSON answers one discovery or index endpoint with a fixed JSON
// body, typed so client-go's content negotiation takes the legacy
// (unaggregated) discovery path.
func serveJSON(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// craftableClusterMux mocks the two live pre-flight surfaces a Session
// resolves before its shell starts: the /openapi/v3 index and discovery
// (core v1 ConfigMap + apps/v1 Deployment, both create-capable).
func craftableClusterMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/openapi/v3", serveJSON(`{"paths":{`+
		`"api/v1":{"serverRelativeURL":"/openapi/v3/api/v1?hash=CORE1HASH"},`+
		`"apis/apps/v1":{"serverRelativeURL":"/openapi/v3/apis/apps/v1?hash=APPS1HASH"}}}`))
	mux.HandleFunc("/api", serveJSON(`{"kind":"APIVersions","versions":["v1"]}`))
	mux.HandleFunc("/api/v1", serveJSON(`{"kind":"APIResourceList","groupVersion":"v1","resources":[`+
		`{"name":"configmaps","singularName":"configmap","namespaced":true,"kind":"ConfigMap",`+
		`"verbs":["create","get","list"],"shortNames":["cm"]}]}`))
	mux.HandleFunc("/apis", serveJSON(`{"kind":"APIGroupList","groups":[{"name":"apps",`+
		`"versions":[{"groupVersion":"apps/v1","version":"v1"}],`+
		`"preferredVersion":{"groupVersion":"apps/v1","version":"v1"}}]}`))
	mux.HandleFunc("/apis/apps/v1", serveJSON(`{"kind":"APIResourceList","groupVersion":"apps/v1","resources":[`+
		`{"name":"deployments","singularName":"deployment","namespaced":true,"kind":"Deployment",`+
		`"verbs":["create","get","list"],"shortNames":["deploy"]}]}`))
	return mux
}

// executeSession runs the root command against the given kubeconfig with a
// recording sessionShell, mirroring one Session launch — extra args ride
// along as the command line's positional args. It returns whatever the
// process wrote to the real stdout during execution, everything the shell
// was launched with, and the execution error.
func executeSession(kubeconfig string, args ...string) (string, []launch, error) {
	GinkgoHelper()
	var launches []launch
	shell := func(_ context.Context, kinds []data.Kind, fetcher data.Fetcher, index []data.GroupVersion, link *tui.DeepLink) error {
		launches = append(launches, launch{kinds: kinds, fetcher: fetcher, index: index, link: link})
		return nil
	}
	cmd := newRootCommand(shell)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(append([]string{"--kubeconfig", kubeconfig}, args...))

	stdout, err := captureStdout(func() error {
		cmd.SetOut(os.Stdout)
		return cmd.Execute()
	})
	return stdout, launches, err
}

var _ = Describe("the root command", func() {
	It("exposes the standard kubectl plugin flags that fix the Session's context", func() {
		cmd := newRootCommand(noShell)

		for _, flag := range []string{"context", "kubeconfig", "namespace"} {
			Expect(cmd.Flags().Lookup(flag)).NotTo(BeNil(), "--%s should be registered", flag)
		}
	})

	When("the Session's cluster serves OpenAPI v3 Documents and discovery", func() {
		It("launches the Session shell with the discovered browsable Kinds, keeping stdout clean", func() {
			server := httptest.NewServer(craftableClusterMux())
			DeferCleanup(server.Close)

			out, launches, err := executeSession(writeKubeconfig(server.URL))

			Expect(err).NotTo(HaveOccurred())
			Expect(launches).To(HaveLen(1),
				"the shell must launch exactly once, with the Kind list resolved before the alt screen opens")
			Expect(launches[0].kinds).To(ConsistOf(
				data.Kind{
					GVK:              schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"},
					GroupVersionPath: "api/v1",
					Plural:           "configmaps",
					ShortNames:       []string{"cm"},
					Preferred:        true,
				},
				data.Kind{
					GVK:              schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
					GroupVersionPath: "apis/apps/v1",
					Plural:           "deployments",
					ShortNames:       []string{"deploy"},
					Preferred:        true,
				},
			), "the shell receives every create-capable Kind with its GVK, Document path, plural, short names, and Preferred marking")
			Expect(launches[0].link).To(BeNil(),
				"a bare launch carries no deep link — the shell opens on the Kind picker")
			Expect(launches[0].fetcher).To(BeAssignableToTypeOf(&data.Cache{}),
				"production wiring hands the shell the hash-validated disk cache over the live client (ADR-0002), "+
					"so lazy group-document fetches warm and reuse it transparently")
			Expect(launches[0].index).To(ConsistOf(
				data.GroupVersion{Path: "api/v1", ContentHash: "CORE1HASH"},
				data.GroupVersion{Path: "apis/apps/v1", ContentHash: "APPS1HASH"},
			), "the shell receives the live index so every lazy fetch is addressed by its server content hash")
			Expect(out).To(BeEmpty(),
				"the command path must write nothing to the process stdout — it is reserved for the Emitted Manifest")
		})
	})

	When("the positional deep-link arg names a Kind", func() {
		It("documents the kubectl-explain syntax in the command's help surfaces", func() {
			cmd := newRootCommand(noShell)

			Expect(cmd.Use).To(ContainSubstring("[kind[.field.path]]"))
			Expect(cmd.Example).To(ContainSubstring("kubectl craft deploy"),
				"the examples must show the kind-only deep link")
			Expect(cmd.Example).To(ContainSubstring("kubectl craft deploy.spec.strategy"),
				"the examples must show the Field Path deep link")
		})

		It("resolves a short name through discovery and launches the shell deep-linked to the Kind", func() {
			server := httptest.NewServer(craftableClusterMux())
			DeferCleanup(server.Close)

			out, launches, err := executeSession(writeKubeconfig(server.URL), "deploy")

			Expect(err).NotTo(HaveOccurred())
			Expect(launches).To(HaveLen(1))
			Expect(launches[0].link).NotTo(BeNil())
			Expect(launches[0].link.Kind.GVK).To(Equal(
				schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
			),
				"deploy must resolve to apps/v1 Deployment at the Preferred Version")
			Expect(launches[0].link.FieldPath).To(BeEmpty(),
				"a kind-only arg deep-links to the Kind's root")
			Expect(out).To(BeEmpty())
		})

		It("carries everything after the first dot as the schema-level Field Path", func() {
			server := httptest.NewServer(craftableClusterMux())
			DeferCleanup(server.Close)

			_, launches, err := executeSession(writeKubeconfig(server.URL), "deploy.spec.strategy")

			Expect(err).NotTo(HaveOccurred())
			Expect(launches).To(HaveLen(1))
			Expect(launches[0].link).NotTo(BeNil())
			Expect(launches[0].link.Kind.GVK.Kind).To(Equal("Deployment"))
			Expect(launches[0].link.FieldPath).To(Equal("spec.strategy"),
				"only the first dot-segment is the kind token; the rest is the Field Path")
		})

		It("hard-fails on an unknown kind token, naming it, before the alt screen ever opens", func() {
			server := httptest.NewServer(craftableClusterMux())
			DeferCleanup(server.Close)

			out, launches, err := executeSession(writeKubeconfig(server.URL), "gizmo.spec")

			Expect(err).To(MatchError(ContainSubstring(`unknown kind "gizmo"`)),
				"the pre-flight failure must name the token that failed to resolve")
			Expect(launches).To(BeEmpty(),
				"an unresolvable deep link must surface before the Session shell ever starts")
			Expect(out).To(BeEmpty())
		})

		It("rejects a second positional arg", func() {
			server := httptest.NewServer(craftableClusterMux())
			DeferCleanup(server.Close)

			_, launches, err := executeSession(writeKubeconfig(server.URL), "deploy", "pod")

			Expect(err).To(HaveOccurred())
			Expect(launches).To(BeEmpty())
		})
	})

	When("the Session's cluster serves the index but discovery fails", func() {
		It("hard-fails on stderr before the Session shell ever starts", func() {
			mux := http.NewServeMux()
			mux.HandleFunc("/openapi/v3", serveJSON(`{"paths":{`+
				`"api/v1":{"serverRelativeURL":"/openapi/v3/api/v1?hash=CORE1HASH"}}}`))
			mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "discovery is down", http.StatusInternalServerError)
			})
			server := httptest.NewServer(mux)
			DeferCleanup(server.Close)

			out, launches, err := executeSession(writeKubeconfig(server.URL))

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Kinds"),
				"the failure must name the Kind discovery step")
			Expect(launches).To(BeEmpty(),
				"a failed discovery must surface before the alt screen ever opens")
			Expect(out).To(BeEmpty())
		})
	})

	When("the Session's cluster does not serve /openapi/v3", func() {
		It("exits non-zero with the minimum-version message without entering the alt screen", func() {
			server := httptest.NewServer(http.NotFoundHandler())
			DeferCleanup(server.Close)

			out, launches, err := executeSession(writeKubeconfig(server.URL))

			Expect(err).To(MatchError(data.ErrOpenAPIV3NotServed))
			Expect(launches).To(BeEmpty(),
				"the capability gate must fire before the Session shell ever starts")
			Expect(out).To(BeEmpty())
		})
	})

	When("the Session's cluster is unreachable", func() {
		It("hard-fails with a clear kubectl-like connection error without entering the alt screen", func() {
			server := httptest.NewServer(http.NotFoundHandler())
			unreachable := server.URL
			server.Close()

			out, launches, err := executeSession(writeKubeconfig(unreachable))

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unable to connect to the server"))
			Expect(launches).To(BeEmpty(),
				"an unreachable cluster must hard-fail before the Session shell ever starts")
			Expect(out).To(BeEmpty())
		})
	})
})
