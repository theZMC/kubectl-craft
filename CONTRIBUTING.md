# Contributing

<!--TOC-->

______________________________________________________________________

- [Toolchain](#toolchain)
- [Test loops](#test-loops)

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
