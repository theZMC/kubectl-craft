# Owned adaptive palette, one color per meaning

The TUI's colors are a designed palette we own — light/dark pairs resolved
against the terminal's queried background — not the terminal's own ANSI-16
theme. Every color carries exactly one meaning, and one meaning may surface in
many places: needs-fixing (missing required marker, Validate finding, editor
rejection), set/ok, pending-ask, structure, metadata. All tokens resolve through
a single theme layer; no styles inline at render sites.

We rejected two alternatives deliberately. **ANSI-16 inheritance** (the
kubectl-tool convention: let the user's terminal theme define the colors) was
initially chosen for its zero-detection robustness, then reversed — an owned
look was worth owning background detection, which bubbletea v2 does properly via
a terminal query on the program's own I/O (ADR-0006). **One color per UI role**
(every role visually unique, ~11 colors) was rejected because adjacent roles
share meaning — three distinct "error-ish" hues would trade the strong
red-means-broken prior for a palette the user has to learn.
