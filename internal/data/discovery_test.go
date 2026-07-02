package data_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"

	"github.com/thezmc/kubectl-craft/internal/data"
)

// stubKindLister is the hermetic stand-in for client-go discovery: it hands
// back a canned aggregated-discovery answer and counts calls so specs can
// pin the live-per-call contract.
type stubKindLister struct {
	groups []*metav1.APIGroup
	lists  []*metav1.APIResourceList
	err    error
	calls  int
}

func (s *stubKindLister) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	s.calls++
	return s.groups, s.lists, s.err
}

// discoveryFixture is a miniature cluster answer mixing every shape the
// filter must discriminate: persistable Kinds, a read-only virtual kind, a
// request-shaped kind, subresources, and a group served at two versions.
// The resource lists arrive deliberately unsorted so specs pin the
// deterministic output ordering.
func discoveryFixture() *stubKindLister {
	return &stubKindLister{
		groups: []*metav1.APIGroup{
			{Name: "", PreferredVersion: metav1.GroupVersionForDiscovery{Version: "v1"}},
			{Name: "apps", PreferredVersion: metav1.GroupVersionForDiscovery{Version: "v1"}},
			{Name: "authentication.k8s.io", PreferredVersion: metav1.GroupVersionForDiscovery{Version: "v1"}},
			{Name: "craft.example.com", PreferredVersion: metav1.GroupVersionForDiscovery{Version: "v2"}},
		},
		lists: []*metav1.APIResourceList{
			{
				GroupVersion: "craft.example.com/v2",
				APIResources: []metav1.APIResource{
					{Name: "widgets", Kind: "Widget", ShortNames: []string{"wgt"}, Verbs: metav1.Verbs{"create", "delete", "get", "list", "watch"}},
				},
			},
			{
				GroupVersion: "v1",
				APIResources: []metav1.APIResource{
					{Name: "configmaps", Kind: "ConfigMap", ShortNames: []string{"cm"}, Verbs: metav1.Verbs{"create", "delete", "get", "list", "watch"}},
					{Name: "componentstatuses", Kind: "ComponentStatus", ShortNames: []string{"cs"}, Verbs: metav1.Verbs{"get", "list"}},
					{Name: "bindings", Kind: "Binding", Verbs: metav1.Verbs{"create"}},
				},
			},
			{
				GroupVersion: "apps/v1",
				APIResources: []metav1.APIResource{
					{Name: "deployments", Kind: "Deployment", ShortNames: []string{"deploy"}, Verbs: metav1.Verbs{"create", "delete", "get", "list", "patch", "update", "watch"}},
					{Name: "deployments/status", Kind: "Deployment", Verbs: metav1.Verbs{"get", "patch", "update"}},
					// Contrived: a subresource serving both create and list
					// must still be excluded — by name shape, not verbs.
					{Name: "deployments/probes", Kind: "DeploymentProbe", Verbs: metav1.Verbs{"create", "list"}},
				},
			},
			{
				GroupVersion: "authentication.k8s.io/v1",
				APIResources: []metav1.APIResource{
					{Name: "tokenreviews", Kind: "TokenReview", Verbs: metav1.Verbs{"create"}},
				},
			},
			{
				GroupVersion: "craft.example.com/v1",
				APIResources: []metav1.APIResource{
					{Name: "widgets", Kind: "Widget", ShortNames: []string{"wgt"}, Verbs: metav1.Verbs{"create", "delete", "get", "list", "watch"}},
				},
			},
		},
	}
}

var _ = Describe("discovering the cluster's browsable Kinds", func() {
	When("the cluster serves a mix of persistable, read-only, request-shaped, and subresource entries", func() {
		var kinds []data.Kind

		BeforeEach(func() {
			var err error
			kinds, err = data.DiscoverKinds(discoveryFixture())
			Expect(err).NotTo(HaveOccurred())
		})

		It("keeps exactly the create-capable Kinds, in deterministic (group, version, kind) order", func() {
			Expect(kinds).To(Equal([]data.Kind{
				{
					GVK:              schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"},
					GroupVersionPath: "api/v1",
					ShortNames:       []string{"cm"},
					Preferred:        true,
				},
				{
					GVK:              schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
					GroupVersionPath: "apis/apps/v1",
					ShortNames:       []string{"deploy"},
					Preferred:        true,
				},
				{
					GVK:              schema.GroupVersionKind{Group: "craft.example.com", Version: "v1", Kind: "Widget"},
					GroupVersionPath: "apis/craft.example.com/v1",
					ShortNames:       []string{"wgt"},
					Preferred:        false,
				},
				{
					GVK:              schema.GroupVersionKind{Group: "craft.example.com", Version: "v2", Kind: "Widget"},
					GroupVersionPath: "apis/craft.example.com/v2",
					ShortNames:       []string{"wgt"},
					Preferred:        true,
				},
			}))
		})

		It("addresses the core group at api/<version> and named groups at apis/<group>/<version>", func() {
			paths := make(map[string]string, len(kinds))
			for _, kind := range kinds {
				paths[kind.GVK.Kind+"/"+kind.GVK.Version] = kind.GroupVersionPath
			}

			Expect(paths).To(HaveKeyWithValue("ConfigMap/v1", "api/v1"),
				"a core Kind's Document path must use the api/<version> shape")
			Expect(paths).To(HaveKeyWithValue("Deployment/v1", "apis/apps/v1"),
				"a named group Kind's Document path must use the apis/<group>/<version> shape")
		})

		It("marks the group's Preferred Version and only that version", func() {
			preferredByVersion := make(map[string]bool, 2)
			for _, kind := range kinds {
				if kind.GVK.Kind == "Widget" {
					preferredByVersion[kind.GVK.Version] = kind.Preferred
				}
			}

			Expect(preferredByVersion).To(Equal(map[string]bool{"v1": false, "v2": true}),
				"only craft.example.com's Preferred Version may carry the marking")
		})

		It("excludes read-only virtual kinds that never serve create", func() {
			Expect(kinds).NotTo(ContainElement(HaveField("GVK.Kind", "ComponentStatus")),
				"a Manifest is meaningless for a read-only kind")
		})

		It("excludes request-shaped kinds that serve create but not list", func() {
			Expect(kinds).NotTo(ContainElement(HaveField("GVK.Kind", "TokenReview")),
				"request envelopes are never persisted, so a Manifest is meaningless")
			Expect(kinds).NotTo(ContainElement(HaveField("GVK.Kind", "Binding")),
				"request envelopes are never persisted, so a Manifest is meaningless")
		})

		It("excludes subresources even when they serve both create and list", func() {
			Expect(kinds).NotTo(ContainElement(HaveField("GVK.Kind", "DeploymentProbe")),
				"subresources are positions on a parent resource, not Kinds of their own")
		})
	})

	When("Kinds are discovered repeatedly", func() {
		It("asks the cluster live on every call, never caching", func() {
			lister := discoveryFixture()

			before, err := data.DiscoverKinds(lister)
			Expect(err).NotTo(HaveOccurred())
			Expect(lister.calls).To(Equal(1))

			// A CRD lands on the cluster between calls; the next answer must
			// reflect it — caching would reintroduce the stale-cluster-view
			// problem (DESIGN.md — Data layer).
			lister.groups = append(lister.groups, &metav1.APIGroup{
				Name:             "late.example.com",
				PreferredVersion: metav1.GroupVersionForDiscovery{Version: "v1"},
			})
			lister.lists = append(lister.lists, &metav1.APIResourceList{
				GroupVersion: "late.example.com/v1",
				APIResources: []metav1.APIResource{
					{Name: "latecomers", Kind: "Latecomer", Verbs: metav1.Verbs{"create", "list"}},
				},
			})

			after, err := data.DiscoverKinds(lister)
			Expect(err).NotTo(HaveOccurred())
			Expect(lister.calls).To(Equal(2))
			Expect(after).To(HaveLen(len(before) + 1))
			Expect(after).To(ContainElement(HaveField("GVK.Kind", "Latecomer")))
		})
	})

	When("one aggregated apiservice fails discovery", func() {
		It("still returns the Kinds that did discover", func() {
			lister := discoveryFixture()
			lister.err = &discovery.ErrGroupDiscoveryFailed{
				Groups: map[schema.GroupVersion]error{
					{Group: "metrics.k8s.io", Version: "v1beta1"}: errors.New("the server is currently unable to handle the request"),
				},
			}

			kinds, err := data.DiscoverKinds(lister)

			Expect(err).NotTo(HaveOccurred(),
				"a single broken apiservice must not take the whole picker down")
			Expect(kinds).To(ContainElement(HaveField("GVK.Kind", "Deployment")))
		})
	})

	When("discovery fails outright", func() {
		It("surfaces the failure instead of an empty Kind list", func() {
			lister := &stubKindLister{err: errors.New("connection refused")}

			kinds, err := data.DiscoverKinds(lister)

			Expect(err).To(MatchError(ContainSubstring("discovering the cluster's Kinds")))
			Expect(err).To(MatchError(ContainSubstring("connection refused")))
			Expect(kinds).To(BeNil())
		})
	})

	When("the cluster answers with an unparsable group version", func() {
		It("surfaces the malformed answer instead of guessing a Document path", func() {
			lister := &stubKindLister{
				lists: []*metav1.APIResourceList{{GroupVersion: "not/a/group-version"}},
			}

			kinds, err := data.DiscoverKinds(lister)

			Expect(err).To(MatchError(ContainSubstring(`parsing the discovered group version "not/a/group-version"`)))
			Expect(kinds).To(BeNil())
		})
	})
})
