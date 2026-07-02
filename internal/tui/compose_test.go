package tui_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/internal/tui"
)

// paletteDocument is a synthetic group document pinning shapes the captured
// corpus does not carry into the detail pane — an enum-valued field.
const paletteDocument = `{"openapi":"3.0.0","info":{"title":"Kubernetes","version":"1.36"},` +
	`"components":{"schemas":{"com.example.craft.v3.Palette":{"type":"object",` +
	`"x-kubernetes-group-version-kind":[{"group":"craft.example.com","kind":"Palette","version":"v3"}],` +
	`"properties":{"spec":{"type":"object","properties":{` +
	`"shade":{"type":"string","enum":["red","green","blue"]}}}}}}}}`

// phantomDocument is a synthetic group document whose spec.ghost field $refs
// a component schema the document does not define — the dangling-$ref shape
// that must surface at expansion, in the detail pane, never as a crash.
const phantomDocument = `{"openapi":"3.0.0","info":{"title":"Kubernetes","version":"1.36"},` +
	`"components":{"schemas":{"com.example.craft.v4.Phantom":{"type":"object",` +
	`"x-kubernetes-group-version-kind":[{"group":"craft.example.com","kind":"Phantom","version":"v4"}],` +
	`"properties":{"spec":{"type":"object","properties":{` +
	`"ghost":{"$ref":"#/components/schemas/com.example.craft.v4.Missing"}}}}}}}}`

// rackDocument is a synthetic group document pinning a map-shaped object
// whose values carry schema-defined fields — the map-crossing shape the
// search landing rule needs (the captured corpus's maps hold scalar values
// only).
const rackDocument = `{"openapi":"3.0.0","info":{"title":"Kubernetes","version":"1.36"},` +
	`"components":{"schemas":{"com.example.craft.v5.Rack":{"type":"object",` +
	`"x-kubernetes-group-version-kind":[{"group":"craft.example.com","kind":"Rack","version":"v5"}],` +
	`"properties":{"spec":{"type":"object","properties":{` +
	`"slots":{"type":"object","additionalProperties":{"type":"object","properties":{` +
	`"label":{"type":"string"}}}}}}}}}}}`

// fetchRecord is one FetchGroupDocument call a stub Fetcher served: the
// (group-version path, content hash) pair addressing it.
type fetchRecord struct {
	groupPath   string
	contentHash string
}

// stubFetcher is the hermetic data.Fetcher for shell specs: it serves the
// checked-in fixture corpus (internal/schema/testdata) by group-version
// path and records every fetch, so laziness and memoization are provable
// without a cluster.
type stubFetcher struct {
	documents map[string][]byte
	failWith  error
	fetches   []fetchRecord
}

var _ data.Fetcher = (*stubFetcher)(nil)

func (f *stubFetcher) FetchIndex(context.Context) ([]data.GroupVersion, error) {
	return nil, errors.New("the Session shell never fetches the index — it is resolved before launch")
}

func (f *stubFetcher) FetchGroupDocument(_ context.Context, groupPath, contentHash string) ([]byte, error) {
	f.fetches = append(f.fetches, fetchRecord{groupPath: groupPath, contentHash: contentHash})
	if f.failWith != nil {
		return nil, f.failWith
	}
	raw, ok := f.documents[groupPath]
	if !ok {
		return nil, fmt.Errorf("no fixture group document for %q", groupPath)
	}
	return raw, nil
}

// fixtureBytes reads one captured group document from the checked-in
// fixture corpus.
func fixtureBytes(name string) []byte {
	GinkgoHelper()
	raw, err := os.ReadFile(filepath.Join("..", "schema", "testdata", name))
	Expect(err).NotTo(HaveOccurred())
	return raw
}

// corpusFetcher serves the fixture corpus by the group-version paths the
// fixture Kind list carries.
func corpusFetcher() *stubFetcher {
	return &stubFetcher{documents: map[string][]byte{
		"apis/apps/v1":                 fixtureBytes("apis_apps_v1.json"),
		"apis/craft.example.com/v1":    fixtureBytes("apis_craft.example.com_v1.json"),
		"apis/craft.example.com/v2":    fixtureBytes("apis_craft.example.com_v2.json"),
		"apis/craft.example.com/v3":    []byte(paletteDocument),
		"apis/craft.example.com/v4":    []byte(phantomDocument),
		"apis/craft.example.com/v5":    []byte(rackDocument),
		"apis/apiextensions.k8s.io/v1": fixtureBytes("apis_apiextensions.k8s.io_v1.json"),
	}}
}

// corpusIndex is the spec fixture live index: the server content hashes
// that must address every lazy group-document fetch.
func corpusIndex() []data.GroupVersion {
	return []data.GroupVersion{
		{Path: "apis/apps/v1", ContentHash: "APPS1HASH"},
		{Path: "apis/craft.example.com/v1", ContentHash: "CRAFT1HASH"},
		{Path: "apis/craft.example.com/v2", ContentHash: "CRAFT2HASH"},
		{Path: "apis/craft.example.com/v3", ContentHash: "CRAFT3HASH"},
		{Path: "apis/craft.example.com/v4", ContentHash: "CRAFT4HASH"},
		{Path: "apis/craft.example.com/v5", ContentHash: "CRAFT5HASH"},
		{Path: "apis/apiextensions.k8s.io/v1", ContentHash: "EXT1HASH"},
	}
}

// newShell builds the Session shell over the fixture Kind list with a stub
// Fetcher serving the fixture corpus.
func newShell() tui.Model {
	return newShellWith(corpusFetcher())
}

// newShellWith builds the Session shell around one specific Fetcher, for
// specs that observe or fail its fetches; the Validator seam gets an
// always-Clean stub, and no default namespace resolves.
func newShellWith(fetcher data.Fetcher) tui.Model {
	return tui.New(context.Background(), browsableKinds(), fetcher, corpusIndex(),
		&stubValidator{outcome: data.Clean{}}, "", nil)
}

// composeKind builds one off-picker fixture Kind for driving the shell
// with KindSelectedMsg directly.
func composeKind(group, version, kind, path string) data.Kind {
	return data.Kind{
		GVK:              schema.GroupVersionKind{Group: group, Version: version, Kind: kind},
		GroupVersionPath: path,
		Preferred:        true,
	}
}

// widgetKind is craft.example.com/v1 Widget: its root Type Schema requires
// spec (and spec requires size), so its empty-Draft required chain is
// non-empty from the first frame.
func widgetKind() data.Kind {
	return composeKind("craft.example.com", "v1", "Widget", "apis/craft.example.com/v1")
}

// crdKind is apiextensions.k8s.io/v1 CustomResourceDefinition, whose Type
// Schema carries the JSONSchemaProps $ref cycle.
func crdKind() data.Kind {
	return composeKind("apiextensions.k8s.io", "v1", "CustomResourceDefinition", "apis/apiextensions.k8s.io/v1")
}

// keyRune is one printable navigate-mode key press.
func keyRune(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// openKind drives the shell through the typed handoff and the lazy fetch
// command into the compose view, the way the Bubble Tea runtime would.
func openKind(model tui.Model, kind data.Kind) tui.Model {
	GinkgoHelper()
	model, cmd := press(model, tui.KindSelectedMsg{Kind: kind})
	Expect(cmd).NotTo(BeNil(), "an unparsed group must fetch lazily, as a command")
	model, _ = press(model, cmd())
	Expect(model.ComposeOpen()).To(BeTrue(), "the compose view must open on %s", kind.GVK.Kind)
	return model
}

// composeDeployment opens the compose view on apps/v1 Deployment through
// the picker, end to end.
func composeDeployment() tui.Model {
	GinkgoHelper()
	model := typeFilter(newShell(), "deploy")
	model, cmd := press(model, tea.KeyMsg{Type: tea.KeyEnter})
	Expect(cmd).NotTo(BeNil())
	model, fetch := press(model, cmd())
	Expect(fetch).NotTo(BeNil())
	model, _ = press(model, fetch())
	Expect(model.ComposeOpen()).To(BeTrue())
	return model
}

// focusField walks the focus down the visible tree until it sits on the
// given schema-level Field Path.
func focusField(model tui.Model, fieldPath string) tui.Model {
	GinkgoHelper()
	for range 512 {
		if model.FocusedFieldPath() == fieldPath {
			return model
		}
		model, _ = press(model, keyRune('j'))
	}
	Fail("the focus never reached Field Path " + fieldPath)
	return model
}

// expandField focuses the given schema-level Field Path and expands it.
func expandField(model tui.Model, fieldPath string) tui.Model {
	GinkgoHelper()
	model = focusField(model, fieldPath)
	model, _ = press(model, keyRune('l'))
	return model
}

var _ = Describe("the compose view", func() {
	When("a picker selection opens a Kind", func() {
		It("fetches the group document addressed by the live index's content hash", func() {
			fetcher := corpusFetcher()
			model := typeFilter(newShellWith(fetcher), "deploy")
			model, cmd := press(model, tea.KeyMsg{Type: tea.KeyEnter})
			model, fetch := press(model, cmd())

			model, _ = press(model, fetch())

			Expect(model.ComposeOpen()).To(BeTrue())
			Expect(fetcher.fetches).To(Equal([]fetchRecord{{groupPath: "apis/apps/v1", contentHash: "APPS1HASH"}}),
				"the lazy fetch must be addressed by the (group-version path, content hash) pair — "+
					"that pair is what keeps the disk cache honest")
		})

		It("shows a loading state with its hint bar while the group document travels", func() {
			model := typeFilter(newShell(), "deploy")
			model, cmd := press(model, tea.KeyMsg{Type: tea.KeyEnter})
			model, _ = press(model, cmd())

			Expect(model.FetchingDocument()).To(BeTrue())
			Expect(model.Breadcrumb()).To(Equal("apps/v1 Deployment"))
			Expect(model.View()).To(ContainSubstring("fetching the apis/apps/v1"))
			Expect(model.View()).To(ContainSubstring("esc Kind picker"),
				"the loading state's hint bar must document the way out")
		})

		It("opens on the Kind's top-level fields with the root focused", func() {
			model := composeDeployment()

			Expect(model.Breadcrumb()).To(Equal("apps/v1 Deployment"),
				"at the root of the field tree, the breadcrumb shows the Kind alone")
			Expect(model.FocusedFieldPath()).To(BeEmpty())
			Expect(model.VisibleFieldPaths()).To(Equal([]string{
				"", "apiVersion", "kind", "metadata", "spec", "status",
			}), "the root expands one level so the view opens showing the top-level fields")
		})

		It("re-uses the Session's parse when another Kind of the same group opens", func() {
			fetcher := corpusFetcher()
			model := openKind(newShellWith(fetcher), kindNamed("Gadget", "v1"))

			model, back := press(model, tea.KeyMsg{Type: tea.KeyEsc})
			Expect(back).NotTo(BeNil())
			model, _ = press(model, back())
			_, selected := model.SelectedKind()
			Expect(selected).To(BeFalse(), "Esc returns to the Kind picker")

			model, cmd := press(model, tui.KindSelectedMsg{Kind: widgetKind()})
			Expect(cmd).To(BeNil(), "an already-parsed group opens immediately, no loading state")
			Expect(model.ComposeOpen()).To(BeTrue())
			Expect(model.Breadcrumb()).To(Equal("craft.example.com/v1 Widget"))
			Expect(fetcher.fetches).To(HaveLen(1),
				"a group document fetches once per Session, on the group's first open")
		})

		It("keeps a fetch that lands after Esc as a memoized parse, without leaving the picker", func() {
			fetcher := corpusFetcher()
			model := typeFilter(newShellWith(fetcher), "deploy")
			model, cmd := press(model, tea.KeyMsg{Type: tea.KeyEnter})
			model, fetch := press(model, cmd())

			model, _ = press(model, tea.KeyMsg{Type: tea.KeyEsc})
			model, _ = press(model, fetch())

			Expect(model.ComposeOpen()).To(BeFalse(),
				"a stale fetch landing after Esc must not yank the Session out of the picker")
			_, selected := model.SelectedKind()
			Expect(selected).To(BeFalse())

			model, cmd = press(model, tui.KindSelectedMsg{Kind: kindNamed("Deployment", "v1")})
			Expect(cmd).To(BeNil(), "the landed parse is kept for the group's next open")
			Expect(model.ComposeOpen()).To(BeTrue())
			Expect(fetcher.fetches).To(HaveLen(1))
		})
	})

	When("the group document fetch or parse fails", func() {
		It("surfaces a failed fetch as an in-TUI error state, never a crash", func() {
			fetcher := &stubFetcher{failWith: errors.New("the cluster hung up mid-fetch")}
			model := typeFilter(newShellWith(fetcher), "deploy")
			model, cmd := press(model, tea.KeyMsg{Type: tea.KeyEnter})
			model, fetch := press(model, cmd())

			model, _ = press(model, fetch())

			message, failed := model.ComposeError()
			Expect(failed).To(BeTrue())
			Expect(message).To(ContainSubstring("the cluster hung up mid-fetch"))
			Expect(model.View()).To(ContainSubstring("the cluster hung up mid-fetch"),
				"the error must render in the TUI, with the Session still running")
			Expect(model.View()).To(ContainSubstring("esc Kind picker"),
				"the error state's hint bar must document the way back")
		})

		It("surfaces an undecodable group document as the same error state", func() {
			fetcher := &stubFetcher{documents: map[string][]byte{"apis/apps/v1": []byte("not a document")}}
			model := openKindExpectingFailure(newShellWith(fetcher), kindNamed("Deployment", "v1"))

			message, failed := model.ComposeError()
			Expect(failed).To(BeTrue())
			Expect(message).To(ContainSubstring("parsing the OpenAPI v3 Document"))
		})

		It("surfaces a group document that defines no Type Schema for the Kind", func() {
			ghost := composeKind("craft.example.com", "v1", "Ghost", "apis/craft.example.com/v1")
			model := openKindExpectingFailure(newShell(), ghost)

			message, failed := model.ComposeError()
			Expect(failed).To(BeTrue())
			Expect(message).To(ContainSubstring("no Type Schema"))
		})

		DescribeTable(
			"the error state keeps the empty-Draft exit grammar",
			func(key tea.KeyMsg) {
				fetcher := &stubFetcher{failWith: errors.New("broken")}
				model := openKindExpectingFailure(newShellWith(fetcher), kindNamed("Deployment", "v1"))

				_, quit := press(model, key)
				Expect(quit).NotTo(BeNil())
				Expect(quit()).To(Equal(tea.QuitMsg{}))
			},
			Entry("q", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}),
			Entry("Ctrl-c", tea.KeyMsg{Type: tea.KeyCtrlC}),
		)

		It("returns to the Kind picker from the error state on Esc", func() {
			fetcher := &stubFetcher{failWith: errors.New("broken")}
			model := openKindExpectingFailure(newShellWith(fetcher), kindNamed("Deployment", "v1"))

			model, _ = press(model, tea.KeyMsg{Type: tea.KeyEsc})

			_, failed := model.ComposeError()
			Expect(failed).To(BeFalse())
			_, highlighted := model.HighlightedKind()
			Expect(highlighted).To(BeTrue(), "the picker is browsable again")
		})
	})

	When("navigate-mode keys move the focus through the field tree", func() {
		DescribeTable(
			"movement keys slide the focus and the breadcrumb tracks it",
			func(down, up tea.KeyMsg) {
				model := composeDeployment()

				model, _ = press(model, down)
				Expect(model.FocusedFieldPath()).To(Equal("apiVersion"))
				Expect(model.Breadcrumb()).To(Equal("apps/v1 Deployment › apiVersion"),
					"the persistent breadcrumb shows the focused node's schema-level Field Path")

				model, _ = press(model, down, down, down)
				Expect(model.FocusedFieldPath()).To(Equal("spec"))

				model, _ = press(model, up)
				Expect(model.FocusedFieldPath()).To(Equal("metadata"))
			},
			Entry("j/k", keyRune('j'), keyRune('k')),
			Entry("arrow keys", tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyUp}),
		)

		It("clamps at the tree's edges instead of wrapping", func() {
			model := composeDeployment()

			model, _ = press(model, keyRune('k'))
			Expect(model.FocusedFieldPath()).To(BeEmpty(), "moving up from the root must clamp")

			for range 16 {
				model, _ = press(model, keyRune('j'))
			}
			Expect(model.FocusedFieldPath()).To(Equal("status"), "moving down past the last row must clamp")
		})

		It("jumps to the top and bottom with g and G", func() {
			model := composeDeployment()

			model, _ = press(model, keyRune('G'))
			Expect(model.FocusedFieldPath()).To(Equal("status"))

			model, _ = press(model, keyRune('g'))
			Expect(model.FocusedFieldPath()).To(BeEmpty())
			Expect(model.Breadcrumb()).To(Equal("apps/v1 Deployment"))
		})
	})

	When("h/l and the arrow keys collapse and expand the focused field", func() {
		DescribeTable(
			"expanding a parent reveals its children and collapsing hides them again",
			func(expand, collapse tea.KeyMsg) {
				model := focusField(composeDeployment(), "spec")

				model, _ = press(model, expand)
				Expect(model.VisibleFieldPaths()).To(ContainElements("spec.replicas", "spec.selector", "spec.template"),
					"expansion materializes the children lazily, at the moment of expanding")
				Expect(model.FocusedFieldPath()).To(Equal("spec"), "expanding keeps the focus on the parent")

				model, _ = press(model, collapse)
				Expect(model.VisibleFieldPaths()).NotTo(ContainElement("spec.replicas"))
				Expect(model.FocusedFieldPath()).To(Equal("spec"))
			},
			Entry("h/l", keyRune('l'), keyRune('h')),
			Entry("arrow keys", tea.KeyMsg{Type: tea.KeyRight}, tea.KeyMsg{Type: tea.KeyLeft}),
		)

		It("steps l into the first child of an already-expanded parent", func() {
			model := expandField(composeDeployment(), "spec")

			model, _ = press(model, keyRune('l'))

			Expect(model.FocusedFieldPath()).To(Equal("spec.minReadySeconds"))
		})

		It("jumps h from a collapsed field to its parent", func() {
			model := expandField(composeDeployment(), "spec")
			model = focusField(model, "spec.replicas")

			model, _ = press(model, keyRune('h'))

			Expect(model.FocusedFieldPath()).To(Equal("spec"))
		})

		It("leaves l inert on a leaf — a scalar promises a value, not structure", func() {
			model := focusField(composeDeployment(), "apiVersion")

			model, _ = press(model, keyRune('l'))

			Expect(model.FocusedFieldPath()).To(Equal("apiVersion"))
			Expect(model.VisibleFieldPaths()).To(HaveLen(6))
		})

		It("labels an array's item row [items], sharing its parent's Field Path", func() {
			model := expandField(composeDeployment(), "spec")
			model = expandField(model, "spec.template")
			model = expandField(model, "spec.template.spec")
			model = expandField(model, "spec.template.spec.containers")

			model, _ = press(model, keyRune('j'))

			Expect(model.FocusedFieldPath()).To(Equal("spec.template.spec.containers"),
				"dots address schema-defined fields, never individual items")
			Expect(model.View()).To(ContainSubstring("[items]"))
		})
	})

	When("Enter toggles expansion", func() {
		It("toggles a parent open and closed", func() {
			model := focusField(composeDeployment(), "spec")

			model, _ = press(model, tea.KeyMsg{Type: tea.KeyEnter})
			Expect(model.VisibleFieldPaths()).To(ContainElement("spec.template"))

			model, _ = press(model, tea.KeyMsg{Type: tea.KeyEnter})
			Expect(model.VisibleFieldPaths()).NotTo(ContainElement("spec.template"))
		})

		It("opens a leaf's value widget in edit mode instead of expanding", func() {
			model := focusField(composeDeployment(), "apiVersion")

			model, cmd := press(model, tea.KeyMsg{Type: tea.KeyEnter})

			Expect(cmd).To(BeNil())
			Expect(model.Editing()).To(BeTrue(),
				"Enter on a leaf opens the value widget — parents keep toggling expansion")
			Expect(model.FocusedFieldPath()).To(Equal("apiVersion"))
			Expect(model.VisibleFieldPaths()).To(HaveLen(6), "a leaf has nothing to expand")
		})
	})

	When("a self-referential Type Schema is browsed", func() {
		It("keeps yielding rows through the JSONSchemaProps cycle instead of recursing", func() {
			model := expandField(openKind(newShell(), crdKind()), "spec")
			model = expandField(model, "spec.versions")
			model, _ = press(model, keyRune('j'), keyRune('l')) // the [items] row shares its parent's Field Path
			model = expandField(model, "spec.versions.schema")
			model = expandField(model, "spec.versions.schema.openAPIV3Schema")
			model = expandField(model, "spec.versions.schema.openAPIV3Schema.properties")
			model, _ = press(model, keyRune('j'), keyRune('l')) // the [value] row of the properties map

			Expect(model.VisibleFieldPaths()).To(ContainElement("spec.versions.schema.openAPIV3Schema.properties.properties"),
				"expanding the cycle's re-entry point simply yields another expandable node")
		})
	})

	When("a $ref fails to resolve at expansion", func() {
		It("surfaces the error in the detail pane and keeps the row collapsed and retryable", func() {
			phantom := composeKind("craft.example.com", "v4", "Phantom", "apis/craft.example.com/v4")
			model := openKind(newShell(), phantom)
			// A wide terminal keeps the detail pane's lines unwrapped, so
			// the substring assertions below stay honest.
			model, _ = press(model, tea.WindowSizeMsg{Width: 400, Height: 60})
			model = expandField(model, "spec")
			model = focusField(model, "spec.ghost")

			Expect(model.View()).To(ContainSubstring("expanding this field failed"),
				"the $ref-resolution error surfaces in the detail pane, never as a crash")
			Expect(model.View()).To(ContainSubstring("does not define"),
				"the error names the dangling $ref's missing component schema")

			before := model.VisibleFieldPaths()
			model, _ = press(model, keyRune('l'))
			Expect(model.VisibleFieldPaths()).To(Equal(before),
				"the broken row stays collapsed — expansion is retryable, not fatal")
			Expect(model.FocusedFieldPath()).To(Equal("spec.ghost"))
			Expect(model.ComposeOpen()).To(BeTrue())
		})

		It("says why a cannot act on the broken row, instead of failing silently", func() {
			phantom := composeKind("craft.example.com", "v4", "Phantom", "apis/craft.example.com/v4")
			model := openKind(newShell(), phantom)
			model = expandField(model, "spec")
			model = focusField(model, "spec.ghost")

			model, _ = press(model, keyRune('a'))

			notice, showing := model.Notice()
			Expect(showing).To(BeTrue(), "a silent failure is a missing message")
			Expect(notice).To(HavePrefix("expanding spec.ghost failed:"),
				"the notice names the row in the same words the detail pane uses")
			Expect(notice).To(ContainSubstring("does not define"))
		})
	})

	When("the detail pane renders the focused node's metadata", func() {
		// A wide terminal keeps the detail pane's lines unwrapped, so the
		// substring assertions below stay honest.
		widen := func(model tui.Model) tui.Model {
			model, _ = press(model, tea.WindowSizeMsg{Width: 200, Height: 50})
			return model
		}

		It("shows the display type, dimmed default placeholder, CEL constraint text, and documentation", func() {
			model := expandField(widen(openKind(newShell(), kindNamed("Gadget", "v1"))), "spec")
			model = focusField(model, "spec.profile")

			view := model.View()
			Expect(view).To(ContainSubstring("type: string"))
			Expect(view).To(ContainSubstring("default: balanced"),
				"the schema default renders as a dimmed placeholder, never as a value in the Draft")
			Expect(view).To(ContainSubstring("rule: self in ['economy', 'balanced', 'performance']"),
				"CEL rules render as constraint text; evaluation stays server-side")
			Expect(view).To(ContainSubstring("Named performance profile for the gadget."))
		})

		It("shows keyword constraints like pattern and length bounds", func() {
			model := expandField(widen(openKind(newShell(), kindNamed("Gadget", "v1"))), "spec")
			model = focusField(model, "spec.nickname")

			view := model.View()
			Expect(view).To(ContainSubstring("pattern: ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"))
			Expect(view).To(ContainSubstring("maxLength: 63"))
		})

		It("shows the int-or-string flavor as its own display type", func() {
			model := expandField(widen(openKind(newShell(), kindNamed("Gadget", "v1"))), "spec")
			model = focusField(model, "spec.maxUnavailable")

			Expect(model.View()).To(ContainSubstring("type: int-or-string"))
		})

		It("says the Type Schema can't describe a schema-blind node", func() {
			model := expandField(widen(openKind(newShell(), kindNamed("Gadget", "v1"))), "spec")
			model = focusField(model, "spec.tuning")

			Expect(model.View()).To(ContainSubstring("can't describe what goes here"),
				"the raw-YAML escape hatch that composes these lands in M3")
		})

		It("lists enum values verbatim", func() {
			palette := composeKind("craft.example.com", "v3", "Palette", "apis/craft.example.com/v3")
			model := expandField(widen(openKind(newShell(), palette)), "spec")
			model = focusField(model, "spec.shade")

			Expect(model.View()).To(ContainSubstring("enum: red · green · blue"))
		})
	})

	When("required-but-unset fields are flagged", func() {
		It("marks the empty-Draft required chain in the tree and the detail pane", func() {
			model := openKind(newShell(), widgetKind())

			Expect(model.MissingRequiredFieldPaths()).To(Equal([]string{"spec", "spec.size"}),
				"contextual requiredness over the empty Draft — the root-level required chain")
			Expect(model.View()).To(ContainSubstring("spec ✱"),
				"the tree pane marks required-but-unset fields")

			model = focusField(model, "spec")
			Expect(model.View()).To(ContainSubstring("required — unset"))

			model = expandField(model, "spec")
			Expect(model.View()).To(ContainSubstring("size ✱"))
		})

		It("flags nothing for a Kind whose root Type Schema declares no required fields", func() {
			model := composeDeployment()

			Expect(model.MissingRequiredFieldPaths()).To(BeEmpty(),
				"apps/v1 Deployment requires nothing until the Draft digs into spec — and there is no Draft in M2")
		})
	})

	When("? opens the full-map help overlay", func() {
		It("renders the command map and any key dismisses it in place", func() {
			model := focusField(composeDeployment(), "spec")

			model, _ = press(model, keyRune('?'))
			Expect(model.HelpOpen()).To(BeTrue())
			Expect(model.View()).To(ContainSubstring("navigate mode"))
			Expect(model.View()).To(ContainSubstring("return to the Kind picker"),
				"the help overlay documents the way back to the picker")

			model, _ = press(model, keyRune('j'))
			Expect(model.HelpOpen()).To(BeFalse())
			Expect(model.FocusedFieldPath()).To(Equal("spec"),
				"the dismissing key is consumed by the overlay, not the tree")
			Expect(model.ComposeOpen()).To(BeTrue())
		})

		It("dismisses on Esc without leaving the compose view", func() {
			model := composeDeployment()

			model, _ = press(model, keyRune('?'))
			model, _ = press(model, tea.KeyMsg{Type: tea.KeyEsc})

			Expect(model.HelpOpen()).To(BeFalse())
			Expect(model.ComposeOpen()).To(BeTrue())
		})

		It("still quits immediately on Ctrl-c — the conventional escape hatch reaches through the overlay", func() {
			model := composeDeployment()

			model, _ = press(model, keyRune('?'))
			_, quit := press(model, tea.KeyMsg{Type: tea.KeyCtrlC})

			Expect(quit).NotTo(BeNil())
			Expect(quit()).To(Equal(tea.QuitMsg{}))
		})
	})

	When("the compose view is browsed within one Session", func() {
		It("shows the contextual hint bar for the focused view", func() {
			view := composeDeployment().View()

			Expect(view).To(ContainSubstring("? help"))
			Expect(view).To(ContainSubstring("q quit"))
			Expect(view).To(ContainSubstring("ctrl+d emit"),
				"the hint bar documents the direct emit-&-quit shortcut")
			Expect(view).To(ContainSubstring("esc Kind picker"),
				"the hint bar documents the key returning to the picker")
		})

		It("returns to the Kind picker on Esc, keeping the picker browsable", func() {
			model := composeDeployment()

			model, cmd := press(model, tea.KeyMsg{Type: tea.KeyEsc})
			Expect(cmd).NotTo(BeNil())
			model, _ = press(model, cmd())

			Expect(model.ComposeOpen()).To(BeFalse())
			_, selected := model.SelectedKind()
			Expect(selected).To(BeFalse(),
				"M2 composes no Draft, so returning to the picker needs no confirmation")
			_, highlighted := model.HighlightedKind()
			Expect(highlighted).To(BeTrue())
			Expect(model.Filter()).To(Equal("deploy"),
				"the picker reopens exactly as it was left, typed filter included")
		})

		DescribeTable(
			"tiny terminals never crash the compose view",
			func(size tea.WindowSizeMsg) {
				model, _ := press(composeDeployment(), size)

				Expect(model.View()).NotTo(BeEmpty())

				model, _ = press(model, keyRune('G'))
				Expect(model.View()).To(ContainSubstring("status"),
					"the tree pane's viewport must follow the focus")
			},
			Entry("a few rows tall", tea.WindowSizeMsg{Width: 30, Height: 5}),
			Entry("one row tall", tea.WindowSizeMsg{Width: 20, Height: 1}),
			Entry("zero-sized", tea.WindowSizeMsg{Width: 0, Height: 0}),
		)
	})
})

// openKindExpectingFailure drives the shell into the in-TUI error state:
// the handoff, the fetch command, and the failure landing.
func openKindExpectingFailure(model tui.Model, kind data.Kind) tui.Model {
	GinkgoHelper()
	model, cmd := press(model, tui.KindSelectedMsg{Kind: kind})
	Expect(cmd).NotTo(BeNil())
	model, _ = press(model, cmd())
	_, failed := model.ComposeError()
	Expect(failed).To(BeTrue(), "opening %s must land in the in-TUI error state", kind.GVK.Kind)
	return model
}
