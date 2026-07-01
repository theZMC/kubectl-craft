# The tool never mutates the cluster

A composed Manifest can be emitted (stdout/file) and Validated via server-side dry-run — a POST the API server evaluates fully (schema, CRD validation rules, admission webhooks) but never persists. The tool deliberately has **no apply capability**: "this tool never mutates your cluster" is a hard guarantee, kept because it makes the safety story crisp, keeps RBAC needs minimal (create permission is needed only for Validate, and only per-Kind), and leaves applying to kubectl where it belongs (`kubectl live-docs ... > x.yaml && kubectl apply -f x.yaml`). Future proposals to add in-TUI apply should be weighed against breaking this guarantee, not treated as a missing feature.

## Considered Options

- **Emit + dry-run Validate** — chosen; live-cluster validation is the differentiator no static generator has, at zero mutation risk.
- **Emit only** — rejected; forfeits the strongest advantage of being cluster-connected.
- **Emit + Validate + apply** — rejected; turns the tool into a cluster mutator, with the confirmation UX, failure handling, and scarier pitch that implies.
