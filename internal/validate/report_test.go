package validate_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/thezmc/kubectl-craft/internal/schema"
	"github.com/thezmc/kubectl-craft/internal/validate"
)

// loadStatus reads one recorded Status fixture — the raw bytes a failed
// dry-run POST answered with, exactly as the server sent them.
func loadStatus(fixture string) metav1.Status {
	GinkgoHelper()
	raw, err := os.ReadFile(filepath.Join("testdata", fixture))
	Expect(err).NotTo(HaveOccurred())
	var status metav1.Status
	Expect(json.Unmarshal(raw, &status)).To(Succeed())
	Expect(status.Kind).To(Equal("Status"), "fixture %s must be a raw Status body", fixture)
	return status
}

// fixtureNames enumerates the recorded Status corpus dynamically, at
// spec-construction time, so a newly captured fixture is swept with no
// helper change.
func fixtureNames() []string {
	matches, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil {
		panic(err) // the pattern is fixed, and only a malformed pattern errors
	}
	fixtures := make([]string, 0, len(matches))
	for _, match := range matches {
		fixtures = append(fixtures, filepath.Base(match))
	}
	return fixtures
}

// describeFixtureTable declares a DescribeTable with one Entry per recorded
// Status fixture, so every table sweeps whatever the corpus carries.
func describeFixtureTable(description string, body func(fixture string)) {
	args := []any{body}
	for _, fixture := range fixtureNames() {
		args = append(args, Entry(fixture, fixture))
	}
	DescribeTable(description, args...)
}

// causeStatus builds a Status carrying the given causes, the shape a failed
// dry-run returns for Manifest errors; the synthetic spellings cover what
// the recorded corpus happens not to.
func causeStatus(causes ...metav1.StatusCause) metav1.Status {
	return metav1.Status{
		Status:  metav1.StatusFailure,
		Message: `Gadget.craft.example.com "sample" is invalid`,
		Reason:  metav1.StatusReasonInvalid,
		Details: &metav1.StatusDetails{Causes: causes},
		Code:    422,
	}
}

var _ = Describe("Validate mapping", func() {
	When("a dry-run failure's Status carries causes with field paths", func() {
		DescribeTable(
			"the server's error-cause spelling normalizes to a Draft-level Field Path",
			func(field, fieldPath string) {
				report := validate.MapStatus(causeStatus(metav1.StatusCause{Field: field, Message: "Required value"}))
				Expect(report.Findings).To(HaveExactElements(
					validate.Finding{FieldPath: fieldPath, Field: field, Message: "Required value"},
				))
				Expect(report.Findings[0].Mappable()).To(BeTrue())
			},
			Entry("a dotted path carries over unchanged",
				"spec.template.spec.restartPolicy", "spec.template.spec.restartPolicy"),
			Entry("a single field carries over unchanged",
				"metadata", "metadata"),
			Entry("a leading dot is the server prefixing the root, not a field",
				".spec.replicas", "spec.replicas"),
			Entry("an indexed path keeps its item indices",
				"spec.containers[0].image", "spec.containers[0].image"),
			Entry("a map key the server spells unquoted comes out double-quoted",
				"spec.containers[0].resources.limits[cpu]", `spec.containers[0].resources.limits["cpu"]`),
			Entry("a map key with non-bare-word characters is quoted the same way",
				"metadata.annotations[kubectl.kubernetes.io/last-applied]",
				`metadata.annotations["kubectl.kubernetes.io/last-applied"]`),
			Entry("a map key containing a double quote survives quoting",
				`metadata.labels[a"b]`, `metadata.labels["a\"b"]`),
			Entry("an all-digit selector is an item index, never a map key",
				"spec.gears[12]", "spec.gears[12]"),
			Entry("selectors and fields chain through each other",
				"spec.tuning[knobs][0].value", `spec.tuning["knobs"][0].value`),
		)

		DescribeTable(
			"the spellings that name no position stay unmappable, text intact",
			func(field string) {
				cause := metav1.StatusCause{Field: field, Message: "the server's text"}
				report := validate.MapStatus(causeStatus(cause))
				Expect(report.Findings).To(HaveExactElements(
					validate.Finding{Field: field, Message: "the server's text"},
				))
				Expect(report.Findings[0].Mappable()).To(BeFalse())
			},
			Entry("an empty field", ""),
			Entry("the rendered nil path", "<nil>"),
			Entry("a root-only path of dots", "..."),
			Entry("a selector that never closes", "spec.gears[0"),
			Entry("an empty selector", "spec.gears[]"),
			Entry("an empty field name between dots", "spec..name"),
			Entry("a trailing dot with no field name", "spec."),
			Entry("text after a selector that is neither '.' nor '['", "spec.gears[0]oops"),
		)

		It("preserves server cause order even when mappable and unmappable causes interleave", func() {
			report := validate.MapStatus(causeStatus(
				metav1.StatusCause{Field: "spec.minReplicas", Message: "Required value"},
				metav1.StatusCause{Field: "", Message: "a freeform denial in the middle"},
				metav1.StatusCause{Field: "spec.gears[1]", Message: "Invalid value"},
			))
			Expect(report.Findings).To(HaveExactElements(
				validate.Finding{FieldPath: "spec.minReplicas", Field: "spec.minReplicas", Message: "Required value"},
				validate.Finding{Field: "", Message: "a freeform denial in the middle"},
				validate.Finding{FieldPath: "spec.gears[1]", Field: "spec.gears[1]", Message: "Invalid value"},
			))
		})
	})

	When("a dry-run failure's Status carries no causes at all", func() {
		It("maps the freeform message to one unmappable finding, so the results pane has the denial's text", func() {
			status := metav1.Status{
				Status:  metav1.StatusFailure,
				Message: `admission webhook "deny.example.com" denied the request: no`,
				Reason:  metav1.StatusReason("Forbidden"),
				Code:    400,
			}
			report := validate.MapStatus(status)
			Expect(report.Summary).To(Equal(validate.Summary{
				Reason:  "Forbidden",
				Message: `admission webhook "deny.example.com" denied the request: no`,
				Code:    400,
			}))
			Expect(report.Findings).To(HaveExactElements(
				validate.Finding{Message: `admission webhook "deny.example.com" denied the request: no`},
			))
			Expect(report.Findings[0].Mappable()).To(BeFalse())
		})

		It("maps a message-less Status to no findings, leaving the summary to speak", func() {
			report := validate.MapStatus(metav1.Status{Status: metav1.StatusFailure, Reason: "InternalError", Code: 500})
			Expect(report.Summary).To(Equal(validate.Summary{Reason: "InternalError", Code: 500}))
			Expect(report.Findings).To(BeEmpty())
		})
	})

	When("a recorded dry-run Status from the fixture corpus is mapped", func() {
		It("sweeps a non-empty corpus", func() {
			Expect(fixtureNames()).NotTo(BeEmpty(),
				"the recorded Status corpus is missing — regenerate with `mise run fixtures:capture-status`")
		})

		describeFixtureTable("the findings partition is exactly the pinned one", func(fixture string) {
			Expect(expectedReports).To(HaveKey(fixture),
				"a recorded fixture without a pinned partition hollows out the corpus guarantee — pin it in expectedReports")
			Expect(validate.MapStatus(loadStatus(fixture))).To(Equal(expectedReports[fixture]))
		})

		describeFixtureTable("every mappable Field Path round-trips through the Draft-path parser", func(fixture string) {
			report := validate.MapStatus(loadStatus(fixture))
			for _, finding := range report.Findings {
				if !finding.Mappable() {
					continue
				}
				Expect(schema.ParseDraftPath(finding.FieldPath)).To(Succeed(),
					"the normalized Field Path %q must parse as a Draft-level Field Path", finding.FieldPath)
			}
		})
	})
})
