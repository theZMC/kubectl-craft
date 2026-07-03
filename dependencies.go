//go:build pin_dependencies

// Package dependencies pins the module's baseline library dependencies
// (DESIGN.md — Stack) in go.mod before any code imports them: later M0
// issues consume these from internal/data and internal/tui. The build
// tag is never enabled; `go mod tidy` still honors these imports.
// Delete an import here once a real package imports it.
package dependencies

import (
	_ "charm.land/bubbles/v2/textinput"
	_ "k8s.io/kube-openapi/pkg/spec3"
)
