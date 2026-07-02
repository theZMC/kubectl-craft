package matchers_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/matchers"
	"github.com/thezmc/kubectl-craft/internal/schema"
)

// sizedWidgetDraft composes the Widget Draft whose golden Manifest the
// matcher specs pin by hand.
func sizedWidgetDraft() *schema.Draft {
	GinkgoHelper()
	draft := widgetDraft()
	Expect(draft.Set("spec.size", 3)).To(Succeed())
	return draft
}

// sizedWidgetManifest is the Manifest sizedWidgetDraft Emits, spelled by hand
// so the matcher is pinned against known bytes, not against itself.
const sizedWidgetManifest = "apiVersion: craft.example.com/v1\nkind: Widget\nspec:\n  size: 3\n"

// writeGolden checks a golden Manifest into a temporary path.
func writeGolden(content string) string {
	GinkgoHelper()
	goldenPath := filepath.Join(GinkgoT().TempDir(), "widget.yaml")
	Expect(os.WriteFile(goldenPath, []byte(content), 0o644)).To(Succeed())
	return goldenPath
}

var _ = Describe("the EmitYAML matcher", func() {
	When("the golden Manifest holds exactly what the Draft Emits", func() {
		It("succeeds on byte identity", func() {
			Expect(sizedWidgetDraft()).To(matchers.EmitYAML(writeGolden(sizedWidgetManifest)))
		})
	})

	When("the golden Manifest differs", func() {
		It("fails, spelling the regeneration path in the message", func() {
			draft := sizedWidgetDraft()
			matcher := matchers.EmitYAML(writeGolden("apiVersion: craft.example.com/v1\nkind: Widget\n"))

			matched, err := matcher.Match(draft)

			Expect(err).NotTo(HaveOccurred())
			Expect(matched).To(BeFalse())
			Expect(matcher.FailureMessage(draft)).To(SatisfyAll(
				ContainSubstring(matchers.UpdateGoldenEnv),
				ContainSubstring("size: 3"),
			), "the failure shows both Manifests and how to regenerate deliberately")
		})
	})

	When("the regeneration path is taken", func() {
		It("rewrites the golden with the emitted Manifest and succeeds", func() {
			GinkgoT().Setenv(matchers.UpdateGoldenEnv, "1")
			goldenPath := filepath.Join(GinkgoT().TempDir(), "golden", "widget.yaml")

			Expect(sizedWidgetDraft()).To(matchers.EmitYAML(goldenPath))

			written, err := os.ReadFile(goldenPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(written)).To(Equal(sizedWidgetManifest),
				"regeneration creates the golden directory and writes what was emitted")
		})
	})

	When("the golden Manifest is missing", func() {
		It("errors with the regeneration path spelled out", func() {
			missing := filepath.Join(GinkgoT().TempDir(), "missing.yaml")

			matched, err := matchers.EmitYAML(missing).Match(sizedWidgetDraft())

			Expect(matched).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring(matchers.UpdateGoldenEnv)))
		})
	})

	When("the actual value is not a Draft", func() {
		It("errors instead of guessing", func() {
			matched, err := matchers.EmitYAML("irrelevant.yaml").Match("not a Draft")

			Expect(matched).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring("EmitYAML matches a *schema.Draft")))
		})
	})
})
