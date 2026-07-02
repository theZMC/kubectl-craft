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
)

// noShell is the sessionShell for specs that never reach a launch.
func noShell(context.Context, []data.Kind) error { return nil }

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
// recording sessionShell, mirroring one Session launch. It returns whatever
// the process wrote to the real stdout during execution, the Kind lists
// the shell was launched with, and the execution error.
func executeSession(kubeconfig string) (string, [][]data.Kind, error) {
	GinkgoHelper()
	var launches [][]data.Kind
	shell := func(_ context.Context, kinds []data.Kind) error {
		launches = append(launches, kinds)
		return nil
	}
	cmd := newRootCommand(shell)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--kubeconfig", kubeconfig})

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
			Expect(launches[0]).To(ConsistOf(
				data.Kind{
					GVK:              schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"},
					GroupVersionPath: "api/v1",
					ShortNames:       []string{"cm"},
					Preferred:        true,
				},
				data.Kind{
					GVK:              schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
					GroupVersionPath: "apis/apps/v1",
					ShortNames:       []string{"deploy"},
					Preferred:        true,
				},
			), "the shell receives every create-capable Kind with its GVK, Document path, short names, and Preferred marking")
			Expect(out).To(BeEmpty(),
				"the command path must write nothing to the process stdout — it is reserved for the Emitted Manifest")
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
