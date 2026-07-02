// Package main is the entrypoint for the kubectl-craft plugin binary
// (named per ADR-0005). It wires the layers together: the data layer
// (internal/data), the pure compose core (internal/schema), and the
// Bubble Tea presentation layer (internal/tui).
package main

import "fmt"

// placeholder returns the walking-skeleton banner printed until the
// binary can Compose Manifests from a cluster's Type Schemas.
func placeholder() string {
	return "kubectl-craft: compose Kubernetes Manifests from your cluster's live Type Schemas (walking skeleton)"
}

func main() {
	fmt.Println(placeholder())
}
