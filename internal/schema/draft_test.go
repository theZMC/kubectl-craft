package schema_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/matchers"
	"github.com/thezmc/kubectl-craft/internal/schema"
)

// newDraft binds an empty Draft to a fixture Kind's field tree.
func newDraft(fixture string, kind schema.GroupVersionKind) *schema.Draft {
	GinkgoHelper()
	return schema.NewDraft(growFieldTree(fixture, kind), kind)
}

// deploymentDraft is the apps/v1 Deployment Draft most specs compose against.
func deploymentDraft() *schema.Draft {
	GinkgoHelper()
	return newDraft("apis_apps_v1.json", gvk("apps", "v1", "Deployment"))
}

// gadgetDraft is the craft.example.com/v1 Gadget Draft — the constraint and
// raw-YAML escape-hatch sample.
func gadgetDraft() *schema.Draft {
	GinkgoHelper()
	return newDraft("apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"))
}

// mustSet fills one value, insisting the Type Schema admits it.
func mustSet(draft *schema.Draft, fieldPath string, value any) {
	GinkgoHelper()
	Expect(draft.Set(fieldPath, value)).To(Succeed())
}

var _ = Describe("the Draft", func() {
	When("scalar values are set, read back, and unset against a Kind's field tree", func() {
		DescribeTable(
			"a set value reads back carrying its schema type, and unset removes the entry entirely",
			func(fixture string, kind schema.GroupVersionKind, fieldPath string, input any, wantType string, wantData any) {
				draft := newDraft(fixture, kind)

				Expect(draft.Set(fieldPath, input)).To(Succeed())

				value, filled := draft.ValueAt(fieldPath)
				Expect(filled).To(BeTrue())
				Expect(value.Type).To(Equal(wantType), "the value must carry its schema type for emission")
				Expect(value.Data).To(Equal(wantData))
				Expect(draft).To(matchers.HaveValueAt(fieldPath, wantData))

				discarded, err := draft.Unset(fieldPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(discarded).To(Equal(1))
				Expect(draft).NotTo(matchers.HaveValueAt(fieldPath))
				Expect(draft.Instantiated()).NotTo(ContainElement(fieldPath),
					"unset removes the entry entirely — sparse semantics, never set-to-empty")
			},
			Entry("an integer on the apps/v1 Deployment",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				"spec.replicas", 3, "integer", int64(3)),
			Entry("an integral float normalizes to the integer it spells",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				"spec.replicas", float64(4), "integer", int64(4)),
			Entry("a boolean on the apps/v1 Deployment",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				"spec.paused", true, "boolean", true),
			Entry("a string leaf on the craft.example.com/v1 Widget",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Widget"),
				"spec.paint", "red", "string", "red"),
			Entry("a number on the craft.example.com/v1 Gadget",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				"spec.efficiency", 0.85, "number", 0.85),
			Entry("an int-or-string in its integer spelling",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				"spec.maxUnavailable", 2, "int-or-string", int64(2)),
			Entry("an int-or-string in its string spelling",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				"spec.maxUnavailable", "25%", "int-or-string", "25%"),
			Entry("an enum value among the admissible ones, deep under an implicitly appended item",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				"spec.template.spec.containers[0].imagePullPolicy", "IfNotPresent", "string", "IfNotPresent"),
			Entry("an array item of a scalar-typed array",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				"spec.gears[0]", "high", "string", "high"),
			Entry("a map value under a quoted key selector",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				`spec.template.metadata.labels["app.kubernetes.io/name"]`, "web", "string", "web"),
		)

		It("replaces the value when the same Field Path is set again", func() {
			draft := deploymentDraft()
			mustSet(draft, "spec.replicas", 1)

			mustSet(draft, "spec.replicas", 5)

			Expect(draft).To(matchers.HaveValueAt("spec.replicas", int64(5)))
			Expect(draft.Instantiated()).To(Equal([]string{"spec.replicas"}))
		})
	})

	When("a subtree with filled descendants is unset", func() {
		It("discards the whole subtree and reports how many values were discarded", func() {
			draft := deploymentDraft()
			mustSet(draft, "spec.replicas", 3)
			mustSet(draft, "spec.paused", true)
			mustSet(draft, "spec.template.spec.containers[0].name", "app")
			mustSet(draft, "spec.template.spec.containers[0].image", "nginx")

			discarded, err := draft.Unset("spec")

			Expect(err).NotTo(HaveOccurred())
			Expect(discarded).To(Equal(4), "the discard count feeds the TUI's destructive-key confirm")
			Expect(draft.Instantiated()).To(BeEmpty())
			Expect(draft.MissingRequired()).To(matchers.BeMissingRequired())
		})

		It("counts a raw-YAML graft as one discarded value", func() {
			draft := gadgetDraft()
			mustSet(draft, "spec.minReplicas", 1)
			Expect(draft.GraftYAML("spec.tuning", "knobs:\n  gain: 3\n  mode: turbo\n")).To(Succeed())

			discarded, err := draft.Unset("spec")

			Expect(err).NotTo(HaveOccurred())
			Expect(discarded).To(Equal(2), "one scalar plus one graft — the graft was one confirmed entry")
		})

		It("discards nothing when an instantiated-but-empty item is unset", func() {
			draft := deploymentDraft()
			itemPath, err := draft.AppendItem("spec.template.spec.containers")
			Expect(err).NotTo(HaveOccurred())

			discarded, err := draft.Unset(itemPath)

			Expect(err).NotTo(HaveOccurred())
			Expect(discarded).To(BeZero())
			Expect(draft.Instantiated()).To(BeEmpty())
		})

		It("errors when the Draft holds nothing at the Field Path", func() {
			draft := deploymentDraft()

			_, err := draft.Unset("spec.replicas")

			Expect(err).To(MatchError(ContainSubstring("the Draft holds nothing at")))
		})

		It("de-instantiates ancestor fields left empty, but keeps explicit items", func() {
			draft := deploymentDraft()
			mustSet(draft, "spec.template.spec.containers[0].name", "app")

			discarded, err := draft.Unset("spec.template.spec.containers[0].name")

			Expect(err).NotTo(HaveOccurred())
			Expect(discarded).To(Equal(1))
			Expect(draft.Instantiated()).To(Equal([]string{"spec.template.spec.containers[0]"}),
				"the item was an explicit position: it stays instantiated when its fields go")

			_, err = draft.Unset("spec.template.spec.containers[0]")
			Expect(err).NotTo(HaveOccurred())
			Expect(draft.Instantiated()).To(BeEmpty(),
				"removing the last item de-instantiates the implicit ancestor chain")
		})
	})

	When("array items and map keys live their lifecycle", func() {
		It("appends items yielding [n] paths, and removing an item renumbers the items after it", func() {
			draft := deploymentDraft()

			first, err := draft.AppendItem("spec.template.spec.containers")
			Expect(err).NotTo(HaveOccurred())
			Expect(first).To(Equal("spec.template.spec.containers[0]"))
			second, err := draft.AppendItem("spec.template.spec.containers")
			Expect(err).NotTo(HaveOccurred())
			Expect(second).To(Equal("spec.template.spec.containers[1]"))
			mustSet(draft, first+".name", "one")
			mustSet(draft, second+".name", "two")

			discarded, err := draft.Unset(first)

			Expect(err).NotTo(HaveOccurred())
			Expect(discarded).To(Equal(1))
			Expect(draft).To(matchers.HaveValueAt("spec.template.spec.containers[0].name", "two"),
				"the renumbering contract: the items after a removed item shift down by one")
			Expect(draft.Instantiated()).To(Equal([]string{"spec.template.spec.containers[0].name"}))
		})

		It("adds map keys yielding quoted-key paths, and removes them by key", func() {
			draft := deploymentDraft()

			keyPath, err := draft.AddKey("spec.template.metadata.labels", "app.kubernetes.io/name")
			Expect(err).NotTo(HaveOccurred())
			Expect(keyPath).To(Equal(`spec.template.metadata.labels["app.kubernetes.io/name"]`))
			Expect(draft.Instantiated()).To(Equal([]string{keyPath}),
				"an added key is instantiated before any value fills it")

			mustSet(draft, keyPath, "web")
			Expect(draft).To(matchers.HaveValueAt(keyPath, "web"))

			discarded, err := draft.Unset(keyPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(discarded).To(Equal(1))
			Expect(draft.Instantiated()).To(BeEmpty())
		})

		It("rejects adding a key the Draft already holds", func() {
			draft := deploymentDraft()
			_, err := draft.AddKey("spec.template.metadata.labels", "app")
			Expect(err).NotTo(HaveOccurred())

			_, err = draft.AddKey("spec.template.metadata.labels", "app")

			Expect(err).To(MatchError(ContainSubstring("already holds")))
		})

		DescribeTable(
			"item and key mutation is validated against the field tree",
			func(verb, fieldPath, complaint string) {
				draft := deploymentDraft()

				var err error
				switch verb {
				case "append an item":
					_, err = draft.AppendItem(fieldPath)
				default:
					_, err = draft.AddKey(fieldPath, "web")
				}

				Expect(err).To(MatchError(ContainSubstring(complaint)))
			},
			Entry("appending an item to a scalar errors",
				"append an item", "spec.replicas", "is not an array"),
			Entry("appending an item to a plain object errors",
				"append an item", "spec", "is not an array"),
			Entry("appending an item to a map-shaped object errors",
				"append an item", "spec.template.metadata.labels", "is not an array"),
			Entry("adding a key to an array errors",
				"add a key", "spec.template.spec.containers", "is not a map-shaped object"),
			Entry("adding a key to a scalar errors",
				"add a key", "spec.replicas", "is not a map-shaped object"),
		)

		It("keeps arrays dense: an item index past the next free one cannot instantiate", func() {
			draft := deploymentDraft()

			err := draft.Set("spec.template.spec.containers[2].name", "gap")

			Expect(err).To(MatchError(ContainSubstring("stay dense")))
			Expect(draft.Instantiated()).To(BeEmpty(), "a rejected set instantiates nothing")
		})
	})

	When("the tree reads collection state back from the Draft", func() {
		It("counts instantiated items at an array position, empty items included", func() {
			draft := deploymentDraft()
			Expect(draft.ItemCount("spec.template.spec.containers")).To(BeZero(),
				"a position the Draft holds nothing at counts zero")

			_, err := draft.AppendItem("spec.template.spec.containers")
			Expect(err).NotTo(HaveOccurred())
			mustSet(draft, "spec.template.spec.containers[1].name", "app")

			Expect(draft.ItemCount("spec.template.spec.containers")).To(Equal(2),
				"the count is Draft-level: an instantiated-but-empty item counts, whatever emission compacts")
		})

		It("lists instantiated map keys sorted, and nothing where the Draft holds no map", func() {
			draft := deploymentDraft()
			Expect(draft.Keys("spec.template.metadata.labels")).To(BeEmpty())

			for _, key := range []string{"zone", "app"} {
				_, err := draft.AddKey("spec.template.metadata.labels", key)
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(draft.Keys("spec.template.metadata.labels")).To(Equal([]string{"app", "zone"}),
				"keys list in the same sorted order Instantiated spells them")
		})

		It("reports the discard count an unset would report, before anything is unset", func() {
			draft := deploymentDraft()
			_, held := draft.DiscardCount("spec")
			Expect(held).To(BeFalse(), "a position the Draft holds nothing at is unset's no-op")

			mustSet(draft, "spec.replicas", 3)
			mustSet(draft, "spec.template.spec.containers[0].name", "app")

			count, held := draft.DiscardCount("spec")
			Expect(held).To(BeTrue())
			Expect(count).To(Equal(2), "the destructive-key confirm asks here before unsetting")

			count, held = draft.DiscardCount("spec.replicas")
			Expect(held).To(BeTrue())
			Expect(count).To(Equal(1))

			discarded, err := draft.Unset("spec")
			Expect(err).NotTo(HaveOccurred())
			Expect(discarded).To(Equal(2), "DiscardCount and Unset tell the same story")
		})

		It("reports an instantiated-but-empty item as held with nothing to discard", func() {
			draft := deploymentDraft()
			itemPath, err := draft.AppendItem("spec.template.spec.containers")
			Expect(err).NotTo(HaveOccurred())

			count, held := draft.DiscardCount(itemPath)

			Expect(held).To(BeTrue(), "the empty item is an explicit position the Draft holds")
			Expect(count).To(BeZero())
		})
	})

	When("setting instantiates ancestors implicitly and completeness delegates to contextual requiredness", func() {
		It("instantiates spec by setting spec.replicas, surfacing the DeploymentSpec required fields", func() {
			draft := deploymentDraft()
			Expect(draft.MissingRequired()).To(matchers.BeMissingRequired(),
				"an empty apps/v1 Deployment Draft misses nothing")

			mustSet(draft, "spec.replicas", 3)

			Expect(draft.Instantiated()).To(Equal([]string{"spec.replicas"}))
			Expect(draft.MissingRequired()).To(matchers.BeMissingRequired("spec.selector", "spec.template"))
		})

		It("makes containers[0].name missing once an item is appended", func() {
			draft := deploymentDraft()

			_, err := draft.AppendItem("spec.template.spec.containers")

			Expect(err).NotTo(HaveOccurred())
			Expect(draft.MissingRequired()).To(matchers.BeMissingRequired(
				"spec.selector", "spec.template.spec.containers[0].name",
			))
		})

		It("reports a required-at-the-root chain until a satisfied Widget Draft misses nothing", func() {
			draft := newDraft("apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Widget"))
			Expect(draft.MissingRequired()).To(matchers.BeMissingRequired("spec", "spec.size"))

			mustSet(draft, "spec.size", 2)

			Expect(draft.MissingRequired()).To(matchers.BeMissingRequired())
		})

		It("spells Instantiated in exactly the grammar MissingRequired accepts", func() {
			draft := deploymentDraft()
			mustSet(draft, "spec.replicas", 1)
			_, err := draft.AppendItem("spec.template.spec.containers")
			Expect(err).NotTo(HaveOccurred())
			_, err = draft.AddKey("spec.template.metadata.labels", "app")
			Expect(err).NotTo(HaveOccurred())

			instantiated := draft.Instantiated()

			Expect(instantiated).To(Equal([]string{
				"spec.replicas",
				`spec.template.metadata.labels["app"]`,
				"spec.template.spec.containers[0]",
			}))
			missing, err := growFieldTree("apis_apps_v1.json", gvk("apps", "v1", "Deployment")).MissingRequired(instantiated)
			Expect(err).NotTo(HaveOccurred(), "the requiredness walk must accept the Draft's spelling verbatim")
			Expect(missing).To(matchers.BeMissingRequired("spec.selector", "spec.template.spec.containers[0].name"))
		})
	})

	When("a value violates the Type Schema's schema-local constraints", func() {
		DescribeTable(
			"the set is rejected with a domain-language error and the Draft stays untouched",
			func(fixture string, kind schema.GroupVersionKind, fieldPath string, value any, complaint string) {
				draft := newDraft(fixture, kind)

				err := draft.Set(fieldPath, value)

				Expect(err).To(MatchError(ContainSubstring(complaint)))
				Expect(draft.Instantiated()).To(BeEmpty(), "a rejected set instantiates nothing")
			},
			Entry("a string where an integer is typed",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				"spec.replicas", "three", "as integer"),
			Entry("a fractional float where an integer is typed",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				"spec.replicas", 2.5, "as integer"),
			Entry("a string where a boolean is typed",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				"spec.paused", "yes", "as boolean"),
			Entry("an integer where a string is typed",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				"spec.nickname", 7, "as string"),
			Entry("a boolean where an int-or-string is typed — neither spelling",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				"spec.maxUnavailable", true, "int-or-string"),
			Entry("a value outside the enum",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				"spec.template.spec.containers[0].imagePullPolicy", "Sometimes", "admits only"),
			Entry("a string breaking the pattern",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				"spec.nickname", "Not-DNS!", "pattern"),
			Entry("a number at the exclusive minimum",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				"spec.efficiency", 0.0, "minimum"),
			Entry("a number above the maximum",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				"spec.efficiency", 1.5, "maximum"),
			Entry("an integer that is not the declared multiple",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				"spec.teeth", 7, "multiple of"),
			Entry("a string longer than maxLength",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				"spec.nickname", strings.Repeat("a", 64), "maxLength"),
			Entry("a string shorter than minLength",
				"apis_craft.example.com_v1.json", gvk("craft.example.com", "v1", "Gadget"),
				"spec.nickname", "", "minLength"),
			Entry("a scalar aimed at a plain object",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				"spec", 1, "holds structure"),
			Entry("a scalar aimed at an array — items are appended, not set",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				"spec.template.spec.containers", "nginx", "appends items"),
			Entry("a scalar aimed at a map-shaped object — keys are added, not set",
				"apis_apps_v1.json", gvk("apps", "v1", "Deployment"),
				"spec.template.metadata.labels", "x", "adds keys"),
		)

		DescribeTable(
			"a Field Path the Type Schema cannot place is rejected",
			func(fieldPath, complaint string) {
				draft := deploymentDraft()

				err := draft.Set(fieldPath, 1)

				Expect(err).To(MatchError(ContainSubstring(complaint)))
			},
			Entry("a field the Type Schema does not define",
				"spec.flavor", `no field "flavor"`),
			Entry("an index selector on a plain object",
				"spec[0]", "is not an array or a map-shaped object"),
			Entry("a key selector on an array",
				`spec.template.spec.containers["web"]`, "is an array"),
			Entry("an index selector on a map-shaped object",
				"spec.template.metadata.labels[0]", "is a map-shaped object"),
			Entry("a field addressed directly under an array",
				"spec.template.spec.containers.name", "is an array"),
			Entry("a malformed Draft-level Field Path",
				"spec..replicas", "malformed Draft-level Field Path"),
		)
	})

	When("raw YAML is grafted at a schema-blind position", func() {
		It("stores the opaque parsed value and counts its leaf paths as instantiated", func() {
			draft := gadgetDraft()
			raw := "knobs:\n  gain: 3\nmodes:\n  - eco\n  - turbo\nlabel: high\n"

			Expect(draft.GraftYAML("spec.tuning", raw)).To(Succeed())

			value, filled := draft.ValueAt("spec.tuning")
			Expect(filled).To(BeTrue())
			Expect(value.Type).To(Equal(schema.TypeRawYAML))
			Expect(value.Data).To(Equal(map[string]any{
				"knobs": map[string]any{"gain": 3},
				"modes": []any{"eco", "turbo"},
				"label": "high",
			}))
			Expect(draft.Instantiated()).To(Equal([]string{
				`spec.tuning["knobs"]["gain"]`,
				`spec.tuning["label"]`,
				`spec.tuning["modes"][0]`,
				`spec.tuning["modes"][1]`,
			}))
			Expect(draft.MissingRequired()).To(matchers.BeMissingRequired("spec.maxReplicas", "spec.minReplicas"),
				"graft leaf paths instantiate spec without tracking anything required beneath the blind subtree")
		})

		It("reads back opaquely: positions inside the graft answer no value", func() {
			draft := gadgetDraft()
			Expect(draft.GraftYAML("spec.tuning", "label: high\n")).To(Succeed())

			Expect(draft).To(matchers.HaveValueAt("spec.tuning"))
			Expect(draft).NotTo(matchers.HaveValueAt(`spec.tuning["label"]`),
				"the graft is opaque: only the graft position itself reads back")
		})

		It("replaces the graft when the same position is grafted again", func() {
			draft := gadgetDraft()
			Expect(draft.GraftYAML("spec.tuning", "label: low\n")).To(Succeed())

			Expect(draft.GraftYAML("spec.tuning", "label: high\n")).To(Succeed())

			Expect(draft).To(matchers.HaveValueAt("spec.tuning", map[string]any{"label": "high"}))
		})

		It("never schema-checks inside the graft", func() {
			draft := gadgetDraft()

			Expect(draft.GraftYAML("spec.tuning", "anything:\n  goes: [1, two, 3.5, true]\n")).To(Succeed())

			Expect(draft.Instantiated()).To(HaveLen(4))
		})

		It("rejects a graft where the Type Schema describes the position", func() {
			draft := gadgetDraft()

			err := draft.GraftYAML("spec.minReplicas", "3")

			Expect(err).To(MatchError(ContainSubstring("schema-blind")))
		})

		It("rejects schema-guided entry at and beneath the schema-blind position", func() {
			draft := gadgetDraft()

			Expect(draft.Set("spec.tuning", "anything")).To(MatchError(ContainSubstring("grafts raw YAML")))
			Expect(draft.Set("spec.tuning.knobs", 1)).To(MatchError(ContainSubstring("grafts raw YAML")))
			_, err := draft.AppendItem("spec.tuning")
			Expect(err).To(MatchError(ContainSubstring("grafts raw YAML")))
			_, err = draft.AddKey("spec.tuning", "knob")
			Expect(err).To(MatchError(ContainSubstring("grafts raw YAML")))
		})

		It("rejects malformed raw YAML with the parse failure wrapped", func() {
			draft := gadgetDraft()

			err := draft.GraftYAML("spec.tuning", "knobs: [broken")

			Expect(err).To(MatchError(ContainSubstring("parsing the raw YAML")))
		})

		It("rejects raw YAML holding no value", func() {
			draft := gadgetDraft()

			Expect(draft.GraftYAML("spec.tuning", "")).To(MatchError(ContainSubstring("holds no value")))
			Expect(draft.GraftYAML("spec.tuning", "null")).To(MatchError(ContainSubstring("holds no value")))
		})

		It("unsets a graft like any other entry", func() {
			draft := gadgetDraft()
			Expect(draft.GraftYAML("spec.tuning", "label: high\n")).To(Succeed())

			discarded, err := draft.Unset("spec.tuning")

			Expect(err).NotTo(HaveOccurred())
			Expect(discarded).To(Equal(1))
			Expect(draft.Instantiated()).To(BeEmpty())
		})
	})
})
