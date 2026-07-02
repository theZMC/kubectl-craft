// Command generate-giant-fixture (re)generates the giant fixture documents
// in internal/schema/testdata from the deterministic generator in
// test/giantcrd — the huge-CRD perf pass's corpus entry (MILESTONES.md —
// M5). Unlike the captured corpus, the giant needs no cluster: its bytes
// are exactly the generator's output, and a spec in internal/schema pins
// the checked-in files against it, so drift fails the fast loop.
//
// It is a plain Go program under hack/ (run explicitly via
// `mise run fixtures:generate-giant`, from the repo root) so regeneration
// stays a deliberate act, like the captures.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/thezmc/kubectl-craft/test/giantcrd"
)

// fixturesDir receives the generated group documents, repo-root-relative:
// the mise task runs from the repo root.
const fixturesDir = "internal/schema/testdata"

func main() {
	log.SetFlags(0)
	if err := run(); err != nil {
		log.Fatalf("generate-giant-fixture: %v", err)
	}
}

func run() error {
	for _, version := range giantcrd.Versions {
		raw, err := giantcrd.Document(version)
		if err != nil {
			return fmt.Errorf("generating the giant %s document: %w", version, err)
		}
		target := filepath.Join(fixturesDir, giantcrd.FixtureName(version))
		if err := os.WriteFile(target, raw, 0o644); err != nil {
			return fmt.Errorf("writing the fixture %s: %w", target, err)
		}
		log.Printf("generated %s (%d bytes)", target, len(raw))
	}
	return nil
}
