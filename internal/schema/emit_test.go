package schema_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/thezmc/kubectl-craft/internal/matchers"
	"github.com/thezmc/kubectl-craft/internal/schema"
)

// golden names one checked-in golden Manifest; regenerate the set with
// KUBECTL_CRAFT_UPDATE_GOLDEN=1 (matchers.UpdateGoldenEnv).
func golden(name string) string {
	return filepath.Join("testdata", "golden", name)
}

// emitManifest Emits the Draft, insisting emission succeeds.
func emitManifest(draft *schema.Draft) []byte {
	GinkgoHelper()
	manifest, err := draft.Emit()
	Expect(err).NotTo(HaveOccurred())
	return manifest
}

// parseUnstructured insists the Manifest is YAML sigs.k8s.io/yaml parses into
// an unstructured object — the shape `kubectl apply -f` accepts.
func parseUnstructured(manifest []byte) *unstructured.Unstructured {
	GinkgoHelper()
	var object map[string]any
	Expect(sigsyaml.Unmarshal(manifest, &object)).To(Succeed())
	return &unstructured.Unstructured{Object: object}
}

// minimalDeploymentDraft composes the smallest useful Deployment: a name and
// one container image.
func minimalDeploymentDraft() *schema.Draft {
	GinkgoHelper()
	draft := deploymentDraft()
	mustSet(draft, "metadata.name", "web")
	mustSet(draft, "spec.template.spec.containers[0].name", "app")
	mustSet(draft, "spec.template.spec.containers[0].image", "nginx:1.29")
	return draft
}

// fieldedDeploymentDraft exercises arrays in index order, map keys with dots
// and slashes, int-or-string in both spellings, explicit zero values, and a
// numeric-looking string YAML must quote.
func fieldedDeploymentDraft() *schema.Draft {
	GinkgoHelper()
	draft := deploymentDraft()
	mustSet(draft, "metadata.name", "web")
	mustSet(draft, `metadata.labels["app.kubernetes.io/name"]`, "web")
	mustSet(draft, `metadata.labels["app.kubernetes.io/version"]`, "80")
	mustSet(draft, "spec.replicas", 0)
	mustSet(draft, "spec.paused", false)
	mustSet(draft, "spec.strategy.type", "RollingUpdate")
	mustSet(draft, "spec.strategy.rollingUpdate.maxSurge", "25%")
	mustSet(draft, "spec.strategy.rollingUpdate.maxUnavailable", 1)
	mustSet(draft, "spec.template.spec.containers[0].name", "app")
	mustSet(draft, "spec.template.spec.containers[0].image", "nginx:1.29")
	mustSet(draft, "spec.template.spec.containers[0].ports[0].containerPort", 8080)
	mustSet(draft, "spec.template.spec.containers[0].ports[0].name", "http")
	mustSet(draft, "spec.template.spec.containers[1].name", "sidecar")
	mustSet(draft, "spec.template.spec.containers[1].image", "envoy")
	mustSet(draft, "spec.template.spec.containers[1].workingDir", "")
	return draft
}

// graftedGadgetDraft composes a Gadget whose spec.tuning holds a raw-YAML
// graft, alongside schema-guided scalars including a number.
func graftedGadgetDraft() *schema.Draft {
	GinkgoHelper()
	draft := gadgetDraft()
	mustSet(draft, "spec.minReplicas", 1)
	mustSet(draft, "spec.maxReplicas", 3)
	mustSet(draft, "spec.efficiency", 0.85)
	mustSet(draft, "spec.gears[0]", "low")
	mustSet(draft, "spec.gears[1]", "high")
	Expect(draft.GraftYAML("spec.tuning",
		"knobs:\n  gain: 3\n  mode: turbo\nthresholds:\n  - 0.5\n  - warm\nadaptive: true\n")).To(Succeed())
	return draft
}

var _ = Describe("Emit", func() {
	When("a Draft is Emitted as a Manifest", func() {
		DescribeTable(
			"the Manifest matches its golden byte-identically and parses into an unstructured object",
			func(compose func() *schema.Draft, goldenName, wantAPIVersion, wantKind string) {
				draft := compose()

				Expect(draft).To(matchers.EmitYAML(golden(goldenName)))

				object := parseUnstructured(emitManifest(draft))
				Expect(object.GetAPIVersion()).To(Equal(wantAPIVersion),
					"apiVersion derives from the Draft's Kind")
				Expect(object.GetKind()).To(Equal(wantKind),
					"kind derives from the Draft's Kind")
			},
			Entry("an empty Draft — the identity-only Manifest",
				deploymentDraft, "empty-draft.yaml", "apps/v1", "Deployment"),
			Entry("a minimal apps/v1 Deployment — a name and one container image",
				minimalDeploymentDraft, "deployment-minimal.yaml", "apps/v1", "Deployment"),
			Entry("an apps/v1 Deployment exercising arrays, maps, int-or-string, and explicit zero values",
				fieldedDeploymentDraft, "deployment-fields.yaml", "apps/v1", "Deployment"),
			Entry("a craft.example.com/v1 Gadget with a raw-YAML graft under spec.tuning",
				graftedGadgetDraft, "gadget-graft.yaml", "craft.example.com/v1", "Gadget"),
		)
	})

	When("the sparse contract decides what appears", func() {
		It("keeps explicitly set zero values and leaves unset fields absent", func() {
			draft := deploymentDraft()
			mustSet(draft, "spec.replicas", 0)
			mustSet(draft, "spec.paused", false)
			mustSet(draft, "spec.minReadySeconds", 5)
			_, err := draft.Unset("spec.minReadySeconds")
			Expect(err).NotTo(HaveOccurred())

			object := parseUnstructured(emitManifest(draft))

			Expect(object.Object["spec"]).To(SatisfyAll(
				HaveKeyWithValue("replicas", BeNumerically("==", 0)),
				HaveKeyWithValue("paused", BeFalse()),
				Not(HaveKey("minReadySeconds")),
			), "set-ness comes from the Draft, never from value truthiness")
		})

		It("leaves instantiated-but-empty items and keys absent — nothing was set there", func() {
			draft := deploymentDraft()
			mustSet(draft, "spec.replicas", 1)
			_, err := draft.AppendItem("spec.template.spec.containers")
			Expect(err).NotTo(HaveOccurred())
			_, err = draft.AddKey("spec.template.metadata.labels", "app")
			Expect(err).NotTo(HaveOccurred())

			object := parseUnstructured(emitManifest(draft))

			Expect(object.Object["spec"]).To(SatisfyAll(
				HaveKeyWithValue("replicas", BeNumerically("==", 1)),
				Not(HaveKey("template")),
			), "an instantiated-but-empty position holds no value the sparse Manifest could carry")
		})

		It("supersedes root-level apiVersion and kind entries with the Draft's Kind", func() {
			draft := deploymentDraft()
			mustSet(draft, "apiVersion", "example.com/v9")
			mustSet(draft, "kind", "Imposter")

			manifest := emitManifest(draft)

			Expect(string(manifest)).To(Equal("apiVersion: apps/v1\nkind: Deployment\n"),
				"identity always comes from the Kind, spelled exactly once")
		})
	})

	When("the same values are composed in different orders", func() {
		It("emits byte-identical YAML — deterministic, lexically keyed output", func() {
			forward := deploymentDraft()
			mustSet(forward, "metadata.name", "web")
			mustSet(forward, `metadata.labels["app"]`, "web")
			mustSet(forward, "spec.replicas", 2)
			mustSet(forward, "spec.paused", true)

			backward := deploymentDraft()
			mustSet(backward, "spec.paused", true)
			mustSet(backward, "spec.replicas", 2)
			mustSet(backward, `metadata.labels["app"]`, "web")
			mustSet(backward, "metadata.name", "web")

			Expect(emitManifest(forward)).To(Equal(emitManifest(backward)))
			Expect(emitManifest(forward)).To(Equal(emitManifest(forward)),
				"the same Draft emits the same bytes every time")
		})
	})

	When("an int-or-string is Emitted", func() {
		DescribeTable(
			"the Manifest carries exactly the spelling that was set",
			func(input any, spelled types.GomegaMatcher) {
				draft := gadgetDraft()
				mustSet(draft, "spec.maxUnavailable", input)

				object := parseUnstructured(emitManifest(draft))

				Expect(object.Object["spec"]).To(HaveKeyWithValue("maxUnavailable", spelled))
			},
			Entry("the integer spelling stays a number", 80, BeNumerically("==", 80)),
			Entry("the percentage spelling stays a string", "80%", Equal("80%")),
			Entry("a numeric-looking string stays a string — quoted so YAML cannot reread it as a number",
				"80", Equal("80")),
		)
	})
})
