# demo.tape — the signature loop, recorded with vhs (https://github.com/charmbracelet/vhs).
#
# Regenerate with `mise run demo:render` (see CONTRIBUTING.md — Demo). The
# tape assumes:
#   - `kubectl-craft` and `kubectl` on PATH (the render task builds and
#     provides both),
#   - KUBECONFIG pointing at a cluster with the sample CRDs from
#     internal/schema/testdata/crds/ installed (`mise run demo:cluster`
#     provides a throwaway k3d cluster),
#   - a clean scratch working directory (the render task provides one).
#
# Every keypress below is a real binding — the source of truth is the `?`
# help overlay (helpText in internal/tui/compose.go) and the README's
# keybinding table.

Require kubectl
Require kubectl-craft

Output demo.gif

Set Shell "bash"
Set FontSize 16
Set Width 1200
Set Height 700
Set TypingSpeed 75ms

# Compose a Gadget (a sample CRD carrying CEL rules) and redirect: stdout
# carries nothing but the Emitted Manifest.
Type "kubectl craft > gadget.yaml"
Enter
Sleep 4s

# Kind picker: type-to-filter, enter selects.
Type "gadget"
Sleep 1.5s
Enter
Sleep 3s

# Set metadata.name (required to Validate): `/` searches the Kind's Field
# Paths, enter jumps to the match, enter opens the value widget.
Type "/"
Sleep 500ms
Type "metadata.name"
Sleep 1.5s
Enter
Sleep 1s
Enter
Sleep 500ms
Type "demo"
Sleep 500ms
Enter
Sleep 1s

# spec.minReplicas and spec.maxReplicas are required by the Gadget schema.
Type "/"
Sleep 500ms
Type "minReplicas"
Sleep 1.5s
Enter
Sleep 1s
Enter
Sleep 500ms
Type "1"
Sleep 500ms
Enter
Sleep 1s

Type "/"
Sleep 500ms
Type "maxReplicas"
Sleep 1.5s
Enter
Sleep 1s
Enter
Sleep 500ms
Type "3"
Sleep 500ms
Enter
Sleep 1s

# spec.profile carries a CEL rule only the server evaluates — a bad value
# confirms fine client-side.
Type "/"
Sleep 500ms
Type "profile"
Sleep 1.5s
Enter
Sleep 1s
Enter
Sleep 500ms
Type "turbo"
Sleep 500ms
Enter
Sleep 1s

# v: Validate via server-side dry-run — nothing persists. The CEL violation
# marks spec.profile in the tree.
Type "v"
Sleep 4s

# n: jump to the finding. d: unset the bad value (sparse semantics — back to
# the dimmed default).
Type "n"
Sleep 2s
Type "d"
Sleep 2s

# Revalidate: clean pass.
Type "v"
Sleep 4s

# ctrl+d: emit the Manifest to stdout (the redirect above) and quit.
Ctrl+D
Sleep 2s

# The Emitted Manifest is sparse: exactly the fields set.
Type "cat gadget.yaml"
Enter
Sleep 5s
