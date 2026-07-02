package schema

import "k8s.io/kube-openapi/pkg/validation/spec"

// FieldPaths enumerates every schema-level Field Path reachable from this
// node, in tree order: parents before children, siblings sorted by field
// name, dots only — an array's item schema and a map's value schema are
// traversed, never listed, because they share their parent's Field Path.
// This is the `/` field search's candidate set (DESIGN.md — Flow §5).
//
// The walk carries the requiredness walk's on-chain cycle guard
// (collectAssured): each resolved Type Schema is descended once per chain,
// so a self-referential Type Schema (JSONSchemaProps) yields finite
// candidates — the cycle's re-entry field is listed, and the lap beneath it
// is never materialized. Enumeration is best-effort by design: a subtree
// whose $ref fails to resolve contributes its own Field Path but nothing
// beneath, matching the compose view, which keeps such rows collapsed and
// surfaces the error at expansion.
func (n *Node) FieldPaths() []string {
	var paths []string
	n.collectFieldPaths(map[*spec.Schema]bool{}, &paths)
	return paths
}

// collectFieldPaths appends the dotted Field Paths beneath one node, depth
// first. onChain marks the resolved Type Schemas the current chain is
// already descending, so a cycle terminates instead of recursing.
func (n *Node) collectFieldPaths(onChain map[*spec.Schema]bool, paths *[]string) {
	chain, err := n.resolveChain(n.schema)
	if err != nil {
		return // an unresolvable $ref: the subtree offers no candidates
	}
	resolved := concreteSchema(chain)
	if onChain[resolved] {
		return
	}
	onChain[resolved] = true
	defer delete(onChain, resolved)

	children, err := n.Children()
	if err != nil {
		return
	}
	for _, child := range children {
		if child.path != n.path {
			*paths = append(*paths, child.path)
		}
		child.collectFieldPaths(onChain, paths)
	}
}
