// Package images centralises the policy for selecting Docker image tags in
// embedded Kubernetes manifests.
//
// The problem this solves: when the obol binary is upgraded, the K8s
// Deployments it creates must trigger a rolling update so old pods are
// replaced with ones running the new image. With ":latest" tags the embedded
// manifest is byte-identical across binary versions, so kubectl apply reports
// "unchanged" and stale pods keep serving the old image forever.
//
// The fix: production binaries are built with version.GitCommit injected via
// ldflags, and CI publishes images tagged with the same short commit SHA.
// Resolve uses that SHA so upgrading the binary changes the image:tag in
// every manifest, which is what triggers K8s to roll the pod.
//
// Dev mode (OBOL_DEVELOPMENT=true), unset/unknown GitCommit, and dirty repos
// fall back to ":latest" — that path matches the buildAndImportLocalImages
// flow that imports freshly-built images into k3d as ":latest".
package images

import (
	"os"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/version"
)

// Resolve returns the fully-qualified image reference for an image whose
// repository part is `repo` (e.g. "ghcr.io/obolnetwork/demo-server").
//
//	images.Resolve("ghcr.io/obolnetwork/demo-server")
//	// → "ghcr.io/obolnetwork/demo-server:abc1234"   (production)
//	// → "ghcr.io/obolnetwork/demo-server:latest"    (dev / unknown commit)
func Resolve(repo string) string {
	if useLatest() {
		return repo + ":latest"
	}
	return repo + ":" + version.GitCommit
}

// useLatest reports whether the current binary should reach for the mutable
// :latest tag rather than a commit-pinned tag.
func useLatest() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OBOL_DEVELOPMENT")), "true") {
		return true
	}
	commit := strings.TrimSpace(version.GitCommit)
	if commit == "" || commit == "unknown" || commit == "dev" {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(version.GitDirty), "true") {
		return true
	}
	return false
}
