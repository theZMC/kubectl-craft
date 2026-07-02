// Command capture-fixtures (re)generates the checked-in fixture corpus of
// raw OpenAPI v3 group documents that backs the hermetic schema specs
// (DESIGN.md — Testing).
//
// It boots the same k3s image the integration suite uses, installs the
// sample CRD manifests from internal/schema/testdata/crds by staging them
// into k3s's auto-apply manifests directory, waits for every corpus
// group-version to appear in the live /openapi/v3 index (gomega Eventually —
// never sleeps), and writes each group's OpenAPI
// v3 Document to internal/schema/testdata as raw response bytes. The bytes
// are never pretty-printed or re-marshalled: the server content hash
// describes raw content (the cache rule, ADR-0002), so raw is what keeps the
// corpus honest.
//
// It is a plain Go program under hack/ (run explicitly via
// `mise run fixtures:capture`, from the repo root) rather than a spec, so
// the fast hermetic loop (`ginkgo --label-filter='!integration' ./...`)
// never touches Docker.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/test/cluster"
)

const (
	// crdsDir holds the sample CRD manifests installed into the capture
	// cluster; fixturesDir receives the captured group documents. Both are
	// repo-root-relative: the mise task runs from the repo root.
	crdsDir     = "internal/schema/testdata/crds"
	fixturesDir = "internal/schema/testdata"

	// k3sManifestsDir is k3s's auto-apply directory: its deploy controller
	// watches it and applies whatever lands there. The sample CRDs are
	// copied in after start — not via k3s.WithManifest — because the image
	// declares VOLUME /var/lib/rancher/k3s, so files staged before start
	// are shadowed by the anonymous volume when the container starts.
	k3sManifestsDir = "/var/lib/rancher/k3s/server/manifests/"

	// indexTimeout and indexPoll bound the wait for the corpus
	// group-versions to appear in the live index; the sample-CRD
	// documents show up only after the API server has processed the
	// installed CRDs.
	indexTimeout = 3 * time.Minute
	indexPoll    = 2 * time.Second
)

// corpusGroupVersions declares the corpus: the group-version paths captured
// into testdata, exactly as the live /openapi/v3 index names them. apps/v1
// covers the workloads, apiextensions.k8s.io/v1 carries the JSONSchemaProps
// $ref cycle, and the craft.example.com documents carry the sample-CRD
// shapes (int-or-string, preserve-unknown-fields, CEL, and the
// multi-version Widget).
var corpusGroupVersions = []string{
	"apis/apiextensions.k8s.io/v1",
	"apis/apps/v1",
	"apis/craft.example.com/v1",
	"apis/craft.example.com/v2",
}

// fixtureFileName maps a group-version path from the live index to its
// fixture filename: path separators become underscores, e.g. "apis/apps/v1"
// → "apis_apps_v1.json". Groups are DNS names and versions are
// alphanumeric — neither contains an underscore — so the mapping
// round-trips.
func fixtureFileName(groupVersionPath string) string {
	return strings.ReplaceAll(groupVersionPath, "/", "_") + ".json"
}

func main() {
	log.SetFlags(0)

	// Ctrl-C (or a supervisor's TERM) cancels the context, which fails the
	// in-flight fetch or Eventually poll and unwinds through run's deferred
	// teardown — the container is terminated, never leaked (with ryuk
	// disabled — the podman workaround — nothing else would clean it up).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		log.Fatalf("capture-fixtures: %v", err)
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

	// k3s.Run hands back a non-nil container even on error, precisely so a
	// partial boot can be terminated: register the teardown before checking
	// the error (with ryuk disabled — the podman workaround — nothing else
	// would clean up).
	container, bootErr := bootCluster(ctx)
	if container != nil {
		defer terminateCluster(ctx, container)
	}
	if bootErr != nil {
		return bootErr
	}

	if installErr := installSampleCRDs(ctx, container, manifests); installErr != nil {
		return installErr
	}

	return captureCorpus(ctx, g, container)
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
		log.Printf("capture-fixtures: terminating the k3s container: %v", err)
	}
}

// captureCorpus waits for the corpus to be served and writes every declared
// group document to its fixture file.
func captureCorpus(ctx context.Context, g gomega.Gomega, container *k3s.K3sContainer) error {
	client, err := clusterClient(ctx, container)
	if err != nil {
		return err
	}

	byPath := waitForCorpus(ctx, g, client)

	for _, groupVersionPath := range corpusGroupVersions {
		if err := captureGroupDocument(ctx, client, byPath[groupVersionPath]); err != nil {
			return err
		}
	}

	return nil
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
			"no sample CRD manifests under %s: run from the repo root (mise run fixtures:capture)", crdsDir,
		)
	}
	return manifests, nil
}

// bootCluster starts the k3s container with the integration suite's
// noise-reduction args.
func bootCluster(ctx context.Context) (*k3s.K3sContainer, error) {
	container, err := k3s.Run(
		// The image pin is shared with the integration suite
		// (test/cluster), so the captured corpus and the live specs
		// describe one cluster version.
		ctx, cluster.K3sImage,
		// Mirror test/integration's args so the captured /openapi/v3
		// index has the same shape the integration suite sees.
		testcontainers.WithCmdArgs(
			"--disable=traefik",
			"--disable=metrics-server",
			"--disable-helm-controller",
		),
	)
	if err != nil {
		return container, fmt.Errorf("booting the k3s container: %w", err)
	}
	return container, nil
}

// installSampleCRDs copies the sample CRD manifests into the running
// container's auto-apply directory; k3s's deploy controller notices and
// applies them, and waitForCorpus absorbs the propagation delay.
func installSampleCRDs(ctx context.Context, container *k3s.K3sContainer, manifests []string) error {
	for _, manifest := range manifests {
		target := k3sManifestsDir + filepath.Base(manifest)
		if err := container.CopyFileToContainer(ctx, manifest, target, 0o644); err != nil {
			return fmt.Errorf("installing the sample CRD manifest %s: %w", manifest, err)
		}
	}
	return nil
}

// clusterClient builds the Session's data client from the container's
// kubeconfig, the same way `kubectl craft` builds it from the resolved
// context's REST config.
func clusterClient(ctx context.Context, container *k3s.K3sContainer) (*data.Client, error) {
	kubeconfig, err := container.GetKubeConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the k3s kubeconfig: %w", err)
	}

	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("building the REST config from the kubeconfig: %w", err)
	}

	client, err := data.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("building the cluster data client: %w", err)
	}
	return client, nil
}

// waitForCorpus polls the live /openapi/v3 index until every corpus
// group-version is served with a content hash, and returns the index keyed
// by path. Polling goes through gomega Eventually — never sleeps.
func waitForCorpus(ctx context.Context, g gomega.Gomega, client *data.Client) map[string]data.GroupVersion {
	byPath := make(map[string]data.GroupVersion)

	g.Eventually(func(g gomega.Gomega) {
		groups, err := client.FetchIndex(ctx)
		g.Expect(err).NotTo(gomega.HaveOccurred())

		clear(byPath)
		for _, gv := range groups {
			byPath[gv.Path] = gv
		}

		for _, want := range corpusGroupVersions {
			g.Expect(byPath).To(gomega.HaveKey(want),
				"the corpus group-version %q must appear in the live index", want)
			g.Expect(byPath[want].ContentHash).NotTo(gomega.BeEmpty(),
				"the corpus group-version %q must carry a server content hash", want)
		}
	}).WithContext(ctx).WithTimeout(indexTimeout).WithPolling(indexPoll).Should(gomega.Succeed())

	return byPath
}

// captureGroupDocument fetches one group's OpenAPI v3 Document at its server
// content hash and writes the raw response bytes to the fixture file —
// exactly as served, never re-marshalled, because the server hash describes
// raw content.
func captureGroupDocument(ctx context.Context, client *data.Client, gv data.GroupVersion) error {
	raw, err := client.FetchGroupDocument(ctx, gv.Path, gv.ContentHash)
	if err != nil {
		return fmt.Errorf("capturing %q: %w", gv.Path, err)
	}
	if !json.Valid(raw) {
		return fmt.Errorf("capturing %q: the response is not valid JSON", gv.Path)
	}

	target := filepath.Join(fixturesDir, fixtureFileName(gv.Path))
	if err := os.WriteFile(target, raw, 0o644); err != nil {
		return fmt.Errorf("writing the fixture %s: %w", target, err)
	}

	log.Printf("captured %s @ %s (%d bytes) -> %s", gv.Path, gv.ContentHash, len(raw), target)
	return nil
}
