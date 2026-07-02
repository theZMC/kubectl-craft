// Package cluster pins the conformant cluster that every cluster-facing
// tool boots: the integration suite (test/integration) and the
// fixture-capture tool (hack/capture-fixtures) both import this pin, so the
// captured corpus and the live specs always describe one cluster version —
// the image cannot be bumped in one place without the other.
package cluster

// K3sImage is the pinned k3s image. The version matrix (oldest-supported +
// latest) is a later CI concern; everything here only assumes a cluster
// that serves OpenAPI v3 Documents.
const K3sImage = "rancher/k3s:v1.36.2-k3s1"
