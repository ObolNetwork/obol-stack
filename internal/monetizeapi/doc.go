// Package monetizeapi defines the Custom Resource Definitions for the
// Obol Stack monetize subsystem.
//
// The Go types in this package are the single source of truth for the
// CRD OpenAPI schemas embedded under
// internal/embed/infrastructure/base/templates/*-crd.yaml.
//
// Edit a field or marker here, then run `just generate` to regenerate
// the CRD YAML manifests + zz_generated_deepcopy.go from kubebuilder
// markers. CI fails if the working tree is dirty after that command
// runs (see .github/workflows/lint-test.yaml::generate-check).
//
// +kubebuilder:object:generate=true
// +groupName=obol.org
// +versionName=v1alpha1
package monetizeapi
