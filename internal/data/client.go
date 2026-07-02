package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

// ErrOpenAPIV3NotServed is the capability gate for minimum cluster support.
// It fires when the connected cluster answers 404 for /openapi/v3 — never on
// a version-number check (DESIGN.md: capability-gated, not version-sniffed).
var ErrOpenAPIV3NotServed = errors.New(
	"this cluster does not serve OpenAPI v3 (/openapi/v3 returned 404): " +
		"kubectl-craft sources every Type Schema from the cluster's OpenAPI v3 " +
		"Documents and needs a cluster that serves them (Kubernetes 1.24 or newer)",
)

// GroupVersion is one per-group entry of the live /openapi/v3 index.
type GroupVersion struct {
	// Path is the group-version path as named by the index,
	// e.g. "api/v1" or "apis/apps/v1".
	Path string

	// ContentHash is the server-provided content hash parsed from the
	// index entry's URL. It names the exact revision of the group's
	// OpenAPI v3 Document; empty when the server didn't provide one.
	ContentHash string
}

// Fetcher is the Session's cache-shaped seam for OpenAPI v3 Documents: the
// index is always fetched live, and group documents are addressed by
// (group-version path, server content hash) and returned as raw response
// bytes.
//
// The shape is deliberate: M2's hash-validated disk cache (ADR-0002) slots
// in behind this interface as a purely additive layer. The (path, hash) pair
// is exactly the cache key (v1/<host_port>/<group-version>@<hash>.json) and
// the raw bytes are exactly the cache payload, so a hit is a pure existence
// check and validation stays honest against the server hash.
type Fetcher interface {
	FetchIndex(ctx context.Context) ([]GroupVersion, error)
	FetchGroupDocument(ctx context.Context, groupPath, contentHash string) ([]byte, error)
}

// Client is the live-cluster Fetcher: it talks to the API server the
// Session's resolved context points to, and nothing else.
type Client struct {
	base       *url.URL
	httpClient *http.Client
}

var _ Fetcher = (*Client)(nil)

// NewClient builds a Client from the Session's resolved REST config
// (kubeconfig, flags, and auth are already folded in by the caller).
func NewClient(cfg *rest.Config) (*Client, error) {
	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("building the cluster HTTP client: %w", err)
	}

	base, err := serverBaseURL(cfg)
	if err != nil {
		return nil, err
	}

	return &Client{base: base, httpClient: httpClient}, nil
}

// serverBaseURL resolves the API server base URL from the Session's REST
// config, mirroring client-go's own defaulting (scheme inference from TLS
// material, "localhost" fallback).
func serverBaseURL(cfg *rest.Config) (*url.URL, error) {
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	defaultTLS := len(cfg.CAFile) != 0 || len(cfg.CAData) != 0 ||
		len(cfg.CertFile) != 0 || len(cfg.CertData) != 0 || cfg.Insecure

	base, _, err := rest.DefaultServerURL(host, cfg.APIPath, schema.GroupVersion{}, defaultTLS)
	if err != nil {
		return nil, fmt.Errorf("resolving the cluster base URL: %w", err)
	}

	return base, nil
}

// v3Index mirrors the JSON shape of the live /openapi/v3 index: a map from
// group-version path to the server-relative URL of that group's OpenAPI v3
// Document, which carries the content hash as a query parameter.
type v3Index struct {
	Paths map[string]v3IndexEntry `json:"paths"`
}

type v3IndexEntry struct {
	ServerRelativeURL string `json:"serverRelativeURL"`
}

// FetchIndex fetches the live /openapi/v3 index and exposes its per-group
// entries with their server-provided content hashes, sorted by path. The
// index is fetched live every Session — it is what keeps the (future) cache
// honest — so this method is also where an unreachable cluster or a cluster
// without OpenAPI v3 support surfaces as a hard failure.
func (c *Client) FetchIndex(ctx context.Context) ([]GroupVersion, error) {
	body, status, err := c.get(ctx, "", "")
	if err != nil {
		return nil, err
	}

	switch {
	case status == http.StatusNotFound:
		return nil, ErrOpenAPIV3NotServed
	case status != http.StatusOK:
		return nil, fmt.Errorf("fetching the OpenAPI v3 index: server returned HTTP %d", status)
	}

	var index v3Index
	if err := json.Unmarshal(body, &index); err != nil {
		return nil, fmt.Errorf("decoding the OpenAPI v3 index: %w", err)
	}

	groups := make([]GroupVersion, 0, len(index.Paths))
	for gvPath, entry := range index.Paths {
		groups = append(groups, GroupVersion{
			Path:        gvPath,
			ContentHash: parseContentHash(entry.ServerRelativeURL),
		})
	}
	slices.SortFunc(groups, func(a, b GroupVersion) int {
		return strings.Compare(a.Path, b.Path)
	})

	return groups, nil
}

// FetchGroupDocument fetches one group's OpenAPI v3 Document at the given
// server content hash and returns the raw response bytes, unparsed.
//
// The signature is cache-shaped on purpose: M2's hash-validated disk cache
// (ADR-0002) slots in behind it, keyed by exactly these arguments and storing
// exactly these bytes (see Fetcher).
func (c *Client) FetchGroupDocument(ctx context.Context, groupPath, contentHash string) ([]byte, error) {
	body, status, err := c.get(ctx, groupPath, contentHash)
	if err != nil {
		return nil, err
	}

	switch {
	case status == http.StatusNotFound:
		return nil, fmt.Errorf(
			"this cluster does not serve an OpenAPI v3 Document for %q (HTTP 404); "+
				"the live index may be stale relative to the document", groupPath,
		)
	case status != http.StatusOK:
		return nil, fmt.Errorf("fetching the OpenAPI v3 Document for %q: server returned HTTP %d",
			groupPath, status)
	}

	return body, nil
}

// get performs one GET under /openapi/v3, returning the raw body and status.
// Transport-level failures are wrapped as a clear, kubectl-like
// unreachable-cluster error.
func (c *Client) get(ctx context.Context, groupPath, contentHash string) ([]byte, int, error) {
	u := *c.base
	// Deviation caveat: we reconstruct the document URL by convention from
	// (groupPath, contentHash) — /openapi/v3/<groupPath>?hash=<contentHash> —
	// rather than using the index entry's serverRelativeURL verbatim the way
	// client-go does. Aggregated apiservers can return serverRelativeURLs not
	// rooted at /openapi/v3 (kubernetes/kubernetes#117463); such groups would
	// fetch from the wrong path here. M2's cache work must inherit this
	// caveat knowingly: the computable-URL assumption is also what makes the
	// cache filename computable.
	u.Path = path.Join(u.Path, "openapi", "v3", groupPath)
	if contentHash != "" {
		u.RawQuery = url.Values{"hash": []string{contentHash}}.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("building the request for %s: %w", u.String(), err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to connect to the server %s: %w", c.base.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("reading the response from %s: %w", u.String(), err)
	}

	return body, resp.StatusCode, nil
}

// parseContentHash extracts the server-provided content hash from an index
// entry's URL (its "hash" query parameter); empty when absent or unparsable.
func parseContentHash(serverRelativeURL string) string {
	u, err := url.Parse(serverRelativeURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("hash")
}
