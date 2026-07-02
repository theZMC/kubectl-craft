package matchers

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/types"

	"github.com/thezmc/kubectl-craft/internal/schema"
)

// UpdateGoldenEnv is the regeneration path for golden Manifests: run the
// specs with KUBECTL_CRAFT_UPDATE_GOLDEN=1 (e.g.
// `KUBECTL_CRAFT_UPDATE_GOLDEN=1 ginkgo ./internal/schema`) and every EmitYAML
// assertion rewrites its golden file with the Manifest actually emitted
// instead of comparing — then review the diff deliberately.
const UpdateGoldenEnv = "KUBECTL_CRAFT_UPDATE_GOLDEN"

// EmitYAML succeeds when a Draft Emits byte-identically the golden Manifest
// checked in at goldenPath (resolved like any testdata path: relative to the
// spec's package directory). Byte identity is the point — Emit's determinism
// contract is part of what the golden pins. A missing or stale golden fails
// with the regeneration path (UpdateGoldenEnv) spelled out.
func EmitYAML(goldenPath string) types.GomegaMatcher {
	return &emitYAMLMatcher{goldenPath: goldenPath}
}

type emitYAMLMatcher struct {
	goldenPath string
	emitted    []byte
	golden     []byte
}

// Match Emits the Draft and compares against the golden Manifest — or, on the
// regeneration path, rewrites the golden with what was emitted.
func (matcher *emitYAMLMatcher) Match(actual any) (bool, error) {
	draft, isDraft := actual.(*schema.Draft)
	if !isDraft {
		return false, fmt.Errorf("EmitYAML matches a *schema.Draft, but got%s", format.Object(actual, 1))
	}
	emitted, err := draft.Emit()
	if err != nil {
		return false, fmt.Errorf("the Draft failed to Emit: %w", err)
	}
	matcher.emitted = emitted
	if os.Getenv(UpdateGoldenEnv) != "" {
		if regenErr := matcher.regenerate(); regenErr != nil {
			return false, regenErr
		}
		matcher.golden = emitted
		return true, nil
	}
	golden, err := os.ReadFile(matcher.goldenPath)
	if err != nil {
		return false, fmt.Errorf("reading the golden Manifest %s (set %s=1 to regenerate): %w",
			matcher.goldenPath, UpdateGoldenEnv, err)
	}
	matcher.golden = golden
	return bytes.Equal(emitted, golden), nil
}

// regenerate rewrites the golden Manifest with the emitted bytes, creating
// the golden directory when it does not exist yet.
func (matcher *emitYAMLMatcher) regenerate() error {
	if err := os.MkdirAll(filepath.Dir(matcher.goldenPath), 0o755); err != nil {
		return fmt.Errorf("regenerating the golden Manifest %s: %w", matcher.goldenPath, err)
	}
	if err := os.WriteFile(matcher.goldenPath, matcher.emitted, 0o644); err != nil {
		return fmt.Errorf("regenerating the golden Manifest %s: %w", matcher.goldenPath, err)
	}
	return nil
}

func (matcher *emitYAMLMatcher) FailureMessage(actual any) string {
	return fmt.Sprintf(
		"Expected the Draft to Emit the golden Manifest %s byte-identically (set %s=1 to regenerate).\nEmitted:\n%s\nGolden:\n%s",
		matcher.goldenPath, UpdateGoldenEnv, format.IndentString(string(matcher.emitted), 1), format.IndentString(string(matcher.golden), 1),
	)
}

func (matcher *emitYAMLMatcher) NegatedFailureMessage(actual any) string {
	return fmt.Sprintf(
		"Expected the Draft not to Emit the golden Manifest %s byte-identically.\nEmitted:\n%s",
		matcher.goldenPath, format.IndentString(string(matcher.emitted), 1),
	)
}
