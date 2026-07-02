// Package main is the entrypoint for the kubectl-craft plugin binary
// (named per ADR-0005). It wires the layers together: the data layer
// (internal/data), the pure compose core (internal/schema), and the
// Bubble Tea presentation layer (internal/tui).
package main

import (
	"fmt"
	"os"

	"k8s.io/cli-runtime/pkg/genericiooptions"
)

// version is injected at build time by goreleaser via ldflags
// (-X main.version=...); "dev" identifies a locally built binary.
var version = "dev"

// placeholder returns the walking-skeleton banner printed until the
// binary can Compose Manifests from a cluster's Type Schemas.
func placeholder() string {
	return fmt.Sprintf(
		"kubectl-craft %s: compose Kubernetes Manifests from your cluster's live Type Schemas (walking skeleton)",
		version,
	)
}

func main() {
	streams := genericiooptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr}
	if err := newRootCommand(streams).Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
