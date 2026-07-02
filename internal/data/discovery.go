package data

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

// Kind is one entry of the cluster's browsable Kind list (CONTEXT.md: a Kind
// is the unit of browsing and composing): a create-capable resource type at
// one served version, discovered live from the cluster.
type Kind struct {
	// GVK identifies the Kind: group/version/kind.
	GVK schema.GroupVersionKind

	// GroupVersionPath is the group-version path addressing the OpenAPI v3
	// Document that defines this Kind's Type Schema, exactly as
	// Fetcher.FetchGroupDocument expects it: "api/v1" for the core group,
	// "apis/<group>/<version>" otherwise.
	GroupVersionPath string

	// Plural is the resource's plural name exactly as discovery serves it
	// (e.g. "deployments" for Deployment); the deep-link arg resolves
	// through it alongside the Kind name and short names.
	Plural string

	// ShortNames are the resource's short names as served by discovery
	// (e.g. "deploy" for Deployment); the deep-link arg resolves through
	// them.
	ShortNames []string

	// Namespaced reports whether the resource lives inside a namespace,
	// exactly as discovery serves it. Validate's endpoint construction
	// hinges on it: a namespaced Kind POSTs under a namespace segment,
	// a cluster-scoped Kind never does. Discovery is the source on
	// purpose — it states scope directly per resource, where the OpenAPI
	// v3 Documents only imply it through their path shapes.
	Namespaced bool

	// Preferred marks whether this version is its group's Preferred
	// Version — the version the browser lands on when the Kind is served
	// at multiple versions (CONTEXT.md).
	Preferred bool
}

// KindLister is the slice of client-go discovery that the Kind list needs.
// It is a seam separate from Fetcher on purpose: discovery answers "which
// Kinds exist" while the Fetcher sources the OpenAPI v3 Documents behind
// their Type Schemas — and only the latter ever gains a cache (DESIGN.md —
// Data layer: discovery is never cached).
type KindLister interface {
	// ServerGroupsAndResources matches k8s.io/client-go/discovery, so the
	// live *discovery.DiscoveryClient satisfies the seam unmodified.
	ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error)
}

var _ KindLister = (*discovery.DiscoveryClient)(nil)

// NewKindLister builds the live discovery client from the Session's resolved
// REST config (kubeconfig, flags, and auth are already folded in by the
// caller). The client speaks aggregated discovery (1.26+), so listing every
// Kind is one live round-trip.
func NewKindLister(cfg *rest.Config) (KindLister, error) {
	client, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building the cluster discovery client: %w", err)
	}
	return client, nil
}

// DiscoverKinds returns the cluster's browsable Kind list: every
// create-capable Kind with its GVK, group-version path, short names,
// namespace scope, and Preferred Version marking, in deterministic
// (group, version, kind) order so the picker renders stably across calls.
//
// Results are fetched live on every call and never cached (DESIGN.md — Data
// layer): aggregated discovery makes the list one round-trip, and caching it
// would reintroduce the stale-cluster-view problem.
//
// A partially failed discovery — one aggregated apiservice down — still
// returns the Kinds that did discover, mirroring kubectl: a single broken
// apiservice must not take the whole picker down.
func DiscoverKinds(lister KindLister) ([]Kind, error) {
	groups, resourceLists, err := lister.ServerGroupsAndResources()
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		return nil, fmt.Errorf("discovering the cluster's Kinds: %w", err)
	}

	preferred := preferredVersions(groups)

	var kinds []Kind
	for _, list := range resourceLists {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil {
			return nil, fmt.Errorf("parsing the discovered group version %q: %w", list.GroupVersion, parseErr)
		}

		for _, resource := range list.APIResources {
			if !isPersistable(resource) {
				continue
			}
			kinds = append(kinds, browsableKind(gv, resource, preferred))
		}
	}

	slices.SortFunc(kinds, compareKinds)
	return kinds, nil
}

// isPersistable is the create-verb capability filter (DESIGN.md — Flow §2):
// a Kind is browsable only when composing a Manifest for it is meaningful,
// which means the resource must be persistable. The discriminator is the
// verb set a persisted resource necessarily serves: both create AND list.
//
//   - Read-only virtual kinds (ComponentStatus) serve list but never
//     create — nothing to compose.
//   - Request-shaped kinds (TokenReview, SubjectAccessReview, Binding, …)
//     serve create — the API "creates" the request envelope to answer it —
//     but never list, because nothing is persisted that could be listed
//     back. A Manifest is meaningless for them.
//   - Subresources (deployments/status, pods/exec, …) are positions on a
//     parent resource, not Kinds of their own; they are excluded by name
//     shape regardless of the verbs they serve.
//
// This is an API-capability filter, never an RBAC one: it asks what the
// cluster can persist, not what the current user may create.
func isPersistable(resource metav1.APIResource) bool {
	if strings.Contains(resource.Name, "/") {
		return false
	}
	return slices.Contains(resource.Verbs, "create") && slices.Contains(resource.Verbs, "list")
}

// browsableKind shapes one discovered resource into a Kind list entry. The
// rare per-resource group/version override (a resource advertised under a
// list but canonically defined in another group-version) is honored so the
// GVK and the Document path stay coherent.
func browsableKind(listGV schema.GroupVersion, resource metav1.APIResource, preferred map[string]string) Kind {
	gvk := schema.GroupVersionKind{Group: listGV.Group, Version: listGV.Version, Kind: resource.Kind}
	if resource.Group != "" {
		gvk.Group = resource.Group
	}
	if resource.Version != "" {
		gvk.Version = resource.Version
	}

	return Kind{
		GVK:              gvk,
		GroupVersionPath: groupVersionPath(gvk.GroupVersion()),
		Plural:           resource.Name,
		ShortNames:       slices.Clone(resource.ShortNames),
		Namespaced:       resource.Namespaced,
		Preferred:        preferred[gvk.Group] == gvk.Version,
	}
}

// groupVersionPath derives the group-version path Fetcher.FetchGroupDocument
// expects: the core group lives at api/<version>, named groups at
// apis/<group>/<version>.
func groupVersionPath(gv schema.GroupVersion) string {
	if gv.Group == "" {
		return "api/" + gv.Version
	}
	return "apis/" + gv.Group + "/" + gv.Version
}

// preferredVersions indexes each group's Preferred Version by group name.
func preferredVersions(groups []*metav1.APIGroup) map[string]string {
	preferred := make(map[string]string, len(groups))
	for _, group := range groups {
		preferred[group.Name] = group.PreferredVersion.Version
	}
	return preferred
}

// compareKinds orders the Kind list deterministically by (group, version,
// kind) so the picker renders stably across calls.
func compareKinds(a, b Kind) int {
	return cmp.Or(
		strings.Compare(a.GVK.Group, b.GVK.Group),
		strings.Compare(a.GVK.Version, b.GVK.Version),
		strings.Compare(a.GVK.Kind, b.GVK.Kind),
	)
}
