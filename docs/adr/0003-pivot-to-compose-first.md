# Pivot: compose-first, browsing as substrate

The project began as a live schema *browser* ("live kubectl explain"). A sweep of the Krew index (2026-07) showed that space already served — `explore` (fuzzy explain), `apidocs` (tree view), `doc` (rendered docs), `crd-wizard` (CRD/CR explorer) — while **no plugin offers an interactive TUI for composing a manifest from the cluster's live schema** (`schemagen` generates YAML but via one-shot CLI flags). We pivoted the core concept to composing: navigate the field tree, fill values with per-field documentation alongside, produce a valid Manifest. Browsing survives intact as the substrate — the compose tree *is* the browse tree — and every prior decision (OpenAPI v3 source, hash-validated cache, fuzzy kind picker, field tree + detail pane, deep-link args) carries over unchanged.

## Considered Options

- **Compose-first** — chosen; occupies an open niche, subsumes browsing, and uniquely exploits the live cluster connection (e.g. server-side dry-run validation is impossible for static tools).
- **Browse-first, compose later** — rejected; enters the crowded space first and defers the differentiator.
- **Pure browser** — rejected; best-in-class polish alone was judged insufficient differentiation against four incumbent plugins.
