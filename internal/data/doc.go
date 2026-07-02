// Package data is the cluster-facing layer.
//
// It owns everything that talks to the API server for a Session:
// kubeconfig/flag handling, discovery of create-capable Kinds, fetching
// the OpenAPI v3 Documents that source every Type Schema, and the
// hash-validated disk cache that lets a warm Session start near-instant
// (ADR-0002). It renders nothing and holds no Draft state.
package data
