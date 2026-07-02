// Package integration_test is the repo's single cluster-facing suite
// (DESIGN.md — Testing): every spec carries Label("integration") so the
// fast hermetic loop (`ginkgo --label-filter='!integration' ./...`) never
// needs Docker, and the whole test binary shares one k3s container booted
// via testcontainers-go.
package integration_test

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
)

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)

	// Cluster-facing assertions poll through Eventually (never sleeps);
	// generous defaults absorb the API server settling after boot.
	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(2 * time.Second)

	// The suite-level label puts every spec in this suite behind the
	// integration gate.
	RunSpecs(t, "Integration Suite", Label("integration"))
}

// defaultSpecTimeout bounds each cluster-facing spec so a wedged API server
// fails the spec instead of hanging the run.
const defaultSpecTimeout = 3 * time.Minute

// k3sImage pins the conformant cluster the suite runs against. The version
// matrix (oldest-supported + latest) is a later CI concern; the suite itself
// only assumes a cluster that serves OpenAPI v3 Documents.
const k3sImage = "rancher/k3s:v1.36.2-k3s1"

// k3sContainer is owned by parallel process 1, which boots and tears down
// the one shared container; it stays nil on every other process.
var k3sContainer *k3s.K3sContainer

// kubeconfigBytes is the Session's doorway into the shared cluster,
// broadcast from process 1 to every parallel process.
var kubeconfigBytes []byte

var _ = SynchronizedBeforeSuite(func(ctx SpecContext) []byte {
	container, err := k3s.Run(
		ctx, k3sImage,
		// Silence the components that would add schema noise to the
		// /openapi/v3 index. The module already disables traefik; it is
		// repeated here so the full noise-reduction list reads in one
		// place and survives a module-default change.
		testcontainers.WithCmdArgs(
			"--disable=traefik",
			"--disable=metrics-server",
			"--disable-helm-controller",
		),
	)
	// k3s.Run hands back a non-nil container even on error, precisely so
	// a partial boot can be terminated: assign the handle before asserting
	// so SynchronizedAfterSuite cleans up on every path (with ryuk
	// disabled — the podman workaround — nothing else would).
	k3sContainer = container
	Expect(err).NotTo(HaveOccurred())

	kubeconfig, err := container.GetKubeConfig(ctx)
	Expect(err).NotTo(HaveOccurred())

	return kubeconfig
}, func(kubeconfig []byte) {
	kubeconfigBytes = kubeconfig
}, NodeTimeout(5*time.Minute))

var _ = SynchronizedAfterSuite(func() {
	// Nothing per-process: specs are read-mostly (ADR-0004) and hold no
	// per-process cluster state.
}, func(ctx SpecContext) {
	if k3sContainer != nil {
		Expect(k3sContainer.Terminate(ctx)).To(Succeed())
	}
}, NodeTimeout(2*time.Minute))
