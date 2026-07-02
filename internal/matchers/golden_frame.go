package matchers

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/types"
)

// MatchGoldenFrame succeeds when a rendered frame matches the golden frame
// checked in at goldenPath (resolved like any testdata path: relative to
// the spec's package directory) byte for byte, after normalization:
// trailing whitespace is stripped from every line and the frame ends with
// exactly one newline. The panes pad their lines to fixed widths, and
// checked-in trailing whitespace would fight the pre-commit
// trailing-whitespace and end-of-file fixers — normalizing here keeps the
// goldens hook-proof without excluding them. A missing or stale golden
// fails with the regeneration path (UpdateGoldenEnv) spelled out.
func MatchGoldenFrame(goldenPath string) types.GomegaMatcher {
	return &goldenFrameMatcher{goldenPath: goldenPath}
}

type goldenFrameMatcher struct {
	goldenPath string
	rendered   []byte
	golden     []byte
}

// Match normalizes the rendered frame and compares it against the golden
// frame — or, on the regeneration path, rewrites the golden with what was
// rendered.
func (matcher *goldenFrameMatcher) Match(actual any) (bool, error) {
	frame, isFrame := actual.(string)
	if !isFrame {
		return false, fmt.Errorf("MatchGoldenFrame matches a rendered frame string, but got%s", format.Object(actual, 1))
	}
	matcher.rendered = normalizeFrame(frame)
	if os.Getenv(UpdateGoldenEnv) != "" {
		if regenErr := matcher.regenerate(); regenErr != nil {
			return false, regenErr
		}
		matcher.golden = matcher.rendered
		return true, nil
	}
	golden, err := os.ReadFile(matcher.goldenPath)
	if err != nil {
		return false, fmt.Errorf("reading the golden frame %s (set %s=1 to regenerate): %w",
			matcher.goldenPath, UpdateGoldenEnv, err)
	}
	matcher.golden = golden
	return bytes.Equal(matcher.rendered, golden), nil
}

// normalizeFrame strips trailing spaces and tabs from every line and pins
// the frame to exactly one trailing newline.
func normalizeFrame(frame string) []byte {
	lines := strings.Split(frame, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " \t")
	}
	return []byte(strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n")
}

// regenerate rewrites the golden frame with the normalized rendered bytes,
// creating the golden directory when it does not exist yet.
func (matcher *goldenFrameMatcher) regenerate() error {
	if err := os.MkdirAll(filepath.Dir(matcher.goldenPath), 0o755); err != nil {
		return fmt.Errorf("regenerating the golden frame %s: %w", matcher.goldenPath, err)
	}
	if err := os.WriteFile(matcher.goldenPath, matcher.rendered, 0o644); err != nil {
		return fmt.Errorf("regenerating the golden frame %s: %w", matcher.goldenPath, err)
	}
	return nil
}

func (matcher *goldenFrameMatcher) FailureMessage(actual any) string {
	return fmt.Sprintf(
		"Expected the rendered frame to match the golden frame %s byte-identically after normalization (set %s=1 to regenerate).\nRendered:\n%s\nGolden:\n%s",
		matcher.goldenPath, UpdateGoldenEnv, format.IndentString(string(matcher.rendered), 1), format.IndentString(string(matcher.golden), 1),
	)
}

func (matcher *goldenFrameMatcher) NegatedFailureMessage(actual any) string {
	return fmt.Sprintf(
		"Expected the rendered frame not to match the golden frame %s.\nRendered:\n%s",
		matcher.goldenPath, format.IndentString(string(matcher.rendered), 1),
	)
}
