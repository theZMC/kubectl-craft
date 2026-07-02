# kubectl-craft — Product Design

<!--TOC-->

______________________________________________________________________

- [Concept](#concept)
- [Flow](#flow)
- [Keybindings](#keybindings)
- [Compose lifecycle](#compose-lifecycle)
- [Data layer](#data-layer)
- [Stack](#stack)
- [Testing](#testing)
- [Deliberate non-goals (MVP)](#deliberate-non-goals-mvp)
- [Distribution](#distribution)

______________________________________________________________________

<!--TOC-->

The decided shape of the tool. Domain language lives in
[CONTEXT.md](../CONTEXT.md); irreversible decisions live in [adr/](./adr/); the
build slicing lives in [MILESTONES.md](./MILESTONES.md).

## Concept

A kubectl plugin (`kubectl craft`) presenting a TUI for composing Kubernetes
Manifests from the cluster's live Type Schemas. Browsing the schemas is the
substrate; composing is the product. The tool never mutates the cluster
(ADR-0004).

## Flow

1. **Launch** — `kubectl craft [kind[.field.path]]`. Standard kubectl plugin
   flags via `genericclioptions.ConfigFlags` (`--context`, `--kubeconfig`, …).
   Context is fixed for the Session. A positional arg in kubectl-explain syntax
   (`deploy.spec.strategy`, short names resolved via discovery) deep-links
   straight to that Kind/Field Path — this is the k9s-plugin integration hook.
1. **Kind picker** — fuzzy-filterable flat list of every **create-capable** Kind
   on the cluster (filtered by the create verb in discovery — API capability,
   not the user's RBAC; group/version as dimmed row metadata). Read-only and
   request-shaped virtual kinds (ComponentStatus, TokenReview, …) are excluded:
   a Manifest is meaningless for them. No group-tree taxonomy.
1. **Compose view** — full schema field tree (left) + detail pane (right:
   description, type, constraints, enum values, per-field validation), Field
   Path breadcrumb persistent. All served versions browsable; Preferred Version
   is the default. Required-but-unset fields are flagged; a status line tracks
   completeness. Requiredness is **contextual** (JSON Schema semantics): the
   root-level required chain counts as missing immediately, but required fields
   nested inside objects the draft hasn't instantiated don't
   (`containers[0].name` is missing only once a container item exists).
1. **Value entry** — inline at the tree node, with type-appropriate widgets
   (HTML-form feel): booleans → toggle/checkbox, enums → drop-down select,
   numbers → validated numeric input, strings → text input. Arrays/maps:
   add/remove items and keys on the node. Fields the schema can't describe
   (`x-kubernetes-preserve-unknown-fields`, untyped `object`) get an inline
   multiline raw-YAML text area — parsed on confirm, grafted at that Field Path
   — with a keybinding to pop the subtree out to `$EDITOR` for heavy editing;
   server-side Validate is the safety net for what the schema can't check.
1. **Field search** — `/` fuzzy-searches the open Kind's **schema-level Field
   Paths** (index-free); selecting a match expands and jumps the tree. Landing
   rule when the match sits under an array/map: jump into the first instantiated
   item; if none exist, land on the collection node itself (where `a` adds one).
   Searching the Draft is a **later-phase second scope inside the same
   overlay**: Tab flips SCHEMA ⇄ DRAFT (hint bar advertises it); DRAFT scope
   fuzzy-matches the filled leaves as `path: value` pairs across both halves
   ("where did I type nginx?"), raw-YAML subtrees matched as text. MVP ships the
   overlay with the single fixed scope; the toggle slots in without UI rework.
   **Field Path syntax**: dots address schema-defined fields; brackets address
   data — `containers[0].image` for array items,
   `labels["app.kubernetes.io/name"]` for map keys (quotes displayed only when
   the key isn't a bare word). This matches the API server's error-cause style,
   so Validate's tree-mapping normalizes trivially.
1. **Output** — the TUI renders to the terminal (`/dev/tty`); **stdout carries
   nothing but the final Manifest**. "Emit & quit" is the single exit ramp: it
   prints the YAML to stdout, so
   `kubectl craft … > x.yaml && kubectl apply -f x.yaml` works verbatim (no
   in-session file-save action; redirect if you want a file). **Validate** via
   server-side dry-run (`dry-run=server` POST — full schema/CRD-rule/webhook
   validation, nothing persisted). `metadata` is ordinary tree fields, but the
   tool knows `metadata.name` (+ namespace for namespaced Kinds) are
   **required-to-Validate**: flagged in the status line from the start, and
   Validate with them unset triggers a quick inline prompt rather than a raw
   server error. Namespace resolves like kubectl: `metadata.namespace` if set,
   else `--namespace`/kubeconfig context default. Validate is **manual
   (keybinding-triggered)**; inline checks as you type stay purely client-side
   and schema-local (types, enums, required, patterns). RBAC 403s and network
   failures render as "Validate unavailable: …" in the results pane — clearly
   distinct from manifest errors. **Validate feedback maps to the tree**:
   `Status.details.causes` entries with field paths annotate the offending tree
   nodes (error marker; message in the detail pane), with jump-to-first-error.
   Unmappable errors (freeform webhook denials, top-level failures) land in a
   validation results pane. Emitted YAML is **sparse by default**: exactly the
   fields the user set (plus apiVersion/kind/metadata identity). Schema defaults
   appear in the detail pane and as dimmed placeholders in the tree, not in the
   output — with an **opt-in expansion toggle** that also emits defaulted fields
   for users who want self-documenting manifests.

## Keybindings

- **Modal, vim-hybrid** in the compose view: navigate mode is the default —
  `j/k/h/l` *and* arrow keys move, single mnemonic letters are commands (`v`
  Validate, `/` field search, `?` help, `q` quit). `Enter` on a field opens its
  widget in **edit mode**; `Enter` confirms, `Esc` cancels back to navigate.
  "HTML-form feel" in Value entry refers to widget types, not navigation —
  printable keys never edit implicitly.
- **Mutation verbs**: `a` on an array/map node appends an item / prompts for a
  key; `d` **unsets** (removes from the Draft — sparse semantics, back to dimmed
  placeholder — never "set to empty"). `d` on a scalar acts instantly; on a node
  with filled descendants it confirms with the count of values discarded. **No
  undo in MVP** — confirmation on destructive keys is the safety net.
- **Exit ramp**: `q` is the single exit verb — on a non-empty Draft it opens a
  three-way prompt (Emit & quit / Discard & quit / Cancel); on an empty Draft it
  just quits. `Ctrl-d` (EOF idiom) is a direct emit-&-quit shortcut. `Ctrl-c` is
  the conventional escape hatch: immediate discard-quit, no prompt.
- **Discoverability**: `?` opens a full-map help overlay; a persistent one-line
  hint bar shows the handful of keys contextual to the focused view (k9s-style).
- **Hardcoded in MVP** — no rebinding config; a config file is a later additive
  feature if demand appears. The vim+arrow hybrid covers both major camps, and
  fixed keys keep the hint bar/help/docs trivially truthful.
- **Command map** (compose view, navigate mode): `Enter` opens the widget on a
  leaf / toggles expand on a parent; `h/l` `←/→` collapse/expand; `g/G`
  top/bottom; `v` Validate; `V` version switch; `e` pop node/subtree to
  `$EDITOR`; `/` field search; `?` help. Remaining bindings (e.g. the
  expanded-output toggle) are assigned at implementation within this grammar.
- **Search surfaces are type-to-filter, fzf-style**: in the Kind picker and the
  `/` field-search overlay, printable keys filter immediately, `↑/↓`/`Ctrl-j/k`
  move the selection, `Enter` selects, `Esc` clears-then-dismisses. Rule of
  thumb: modal command-letters where a view has many verbs; type-to-filter
  inside any dedicated search surface.

## Compose lifecycle

- A Session composes **one Manifest at a time**; returning to the Kind picker
  mid-compose warns that the draft will be discarded. Multi-manifest workspaces
  (multiple drafts, multi-doc YAML emit) are a later phase alongside round-trip
  editing.
- Switching the Kind's version mid-compose **carries values over by Field
  Path**: values whose path exists with a compatible type in the target
  version's Type Schema are kept; the rest are dropped with an explicit report,
  behind a confirmation showing what would drop.
- Drafts are **ephemeral** — nothing persists across processes. The only ways
  out are "emit & quit" (Manifest to stdout) and plain quit; plain quit with a
  non-empty draft asks for confirmation. Draft recovery arrives with the
  round-trip-editing phase (it needs the same YAML→tree hydration).

## Data layer

- Schemas from `/openapi/v3` per-group documents (ADR-0001); kind list from
  discovery.
- Hash-validated disk cache keyed by cluster + server content hash; index
  fetched live every Session; group docs fetched lazily on first open
  (ADR-0002).
- Cache lives at `os.UserCacheDir()/kubectl-craft` (`XDG_CACHE_HOME` semantics;
  macOS/Windows equivalents handled by Go). Deleting the directory at any time
  is always safe — worst case is a slower next Session.
- **Cache layout**:
  `kubectl-craft/v1/<sanitized server host_port>/<sanitized group-version>@<content hash>.json`
  (kubectl's cluster-key precedent; the `v1/` segment is layout-change
  insurance). A hit is a pure existence check — the filename is computable from
  the live index. On refetch, write atomically (temp + rename), then delete
  superseded `<group-version>@*` siblings: self-cleaning, no TTL machinery, at
  most one stale file per group per crash.
- Cached files are the **raw response bytes** — the server hash describes raw
  content, so validation stays honest and there's no cache-format versioning.
  Unreadable/corrupt files are treated as cache misses; concurrent Sessions race
  harmlessly.
- **Discovery is never cached** — aggregated discovery (1.26+) makes the Kind
  list one live round-trip, and caching it would reintroduce the
  stale-cluster-view problem rejected in the offline decision.
- Tree building must be lazy/cycle-safe ($ref cycles exist, e.g.
  JSONSchemaProps).
- **Cluster unreachable at launch → hard-fail** with a clear error, mirroring
  kubectl. No offline mode: the cache is a speedup, never a fallback (the live
  index fetch is what keeps it honest, and both differentiators — live schemas,
  server-side Validate — are dead offline anyway).

## Stack

- Go, Bubble Tea (+ bubbles/lipgloss) for the TUI.
- `k8s.io/cli-runtime` for kubeconfig/flags, `k8s.io/client-go` for
  discovery/requests, `k8s.io/kube-openapi` types for v3 documents.

## Testing

- **Cluster-facing integration tests run against testcontainers-go k3s** (one
  shared container per test binary — startup ~15–30s amortized): real discovery,
  `/openapi/v3`, cache validation, and Validate dry-run, with CRDs installed to
  exercise CEL rules and version carry-over; real webhook registration is
  feasible when the unmappable-error path needs end-to-end coverage. Docker is
  therefore a hard test dependency (CI + contributors). k3s ships bundled extras
  (traefik, helm-controller) — assertions on the Kind list are membership-based,
  and noisy components are disabled via k3s args.
- **The compose core is pure**: Draft model, tree building, carry-over,
  completeness, and emission live in packages with no Bubble Tea imports,
  unit-tested against the fixture corpus (emission via golden YAML files).
- **TUI tests are state-first**: drive `Update()` with synthetic key sequences
  and assert on model state — this is where the modal keybinding grammar
  (navigate/edit, exit menu, confirms) is pinned. A handful of teatest golden
  frames (one per major view: picker, compose, exit menu) serve as layout smoke
  tests, updated deliberately.
- **A checked-in fixture corpus** of group documents captured from the k3s
  container (JSONSchemaProps `$ref` cycle, int-or-string,
  preserve-unknown-fields, CEL-validated CRDs) backs hermetic unit tests for the
  tree builder and emission — no Docker needed for the fast loop.
- **Test pattern: ginkgo/gomega everywhere, BDD-style.** One dialect for the
  whole repo — `DescribeTable`/`Entry` for fixture-corpus sweeps, golden-frame
  comparisons inside `It`s, gomega for every assertion. Spec descriptions speak
  the CONTEXT.md glossary verbatim (Draft, Emit, Field Path, Validate), so
  `ginkgo` output reads as an executable restatement of the design docs.
- **Gating**: cluster-facing specs consolidate into one integration suite
  (`test/integration` — one suite, one shared k3s) carrying
  `Label("integration")`; the fast loop is
  `ginkgo --label-filter='!integration' ./...`, CI runs everything. No build
  tags — integration code always compiles, so it can't rot silently.
- **Parallelism**: `SynchronizedBeforeSuite` boots k3s + installs the CRD corpus
  on proc 1 and broadcasts the kubeconfig; specs run parallel because the suite
  is read-mostly (Validate never persists — ADR-0004's testing dividend).
  Mutating specs (live-index liveness, cache invalidation on CRD change) are
  decorated `Serial`.
- **Domain matchers**: a small internal gomega matcher package (`HaveValueAt`,
  `BeMissingRequired`, `EmitYAML(golden)`, …) keeps specs in domain language;
  async assertions (teatest output, k3s readiness) always go through
  `Eventually`, never sleeps.
- **Version support is capability-gated, not version-sniffed**: the "minimum
  version" error fires when the server doesn't serve `/openapi/v3`, never on a
  version-number check (any ≈1.24+ cluster works, best-effort). *Supported* =
  the upstream-supported minors (rolling window); CI matrix = two k3s image
  tags, oldest-supported and latest.

## Deliberate non-goals (MVP)

- No Instance browsing, ever (k9s territory).
- No apply, ever (ADR-0004).
- No editing of existing YAML files — compose-from-scratch only; faithful
  round-trip editing is a committed later phase.
- No in-TUI context switching — one context per Session; later phase.
- No OpenAPI v2 fallback in MVP — v3-only with a clear minimum-version error;
  fallback is a committed later phase (ADR-0001).
- No global cross-Kind field search in MVP.

## Distribution

GitHub Releases via goreleaser first; Krew submission (`craft`) as a fast-follow
once stable.
