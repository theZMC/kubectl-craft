package schema_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/matchers"
	"github.com/thezmc/kubectl-craft/internal/schema"
)

// The Widget corpus pair (v1 ⇄ v2) carries the spelling cases — spec.size
// exists in both versions, spec.paint is respelled color — but no type
// change and no schema-blind respelling. The synthetic Mixer pair pins those
// remaining carry-over shapes, like the compose view's Palette/Phantom/Rack
// documents pin shapes the captured corpus does not carry.
const mixerV1Document = `{"openapi":"3.0.0","info":{"title":"Kubernetes","version":"1.36"},` +
	`"components":{"schemas":{"com.example.craft.v1.Mixer":{"type":"object",` +
	`"x-kubernetes-group-version-kind":[{"group":"craft.example.com","kind":"Mixer","version":"v1"}],` +
	`"properties":{"spec":{"type":"object","properties":{` +
	`"gain":{"type":"integer"},` +
	`"volume":{"type":"integer"},` +
	`"label":{"type":"string"},` +
	`"shift":{"x-kubernetes-int-or-string":true},` +
	`"free":{"type":"string"},` +
	`"raw":{"type":"object","x-kubernetes-preserve-unknown-fields":true},` +
	`"tuning":{"type":"object","x-kubernetes-preserve-unknown-fields":true},` +
	`"steps":{"type":"array","items":{"type":"object","properties":{` +
	`"name":{"type":"string"},"count":{"type":"integer"}}}},` +
	`"notes":{"type":"object","additionalProperties":{"type":"string"}},` +
	`"gears":{"type":"array","items":{"type":"string"}},` +
	`"legacy":{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"integer"}}}` +
	`}}}}}}}`

const mixerV2Document = `{"openapi":"3.0.0","info":{"title":"Kubernetes","version":"1.36"},` +
	`"components":{"schemas":{"com.example.craft.v2.Mixer":{"type":"object",` +
	`"x-kubernetes-group-version-kind":[{"group":"craft.example.com","kind":"Mixer","version":"v2"}],` +
	`"properties":{"spec":{"type":"object","properties":{` +
	`"gain":{"type":"string"},` +
	`"volume":{"x-kubernetes-int-or-string":true},` +
	`"label":{"x-kubernetes-int-or-string":true},` +
	`"shift":{"type":"integer"},` +
	`"free":{"type":"object","x-kubernetes-preserve-unknown-fields":true},` +
	`"raw":{"type":"object","x-kubernetes-preserve-unknown-fields":true},` +
	`"tuning":{"type":"object","properties":{"mode":{"type":"string"}}},` +
	`"steps":{"type":"array","items":{"type":"object","properties":{` +
	`"name":{"type":"string"}}}},` +
	`"notes":{"type":"object","additionalProperties":{"type":"integer"}},` +
	`"gears":{"type":"string"}` +
	`}}}}}}}`

// treeRef names one version's field tree for the carry-over table: how to
// grow it, and the Kind it binds a Draft to.
type treeRef struct {
	grow func() *schema.Node
	kind schema.GroupVersionKind
}

// widgetTree is the captured Widget corpus pair at one version.
func widgetTree(version string) treeRef {
	kind := gvk("craft.example.com", version, "Widget")
	return treeRef{
		grow: func() *schema.Node {
			return growFieldTree("apis_craft.example.com_"+version+".json", kind)
		},
		kind: kind,
	}
}

// mixerTree is the synthetic Mixer pair at one version.
func mixerTree(version string) treeRef {
	kind := gvk("craft.example.com", version, "Mixer")
	document := mixerV1Document
	if version == "v2" {
		document = mixerV2Document
	}
	return treeRef{
		grow: func() *schema.Node {
			GinkgoHelper()
			doc, err := schema.ParseDocument([]byte(document))
			Expect(err).NotTo(HaveOccurred())
			root, err := doc.FieldTree(kind)
			Expect(err).NotTo(HaveOccurred())
			return root
		},
		kind: kind,
	}
}

// droppedAs is one expected drop report entry: the exact Draft-level Field
// Path and a fragment of its reason.
type droppedAs struct {
	path   string
	reason string
}

// carryOver fills a Draft against the source tree and carries it over to the
// target tree, returning the carried Draft and the drop report.
func carryOver(source, target treeRef, fill func(*schema.Draft)) (*schema.Draft, []schema.Drop) {
	GinkgoHelper()
	draft := schema.NewDraft(source.grow(), source.kind)
	fill(draft)
	return draft.CarryOver(target.grow(), target.kind)
}

var _ = Describe("the version carry-over", func() {
	When("a Draft against version A is partitioned against version B's field tree", func() {
		DescribeTable(
			"values carry by Field Path and display-type family; the rest land in the ordered drop report",
			func(source, target treeRef, fill func(*schema.Draft), carried map[string]any, dropped []droppedAs) {
				draft, drops := carryOver(source, target, fill)

				for path, data := range carried {
					Expect(draft).To(matchers.HaveValueAt(path, data))
				}
				Expect(drops).To(HaveLen(len(dropped)), "the drop report must name exactly the dropped positions")
				for index, want := range dropped {
					Expect(drops[index].Path).To(Equal(want.path), "the report is ordered like Instantiated")
					Expect(drops[index].Reason).To(ContainSubstring(want.reason))
					Expect(draft).NotTo(matchers.HaveValueAt(want.path),
						"a dropped position must hold nothing in the carried Draft")
				}
			},
			Entry("a value at a Field Path both Widget versions spell carries",
				widgetTree("v1"), widgetTree("v2"),
				func(draft *schema.Draft) { mustSet(draft, "spec.size", 5) },
				map[string]any{"spec.size": int64(5)},
				nil),
			Entry("a value at the v1-only spelling drops — v2 respells paint as color",
				widgetTree("v1"), widgetTree("v2"),
				func(draft *schema.Draft) { mustSet(draft, "spec.paint", "red") },
				nil,
				[]droppedAs{{path: "spec.paint", reason: `no field "paint"`}}),
			Entry("one Draft partitions: the shared spelling carries while the respelled one drops",
				widgetTree("v1"), widgetTree("v2"),
				func(draft *schema.Draft) {
					mustSet(draft, "spec.paint", "red")
					mustSet(draft, "spec.size", 5)
				},
				map[string]any{"spec.size": int64(5)},
				[]droppedAs{{path: "spec.paint", reason: `no field "paint"`}}),
			Entry("switching back carries the shared spelling the other way",
				widgetTree("v2"), widgetTree("v1"),
				func(draft *schema.Draft) {
					mustSet(draft, "spec.color", "teal")
					mustSet(draft, "spec.finish", "matte")
					mustSet(draft, "spec.size", 2)
				},
				map[string]any{"spec.size": int64(2)},
				[]droppedAs{
					{path: "spec.color", reason: `no field "color"`},
					{path: "spec.finish", reason: `no field "finish"`},
				}),
			Entry("a value whose position changes display type drops",
				mixerTree("v1"), mixerTree("v2"),
				func(draft *schema.Draft) { mustSet(draft, "spec.gain", 3) },
				nil,
				[]droppedAs{{path: "spec.gain", reason: "types it as string, not integer"}}),
			Entry("an int-or-string target accepts an integer value",
				mixerTree("v1"), mixerTree("v2"),
				func(draft *schema.Draft) { mustSet(draft, "spec.volume", 7) },
				map[string]any{"spec.volume": int64(7)},
				nil),
			Entry("an int-or-string target accepts a string value",
				mixerTree("v1"), mixerTree("v2"),
				func(draft *schema.Draft) { mustSet(draft, "spec.label", "loud") },
				map[string]any{"spec.label": "loud"},
				nil),
			Entry("an int-or-string value in its integer spelling carries to an integer position",
				mixerTree("v1"), mixerTree("v2"),
				func(draft *schema.Draft) { mustSet(draft, "spec.shift", 2) },
				map[string]any{"spec.shift": int64(2)},
				nil),
			Entry("an int-or-string value in its string spelling drops at an integer position",
				mixerTree("v1"), mixerTree("v2"),
				func(draft *schema.Draft) { mustSet(draft, "spec.shift", "20%") },
				nil,
				[]droppedAs{{path: "spec.shift", reason: "types it as integer, not int-or-string"}}),
			Entry("a scalar drops where the target Type Schema goes blind",
				mixerTree("v1"), mixerTree("v2"),
				func(draft *schema.Draft) { mustSet(draft, "spec.free", "form") },
				nil,
				[]droppedAs{{path: "spec.free", reason: "blind at"}}),
			Entry("a raw-YAML graft carries when the target position is also schema-blind",
				mixerTree("v1"), mixerTree("v2"),
				func(draft *schema.Draft) {
					Expect(draft.GraftYAML("spec.raw", "mode: hot\n")).To(Succeed())
				},
				map[string]any{"spec.raw": map[string]any{"mode": "hot"}},
				nil),
			Entry("a raw-YAML graft drops where the target Type Schema describes the position",
				mixerTree("v1"), mixerTree("v2"),
				func(draft *schema.Draft) {
					Expect(draft.GraftYAML("spec.tuning", "mode: hot\n")).To(Succeed())
				},
				nil,
				[]droppedAs{{path: "spec.tuning", reason: "raw YAML grafts only onto a schema-blind position"}}),
			Entry("per-item drops keep the surviving items' indices — carried values stay at the same paths",
				mixerTree("v1"), mixerTree("v2"),
				func(draft *schema.Draft) {
					mustSet(draft, "spec.steps[0].name", "warm up")
					mustSet(draft, "spec.steps[0].count", 1)
					mustSet(draft, "spec.steps[1].count", 2)
				},
				map[string]any{"spec.steps[0].name": "warm up"},
				[]droppedAs{
					{path: "spec.steps[0].count", reason: `no field "count"`},
					{path: "spec.steps[1].count", reason: `no field "count"`},
				}),
			Entry("a map value whose value schema changes type drops, the key staying instantiated",
				mixerTree("v1"), mixerTree("v2"),
				func(draft *schema.Draft) { mustSet(draft, `spec.notes["a"]`, "x") },
				nil,
				[]droppedAs{{path: `spec.notes["a"]`, reason: "types it as integer, not string"}}),
			Entry("array items drop where the target respells the collection as a scalar",
				mixerTree("v1"), mixerTree("v2"),
				func(draft *schema.Draft) { mustSet(draft, "spec.gears[0]", "high") },
				nil,
				[]droppedAs{{path: "spec.gears[0]", reason: "not an array or a map-shaped object"}}),
			Entry("a missing subtree is reported once, at the highest position that failed to place",
				mixerTree("v1"), mixerTree("v2"),
				func(draft *schema.Draft) {
					mustSet(draft, "spec.legacy.a", "x")
					mustSet(draft, "spec.legacy.b", 4)
				},
				nil,
				[]droppedAs{{path: "spec.legacy", reason: `no field "legacy"`}}),
		)

		It("keeps an item whose values all dropped as an instantiated-but-empty item", func() {
			draft, _ := carryOver(mixerTree("v1"), mixerTree("v2"), func(draft *schema.Draft) {
				mustSet(draft, "spec.steps[0].name", "warm up")
				mustSet(draft, "spec.steps[1].count", 2)
			})

			Expect(draft.ItemCount("spec.steps")).To(Equal(2),
				"items are explicit acts: indices never shift, so carried values stay at the same paths")
			Expect(draft.Instantiated()).To(Equal([]string{"spec.steps[0].name", "spec.steps[1]"}))
		})

		It("never mutates the source Draft — cancelling a switch keeps composing untouched", func() {
			source := schema.NewDraft(widgetTree("v1").grow(), widgetTree("v1").kind)
			mustSet(source, "spec.paint", "red")
			mustSet(source, "spec.size", 5)
			before := source.Instantiated()

			_, drops := source.CarryOver(widgetTree("v2").grow(), widgetTree("v2").kind)

			Expect(drops).NotTo(BeEmpty())
			Expect(source.Instantiated()).To(Equal(before))
			Expect(source).To(matchers.HaveValueAt("spec.paint", "red"))
			Expect(source).To(matchers.HaveValueAt("spec.size", int64(5)))
		})

		It("binds the carried Draft to the target Kind: completeness and emission speak version B", func() {
			carried, drops := carryOver(widgetTree("v1"), widgetTree("v2"), func(draft *schema.Draft) {
				mustSet(draft, "spec.size", 5)
			})

			Expect(drops).To(BeEmpty())
			Expect(carried.MissingRequired()).To(matchers.BeMissingRequired(),
				"spec.size carried, so version B's required chain is satisfied")
			manifest, err := carried.Emit()
			Expect(err).NotTo(HaveOccurred())
			Expect(string(manifest)).To(HavePrefix("apiVersion: craft.example.com/v2\nkind: Widget\n"))
		})

		It("recomputes completeness over what actually carried", func() {
			carried, _ := carryOver(widgetTree("v1"), widgetTree("v2"), func(draft *schema.Draft) {
				mustSet(draft, "spec.paint", "red")
			})

			Expect(carried.Instantiated()).To(BeEmpty(),
				"the only value dropped, and ancestors de-instantiate implicitly")
			Expect(carried.MissingRequired()).To(matchers.BeMissingRequired("spec", "spec.size"))
		})
	})

	When("the target version is the same version — the identity carry-over", func() {
		It("carries every value and reports nothing dropped", func() {
			gadget := gvk("craft.example.com", "v1", "Gadget")
			source := schema.NewDraft(growFieldTree("apis_craft.example.com_v1.json", gadget), gadget)
			mustSet(source, "spec.nickname", "gizmo")
			mustSet(source, "spec.maxUnavailable", "25%")
			mustSet(source, "spec.gears[0]", "high")
			Expect(source.GraftYAML("spec.tuning", "knob: 11\n")).To(Succeed())

			carried, drops := source.CarryOver(growFieldTree("apis_craft.example.com_v1.json", gadget), gadget)

			Expect(drops).To(BeEmpty(), "the same version's tree places every value where it already sits")
			Expect(carried.Instantiated()).To(Equal(source.Instantiated()))
			Expect(carried).To(matchers.HaveValueAt("spec.nickname", "gizmo"))
			Expect(carried).To(matchers.HaveValueAt("spec.maxUnavailable", "25%"))
			Expect(carried).To(matchers.HaveValueAt("spec.gears[0]", "high"))
			Expect(carried).To(matchers.HaveValueAt("spec.tuning", map[string]any{"knob": int(11)}))
		})
	})
})
