# OpenAPI v3 as the schema source, with a deferred v2 fallback

Type Schemas are sourced live from the cluster's `/openapi/v3` endpoint (per-group documents, discovered via the API server) — the same source modern `kubectl explain` (1.28+) uses, giving 1:1 fidelity including CRDs and aggregated APIs. For clusters that don't serve v3 (pre-1.24 or odd aggregated servers), we will fall back to parsing the aggregated `/openapi/v2` Swagger document, accepting a flatter, degraded schema view. The fallback is committed direction but deliberately **deferred past the MVP**: the MVP is v3-only and fails with a clear minimum-version message, so the navigation UX is proven on a single parser/tree-builder before the second data path doubles that surface.

## Considered Options

- **OpenAPI v3 only** — simplest, one code path; rejected as the end state because we want broad cluster support, but adopted as the MVP posture.
- **OpenAPI v3 with v2 fallback** — chosen end state; broadest compatibility at the cost of two parsers feeding one navigation model.
- **Assembling schemas from CRD objects + discovery** — rejected; reinvents the aggregation the API server already does, and built-in kinds have no CRD object to read.
