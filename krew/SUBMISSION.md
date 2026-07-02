# krew-index submission

<!--TOC-->

______________________________________________________________________

- [What is staged](#what-is-staged)
- [Verification performed](#verification-performed)
- [Opening the krew-index PR (maintainer steps)](#opening-the-krew-index-pr-maintainer-steps)
- [Updating the manifest for future releases](#updating-the-manifest-for-future-releases)

______________________________________________________________________

<!--TOC-->

[`craft.yaml`](craft.yaml) in this directory is the staged
[krew](https://krew.sigs.k8s.io/) plugin manifest: its content is exactly what
the [krew-index](https://github.com/kubernetes-sigs/krew-index) submission needs
at `plugins/craft.yaml`. This page records how it was verified, the exact steps
the maintainer follows to open the krew-index PR (the submission is a PR from a
personal fork, so it is deliberately not automated here), and how future
releases keep the manifest fresh without hand-editing hashes.

## What is staged

- Plugin name `craft` — lowercase, matching the manifest file name and the
  `kubectl craft` invocation, per krew's
  [naming guide](https://krew.sigs.k8s.io/docs/developer-guide/develop/naming-guide/).
- Platforms: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 — the exact
  v0.1.0 artifact set. Windows is deliberately absent: the TUI acquires its
  terminal via `/dev/tty`, which does not exist on Windows
  ([#15](https://github.com/theZMC/kubectl-craft/issues/15)).
- `sha256` values come from the published release's `checksums.txt`; the
  archives carry the `kubectl-craft` binary plus `LICENSE` and `README.md`, so
  krew's default extract-everything rule satisfies the "extract the LICENSE file
  during installation" checklist item.
- `shortDescription` (under 50 characters) and `description` speak the glossary
  in [CONTEXT.md](../CONTEXT.md): composing Manifests from live Type Schemas.

## Verification performed

Per krew's
[local testing guide](https://krew.sigs.k8s.io/docs/developer-guide/testing-locally/),
against the published v0.1.0 artifacts on darwin/arm64
(`krew install --manifest` doubles as the practical manifest lint):

```sh
# Local-archive path: verifies the manifest installs the downloaded archive.
curl -sLO https://github.com/theZMC/kubectl-craft/releases/download/v0.1.0/kubectl-craft_0.1.0_darwin_arm64.tar.gz
shasum -a 256 kubectl-craft_0.1.0_darwin_arm64.tar.gz   # matches craft.yaml
KREW_ROOT=$(mktemp -d) kubectl krew install \
  --manifest=krew/craft.yaml \
  --archive=kubectl-craft_0.1.0_darwin_arm64.tar.gz

# Remote path: verifies the uri and sha256 end-to-end (no --archive).
KREW_ROOT=$(mktemp -d) kubectl krew install --manifest=krew/craft.yaml
```

Both installs succeeded and the installed plugin runs: `kubectl craft --version`
(with `$KREW_ROOT/bin` on `PATH`) reports `kubectl-craft version 0.1.0`. Other
platforms can be spot-checked the same way with `KREW_OS=linux KREW_ARCH=amd64`
overrides; krew-index CI installs every platform in the manifest anyway.

## Opening the krew-index PR (maintainer steps)

1. Sign the [Kubernetes CLA](https://git.k8s.io/community/CLA.md) if not already
   signed — krew-index is a kubernetes-sigs repo and its bot blocks PRs without
   it.
1. Fork and branch:
   ```sh
   gh repo fork kubernetes-sigs/krew-index --clone && cd krew-index
   git checkout -b new-plugin-craft
   ```
1. Copy the staged manifest into place — the file name must match the plugin
   name:
   ```sh
   cp ../kubectl-craft/krew/craft.yaml plugins/craft.yaml
   ```
1. Commit and open the PR against `kubernetes-sigs/krew-index@master`, titled
   `New plugin: craft` (the convention their merged new-plugin PRs follow):
   ```sh
   git add plugins/craft.yaml
   git commit -s -m 'New plugin: craft'
   gh pr create --repo kubernetes-sigs/krew-index --title 'New plugin: craft'
   ```
1. Their CI validates the manifest schema and name/file-name match, downloads
   every `uri`, checks every `sha256`, and installs the plugin on every declared
   platform. New plugins then wait for a manual review by the krew maintainers
   (this can take a while; updates after acceptance are auto-approved by their
   bot).

## Updating the manifest for future releases

Nothing is hand-edited. The `krews` section of
[`.goreleaser.yaml`](../.goreleaser.yaml) (with `skip_upload: true`, since
krew-index only accepts updates through its own PR flow) regenerates the
manifest with the new version and sha256s on every tag release, and the release
workflow attaches it to the GitHub Release as `craft.yaml`. To ship v0.1.x+ to
krew:

1. Download `craft.yaml` from the release assets and copy it over
   `krew/craft.yaml` (pre-commit's yamlfmt may reformat it; the content is what
   matters).
1. Repeat the verification above against the new artifacts.
1. Open a krew-index PR replacing `plugins/craft.yaml`, titled
   `release new version v0.1.x of craft`.

Once the plugin is accepted into krew-index, version-bump PRs can be automated
with [krew-release-bot](https://github.com/rajatjindal/krew-release-bot) —
adopting it is deliberately deferred until there is an accepted plugin to bump.
