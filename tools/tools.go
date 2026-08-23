//go:build tools

package tools

import (
	// Blank import pins controller-gen in go.mod so `make generate`
	// installs a version consistent with the module graph.
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)
