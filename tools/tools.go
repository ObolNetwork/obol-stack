//go:build tools

// Package tools tracks build-time dependencies that are not imported by
// production code. controller-gen is the canonical source-of-truth tool
// for generating CRD manifests and DeepCopy methods from kubebuilder
// markers on the Go types in internal/monetizeapi.
//
// See `just generate`. CI fails if generated artifacts drift from the
// markers (see .github/workflows/lint-test.yaml::generate-check).
package tools

import (
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)
