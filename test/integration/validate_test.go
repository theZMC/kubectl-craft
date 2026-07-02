package integration_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"

	"github.com/thezmc/kubectl-craft/internal/data"
)

// discoveredKind resolves one GVK from live discovery, so every dry-run
// POST is addressed by exactly the discovery data a Session would hold.
func discoveredKind(ctx SpecContext, gvk schema.GroupVersionKind) data.Kind {
	GinkgoHelper()

	byGVK := kindsByGVK(discoverKinds(ctx, sessionKindLister()))
	Expect(byGVK).To(HaveKey(gvk), "discovery must offer %s", gvk)
	return byGVK[gvk]
}

var (
	deploymentGVK = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	namespaceGVK  = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"}
	configMapGVK  = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	gadgetGVK     = schema.GroupVersionKind{Group: "craft.example.com", Version: "v1", Kind: "Gadget"}
)

// Dry-run persists nothing (ADR-0004's testing dividend), so every spec
// here except the CRD-installing one runs read-parallel.
var _ = Describe("Validate against a live k3s cluster", func() {
	When("the Manifest is valid", func() {
		It("answers Clean from a real dry-run create", func(ctx SpecContext) {
			validator := sessionClient()
			kind := discoveredKind(ctx, configMapGVK)
			manifest := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: craft-validate-clean
data:
  purpose: validate clean pass
`)
			namespace := data.ResolveNamespace(manifest, "default")

			Eventually(func(g Gomega) {
				g.Expect(validator.Validate(ctx, kind, manifest, namespace)).To(Equal(data.Clean{}),
					"a valid Manifest must pass the server's full dry-run validation")
			}).WithContext(ctx).Should(Succeed())
		}, NodeTimeout(defaultSpecTimeout))
	})

	When("the Manifest omits a required field", func() {
		It("answers Invalid with a Status carrying the server's causes", func(ctx SpecContext) {
			validator := sessionClient()
			kind := discoveredKind(ctx, deploymentGVK)
			// No spec.selector: the server answers 422 with
			// FieldValueRequired causes on dotted paths.
			manifest := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: craft-validate-required
spec:
  replicas: 1
  template:
    metadata:
      labels:
        app: craft
    spec:
      containers:
      - name: app
        image: nginx
`)

			var invalid data.Invalid
			Eventually(func(g Gomega) {
				outcome := validator.Validate(ctx, kind, manifest, "default")
				var isInvalid bool
				invalid, isInvalid = outcome.(data.Invalid)
				g.Expect(isInvalid).To(BeTrue(), "the server's rejection must classify as Invalid, got %#v", outcome)
			}).WithContext(ctx).Should(Succeed())

			Expect(invalid.Status.Reason).To(Equal(metav1.StatusReasonInvalid))
			Expect(invalid.Status.Details).NotTo(BeNil(),
				"the raw Status travels out for internal/validate to map")
			Expect(invalid.Status.Details.Causes).To(ContainElement(SatisfyAll(
				HaveField("Field", "spec.selector"),
				HaveField("Message", ContainSubstring("Required value")),
			)), "the Status must carry the required-field cause on its dotted path")
		}, NodeTimeout(defaultSpecTimeout))
	})

	When("the Kind is cluster-scoped", func() {
		It("answers Clean from a POST without a namespace segment", func(ctx SpecContext) {
			validator := sessionClient()
			kind := discoveredKind(ctx, namespaceGVK)
			Expect(kind.Namespaced).To(BeFalse(),
				"live discovery must report Namespace as cluster-scoped")
			manifest := []byte(`apiVersion: v1
kind: Namespace
metadata:
  name: craft-validate-scope-probe
`)

			Eventually(func(g Gomega) {
				// Any resolved namespace is ignored for a cluster-scoped Kind.
				g.Expect(validator.Validate(ctx, kind, manifest, "default")).To(Equal(data.Clean{}))
			}).WithContext(ctx).Should(Succeed())
		}, NodeTimeout(defaultSpecTimeout))
	})

	When("the user has no permission to create the Kind", func() {
		It("answers Unavailable(403) — an RBAC refusal never reads as a manifest error", func(ctx SpecContext) {
			cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
			Expect(err).NotTo(HaveOccurred())
			// The kubeconfig user (cluster-admin) impersonates a user with
			// no bindings at all, so the dry-run POST is refused by RBAC.
			cfg.Impersonate = rest.ImpersonationConfig{UserName: "craft-nobody"}
			validator, err := data.NewClient(cfg)
			Expect(err).NotTo(HaveOccurred())

			kind := discoveredKind(ctx, configMapGVK)
			manifest := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: craft-validate-forbidden
`)

			var unavailable data.Unavailable
			Eventually(func(g Gomega) {
				outcome := validator.Validate(ctx, kind, manifest, "default")
				var isUnavailable bool
				unavailable, isUnavailable = outcome.(data.Unavailable)
				g.Expect(isUnavailable).To(BeTrue(),
					"the RBAC refusal must classify as Unavailable, got %#v", outcome)
			}).WithContext(ctx).Should(Succeed())

			Expect(unavailable.Reason).To(ContainSubstring("403"))
			Expect(unavailable.Reason).To(ContainSubstring("forbidden"),
				"the reason must carry the server's own words for the results pane")
		}, NodeTimeout(defaultSpecTimeout))
	})
})

// gadgetCRDPath is the corpus CRD whose Type Schema carries the CEL rules
// (shared with the fixture captures, so the live spec and the recorded
// Statuses describe one CRD).
const gadgetCRDPath = "../../internal/schema/testdata/crds/gadgets.craft.example.com.yaml"

// gadgetCRD loads the corpus Gadget CRD manifest as an unstructured object
// for the dynamic client to install.
func gadgetCRD() *unstructured.Unstructured {
	GinkgoHelper()

	raw, err := os.ReadFile(gadgetCRDPath)
	Expect(err).NotTo(HaveOccurred())

	var object map[string]any
	Expect(yaml.Unmarshal(raw, &object)).To(Succeed())
	return &unstructured.Unstructured{Object: object}
}

// The spec installs a CRD, so it is decorated Serial: the Kind list it
// changes is shared by every parallel process (DESIGN.md — Testing: mutating
// specs are Serial).
var _ = Describe("Validate of a CEL-ruled corpus CRD", Serial, func() {
	It("answers Invalid carrying the CEL violation from the server", func(ctx SpecContext) {
		cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
		Expect(err).NotTo(HaveOccurred())

		dynamicClient, err := dynamic.NewForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())
		crds := dynamicClient.Resource(crdGVR)

		By("installing the corpus Gadget CRD")
		crd := gadgetCRD()
		_, err = crds.Create(ctx, crd, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) {
			Expect(crds.Delete(ctx, crd.GetName(), metav1.DeleteOptions{})).To(Succeed())
		})

		By("waiting for discovery to serve the Gadget Kind")
		var gadget data.Kind
		Eventually(func(g Gomega) {
			byGVK := kindsByGVK(discoverKinds(ctx, sessionKindLister()))
			g.Expect(byGVK).To(HaveKey(gadgetGVK))
			gadget = byGVK[gadgetGVK]
		}).WithContext(ctx).Should(Succeed())

		By("dry-running a Gadget that violates both CEL rules")
		validator := sessionClient()
		// minReplicas > maxReplicas violates the object-level rule;
		// the profile violates the scalar-level rule.
		manifest := []byte(`apiVersion: craft.example.com/v1
kind: Gadget
metadata:
  name: craft-validate-cel
spec:
  minReplicas: 5
  maxReplicas: 1
  profile: turbo
`)

		// A 404 while the freshly installed CRD is still registering also
		// arrives Status-shaped, so the poll pins the CEL message itself,
		// never just the Invalid classification.
		var invalid data.Invalid
		Eventually(func(g Gomega) {
			outcome := validator.Validate(ctx, gadget, manifest, "default")
			var isInvalid bool
			invalid, isInvalid = outcome.(data.Invalid)
			g.Expect(isInvalid).To(BeTrue(), "the CEL rejection must classify as Invalid, got %#v", outcome)
			g.Expect(invalid.Status.Message).To(ContainSubstring("minReplicas must not exceed maxReplicas"))
		}).WithContext(ctx).Should(Succeed())

		Expect(invalid.Status.Reason).To(Equal(metav1.StatusReasonInvalid))
		Expect(invalid.Status.Details.Causes).To(ContainElement(
			HaveField("Message", ContainSubstring("minReplicas must not exceed maxReplicas")),
		), "the object-level CEL rule must arrive as a Status cause")
		Expect(invalid.Status.Details.Causes).To(ContainElement(
			HaveField("Message", ContainSubstring("profile must be economy, balanced, or performance")),
		), "the scalar-level CEL rule must arrive as a Status cause")
	}, NodeTimeout(defaultSpecTimeout))
})
