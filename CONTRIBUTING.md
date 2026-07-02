# Contributing

<!--TOC-->

______________________________________________________________________

- [Toolchain](#toolchain)
- [Test loops](#test-loops)
- [Fixture corpus](#fixture-corpus)
- [Recorded Status corpus](#recorded-status-corpus)
- [Golden frames](#golden-frames)

______________________________________________________________________

<!--TOC-->

## Toolchain

Every tool is pinned in [`.mise/config.toml`](.mise/config.toml) and managed by
[mise](https://mise.jdx.dev): `mise install` once, then work inside an activated
mise environment (or prefix commands with `mise exec --`).

## Test loops

The whole repo speaks ginkgo/gomega, BDD-style (see
[docs/DESIGN.md — Testing](docs/DESIGN.md#testing)).

- **Fast loop** — hermetic, no Docker:

  ```sh
  ginkgo --label-filter='!integration' ./...
  ```

- **Integration loop** — **requires a running Docker daemon**. The single
  cluster-facing suite in `test/integration` boots one shared k3s cluster via
  [testcontainers-go](https://golang.testcontainers.org/modules/k3s/) per run
  (startup is ~15–30s, amortized across all specs) and runs cleanly in parallel:

  ```sh
  ginkgo -p ./...
  ```

  Podman note (known testcontainers-on-mac quirk): podman's Docker-compatible
  socket has no `bridge` network, which the ryuk reaper container asks for. Run
  the loop with `TESTCONTAINERS_RYUK_DISABLED=true` when Docker is backed by
  podman. With ryuk disabled, aborted or failed runs can leave k3s containers
  behind — remove them manually (`docker ps` / `docker rm -f`).

CI runs both loops; the integration job runs on Docker-equipped runners.

## Fixture corpus

The hermetic schema specs run against a checked-in corpus of OpenAPI v3 group
documents in `internal/schema/testdata/`, named by sanitized group-version
(`apis/apps/v1` → `apis_apps_v1.json`). The fixtures are the **raw response
bytes** exactly as the cluster served them — never pretty-printed or
re-marshalled — because the server content hash describes raw content
([docs/DESIGN.md — Data layer](docs/DESIGN.md#data-layer)); the pre-commit fixer
hooks are configured to leave them alone.

The sample CRD manifests in `internal/schema/testdata/crds/` feed the corpus its
known-nasty shapes: int-or-string, `x-kubernetes-preserve-unknown-fields`, CEL
rules (`x-kubernetes-validations`), the constraint keywords node metadata
surfaces (pattern, numeric bounds, length and item bounds, multipleOf, a
declared default), and a Kind served at two versions.

Regenerate the whole corpus with one command (**requires a running Docker
daemon**; the podman note above applies):

```sh
mise run fixtures:capture
```

It boots a fresh k3s container (the same image the integration suite pins),
installs the sample CRDs, waits for every corpus group-version to appear in the
live `/openapi/v3` index, and rewrites the fixture files. Commit the result.

One corpus member is generated, not captured: the **giant fixture**
(`apis_giant.example.com_v1.json` / `_v2.json`), the huge-CRD perf pass's
10k+-node group document. Real giant CRDs top out around ~2k schema nodes per
Kind (prometheuses ≈ 2,000 at prometheus-operator v0.92.1), so the giant is
built by the deterministic generator in `test/giantcrd` instead — deep nesting,
wide sibling fans, big enums — and a fast-loop spec pins the checked-in bytes
against the generator's output. Regenerate with (no Docker needed):

```sh
mise run fixtures:generate-giant
```

The perf specs themselves carry `Label("perf")`: they run in the fast loop
(budgets are generous regression tripwires, not tight timings), and
`ginkgo --label-filter='!perf'` excludes them if a noisy machine needs it. The
precise numbers live in the benchmarks:

```sh
go test -bench=Giant -run='^$' ./internal/schema
go test -bench=Keystroke -run='^$' ./internal/tui
```

## Recorded Status corpus

The hermetic Validate specs run against a checked-in corpus of dry-run failure
payloads in `internal/validate/testdata/`: the raw `metav1.Status` bodies a real
API server answered with when deliberately-invalid Manifests were POSTed with
`?dryRun=All`. Like the OpenAPI corpus, the fixtures are the **raw response
bytes** exactly as the cluster served them — never pretty-printed or
re-marshalled — and the pre-commit fixer hooks leave them alone.

The scenarios are declared in `hack/capture-status-fixtures`: a required-field
violation (apps/v1 Deployment), a CEL rule violation (the sample Gadget CRD's
`x-kubernetes-validations`), an enum violation on an indexed path plus a map-key
path (v1 Pod), and a webhook denial. The denial is recorded from a real
always-deny `ValidatingWebhookConfiguration`: the capture tool serves an
in-process HTTPS admission server on the host and the cluster reaches it through
testcontainers' host port access (`host.testcontainers.internal`).

Regenerate the whole corpus with one command (**requires a running Docker
daemon**; the podman note above applies):

```sh
mise run fixtures:capture-status
```

It boots a fresh k3s container (the same image the integration suite pins),
installs the sample CRDs, registers the always-deny webhook, POSTs each scenario
Manifest with `?dryRun=All`, and rewrites the fixture files. Commit the result.

## Golden frames

The teatest specs in `internal/tui/frames_test.go` pin the Session shell's
signature surfaces — the Kind picker, the compose view, and the exit menu — as
rendered frames in `internal/tui/testdata/golden/`. Each spec drives a real
`tea.Program` on teatest's in-memory terminal (no PTY, so they run in the fast
loop) at a fixed **100×30** and with the color profile forced to Ascii, so CI
and a local terminal pin identical bytes. The frames are normalized before
comparing or writing: trailing whitespace is stripped from every line (the panes
pad lines to their widths) and each frame ends with exactly one newline, which
keeps the checked-in goldens out of the pre-commit fixer hooks' way.

Regenerate them with (the golden Manifests in `internal/schema/testdata/golden/`
answer to the same switch — point ginkgo at `./internal/schema` for those):

```sh
KUBECTL_CRAFT_UPDATE_GOLDEN=1 go run github.com/onsi/ginkgo/v2/ginkgo ./internal/tui
```

Then **eyeball the diff before committing**: a golden diff is the review
surface, not an inconvenience. Read it as a screenshot — check that the
breadcrumb, tree rows, markers (`✱` required, dimmed defaults like
`profile: balanced`), status line, and hint bar moved the way the change
intended, and that nothing else drifted (layout shifts, truncation at column
100, style bleed, glyph collisions). A diff you cannot explain is a regression,
not a regen.
