# kubectl-craft

<!--TOC-->

______________________________________________________________________

- [Demo](#demo)
- [Install](#install)
- [Quickstart](#quickstart)
- [Keybindings](#keybindings)
  - [Compose view — navigate mode](#compose-view--navigate-mode)
  - [Edit mode — `enter` on a leaf opens its type-appropriate widget](#edit-mode--enter-on-a-leaf-opens-its-type-appropriate-widget)
  - [Session](#session)
  - [Search surfaces (Kind picker and the `/` field-search overlay)](#search-surfaces-kind-picker-and-the--field-search-overlay)
- [Validate](#validate)
- [Contracts](#contracts)
- [Supported platforms](#supported-platforms)
- [Development](#development)

______________________________________________________________________

<!--TOC-->

[![CI](https://github.com/theZMC/kubectl-craft/actions/workflows/ci.yaml/badge.svg)](https://github.com/theZMC/kubectl-craft/actions/workflows/ci.yaml)
[![Go version](https://img.shields.io/github/go-mod/go-version/theZMC/kubectl-craft)](go.mod)

<!-- TODO(license): Apache-2.0 is chosen; the LICENSE file and its badge land
with the release issue (#64). -->

A `kubectl` plugin that presents a TUI for **composing** Kubernetes
**Manifests** from the **Type Schemas** defined on the cluster your current
context points to. Navigate a **Kind**'s field tree with per-field documentation
alongside, fill values with type-appropriate widgets, **Validate** the **Draft**
against the live API server (server-side dry-run — nothing is ever persisted),
and **Emit** the finished Manifest to stdout. Browsing the schemas is the
substrate; composing is the product. The tool never mutates the cluster.

## Demo

The demo is scripted in [`demo.tape`](demo.tape) and reproducible in one command
(`mise run demo:render`, a [vhs](https://github.com/charmbracelet/vhs) recording
against a throwaway k3d cluster) — see
[CONTRIBUTING.md — Demo](CONTRIBUTING.md#demo) for the recipe. The rendered GIF
is published with releases rather than committed (it outweighs the repo's
large-file cap); until the first release the tape is the demo's source of truth.

<!-- TODO(#64): attach the rendered demo.gif to the v0.1.0 release and embed
it here. -->

The signature loop it records: pick a Kind, `/`-search a Field Path, fill
values, `v` Validate (a CEL violation marks the offending tree node), `n` jump
to the finding, fix it, revalidate to a clean pass, `ctrl+d` Emit.

## Install

**GitHub Releases** (recommended): download the archive for your platform from
[the releases page](https://github.com/theZMC/kubectl-craft/releases), extract
it, and put the `kubectl-craft` binary anywhere on your `PATH`. That is all a
kubectl plugin installation needs — kubectl discovers plugins by the
`kubectl-<name>` binary naming convention, so `kubectl craft` works as soon as
`kubectl-craft` is on `PATH`. (`kubectl krew install craft` is coming as a
fast-follow once v0.1 is stable.)

**From source**:

```sh
go install github.com/thezmc/kubectl-craft/cmd/kubectl-craft@latest
```

## Quickstart

```sh
kubectl craft > deployment.yaml && kubectl apply -f deployment.yaml
```

The TUI renders to your terminal while stdout carries nothing but the Emitted
Manifest, so redirecting just works. With no argument the Session opens on the
Kind picker — a fuzzy-filterable list of every create-capable Kind on the
cluster. An optional positional argument in kubectl-explain syntax deep-links
straight to a Kind, or a Field Path within it:

```sh
kubectl craft deploy                # apps/v1 Deployment, short name resolved via discovery
kubectl craft deploy.spec.strategy  # jump to a Field Path inside its Type Schema
```

Standard kubectl plugin flags (`--context`, `--kubeconfig`, `--namespace`, …)
apply; the context is fixed for the Session.

## Keybindings

Keys are fixed (no rebinding config in v0.1) so this table, the in-app `?` help,
and the hint bar stay trivially truthful. This table mirrors the `?` help
overlay in the compose view.

### Compose view — navigate mode

| Key              | Action                                                                                                                                                                                                                                                                          |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `j`/`k`, `↑`/`↓` | move focus                                                                                                                                                                                                                                                                      |
| `l`, `→`         | expand the focused field; on an expanded field: step to its first child                                                                                                                                                                                                         |
| `h`, `←`         | collapse the focused field; on a collapsed field: jump to its parent                                                                                                                                                                                                            |
| `enter`          | open the value widget on a leaf; toggle expansion on a parent                                                                                                                                                                                                                   |
| `a`              | append an item on an array node; prompt for a key on a map node                                                                                                                                                                                                                 |
| `d`              | unset the focused value — a subtree with filled values confirms the discard count first                                                                                                                                                                                         |
| `e`              | pop a schema-blind subtree out to `$EDITOR` as raw YAML                                                                                                                                                                                                                         |
| `g` / `G`        | jump to the top / bottom of the tree                                                                                                                                                                                                                                            |
| `/`              | search the Kind's Field Paths and jump to a match                                                                                                                                                                                                                               |
| `v`              | Validate the Draft against the live cluster (server dry-run) — nothing persists; findings mark the offending tree nodes, and anything unmappable lands in the results pane. Editing the Draft marks findings stale (`v` revalidates); switching the version drops them entirely |
| `n`              | jump to the first Validate finding's node; pressing again cycles through the findings in order                                                                                                                                                                                  |
| `r`              | open the Validate results pane                                                                                                                                                                                                                                                  |
| `V`              | switch the open Kind's version — values carry over by Field Path, and a drop report confirms anything that would not survive                                                                                                                                                    |
| `?`              | open the help overlay (any key closes it)                                                                                                                                                                                                                                       |

### Edit mode — `enter` on a leaf opens its type-appropriate widget

A schema-blind leaf opens the raw-YAML text area instead.

| Key              | Action                                                      |
| ---------------- | ----------------------------------------------------------- |
| `enter`          | confirm the value into the Draft; rejections render inline  |
| `esc`            | cancel back to navigate mode without touching the Draft     |
| `space`, `←`/`→` | flip a boolean toggle                                       |
| `↑`/`↓`          | choose from an enum select                                  |
| `ctrl+s`         | confirm the raw-YAML text area — `enter` types its newlines |

### Session

| Key      | Action                                                                |
| -------- | --------------------------------------------------------------------- |
| `esc`    | return to the Kind picker — a non-empty Draft warns before discarding |
| `q`      | quit — a non-empty Draft offers Emit & quit / Discard & quit / Cancel |
| `ctrl+d` | emit the Manifest to stdout and quit — no menu                        |
| `ctrl+c` | quit immediately, discarding the Draft                                |

### Search surfaces (Kind picker and the `/` field-search overlay)

Type-to-filter, fzf-style: printable keys filter immediately, `↑`/`↓` (or
`ctrl+j`/`ctrl+k`) move the selection, `enter` selects, `esc`
clears-then-dismisses.

## Validate

`v` submits the Draft to your API server as a **server-side dry-run**
(`dryRun=All`): full schema validation, CRD CEL rules, and admission webhooks
run for real, and **nothing is persisted**. Findings with field paths annotate
the offending tree nodes (`n` jumps to them); anything unmappable — freeform
webhook denials, top-level failures — lands in the results pane (`r`).

Validate needs `metadata.name` (plus a namespace for namespaced Kinds — taken
from `metadata.namespace` if set, else `--namespace`/the kubeconfig context
default, exactly like kubectl). It also needs RBAC permission to **create** the
Kind (a dry-run create is still a create to the authorizer). An RBAC 403 or a
network failure renders as "Validate unavailable" in the results pane — clearly
distinct from Manifest errors.

## Contracts

- **Clean stdout**: the TUI renders to the terminal (`/dev/tty`); stdout carries
  nothing but the final Emitted Manifest. Redirection is the workflow, not a
  special mode.
- **Sparse emission**: the Emitted YAML contains exactly the fields you set
  (plus the apiVersion/kind identity). Schema defaults appear as dimmed
  placeholders in the tree and in the detail pane, never in the output.
- **Nothing persists**: Drafts are ephemeral — they live and die with the
  Session, and Validate never creates anything on the cluster.
- **The cache is disposable**: OpenAPI v3 group documents are cached on disk at
  `os.UserCacheDir()/kubectl-craft` (`~/.cache/kubectl-craft` under XDG
  semantics; macOS/Windows equivalents handled by Go), keyed by cluster and
  server content hash and validated against the live index every Session.
  Deleting the directory at any time is always safe — worst case is a slower
  next Session. See [docs/DESIGN.md — Data layer](docs/DESIGN.md#data-layer).

## Supported platforms

Linux and macOS (amd64 and arm64) are supported. Any cluster serving
`/openapi/v3` (roughly Kubernetes 1.24+) works; the tool fails with a clear
minimum-version error otherwise.

<!-- TODO(#15): Windows posture is undecided — goreleaser is configured to
build windows archives, but the TUI cannot yet acquire a console on Windows,
so they would fail gracefully at launch. Update this section when issue #15
lands (console support or dropped artifacts). -->

Windows builds are configured but do not yet run: the TUI needs a controlling
terminal (`/dev/tty`) and exits gracefully with an error on Windows.
[Issue #15](https://github.com/theZMC/kubectl-craft/issues/15) tracks whether
v0.1 ships Windows console support or drops the Windows archives.

## Development

Start with [CONTRIBUTING.md](CONTRIBUTING.md) (toolchain, test loops, fixture
corpora, demo regeneration). The domain language lives in
[CONTEXT.md](CONTEXT.md), the decided product shape in
[docs/DESIGN.md](docs/DESIGN.md), irreversible decisions in
[docs/adr/](docs/adr/), and the build slicing in
[docs/MILESTONES.md](docs/MILESTONES.md).
