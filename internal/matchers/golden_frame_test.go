package matchers_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/matchers"
)

// paddedFrame is a rendered frame the way the panes actually emit one:
// lines padded with trailing spaces and no trailing newline.
const paddedFrame = "> hpa   \n  HorizontalPodAutoscaler  autoscaling/v2 (hpa)   "

// normalizedFrame is paddedFrame after the matcher's normalization:
// trailing whitespace stripped, exactly one trailing newline.
const normalizedFrame = "> hpa\n  HorizontalPodAutoscaler  autoscaling/v2 (hpa)\n"

// writeGoldenFrame checks a golden frame into a temporary path.
func writeGoldenFrame(content string) string {
	GinkgoHelper()
	goldenPath := filepath.Join(GinkgoT().TempDir(), "frame.golden")
	Expect(os.WriteFile(goldenPath, []byte(content), 0o644)).To(Succeed())
	return goldenPath
}

var _ = Describe("the MatchGoldenFrame matcher", func() {
	When("the golden frame holds exactly the normalized rendering", func() {
		It("succeeds on byte identity, insensitive to trailing pad whitespace", func() {
			Expect(paddedFrame).To(matchers.MatchGoldenFrame(writeGoldenFrame(normalizedFrame)))
		})
	})

	When("the golden frame differs", func() {
		It("fails, spelling the regeneration path in the message", func() {
			matcher := matchers.MatchGoldenFrame(writeGoldenFrame("> deploy\n"))

			matched, err := matcher.Match(paddedFrame)

			Expect(err).NotTo(HaveOccurred())
			Expect(matched).To(BeFalse())
			Expect(matcher.FailureMessage(paddedFrame)).To(SatisfyAll(
				ContainSubstring(matchers.UpdateGoldenEnv),
				ContainSubstring("HorizontalPodAutoscaler"),
			), "the failure shows both frames and how to regenerate deliberately")
		})
	})

	When("the regeneration path is taken", func() {
		It("rewrites the golden with the normalized frame and succeeds", func() {
			GinkgoT().Setenv(matchers.UpdateGoldenEnv, "1")
			goldenPath := filepath.Join(GinkgoT().TempDir(), "golden", "frame.golden")

			Expect(paddedFrame).To(matchers.MatchGoldenFrame(goldenPath))

			written, err := os.ReadFile(goldenPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(written)).To(Equal(normalizedFrame),
				"regeneration creates the golden directory and writes the normalized frame — "+
					"never trailing whitespace the pre-commit fixers would fight over")
		})
	})

	When("the golden frame is missing", func() {
		It("errors with the regeneration path spelled out", func() {
			missing := filepath.Join(GinkgoT().TempDir(), "missing.golden")

			matched, err := matchers.MatchGoldenFrame(missing).Match(paddedFrame)

			Expect(matched).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring(matchers.UpdateGoldenEnv)))
		})
	})

	When("the actual value is not a rendered frame", func() {
		It("errors instead of guessing", func() {
			matched, err := matchers.MatchGoldenFrame("irrelevant.golden").Match(42)

			Expect(matched).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring("MatchGoldenFrame matches a rendered frame string")))
		})
	})
})
