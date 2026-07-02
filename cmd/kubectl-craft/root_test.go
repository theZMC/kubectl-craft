package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/thezmc/kubectl-craft/internal/data"
)

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

// executeSession runs the root command against the given kubeconfig and
// returns its stdout and error, mirroring one Session launch.
func executeSession(kubeconfig string) (string, error) {
	GinkgoHelper()
	out := &bytes.Buffer{}
	streams := genericiooptions.IOStreams{In: &bytes.Buffer{}, Out: out, ErrOut: io.Discard}
	cmd := newRootCommand(streams)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--kubeconfig", kubeconfig})
	err := cmd.Execute()
	return out.String(), err
}

var _ = Describe("the root command", func() {
	It("exposes the standard kubectl plugin flags that fix the Session's context", func() {
		cmd := newRootCommand(genericiooptions.NewTestIOStreamsDiscard())

		for _, flag := range []string{"context", "kubeconfig", "namespace"} {
			Expect(cmd.Flags().Lookup(flag)).NotTo(BeNil(), "--%s should be registered", flag)
		}
	})

	Context("when the Session's cluster serves OpenAPI v3 Documents", func() {
		It("connects and reports the group versions in the live index", func() {
			mux := http.NewServeMux()
			mux.HandleFunc("/openapi/v3", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"paths":{` +
					`"api/v1":{"serverRelativeURL":"/openapi/v3/api/v1?hash=CORE1HASH"},` +
					`"apis/apps/v1":{"serverRelativeURL":"/openapi/v3/apis/apps/v1?hash=APPS1HASH"}}}`))
			})
			server := httptest.NewServer(mux)
			DeferCleanup(server.Close)

			out, err := executeSession(writeKubeconfig(server.URL))

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("2 group versions serve OpenAPI v3 Documents"))
		})
	})

	Context("when the Session's cluster does not serve /openapi/v3", func() {
		It("exits non-zero with the minimum-version message instead of launching a TUI", func() {
			server := httptest.NewServer(http.NotFoundHandler())
			DeferCleanup(server.Close)

			out, err := executeSession(writeKubeconfig(server.URL))

			Expect(err).To(MatchError(data.ErrOpenAPIV3NotServed))
			Expect(out).To(BeEmpty())
		})
	})

	Context("when the Session's cluster is unreachable", func() {
		It("hard-fails with a clear kubectl-like connection error instead of launching a TUI", func() {
			server := httptest.NewServer(http.NotFoundHandler())
			unreachable := server.URL
			server.Close()

			out, err := executeSession(writeKubeconfig(unreachable))

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unable to connect to the server"))
			Expect(out).To(BeEmpty())
		})
	})
})
