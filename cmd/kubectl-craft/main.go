// Package main is the entrypoint for the kubectl-craft plugin binary
// (named per ADR-0005). It wires the layers together: the data layer
// (internal/data), the pure compose core (internal/schema), and the
// Bubble Tea presentation layer (internal/tui).
package main

import (
	"fmt"
	"os"

	"github.com/thezmc/kubectl-craft/internal/tui"
)

// version is injected at build time by goreleaser via ldflags
// (-X main.version=...); "dev" identifies a locally built binary.
var version = "dev"

func main() {
	if err := newRootCommand(tui.Run).Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
