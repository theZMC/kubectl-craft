package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/internal/tui"
)

// The shell-driving helpers below mirror internal/tui's spec helpers, built
// on the public API only: the state-first pattern (DESIGN.md — Testing)
// drives Update() with real messages and asserts through the exported
// accessors, never on internals.

var (
	enterKey     = tea.KeyPressMsg{Code: tea.KeyEnter}
	escKey       = tea.KeyPressMsg{Code: tea.KeyEsc}
	backspaceKey = tea.KeyPressMsg{Code: tea.KeyBackspace}
)

// keyRune is one printable navigate-mode key press.
func keyRune(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// press drives the Session shell's Update with synthetic messages, the way
// the Bubble Tea runtime would: it returns the model after every message
// and the command from the last one.
func press(model tui.Model, msgs ...tea.Msg) (tui.Model, tea.Cmd) {
	GinkgoHelper()
	var cmd tea.Cmd
	for _, msg := range msgs {
		var updated tea.Model
		updated, cmd = model.Update(msg)
		model = updated.(tui.Model)
	}
	return model, cmd
}

// render renders the shell's frame as the plain string the v1 suite saw
// under its Ascii color profile: lipgloss v2 styles always emit ANSI, so
// specs that read the model directly strip the styling themselves.
func render(model tui.Model) string {
	return ansi.Strip(model.View().Content)
}

// typeText presses one printable key per rune, the way a user types into a
// prompt or widget.
func typeText(model tui.Model, text string) tui.Model {
	GinkgoHelper()
	for _, r := range text {
		model, _ = press(model, keyRune(r))
	}
	return model
}

// widen keeps the panes' lines unwrapped so substring assertions on the
// rendered view stay honest.
func widen(model tui.Model) tui.Model {
	GinkgoHelper()
	model, _ = press(model, tea.WindowSizeMsg{Width: 200, Height: 60})
	return model
}

// openSessionKind drives the shell through the typed handoff and the lazy
// live fetch into the compose view.
func openSessionKind(model tui.Model, kind data.Kind) tui.Model {
	GinkgoHelper()
	model, fetch := press(model, tui.KindSelectedMsg{Kind: kind})
	Expect(fetch).NotTo(BeNil(), "an unparsed group must fetch lazily, as a command")
	model, _ = press(model, fetch()) // the live fetch and parse
	Expect(model.ComposeOpen()).To(BeTrue(), "the compose view must open on %s", kind.GVK.Kind)
	return model
}

// focusField walks the focus down the visible tree until it sits on the
// given schema-level Field Path (bounded, so a regression fails instead of
// spinning).
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

// confirmLeaf opens a leaf's value widget, types the given text, and
// confirms it into the Draft — the real widget flow.
func confirmLeaf(model tui.Model, fieldPath, text string) tui.Model {
	GinkgoHelper()
	model = focusField(model, fieldPath)
	return confirmFocusedLeaf(model, text)
}

// confirmFocusedLeaf opens the focused leaf's value widget, clears any
// prefilled buffer, types the given text, and confirms it into the Draft.
func confirmFocusedLeaf(model tui.Model, text string) tui.Model {
	GinkgoHelper()
	model, _ = press(model, enterKey)
	Expect(model.Editing()).To(BeTrue(), "Enter on the focused leaf must open its value widget")
	for range 32 { // a filled leaf prefills its widget; backspace past it
		model, _ = press(model, backspaceKey)
	}
	model = typeText(model, text)
	model, _ = press(model, enterKey)
	Expect(model.Editing()).To(BeFalse(), "confirming %q must close the widget", text)
	return model
}

// confirmGatePrompt answers one required-to-Validate gate prompt: it asserts
// the gate is asking for the expected Field Path, types the value, and
// confirms — returning the model and whatever command the confirm released.
func confirmGatePrompt(model tui.Model, fieldPath, value string) (tui.Model, tea.Cmd) {
	GinkgoHelper()
	prompted, prompting := model.PromptingForValidateMetadata()
	Expect(prompting).To(BeTrue(), "the required-to-Validate gate must be prompting")
	Expect(prompted).To(Equal(fieldPath))
	model = typeText(model, value)
	return press(model, enterKey)
}

// pumpValidate delivers a released Validate request into the shell and pumps
// the dry-run command through, landing the Outcome — the two async hops a
// running program's runtime would execute.
func pumpValidate(model tui.Model, request tea.Cmd) tui.Model {
	GinkgoHelper()
	model, post := press(model, request())
	Expect(model.Validating()).To(BeTrue(), "the dry-run POSTs async, behind the in-flight state")
	Expect(post).NotTo(BeNil(), "the POST must run as a command, off the Update loop")
	model, _ = press(model, post()) // the live server-side dry-run
	Expect(model.Validating()).To(BeFalse(), "the landed Outcome must end the in-flight state")
	return model
}

// dismissResults closes the results pane when a landed Outcome opened it, so
// navigate-mode keys reach the tree again.
func dismissResults(model tui.Model) tui.Model {
	GinkgoHelper()
	if model.ResultsPaneOpen() {
		model, _ = press(model, escKey)
	}
	return model
}

// revalidate presses v past the already-satisfied gate and drives the whole
// flight through — the re-Validate half of the fix-and-revalidate loop.
func revalidate(model tui.Model) tui.Model {
	GinkgoHelper()
	model = dismissResults(model)
	model, request := press(model, keyRune('v'))
	Expect(request).NotTo(BeNil(), "a satisfied gate emits the Draft and requests the Validate")
	return pumpValidate(model, request)
}

// jumpToFinding cycles the jump key until the focus sits on the given
// Draft-level Field Path, bounded by the mapped finding count.
func jumpToFinding(model tui.Model, draftPath string) tui.Model {
	GinkgoHelper()
	for range len(model.ValidateFindingPaths()) + 1 {
		if model.FocusedDraftPath() == draftPath {
			return model
		}
		model, _ = press(model, keyRune('n'))
	}
	Fail("the jump key never landed on " + draftPath)
	return model
}

// This is M4's exit-criterion gate (MILESTONES.md): compose → Validate →
// fix-by-jumping → clean pass, live against k3s — the real Fetcher, the
// real Validator, no stubs, the whole loop through the shell's Update().
// The spec installs a CRD, so it is decorated Serial: the Kind list it
// changes is shared by every parallel process.
var _ = Describe("the Session's manual Validate against a live k3s cluster", Serial, func() {
	It("drives compose → Validate → fix-by-jumping → clean pass on the CEL-ruled corpus CRD", func(ctx SpecContext) {
		cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
		Expect(err).NotTo(HaveOccurred())
		dynamicClient, err := dynamic.NewForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())
		crds := dynamicClient.Resource(crdGVR)

		By("installing the corpus Gadget CRD")
		crd := gadgetCRD()
		// The data-layer corpus spec installs and deletes this same CRD,
		// and CRD deletion finishes asynchronously — so the create retries
		// while a previous spec's deletion is still finalizing.
		Eventually(func(g Gomega) {
			_, createErr := crds.Create(ctx, crd, metav1.CreateOptions{})
			g.Expect(createErr).NotTo(HaveOccurred())
		}).WithContext(ctx).Should(Succeed())
		DeferCleanup(func(ctx SpecContext) {
			Expect(crds.Delete(ctx, crd.GetName(), metav1.DeleteOptions{})).To(Succeed())
			// Wait the asynchronous deletion out, so a later corpus spec's
			// plain create never races this spec's finalizing CRD.
			Eventually(func(g Gomega) {
				_, getErr := crds.Get(ctx, crd.GetName(), metav1.GetOptions{})
				g.Expect(apierrors.IsNotFound(getErr)).To(BeTrue(),
					"the Gadget CRD must be fully gone, got %v", getErr)
			}).WithContext(ctx).Should(Succeed())
		})

		By("waiting for discovery to serve the Gadget Kind")
		client := sessionClient()
		lister := sessionKindLister()
		var kinds []data.Kind
		var gadget data.Kind
		Eventually(func(g Gomega) {
			discovered, discoverErr := data.DiscoverKinds(lister)
			g.Expect(discoverErr).NotTo(HaveOccurred())
			byGVK := kindsByGVK(discovered)
			g.Expect(byGVK).To(HaveKey(gadgetGVK))
			kinds, gadget = discovered, byGVK[gadgetGVK]
		}).WithContext(ctx).Should(Succeed())

		By("waiting for the live index to serve the Gadget group document")
		var index []data.GroupVersion
		Eventually(func(g Gomega) {
			groups, indexErr := client.FetchIndex(ctx)
			g.Expect(indexErr).NotTo(HaveOccurred())
			byPath := groupsByPath(groups)
			g.Expect(byPath).To(HaveKey(gadget.GroupVersionPath))
			entry := byPath[gadget.GroupVersionPath]
			g.Expect(entry.ContentHash).NotTo(BeEmpty())
			_, fetchErr := client.FetchGroupDocument(ctx, entry.Path, entry.ContentHash)
			g.Expect(fetchErr).NotTo(HaveOccurred())
			index = groups
		}).WithContext(ctx).Should(Succeed())

		By("waiting for the Gadget endpoint to serve dry-runs with its CEL rules active")
		// A 404 while the freshly installed CRD is still registering also
		// arrives Status-shaped, so the readiness probe pins the CEL
		// message itself before the single-shot TUI flight starts.
		probe := []byte(`apiVersion: craft.example.com/v1
kind: Gadget
metadata:
  name: craft-session-probe
spec:
  minReplicas: 5
  maxReplicas: 1
  profile: turbo
`)
		Eventually(func(g Gomega) {
			outcome := client.Validate(ctx, gadget, probe, "default")
			invalid, isInvalid := outcome.(data.Invalid)
			g.Expect(isInvalid).To(BeTrue(), "the probe must classify as Invalid, got %#v", outcome)
			g.Expect(invalid.Status.Message).To(ContainSubstring("minReplicas must not exceed maxReplicas"))
		}).WithContext(ctx).Should(Succeed())

		By("opening the Gadget compose view through the shell")
		// An empty Session default namespace forces the gate's namespace
		// prompt, so both metadata prompts are exercised live.
		model := widen(tui.New(ctx, kinds, client, index, client, "", nil))
		model = openSessionKind(model, gadget)

		By("composing a Draft that violates a required field and a CEL rule")
		model = expandField(model, "spec")
		model = confirmLeaf(model, "spec.minReplicas", "5")
		// spec.maxReplicas stays unset: the required-field violation.
		model = confirmLeaf(model, "spec.profile", "turbo") // the CEL violation

		By("pressing v through the required-to-Validate gate")
		model, released := press(model, keyRune('v'))
		Expect(released).To(BeNil(), "the gate holds the Validate until the identity is confirmed")
		model, released = confirmGatePrompt(model, "metadata.name", "craft-live-gadget")
		Expect(released).To(BeNil(), "confirming the name moves to the namespace prompt, not to the POST")
		model, released = confirmGatePrompt(model, "metadata.namespace", "default")
		Expect(released).NotTo(BeNil(), "the confirmed gate releases the held Validate")
		model = pumpValidate(model, released)

		By("asserting the live findings mark the right tree nodes")
		// The server runs the CEL rules only once schema validation passes,
		// so the first round carries the required-field cause alone — the
		// CEL violation surfaces on the next Validate, which is exactly the
		// fix-and-revalidate loop this spec proves.
		Expect(model.ValidateFindingPaths()).To(ContainElement("spec.maxReplicas"),
			"the server's required-field cause must map onto its tree node")
		Expect(model.ValidateFindingMessages("spec.maxReplicas")).To(ContainElement(ContainSubstring("Required value")))
		model = dismissResults(model)
		Expect(render(model)).To(ContainSubstring("maxReplicas ✱ ✘"),
			"the finding marker joins the required marker on the omitted row — "+
				"'the server rejected this' next to 'you haven't filled this'")

		By("jumping to the first finding with n")
		firstFinding := model.ValidateFindingPaths()[0]
		model, _ = press(model, keyRune('n'))
		Expect(model.FocusedDraftPath()).To(Equal(firstFinding), "n lands on the first finding's node")

		By("fixing the required field through the real widget flow")
		model = jumpToFinding(model, "spec.maxReplicas")
		model = confirmFocusedLeaf(model, "10")
		Expect(model.ValidateStale()).To(BeTrue(),
			"a confirmed mutation marks the findings stale until v revalidates")
		Expect(model.ValidateFindingPaths()).NotTo(BeEmpty(),
			"stale findings keep their markers as a to-do list")

		By("re-Validating: the schema now passes, so the CEL violation surfaces")
		model = revalidate(model)
		Expect(model.ValidateStale()).To(BeFalse(), "fresh findings replace the stale ones")
		Expect(model.ValidateFindingPaths()).To(Equal([]string{"spec.profile"}),
			"with the required field fixed, the scalar CEL rule is the one remaining violation")
		Expect(model.ValidateFindingMessages("spec.profile")).To(ContainElement(
			ContainSubstring("profile must be economy, balanced, or performance"),
		))
		model = dismissResults(model)
		Expect(render(model)).To(ContainSubstring("profile: turbo ✘"),
			"the error marker sits on the CEL-violating row, next to its set value")

		By("jumping to the remaining finding and fixing it")
		model, _ = press(model, keyRune('n'))
		Expect(model.FocusedDraftPath()).To(Equal("spec.profile"))
		model = confirmFocusedLeaf(model, "balanced")

		By("re-Validating to the clean pass")
		model = revalidate(model)
		Expect(model.ValidatePassed()).To(BeTrue(), "the fixed Draft must pass the server's full dry-run")
		Expect(model.ValidateFindingPaths()).To(BeEmpty())
		Expect(render(model)).To(ContainSubstring("✔ Validate passed"))
		Expect(render(model)).NotTo(ContainSubstring("✘"), "a clean pass clears every prior marker")

		By("emitting the clean-pass Manifest and asserting it round-trips")
		model, emit := press(model, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
		Expect(emit).NotTo(BeNil())
		model, _ = press(model, emit())
		manifest, emitted := model.EmittedManifest()
		Expect(emitted).To(BeTrue(), "Ctrl-d must end the Session on the emit ramp")

		var object map[string]any
		Expect(yaml.Unmarshal(manifest, &object)).To(Succeed(), "the emitted bytes must still parse")
		identity := unstructured.Unstructured{Object: object}
		Expect(identity.GetAPIVersion()).To(Equal("craft.example.com/v1"))
		Expect(identity.GetKind()).To(Equal("Gadget"))
		Expect(identity.GetName()).To(Equal("craft-live-gadget"),
			"the gate-confirmed name must travel into the Emitted Manifest")
		Expect(identity.GetNamespace()).To(Equal("default"),
			"the gate-confirmed namespace must travel into the Emitted Manifest")
		spec, isMap := object["spec"].(map[string]any)
		Expect(isMap).To(BeTrue(), "the emitted spec must be a mapping")
		Expect(spec).To(HaveKeyWithValue("minReplicas", BeNumerically("==", 5)))
		Expect(spec).To(HaveKeyWithValue("maxReplicas", BeNumerically("==", 10)),
			"the jump-fixed value must travel into the Emitted Manifest")
		Expect(spec).To(HaveKeyWithValue("profile", "balanced"),
			"the jump-fixed value must travel into the Emitted Manifest")
	}, NodeTimeout(defaultSpecTimeout))
})

// Dry-run persists nothing and impersonation changes no cluster state, so
// the unavailable-path spec runs read-parallel.
var _ = Describe("the Session's Validate when the cluster refuses the dry-run", func() {
	It("renders Validate unavailable through the full shell without marking any tree node", func(ctx SpecContext) {
		client := sessionClient()

		// The kubeconfig user (cluster-admin) impersonates a user with no
		// bindings at all, so the dry-run POST is refused by RBAC — the
		// schema fetches stay on the admin client, only the Validator seam
		// is powerless.
		cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
		Expect(err).NotTo(HaveOccurred())
		cfg.Impersonate = rest.ImpersonationConfig{UserName: "craft-nobody"}
		nobody, err := data.NewClient(cfg)
		Expect(err).NotTo(HaveOccurred())

		index := fetchIndex(ctx, client)
		kinds := discoverSessionKinds()
		configMap := discoveredKind(ctx, configMapGVK)

		By("waiting for the RBAC refusal to answer at the data layer")
		probe := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: craft-session-forbidden\n")
		Eventually(func(g Gomega) {
			outcome := nobody.Validate(ctx, configMap, probe, "default")
			_, isUnavailable := outcome.(data.Unavailable)
			g.Expect(isUnavailable).To(BeTrue(), "the refusal must classify as Unavailable, got %#v", outcome)
		}).WithContext(ctx).Should(Succeed())

		By("driving v through the shell against the powerless Validator")
		model := widen(tui.New(ctx, kinds, client, index, nobody, "default", nil))
		model = openSessionKind(model, configMap)

		model, released := press(model, keyRune('v'))
		Expect(released).To(BeNil(), "the gate holds the Validate until the name is confirmed")
		model, released = confirmGatePrompt(model, "metadata.name", "craft-session-forbidden")
		Expect(released).NotTo(BeNil(), "the confirmed gate releases the held Validate")
		model = pumpValidate(model, released)

		Expect(model.ResultsPaneOpen()).To(BeTrue(), "unavailability opens the results pane by itself")
		reason, unavailable := model.ValidateUnavailable()
		Expect(unavailable).To(BeTrue())
		Expect(reason).To(ContainSubstring("403"))
		Expect(reason).To(ContainSubstring("forbidden"),
			"the reason must carry the server's own words for the results pane")
		Expect(render(model)).To(ContainSubstring("Validate unavailable"))
		Expect(render(model)).To(ContainSubstring("says nothing about the Manifest"),
			"unavailability must read as the cluster's failure, never the Manifest's")
		Expect(model.ValidateFindingPaths()).To(BeEmpty())
		Expect(render(model)).NotTo(ContainSubstring("✘"), "an unavailable Validate never marks tree nodes")
	}, NodeTimeout(defaultSpecTimeout))
})

// replayValidator is a stub data.Validator replaying one recorded Status as
// Invalid — the fixture-level webhook-denial path, so the full TUI flow is
// asserted without standing up a live admission webhook.
type replayValidator struct {
	status metav1.Status
}

var _ data.Validator = replayValidator{}

func (v replayValidator) Validate(context.Context, data.Kind, []byte, string) data.Outcome {
	return data.Invalid{Status: v.status}
}

// webhookDenialStatus replays the captured webhook-denial Status from the
// recorded corpus — a real API server answer, through the stub.
func webhookDenialStatus() metav1.Status {
	GinkgoHelper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "validate", "testdata", "configmap_webhook_denial.json"))
	Expect(err).NotTo(HaveOccurred())
	var status metav1.Status
	Expect(json.Unmarshal(raw, &status)).To(Succeed())
	return status
}

// The webhook-denial spec composes over the live cluster's schemas but stubs
// the Validator seam; nothing mutates, so it runs read-parallel.
var _ = Describe("the Session's Validate on a webhook denial", func() {
	It("routes the recorded denial through the full shell into the results pane", func(ctx SpecContext) {
		client := sessionClient()
		index := fetchIndex(ctx, client)
		kinds := discoverSessionKinds()
		configMap := discoveredKind(ctx, configMapGVK)

		model := widen(tui.New(ctx, kinds, client, index, replayValidator{status: webhookDenialStatus()}, "default", nil))
		model = openSessionKind(model, configMap)

		model, released := press(model, keyRune('v'))
		Expect(released).To(BeNil(), "the gate holds the Validate until the name is confirmed")
		model, released = confirmGatePrompt(model, "metadata.name", "craft-session-denied")
		Expect(released).NotTo(BeNil(), "the confirmed gate releases the held Validate")
		model = pumpValidate(model, released)

		Expect(model.ResultsPaneOpen()).To(BeTrue(),
			"a cause-less denial has no tree position — the results pane carries it")
		Expect(model.ValidateFindingPaths()).To(BeEmpty(), "the denial names no Field Path to mark")
		Expect(model.UnmappableFindings()).To(HaveLen(1))
		Expect(render(model)).To(ContainSubstring("Validate results"))
		Expect(render(model)).To(ContainSubstring("(HTTP 400)"))
		Expect(render(model)).To(ContainSubstring("admission webhook"),
			"the denial's own words are what the user needs to see")
	}, NodeTimeout(defaultSpecTimeout))
})
