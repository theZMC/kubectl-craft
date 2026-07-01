# Hash-validated disk cache for schema documents

"Live" and "fast" pull against each other: full OpenAPI v3 document sets on CRD-heavy clusters run to many MBs, but the tool's premise is that it always reflects the cluster as it is now. We resolve this by caching per-group v3 documents on disk keyed by cluster and the server-provided content hash (the `/openapi/v3` index embeds a hash per group URL): every Session fetches the small index live, loads unchanged groups from disk, refetches only changed ones, and fetches group documents lazily on first open. This is the same strategy kubectl itself uses, so live-ness is never sacrificed — a just-installed CRD appears immediately because the index is always fresh — while repeat Sessions open near-instantly.

## Considered Options

- **Memory-only, fetch every Session** — zero cache code, but pays full network cost every session; rejected since the hash mechanism makes correct caching cheap.
- **Eager prefetch of all groups** — most predictable once loaded, but the heavy cold-start against large clusters lands on exactly the first-run impression; lazy-per-group keeps startup light.
