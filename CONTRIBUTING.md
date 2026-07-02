# Contributing

<!--TOC-->

______________________________________________________________________

- [Toolchain](#toolchain)
- [Test loops](#test-loops)
- [Fixture corpus](#fixture-corpus)

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
rules (`x-kubernetes-validations`), and a Kind served at two versions.

Regenerate the whole corpus with one command (**requires a running Docker
daemon**; the podman note above applies):

```sh
mise run fixtures:capture
```

It boots a fresh k3s container (the same image the integration suite pins),
installs the sample CRDs, waits for every corpus group-version to appear in the
live `/openapi/v3` index, and rewrites the fixture files. Commit the result.
