package data

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Cache is the hash-validated disk cache for OpenAPI v3 Documents (ADR-0002):
// a Fetcher wrapping the live Client so a warm Session starts near-instant.
// The index is always fetched live through the inner Fetcher — the live index
// is what keeps the cache honest, so it is never cached — while group
// documents are served from disk when the (group-version path, content hash)
// pair already has a file, and fetched through (then written) otherwise.
//
// Layout (DESIGN.md — Data layer):
//
//	<root>/v1/<sanitized server host_port>/<sanitized group-version>@<content hash>.json
//
// The v1 segment is layout-change insurance and sanitization follows
// kubectl's cluster-key precedent. A hit is a pure existence check — the
// filename is computable from the live index alone. This computable-filename
// assumption knowingly inherits the Client's URL-reconstruction caveat (see
// Client.get): aggregated apiservers can serve group documents from URLs not
// rooted at /openapi/v3 (kubernetes/kubernetes#117463), and such groups would
// cache under a filename reconstructed by the same convention.
//
// Cached files are the raw response bytes — the server hash describes raw
// content, so validation stays honest and there is no cache-format
// versioning. Unreadable or corrupt files are misses, concurrent Sessions
// race harmlessly, and deleting the cache directory at any time is always
// safe: the cache is a speedup, never a fallback.
type Cache struct {
	inner Fetcher

	// dir is the per-cluster cache directory:
	// <root>/v1/<sanitized server host_port>.
	dir string
}

var _ Fetcher = (*Cache)(nil)

// cacheLayoutVersion is the layout-change insurance segment: bump it when
// the on-disk layout changes shape, and old Sessions' files simply go cold.
const cacheLayoutVersion = "v1"

// NewCache wraps the inner Fetcher (in production, the live Client) with the
// disk cache rooted at root — normally DefaultCacheRoot(), a temp dir in
// specs.
//
// serverHost tells the cache which cluster's documents it is holding; pass
// the Session's rest.Config.Host, mirroring kubectl's cluster-key precedent
// (kubectl keys its per-cluster cache directories on the config host the
// same way). The scheme is stripped and the rest sanitized into the
// host_port directory segment.
func NewCache(inner Fetcher, root, serverHost string) *Cache {
	return &Cache{
		inner: inner,
		dir:   filepath.Join(root, cacheLayoutVersion, hostSegment(serverHost)),
	}
}

// DefaultCacheRoot resolves the production cache root,
// os.UserCacheDir()/kubectl-craft (XDG_CACHE_HOME semantics; macOS/Windows
// equivalents handled by Go).
func DefaultCacheRoot() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving the user cache directory: %w", err)
	}
	return filepath.Join(base, "kubectl-craft"), nil
}

// FetchIndex always delegates to the live Fetcher. The index is never
// cached: fetching it live every Session is what keeps every cached group
// document honest (ADR-0002).
func (c *Cache) FetchIndex(ctx context.Context) ([]GroupVersion, error) {
	return c.inner.FetchIndex(ctx)
}

// FetchGroupDocument serves the group's OpenAPI v3 Document from disk when a
// file already exists for this (group-version path, content hash) pair, and
// otherwise fetches through the live Fetcher, returns the raw response
// bytes, and writes them for the next Session.
//
// An empty content hash bypasses the cache entirely — without a server hash
// there is no key to validate against, so the fetch goes through live and
// nothing is written.
func (c *Cache) FetchGroupDocument(ctx context.Context, groupPath, contentHash string) ([]byte, error) {
	if contentHash == "" {
		return c.inner.FetchGroupDocument(ctx, groupPath, contentHash)
	}

	path := c.documentPath(groupPath, contentHash)
	if body, ok := readCachedDocument(path); ok {
		return body, nil
	}

	body, err := c.inner.FetchGroupDocument(ctx, groupPath, contentHash)
	if err != nil {
		return nil, err
	}

	// Best-effort write: the cache is a speedup, never a gate, so a full
	// disk or an unwritable directory must not fail a fetch that already
	// succeeded live.
	if err := writeDocument(path, body); err == nil {
		c.evictSuperseded(groupPath, path)
	}

	return body, nil
}

// documentPath computes the cached file path for a (group-version path,
// content hash) pair — the pure-existence-check hit lives or dies on this
// being computable from the live index alone.
func (c *Cache) documentPath(groupPath, contentHash string) string {
	return filepath.Join(c.dir, sanitizeSegment(groupPath)+"@"+sanitizeSegment(contentHash)+".json")
}

// readCachedDocument reads a cached document, treating any unreadable or
// corrupt (non-JSON) file as a miss so the fetch falls through to live.
func readCachedDocument(path string) ([]byte, bool) {
	body, err := os.ReadFile(path)
	if err != nil || !json.Valid(body) {
		return nil, false
	}
	return body, true
}

// writeDocument writes the raw response bytes atomically: a temp file in the
// same directory, then a rename onto the final name. Concurrent Sessions
// writing the same document race harmlessly — renames are atomic and both
// sides carry identical bytes for the same hash. The temp prefix keeps
// in-flight files out of the superseded-sibling glob.
func writeDocument(path string, body []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating the cache directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".craft-tmp-*")
	if err != nil {
		return fmt.Errorf("creating a temp file in %s: %w", dir, err)
	}

	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("writing the cached document %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("closing the cached document %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("renaming the cached document into %s: %w", path, err)
	}

	return nil
}

// evictSuperseded deletes the group's other <group-version>@* siblings after
// a successful write: self-cleaning replace-on-refetch eviction with no TTL
// machinery, leaving at most one stale file per group per crash. Removal
// errors are ignored — a concurrent Session may have already replaced or
// removed a sibling, and that race is harmless.
func (c *Cache) evictSuperseded(groupPath, currentPath string) {
	// The sanitized segment contains only word characters, dots, and
	// underscores, so it is glob-safe by construction.
	siblings, err := filepath.Glob(filepath.Join(c.dir, sanitizeSegment(groupPath)+"@*.json"))
	if err != nil {
		return
	}
	for _, sibling := range siblings {
		if sibling != currentPath {
			_ = os.Remove(sibling)
		}
	}
}

// unsafeSegmentCharacters matches everything outside the overly cautious
// safe set (word characters and dots), following kubectl's cluster-key
// precedent: collapse anything that might upset a filesystem into "_" —
// collisions are possible but unlikely, and short-lived if they happen.
var unsafeSegmentCharacters = regexp.MustCompile(`[^\w.]`)

// sanitizeSegment collapses one path segment (host_port, group-version
// path, or content hash) into a single safe file name component. Dots are
// kept for readability (hosts and CRD groups are dotted), but a segment
// that sanitizes to a bare "." or ".." is neutralized so it can never
// escape the cache directory.
func sanitizeSegment(segment string) string {
	safe := unsafeSegmentCharacters.ReplaceAllString(segment, "_")
	if safe == "." || safe == ".." {
		return strings.ReplaceAll(safe, ".", "_")
	}
	return safe
}

// hostSegment derives the sanitized host_port directory segment from the
// Session's rest.Config.Host: strip the scheme, sanitize the rest, and fall
// back to "localhost" for an empty host the same way the Client's base-URL
// resolution does.
func hostSegment(serverHost string) string {
	host := strings.TrimPrefix(serverHost, "https://")
	host = strings.TrimPrefix(host, "http://")
	if host == "" {
		host = "localhost"
	}
	return sanitizeSegment(host)
}
