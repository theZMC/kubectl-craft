# kubectl-craft — Build Milestones

<!--TOC-->

______________________________________________________________________

- [M0 — Walking skeleton](#m0--walking-skeleton)
- [M1 — Schema core (pure)](#m1--schema-core-pure)
- [M2 — Browse substrate + cache](#m2--browse-substrate--cache)
- [M3 — Compose & Emit](#m3--compose--emit)
- [M4 — Validate](#m4--validate)
- [M5 — Polish & v0.1](#m5--polish--v01)
- [v0.1 release gate](#v01-release-gate)

______________________________________________________________________

<!--TOC-->

Slicing of [DESIGN.md](./DESIGN.md) into implementation milestones. Philosophy:
**skeleton, then layers** — M0 is a thin vertical walking skeleton proving every
piece of plumbing and toolchain; each later milestone is a horizontal layer that
ends green and demoable. Domain language per [CONTEXT.md](../CONTEXT.md).

## M0 — Walking skeleton

Prove the plumbing end-to-end; the toolchain exists on day one.

- Repo bootstrap: `git init`, module layout (`cmd/kubectl-craft`, `internal/…`),
  lint config.
- CI: fast loop (`ginkgo --label-filter='!integration' ./...`) and a
  Docker-equipped integration job; goreleaser config with a snapshot build.
- Plugin binary: `genericclioptions.ConfigFlags`, connects to the current
  context, **capability check** (no `/openapi/v3` → the clear minimum-version
  error; unreachable cluster → hard-fail), fetches the live index.
- Minimal Bubble Tea shell: renders "connected: N API groups", `q` quits —
  proving the `/dev/tty` render vs clean-stdout split.
- testcontainers k3s suite: `SynchronizedBeforeSuite` boot + a smoke spec (index
  served, per-group hashes present).
- Fixture-capture script: pulls group documents from the k3s container into
  `testdata/`.
- The data-layer fetch interface is **cache-shaped** (fetch-by-group, content
  hash in the signature) so M2's disk layer is additive.

**Exit:** `kubectl craft` against a real cluster shows the group count; both CI
loops green; snapshot binary builds.

## M1 — Schema core (pure)

The hardest correctness work, hermetic against the fixture corpus. No TUI
change.

- Group document → lazy, **cycle-safe** field tree (`$ref` cycles:
  JSONSchemaProps).
- Node metadata: type, docs, requiredness, enums, constraints, defaults;
  int-or-string; `x-kubernetes-preserve-unknown-fields` flagged (feeds the
  raw-YAML escape hatch).
- Contextual requiredness computation (completeness semantics per DESIGN.md).
- `DescribeTable` sweeps over the corpus; spec descriptions in glossary
  language.

**Exit:** every corpus fixture builds a correct tree.

## M2 — Browse substrate + cache

The first demoable artifact: a complete, warm-starting, read-only schema
browser.

- Kind picker: discovery, create-verb filter, fzf type-to-filter.
- Compose view (read-only): tree nav per the keybinding grammar, detail pane,
  Field Path breadcrumb, expand/collapse, hint bar, `?` help.
- `/` schema-path search with the landing rule.
- Deep-link arg: kind (`kubectl craft deploy`) **and** field path
  (`deploy.spec.strategy`) — the k9s integration hook.
- Disk cache per DESIGN.md layout, as a **self-contained sub-scope** behind M0's
  fetch interface (atomic writes, replace-on-refetch eviction).

**Exit:** browse any Kind on a live cluster; warm start is near-instant; k3s
specs cover picker filtering and cache staleness (`Serial`).

## M3 — Compose & Emit

Browsing becomes composing; the tool produces Manifests.

- Draft model: set/unset (`a`/`d` + subtree confirms), edit mode
  (`Enter`/`Esc`), widgets (text, numeric, toggle, enum select), arrays/maps.
- Raw-YAML escape hatch + `$EDITOR` pop-out for schema-blind fields.
- Completeness status line; dimmed default placeholders.
- Sparse emission with golden YAML tests.
- Exit ramp: `q` three-way menu, `Ctrl-d` direct emit, `Ctrl-c` escape hatch;
  Manifest to stdout.
- Version switch with carry-over-by-path + drop report.

**Exit:** compose a Deployment end-to-end and
`kubectl craft > x.yaml && kubectl apply -f x.yaml` by hand.

## M4 — Validate

The differentiator.

- `dry-run=server` POST; required-to-Validate metadata gate (inline prompt);
  namespace resolution like kubectl.
- Error-cause → tree-node mapping (bracket-path normalization), results pane for
  unmappables, jump-to-first-error.
- RBAC 403 / network failures render as "Validate unavailable", never as
  manifest errors.
- k3s specs: CEL-validated CRDs, required-field violations, webhook-denial
  mapping from a recorded Status fixture.

**Exit:** compose → Validate → fix-by-jumping → clean pass, live against k3s.

## M5 — Polish & v0.1

- teatest golden frames (picker, compose, exit menu); huge-CRD perf pass;
  error-message audit; README + demo.
- goreleaser v0.1.0 via GitHub Releases; Krew submission (`craft`) as
  fast-follow once stable (per DESIGN.md distribution).

## v0.1 release gate

Gate (chosen by recommendation — revisit if wrong): **version-switch
carry-over** (M3) and **field-path deep-link** (M2) ship in v0.1. The
**expanded-output toggle** and **teatest goldens** may trail into v0.1.x if
schedule presses.

Explicitly post-v0.1 (already-decided later phases): OpenAPI v2 fallback,
round-trip editing + draft recovery, multi-manifest workspaces, DRAFT search
scope, in-TUI context switching, rebindable keys.
