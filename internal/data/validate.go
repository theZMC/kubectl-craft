package data

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// Validator is the Session's Validate seam beside Fetcher: it submits a
// Manifest to the API server with server-side dry-run — full schema, CRD
// rule, and admission webhook validation, nothing persisted (DESIGN.md —
// Output) — and classifies what came back.
//
// Validate never returns an error: every failure mode is a value of the
// three-way Outcome, so the TUI structurally cannot confuse infrastructure
// failure with manifest errors — "RBAC 403 / network failures render as
// Validate unavailable, never as manifest errors" (MILESTONES.md). With no
// error crossing the seam, no wrapcheck ignore entry is needed for it
// (unlike Fetcher, whose decorators pass errors through).
//
// That includes cancellation: a canceled ctx surfaces as Unavailable with
// a "context canceled" reason, so a caller abandoning an in-flight
// Validate should check its own ctx.Err() before rendering an
// unavailable notice.
type Validator interface {
	// Validate POSTs the Manifest bytes to the Kind's resource endpoint
	// with dryRun=All. The namespace is the resolved one (see
	// ResolveNamespace); cluster-scoped Kinds ignore it entirely.
	Validate(ctx context.Context, kind Kind, manifest []byte, namespace string) Outcome
}

var _ Validator = (*Client)(nil)

// Outcome is the three-way result of one Validate: Clean, Invalid, or
// Unavailable. The variants are distinct types — a type switch consumes
// them — so no caller ever discriminates on strings.
type Outcome interface {
	// sealed keeps the variant set closed to this package: exactly the
	// three cases exist, and a type switch over them is exhaustive.
	sealed()
}

// Clean is the Outcome of a dry-run the server accepted: the Manifest
// passed full schema, CRD-rule, and webhook validation.
type Clean struct{}

// Invalid is the Outcome of a dry-run the server rejected with validation
// content: the raw metav1.Status, verbatim, for internal/validate to map
// into findings. The data layer does not interpret causes (that is
// validate.MapStatus's job).
type Invalid struct {
	Status metav1.Status
}

// Unavailable is the Outcome when Validate could not run or could not be
// trusted: RBAC 401/403, network/DNS/timeout failures, non-Status error
// bodies, unexpected 5xx. Reason is the human-readable sentence the results
// pane renders after "Validate unavailable: ".
type Unavailable struct {
	Reason string
}

func (Clean) sealed()       {}
func (Invalid) sealed()     {}
func (Unavailable) sealed() {}

// Validate POSTs the Manifest to the Kind's resource collection with
// dryRun=All and classifies the answer.
//
// It is a bare create POST, not server-side apply, on purpose: composing is
// building a NEW Manifest (CONTEXT.md), and a create dry-run validates it
// exactly as `kubectl create --dry-run=server` would — as a new object. An
// SSA dry-run would instead validate the patch MERGED into any existing
// Instance of the same name, silently validating a different object than
// the one being composed.
//
// The Manifest bytes travel verbatim as application/yaml — the API server
// decodes YAML natively for write requests — so Validate checks exactly the
// bytes the Session would Emit, never a re-marshalled rendition.
func (c *Client) Validate(ctx context.Context, kind Kind, manifest []byte, namespace string) Outcome {
	endpoint, err := c.resourceEndpoint(kind, namespace)
	if err != nil {
		return Unavailable{Reason: err.Error()}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(manifest))
	if err != nil {
		return Unavailable{Reason: fmt.Sprintf("building the dry-run request for %s: %v", endpoint, err)}
	}
	req.Header.Set("Content-Type", "application/yaml")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Unavailable{Reason: fmt.Sprintf("unable to connect to the server %s: %v", c.base.Host, err)}
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Unavailable{Reason: fmt.Sprintf("reading the dry-run response from %s: %v", c.base.Host, err)}
	}

	return classify(resp.StatusCode, body)
}

// resourceEndpoint builds the Kind's resource collection URL from the
// discovery data already on the Kind: the group-version path plus the
// plural, with a namespace segment for namespaced Kinds and never for
// cluster-scoped ones (which ignore any resolved namespace). The dryRun=All
// query is what keeps the POST persistence-free; fieldValidation=Strict
// makes the server reject unknown and duplicate fields instead of merely
// warning — under the default (Warn) a typo'd field name would pass as
// Clean, and a lying Clean breaks "Validate is the safety net" (DESIGN.md).
// Servers predating field validation ignore the unknown parameter.
//
// A namespaced Kind with no namespace at all — or a namespace that is not
// even a DNS label — is refused here, before any request leaves: joined
// into the path raw, either would send a malformed POST the server answers
// with a misleading 404 Status, and rendering that as a manifest error
// would blame the Manifest for a request the Session failed to address. It
// is Unavailable — Validate could not run — with a reason that names the
// fix.
func (c *Client) resourceEndpoint(kind Kind, namespace string) (string, error) {
	u := *c.base
	switch {
	case kind.Namespaced && namespace == "":
		return "", fmt.Errorf(
			"%s is namespaced but no namespace resolved: set metadata.namespace in the Draft "+
				"or launch with --namespace", kind.GVK.Kind,
		)
	case kind.Namespaced && !isDNSLabel(namespace):
		return "", fmt.Errorf(
			"%q is not a valid namespace name (a lowercase DNS label of at most 63 characters)",
			namespace,
		)
	case kind.Namespaced:
		u.Path = path.Join(u.Path, kind.GroupVersionPath, "namespaces", namespace, kind.Plural)
	default:
		u.Path = path.Join(u.Path, kind.GroupVersionPath, kind.Plural)
	}
	u.RawQuery = url.Values{
		"dryRun":          []string{"All"},
		"fieldValidation": []string{"Strict"},
	}.Encode()
	return u.String(), nil
}

// isDNSLabel reports whether the namespace spells an RFC 1123 DNS label —
// the shape every real namespace name has, and the only shape that joins
// into a request path without rewriting it.
func isDNSLabel(namespace string) bool {
	if namespace == "" || len(namespace) > 63 {
		return false
	}
	for i, r := range namespace {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i > 0 && i < len(namespace)-1:
		default:
			return false
		}
	}
	return true
}

// classify maps one dry-run answer to its Outcome:
//
//   - 2xx — Clean: the server accepted the dry-run.
//   - 401/403 — Unavailable: an auth or RBAC refusal says nothing about
//     the Manifest, even though it arrives Status-shaped.
//   - 429 — Unavailable: API Priority & Fairness throttling also arrives
//     Status-shaped (Reason TooManyRequests), and a throttled cluster is
//     never the Manifest's fault.
//   - any other 4xx carrying a metav1.Status — Invalid: the server judged
//     the Manifest (schema violations at 422, webhook denials at 400, …)
//     and the raw Status travels out for internal/validate to map.
//   - everything else — Unavailable: a non-Status error body was not a
//     validation verdict, and an unexpected 5xx is the cluster failing,
//     not the Manifest.
func classify(code int, body []byte) Outcome {
	status, isStatus := decodeStatus(body)

	switch {
	case code >= 200 && code < 300:
		return Clean{}
	case code == http.StatusUnauthorized || code == http.StatusForbidden,
		code == http.StatusTooManyRequests:
		return Unavailable{Reason: refusalReason(code, status, isStatus)}
	case code < 500 && isStatus:
		return Invalid{Status: status}
	case isStatus:
		return Unavailable{Reason: fmt.Sprintf(
			"the cluster failed the dry-run (HTTP %d): %s", code, status.Message,
		)}
	default:
		return Unavailable{Reason: fmt.Sprintf(
			"the cluster answered the dry-run with HTTP %d and no Status", code,
		)}
	}
}

// decodeStatus reads the response body as a metav1.Status when it is one —
// discriminated by its declared kind, so an arbitrary JSON error page never
// passes as a validation verdict.
func decodeStatus(body []byte) (metav1.Status, bool) {
	var status metav1.Status
	if err := json.Unmarshal(body, &status); err != nil {
		return metav1.Status{}, false
	}
	return status, status.Kind == "Status"
}

// refusalReason spells an auth/RBAC/throttling refusal for the results
// pane, carrying the server's own words when it sent any.
func refusalReason(code int, status metav1.Status, isStatus bool) string {
	if isStatus && status.Message != "" {
		return fmt.Sprintf("the cluster refused the dry-run (HTTP %d): %s", code, status.Message)
	}
	return fmt.Sprintf("the cluster refused the dry-run (HTTP %d)", code)
}

// ResolveNamespace resolves the namespace a Manifest validates in, the way
// kubectl resolves it: metadata.namespace from the Draft when set, else the
// Session's default (the --namespace flag / kubeconfig context default the
// command resolved at launch). It is a pure helper — no I/O, no cluster —
// and a Manifest whose metadata cannot even be parsed simply falls back to
// the Session default: the dry-run itself will say what is wrong with it.
func ResolveNamespace(manifest []byte, sessionDefault string) string {
	var typed struct {
		Metadata struct {
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	}
	if err := yaml.Unmarshal(manifest, &typed); err != nil {
		return sessionDefault
	}
	if typed.Metadata.Namespace != "" {
		return typed.Metadata.Namespace
	}
	return sessionDefault
}
