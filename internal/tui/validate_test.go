package tui_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/internal/tui"
)

// validateCall is one Validate the stub Validator served: the Kind, the
// Manifest bytes, and the resolved namespace, exactly as the seam carries
// them.
type validateCall struct {
	kind      data.Kind
	manifest  []byte
	namespace string
}

// stubValidator is the hermetic data.Validator for shell specs: it records
// every Validate and answers with a canned Outcome — and, like the real
// client, answers a canceled context as Unavailable rather than an error.
type stubValidator struct {
	outcome data.Outcome
	calls   []validateCall
}

var _ data.Validator = (*stubValidator)(nil)

func (v *stubValidator) Validate(ctx context.Context, kind data.Kind, manifest []byte, namespace string) data.Outcome {
	v.calls = append(v.calls, validateCall{kind: kind, manifest: manifest, namespace: namespace})
	if ctx.Err() != nil {
		return data.Unavailable{Reason: "posting the dry-run: context canceled"}
	}
	return v.outcome
}

// fixtureStatus replays one captured metav1.Status from the recorded
// corpus internal/validate/testdata — real API server answers, through the
// stub.
func fixtureStatus(name string) metav1.Status {
	GinkgoHelper()
	raw, err := os.ReadFile(filepath.Join("..", "validate", "testdata", name))
	Expect(err).NotTo(HaveOccurred())
	var status metav1.Status
	Expect(json.Unmarshal(raw, &status)).To(Succeed())
	return status
}

// validatingShell builds the Session shell over the fixture corpus around
// one specific Validator and Session default namespace.
func validatingShell(validator data.Validator, defaultNamespace string) tui.Model {
	return tui.New(context.Background(), browsableKinds(), corpusFetcher(), corpusIndex(),
		validator, defaultNamespace, nil)
}

// composeValidatable opens the compose view on a fixture Kind and confirms
// metadata.name into the Draft, so the required-to-Validate gate is
// satisfied wherever the shell's default namespace resolves.
func composeValidatable(model tui.Model, kind data.Kind) tui.Model {
	GinkgoHelper()
	model = widen(openKind(model, kind))
	model = expandField(model, "metadata")
	return confirmLeaf(model, "metadata.name", "web")
}

// startValidate presses v past a satisfied gate and delivers the request
// to the shell, returning the in-flight model and the dry-run POST command.
func startValidate(model tui.Model) (tui.Model, tea.Cmd) {
	GinkgoHelper()
	model, request := press(model, keyRune('v'))
	Expect(request).NotTo(BeNil(), "v must emit the Draft and request the Validate")
	model, post := press(model, request())
	Expect(model.Validating()).To(BeTrue(), "the dry-run POSTs async, behind an in-flight state")
	Expect(post).NotTo(BeNil(), "the POST must run as a command, off the Update loop")
	return model, post
}

// validateThrough drives one whole Validate flight: the request, the
// in-flight state, and the Outcome landing.
func validateThrough(model tui.Model) tui.Model {
	GinkgoHelper()
	model, post := startValidate(model)
	model, _ = press(model, post())
	Expect(model.Validating()).To(BeFalse(), "the landed Outcome must end the in-flight state")
	return model
}

// statusWithCause builds one Invalid Status around a single cause — for
// shapes the recorded corpus does not carry, like an instantiated item
// selector.
func statusWithCause(field, message string) metav1.Status {
	return metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure,
		Reason:   metav1.StatusReasonInvalid,
		Code:     422,
		Message:  message,
		Details: &metav1.StatusDetails{
			Causes: []metav1.StatusCause{{Field: field, Message: message}},
		},
	}
}

var _ = Describe("the manual Validate", func() {
	When("v runs the Validate flow in navigate mode", func() {
		It("POSTs exactly the exit ramp's emission, async, with the resolved namespace", func() {
			validator := &stubValidator{outcome: data.Clean{}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1"))

			fork, emit := press(model, tea.KeyMsg{Type: tea.KeyCtrlD})
			Expect(emit).NotTo(BeNil())
			fork, _ = press(fork, emit())
			wantManifest, emitted := fork.EmittedManifest()
			Expect(emitted).To(BeTrue())

			model = validateThrough(model)

			Expect(model.ValidatePassed()).To(BeTrue())
			Expect(validator.calls).To(HaveLen(1))
			Expect(validator.calls[0].manifest).To(Equal(wantManifest),
				"Validate must check exactly the bytes the exit ramp would print — pure emission, same path")
			Expect(validator.calls[0].kind).To(Equal(kindNamed("Deployment", "v1")))
			Expect(validator.calls[0].namespace).To(Equal("team-a"),
				"with no metadata.namespace in the Draft, the Session default resolves")
		})

		It("resolves the Draft's metadata.namespace over the Session default, the way kubectl would", func() {
			validator := &stubValidator{outcome: data.Clean{}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1"))
			model = confirmLeaf(model, "metadata.namespace", "prod")

			validateThrough(model)

			Expect(validator.calls).To(HaveLen(1))
			Expect(validator.calls[0].namespace).To(Equal("prod"))
		})

		It("shows the in-flight loading state, and Esc cancels the wait with the Draft untouched", func() {
			validator := &stubValidator{outcome: data.Clean{}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1"))

			model, post := startValidate(model)
			Expect(model.View()).To(ContainSubstring("validating the Draft"))
			Expect(model.View()).To(ContainSubstring("esc/q cancel the Validate"),
				"the in-flight state documents cancelling, in the loading grammar")

			model, _ = press(model, escKey)
			Expect(model.Validating()).To(BeFalse())
			Expect(model.ComposeOpen()).To(BeTrue(), "cancelling returns to composing")
			Expect(draftValue(model, "metadata.name")).To(Equal("web"), "the Draft is untouched")

			model, _ = press(model, post())
			Expect(model.ComposeOpen()).To(BeTrue())
			Expect(model.ValidatePassed()).To(BeFalse(),
				"a stale Outcome landing after the cancel must not clobber the compose view")
			Expect(model.ResultsPaneOpen()).To(BeFalse())
		})

		It("returns quietly to composing when the Session's own context ends mid-flight", func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			validator := &stubValidator{outcome: data.Clean{}}
			model := tui.New(ctx, browsableKinds(), corpusFetcher(), corpusIndex(), validator, "team-a", nil)
			model = composeValidatable(model, kindNamed("Deployment", "v1"))

			model, post := startValidate(model)
			cancel()
			model, _ = press(model, post())

			Expect(model.ComposeOpen()).To(BeTrue())
			_, unavailable := model.ValidateUnavailable()
			Expect(unavailable).To(BeFalse(),
				"a canceled context comes back dressed as Unavailable — the Session must not render it")
			Expect(model.ResultsPaneOpen()).To(BeFalse())
		})

		It("keeps v inert in edit mode — it types into the open widget instead", func() {
			validator := &stubValidator{outcome: data.Clean{}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1"))
			model = openWidget(model, "metadata.namespace")

			model, cmd := press(model, keyRune('v'))

			Expect(cmd).To(BeNil())
			Expect(model.Editing()).To(BeTrue())
			Expect(model.Validating()).To(BeFalse())
			Expect(validator.calls).To(BeEmpty(), "no dry-run leaves while a value widget is open")
		})

		It("keeps v inert in the overlays — a search surface has no command letters", func() {
			validator := &stubValidator{outcome: data.Clean{}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1"))
			model, _ = press(model, keyRune('/'))

			model, cmd := press(model, keyRune('v'))

			Expect(cmd).To(BeNil())
			Expect(model.SearchFilter()).To(Equal("v"))
			Expect(model.Validating()).To(BeFalse())
			Expect(validator.calls).To(BeEmpty())
		})
	})

	When("the required-to-Validate gate guards the dry-run", func() {
		It("flags the required-to-Validate metadata in the status line from session start", func() {
			model := widen(openKind(validatingShell(&stubValidator{outcome: data.Clean{}}, ""), kindNamed("Deployment", "v1")))

			Expect(model.View()).To(ContainSubstring("Validate needs metadata.name and metadata.namespace"),
				"a namespaced Kind with no resolvable default needs both, before any v is pressed")
			Expect(model.View()).To(ContainSubstring("no required fields missing"),
				"the flag is distinct from the schema-required count — apps/v1 Deployment requires nothing at the root")
		})

		It("drops the namespace from the flag when the Session default resolves, and the whole flag once satisfied", func() {
			model := widen(openKind(validatingShell(&stubValidator{outcome: data.Clean{}}, "team-a"), kindNamed("Deployment", "v1")))
			Expect(model.View()).To(ContainSubstring("Validate needs metadata.name"))
			Expect(model.View()).NotTo(ContainSubstring("metadata.namespace"))

			model = expandField(model, "metadata")
			model = confirmLeaf(model, "metadata.name", "web")

			Expect(model.View()).NotTo(ContainSubstring("Validate needs"))
		})

		It("prompts inline for metadata.name and confirms it into the Draft at the real Field Path", func() {
			validator := &stubValidator{outcome: data.Clean{}}
			model := widen(openKind(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1")))

			model, cmd := press(model, keyRune('v'))
			Expect(cmd).To(BeNil(), "the gate holds the Validate — nothing POSTs yet")
			prompted, prompting := model.PromptingForValidateMetadata()
			Expect(prompting).To(BeTrue())
			Expect(prompted).To(Equal("metadata.name"))
			Expect(model.View()).To(ContainSubstring("Validate needs metadata.name >"))

			model = typeFilter(model, "web")
			model, cmd = press(model, enterKey)
			Expect(cmd).NotTo(BeNil(), "the confirmed gate releases the held Validate")
			Expect(draftValue(model, "metadata.name")).To(Equal("web"),
				"the prompt confirms into the Draft at the real Field Path")

			model, post := press(model, cmd())
			Expect(model.Validating()).To(BeTrue())
			press(model, post())
			Expect(validator.calls).To(HaveLen(1))
			Expect(string(validator.calls[0].manifest)).To(ContainSubstring("name: web"))
		})

		It("prompts for the namespace when the Kind is namespaced and no default resolves", func() {
			validator := &stubValidator{outcome: data.Clean{}}
			model := composeValidatable(validatingShell(validator, ""), kindNamed("Deployment", "v1"))

			model, cmd := press(model, keyRune('v'))
			Expect(cmd).To(BeNil())
			prompted, prompting := model.PromptingForValidateMetadata()
			Expect(prompting).To(BeTrue())
			Expect(prompted).To(Equal("metadata.namespace"))

			model = typeFilter(model, "prod")
			model, cmd = press(model, enterKey)
			Expect(cmd).NotTo(BeNil())
			Expect(draftValue(model, "metadata.namespace")).To(Equal("prod"))

			model, post := press(model, cmd())
			press(model, post())
			Expect(validator.calls).To(HaveLen(1))
			Expect(validator.calls[0].namespace).To(Equal("prod"))
		})

		It("chains the prompts when both name and namespace are missing", func() {
			validator := &stubValidator{outcome: data.Clean{}}
			model := widen(openKind(validatingShell(validator, ""), kindNamed("Deployment", "v1")))

			model, _ = press(model, keyRune('v'))
			prompted, _ := model.PromptingForValidateMetadata()
			Expect(prompted).To(Equal("metadata.name"))

			model = typeFilter(model, "web")
			model, cmd := press(model, enterKey)
			Expect(cmd).To(BeNil(), "confirming the name moves to the namespace prompt, not to the POST")
			prompted, prompting := model.PromptingForValidateMetadata()
			Expect(prompting).To(BeTrue())
			Expect(prompted).To(Equal("metadata.namespace"))

			model = typeFilter(model, "prod")
			model, cmd = press(model, enterKey)
			Expect(cmd).NotTo(BeNil())
			model, _ = press(model, cmd())
			Expect(model.Validating()).To(BeTrue())
		})

		It("skips the namespace gate entirely for a cluster-scoped Kind", func() {
			validator := &stubValidator{outcome: data.Clean{}}
			model := validatingShell(validator, "")
			model = composeValidatable(model, widgetKind())

			validateThrough(model)

			Expect(validator.calls).To(HaveLen(1))
			Expect(validator.calls[0].namespace).To(BeEmpty(),
				"a cluster-scoped Kind never resolves a namespace — the endpoint ignores it")
		})

		It("Esc cancels the whole Validate from the prompt, the Draft untouched", func() {
			validator := &stubValidator{outcome: data.Clean{}}
			model := widen(openKind(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1")))
			model, _ = press(model, keyRune('v'))
			model = typeFilter(model, "we")

			model, _ = press(model, escKey)

			_, prompting := model.PromptingForValidateMetadata()
			Expect(prompting).To(BeFalse())
			Expect(model.Validating()).To(BeFalse())
			Expect(validator.calls).To(BeEmpty(), "a cancelled gate never POSTs")
			_, filled := model.DraftValueAt("metadata.name")
			Expect(filled).To(BeFalse())
		})

		It("rejects an empty confirm inline and keeps the prompt open", func() {
			model := widen(openKind(validatingShell(&stubValidator{outcome: data.Clean{}}, "team-a"), kindNamed("Deployment", "v1")))
			model, _ = press(model, keyRune('v'))

			model, cmd := press(model, enterKey)

			Expect(cmd).To(BeNil())
			_, prompting := model.PromptingForValidateMetadata()
			Expect(prompting).To(BeTrue())
			Expect(model.View()).To(ContainSubstring("type a value"))
		})
	})

	When("mappable findings land on the tree", func() {
		It("marks the offending rows and shows the server's messages in the detail pane", func() {
			validator := &stubValidator{outcome: data.Invalid{Status: fixtureStatus("deployment_required.json")}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1"))

			model = validateThrough(model)

			Expect(model.ValidateFindingPaths()).To(Equal([]string{"spec.selector", "spec.template.metadata.labels"}),
				"the mapped findings keep the server's cause order")
			Expect(model.ResultsPaneOpen()).To(BeFalse(),
				"with every finding mapped, the tree is the whole story — no pane interrupts")
			Expect(model.View()).To(ContainSubstring("2 Validate findings"))

			model, _ = press(model, keyRune('g'))
			model = expandField(model, "spec")
			Expect(model.View()).To(ContainSubstring("selector ✘"),
				"the error marker sits on the offending row, distinct from the required marker")

			model = focusField(model, "spec.selector")
			Expect(model.ValidateFindingMessages("spec.selector")).To(Equal([]string{"Required value"}))
			Expect(model.View()).To(ContainSubstring("Required value"),
				"the finding's message renders in the detail pane when the row is focused")
		})

		It("degrades a finding whose path the tree cannot resolve to the results pane", func() {
			validator := &stubValidator{outcome: data.Invalid{Status: fixtureStatus("pod_enum.json")}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1"))

			model = validateThrough(model)

			Expect(model.ValidateFindingPaths()).To(BeEmpty(),
				"spec.containers[0] spells a valid Field Path, but apps/v1 Deployment's tree cannot reach it")
			Expect(model.ResultsPaneOpen()).To(BeTrue())
			Expect(model.UnmappableFindings()).To(HaveLen(5))
			Expect(model.View()).To(ContainSubstring("spec.containers[0].imagePullPolicy — Unsupported value"),
				"the degraded finding keeps the server's raw field spelling as provenance")
		})

		It("maps a finding through an instantiated item selector onto the item's row", func() {
			validator := &stubValidator{outcome: data.Invalid{
				Status: statusWithCause("spec.template.spec.containers[0].name", "Required value"),
			}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1"))
			model = expandField(model, "spec")
			model = expandField(model, "spec.template")
			model = expandField(model, "spec.template.spec")
			model = focusField(model, "spec.template.spec.containers")
			model, _ = press(model, keyRune('a')) // instantiate containers[0]

			model = validateThrough(model)

			Expect(model.ValidateFindingPaths()).To(Equal([]string{"spec.template.spec.containers[0].name"}),
				"the bracket selector resolves onto the instantiated item's row")
			Expect(model.ResultsPaneOpen()).To(BeFalse())
		})
	})

	When("the jump key walks the findings", func() {
		It("focuses the first finding's node with ancestors expanded, then cycles in order", func() {
			validator := &stubValidator{outcome: data.Invalid{Status: fixtureStatus("gadget_cel.json")}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Gadget", "v1"))
			model = validateThrough(model)
			Expect(model.ValidateFindingPaths()).To(Equal([]string{"spec", "spec.profile"}))

			model, _ = press(model, keyRune('n'))
			Expect(model.FocusedDraftPath()).To(Equal("spec"), "n lands on the first finding")

			model, _ = press(model, keyRune('n'))
			Expect(model.FocusedFieldPath()).To(Equal("spec.profile"))
			Expect(model.VisibleFieldPaths()).To(ContainElement("spec.profile"),
				"the jump expands the finding's ancestors, like the deep-link landing")

			model, _ = press(model, keyRune('n'))
			Expect(model.FocusedDraftPath()).To(Equal("spec"), "subsequent presses cycle through the findings")
		})

		It("reaches a finding inside a collapsed subtree, item selector included", func() {
			validator := &stubValidator{outcome: data.Invalid{
				Status: statusWithCause("spec.template.spec.containers[0].name", "Required value"),
			}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1"))
			model = expandField(model, "spec")
			model = expandField(model, "spec.template")
			model = expandField(model, "spec.template.spec")
			model = focusField(model, "spec.template.spec.containers")
			model, _ = press(model, keyRune('a'))
			model, _ = press(model, keyRune('g'))
			model = focusField(model, "spec")
			model, _ = press(model, keyRune('h')) // collapse the whole spec subtree
			model = validateThrough(model)

			model, _ = press(model, keyRune('n'))

			Expect(model.FocusedDraftPath()).To(Equal("spec.template.spec.containers[0].name"))
			Expect(model.VisibleFieldPaths()).To(ContainElement("spec.template.spec.containers.name"),
				"the jump re-expands every collapsed ancestor down to the item's leaf")
		})
	})

	When("the results pane carries what the tree cannot", func() {
		It("opens on a cause-less webhook denial with the Status summary and the denial's text", func() {
			validator := &stubValidator{outcome: data.Invalid{Status: fixtureStatus("configmap_webhook_denial.json")}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1"))

			model = validateThrough(model)

			Expect(model.ResultsPaneOpen()).To(BeTrue())
			Expect(model.ValidateFindingPaths()).To(BeEmpty())
			Expect(model.UnmappableFindings()).To(HaveLen(1))
			Expect(model.View()).To(ContainSubstring("Validate results"))
			Expect(model.View()).To(ContainSubstring("(HTTP 400)"))
			Expect(model.View()).To(ContainSubstring("admission webhook"),
				"the denial's own words are what the user needs to see")
			Expect(model.View()).To(ContainSubstring("esc dismiss"))
		})

		It("renders a 404's Status summary rather than an empty findings list", func() {
			status := metav1.Status{
				TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
				Status:   metav1.StatusFailure,
				Reason:   metav1.StatusReasonNotFound,
				Code:     404,
				Message:  `namespaces "gone" not found`,
			}
			validator := &stubValidator{outcome: data.Invalid{Status: status}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1"))

			model = validateThrough(model)

			Expect(model.ResultsPaneOpen()).To(BeTrue())
			Expect(model.View()).To(ContainSubstring("(HTTP 404)"))
			Expect(model.View()).To(ContainSubstring(`namespaces "gone" not found`),
				"a cause-less Status still explains itself through its summary message")
		})

		It("dismisses on Esc, stays reopenable on r, and keeps other keys inert while open", func() {
			validator := &stubValidator{outcome: data.Invalid{Status: fixtureStatus("configmap_webhook_denial.json")}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1"))
			model = validateThrough(model)
			focused := model.FocusedFieldPath()

			model, cmd := press(model, keyRune('j'), keyRune('q'), keyRune('v'))
			Expect(cmd).To(BeNil(), "the open pane consumes the keys — one overlay at a time")
			Expect(model.ResultsPaneOpen()).To(BeTrue())
			Expect(model.FocusedFieldPath()).To(Equal(focused))
			Expect(validator.calls).To(HaveLen(1), "v is inert while the pane is open")

			model, _ = press(model, escKey)
			Expect(model.ResultsPaneOpen()).To(BeFalse())
			Expect(model.ComposeOpen()).To(BeTrue())

			model, _ = press(model, keyRune('r'))
			Expect(model.ResultsPaneOpen()).To(BeTrue(), "r reopens the pane while results exist")
		})
	})

	When("Validate is unavailable", func() {
		It("renders the reason in the results pane, distinct from manifest errors, marking nothing", func() {
			validator := &stubValidator{outcome: data.Unavailable{
				Reason: "the cluster refused the dry-run (HTTP 403): forbidden",
			}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1"))

			model = validateThrough(model)

			Expect(model.ResultsPaneOpen()).To(BeTrue())
			reason, unavailable := model.ValidateUnavailable()
			Expect(unavailable).To(BeTrue())
			Expect(reason).To(ContainSubstring("HTTP 403"))
			Expect(model.View()).To(ContainSubstring("Validate unavailable: the cluster refused the dry-run"))
			Expect(model.View()).To(ContainSubstring("says nothing about the Manifest"),
				"unavailability must read as the cluster's failure, never the Manifest's")
			Expect(model.ValidateFindingPaths()).To(BeEmpty())
			Expect(model.View()).NotTo(ContainSubstring("✘"), "an unavailable Validate never marks tree nodes")
		})
	})

	When("a clean pass lands", func() {
		It("confirms positively and clears the previous findings' markers", func() {
			validator := &stubValidator{outcome: data.Invalid{Status: fixtureStatus("deployment_required.json")}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1"))
			model = validateThrough(model)
			model, _ = press(model, keyRune('g'))
			model = expandField(model, "spec")
			Expect(model.View()).To(ContainSubstring("✘"))

			validator.outcome = data.Clean{}
			model = validateThrough(model)

			Expect(model.ValidatePassed()).To(BeTrue())
			Expect(model.View()).To(ContainSubstring("✔ Validate passed"))
			Expect(model.ValidateFindingPaths()).To(BeEmpty())
			Expect(model.View()).NotTo(ContainSubstring("✘"), "a clean pass clears every prior marker")
		})
	})

	When("the marker lifecycle answers the Draft", func() {
		It("marks findings stale on a confirmed mutation — they stay browsable until v revalidates", func() {
			validator := &stubValidator{outcome: data.Invalid{Status: fixtureStatus("deployment_required.json")}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1"))
			model = validateThrough(model)
			model, _ = press(model, keyRune('g'))
			model = expandField(model, "spec")

			model = confirmLeaf(model, "spec.replicas", "3")

			Expect(model.ValidateStale()).To(BeTrue())
			Expect(model.ValidateFindingPaths()).To(HaveLen(2), "stale findings keep their markers as a to-do list")
			Expect(model.View()).To(ContainSubstring("✘"))
			Expect(model.View()).To(ContainSubstring("stale, v revalidates"))

			model = focusField(model, "spec.selector")
			Expect(model.View()).To(ContainSubstring("stale — the Draft changed since this Validate"),
				"the detail pane says why the finding may no longer hold")
		})

		It("drops a clean confirmation on mutation — the pass no longer speaks for the Draft", func() {
			validator := &stubValidator{outcome: data.Clean{}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1"))
			model = validateThrough(model)
			Expect(model.ValidatePassed()).To(BeTrue())

			model, _ = press(model, keyRune('g'))
			model = expandField(model, "spec")
			model = confirmLeaf(model, "spec.replicas", "3")

			Expect(model.ValidatePassed()).To(BeFalse())
			Expect(model.View()).NotTo(ContainSubstring("Validate passed"))
		})

		It("drops findings entirely on a version switch — the paths may not exist anymore", func() {
			validator := &stubValidator{outcome: data.Invalid{Status: statusWithCause("spec.size", "Invalid value")}}
			model := newVersionedShellWith(corpusFetcher(), validator)
			model = composeValidatable(model, widgetV1())
			model = expandField(model, "spec")
			model = confirmLeaf(model, "spec.size", "5")
			model = validateThrough(model)
			Expect(model.ValidateFindingPaths()).To(Equal([]string{"spec.size"}))

			model = switchWidgetToV2(model)

			Expect(model.ConfirmingVersionSwitch()).To(BeFalse(), "name and size both carry — a clean switch")
			Expect(model.Breadcrumb()).To(HavePrefix("craft.example.com/v2 Widget"))
			Expect(model.ValidateFindingPaths()).To(BeEmpty())
			Expect(model.ValidateStale()).To(BeFalse())
			Expect(model.View()).NotTo(ContainSubstring("Validate finding"),
				"the rebuilt compose view starts with no Validate state at all")
		})
	})

	When("the hint bar and help document the Validate keys", func() {
		It("always spells v, and adds n and r contextually once findings exist", func() {
			validator := &stubValidator{outcome: data.Invalid{Status: fixtureStatus("deployment_required.json")}}
			model := composeValidatable(validatingShell(validator, "team-a"), kindNamed("Deployment", "v1"))
			Expect(model.View()).To(ContainSubstring("v validate"))
			Expect(model.View()).NotTo(ContainSubstring("n next finding"))

			model = validateThrough(model)

			Expect(model.View()).To(ContainSubstring("n next finding"))
			Expect(model.View()).To(ContainSubstring("r results"))
		})

		It("documents v, n, r, and the marker lifecycle in the ? help overlay", func() {
			model, _ := press(composeDeployment(), keyRune('?'))

			Expect(model.HelpOpen()).To(BeTrue())
			Expect(model.View()).To(ContainSubstring("server-side dry-run"))
			Expect(model.View()).To(ContainSubstring("jump to the first Validate finding"))
			Expect(model.View()).To(ContainSubstring("results pane"))
			Expect(model.View()).To(ContainSubstring("marks findings stale"))
		})
	})
})
