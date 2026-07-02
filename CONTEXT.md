# kubectl-craft

<!--TOC-->

______________________________________________________________________

- [Language](#language)
- [Relationships](#relationships)
- [Example dialogue](#example-dialogue)
- [Flagged ambiguities](#flagged-ambiguities)

______________________________________________________________________

<!--TOC-->

A `kubectl` plugin that presents a TUI for **composing** Kubernetes manifests
from the **Type Schemas** defined on the cluster the current context points to —
navigate the field tree, fill values with per-field documentation alongside,
produce a valid **Manifest**. Browsing the schemas is the substrate; composing
is the product. This file is the domain glossary the whole repo speaks; the
user-facing story lives in the [README](README.md) and the decided shape in
[docs/DESIGN.md](docs/DESIGN.md).

## Language

**Type Schema**: The structure and field-level documentation of a resource
*kind* (its fields, types, and descriptions), sourced live from the cluster.
_Avoid_: definition, resource definition, model

**Compose**: Building a **Manifest** by navigating a **Kind**'s **Type Schema**
and filling in field values. _Avoid_: create, generate, scaffold

**Manifest**: The YAML document produced by composing — a candidate resource
that has not been applied to the cluster. _Avoid_: resource, object, spec

**Validate**: Submitting a **Manifest** to the API server with server-side
dry-run, so the real cluster (schema validation, CRD rules, admission webhooks)
checks it without anything being persisted. _Avoid_: lint, check (client-side
connotations), apply

**Instance**: An actual object living on the cluster (e.g. a specific running
Deployment). Browsing Instances is explicitly **out of scope** — that is k9s /
`kubectl get` territory. _Avoid_: object, resource (when referring to live data)

**Draft**: The in-progress state of composing — the values filled so far against
a **Kind**'s **Type Schema**, before being **Emitted** as a **Manifest**. A
Draft lives and dies with its **Session**. _Avoid_: work-in-progress, buffer,
unsaved manifest ("unsaved" implies a save that doesn't exist)

**Emit**: Producing the composed **Manifest** as YAML — the act that ends
composing. The only way a Manifest leaves the tool. _Avoid_: save, export,
write, output (as a verb)

**Kind**: A resource type identified by group/version/kind (GVK), e.g.
`apps/v1 Deployment`. The unit of browsing and composing; each served version of
a kind has its own **Type Schema**. _Avoid_: resource type, object type

**Preferred Version**: The version the cluster reports as the default for a
kind's group; the version the browser lands on when a **Kind** is served at
multiple versions. _Avoid_: latest version, storage version (a distinct
API-server concept)

**Field Path**: The path identifying a position, in two flavors:
**schema-level** — dots only, a field within a **Kind**'s **Type Schema**, e.g.
`spec.template.spec.containers` — and **Draft-level**, which adds bracket
selectors for data the schema can't name: `containers[0].image`,
`labels["app.kubernetes.io/name"]`. Dots address schema-defined fields; brackets
address items and map keys in a **Draft**. _Avoid_: JSON path, breadcrumb (the
breadcrumb *displays* the Field Path)

**OpenAPI v3 Document**: The cluster's per-group schema documents served at
`/openapi/v3`, discovered via the API server. The authoritative source of every
**Type Schema**, including CRDs and aggregated APIs. _Avoid_: swagger, OpenAPI
v2 (the older single-document path we are not using)

**Session**: One run of the TUI, bound at invocation to a single kubeconfig
context; switching clusters means starting a new Session. _Avoid_: connection

## Relationships

- The tool browses **Type Schemas** and composes **Manifests**; it never browses
  **Instances**.
- A **Manifest** is composed against exactly one **Kind** at one version.
- Every **Type Schema** is derived from the cluster's **OpenAPI v3 Document**.
- A **Kind** may be served at multiple versions; all are browsable, and the
  **Preferred Version** is the default.
- A **Session** holds at most one **Draft**; **Emitting** it (or discarding it)
  is the only way out.

## Example dialogue

> **Dev:** "When I **compose** an Ingress, do I get an **Instance** on the
> cluster?" **Domain expert:** "No — composing produces a **Manifest**. Whether
> it ever touches the cluster is a separate act; the TUI's job ends at a valid
> YAML document." **Dev:** "And if the cluster serves `v1` and `v1beta1` of a
> CRD?" **Domain expert:** "Each version has its own **Type Schema**; you land
> on the **Preferred Version** and can switch."

## Flagged ambiguities

- "resource definition" was used loosely to mean the browsable subject —
  resolved to **Type Schema** (schemas/docs of kinds), explicitly *not* live
  **Instances**.
- "create" is ambiguous (compose a Manifest vs. create an Instance on the
  cluster) — resolved: the TUI **composes Manifests**; creating Instances is
  `kubectl apply`'s job.
