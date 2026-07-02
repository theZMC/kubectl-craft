// Package data is the cluster-facing layer.
//
// It owns everything that talks to the API server for a Session:
// kubeconfig/flag handling, discovery of create-capable Kinds, and
// fetching the OpenAPI v3 Documents that source every Type Schema
// (later joined by the hash-validated schema cache). It renders
// nothing and holds no Draft state.
package data
