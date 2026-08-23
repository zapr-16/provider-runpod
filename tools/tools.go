//go:build tools

package tools

import (
	// Blank imports pin these tools in go.mod/go.sum so `make generate`
	// and CI install versions locked by the module graph rather than
	// resolving them at install time.
	_ "golang.org/x/vuln/cmd/govulncheck"
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)
