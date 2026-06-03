// Package k8sperm centralizes obol-stack's cross-platform fix for the one
// recurring class of Kubernetes failure in this codebase: a non-root workload
// cannot write a freshly provisioned local-path volume.
//
// # Why this exists
//
// obol-stack persists workload state on rancher local-path-provisioner. Those
// PVs are hostPath-typed, and the kubelet treats hostPath volumes as
// "unmanaged" — it never runs SetVolumeOwnership for them, so a pod's
// securityContext.fsGroup and fsGroupChangePolicy are genuine NO-OPs on every
// local-path volume. (This holds identically on k3d/macOS, where the cluster
// runs inside Docker, and on native k3s/Linux — it is the same kubelet binary.)
//
// The provisioner's setup script chowns each new volume to a single hardcoded
// owner (1000:1000). Any workload that runs as a different UID therefore cannot
// write its own volume unless something re-owns the directory first. The only
// mechanism that does this deterministically across both backends, regardless
// of volume type, is a root-privileged init container that chowns the mount to
// the workload's UID/GID before the main container starts. The master Hermes
// agent has always relied on exactly this; this package makes the same
// mechanism reusable so controller-spawned workloads cannot silently drift out
// of sync with it (the bug that left spawned agents stuck Provisioning).
//
// # Pod Security Standard constraint
//
// The returned init container runs as UID 0, which violates the "restricted"
// Pod Security Standard. Use it ONLY in namespaces that do not enforce
// restricted PSS (agent-*, hermes-*, openclaw-*, and the cluster-default
// namespaces). It is rejected at admission in the llm and x402 namespaces; for
// workloads there, align the container's runAsUser/runAsGroup with the
// provisioner's owner instead. See plans/volume-permission-hardening.md.
package k8sperm

import (
	"fmt"
	"strings"
)

// ChownMount identifies a single volume mount whose contents must be re-owned
// to the workload's UID/GID before the main container starts.
type ChownMount struct {
	// Name is the pod volume name (must match an entry in the pod's volumes).
	Name string
	// MountPath is where the volume is mounted inside the init container.
	MountPath string
}

// RootChownInitContainer returns an init-container spec, as an unstructured
// map[string]any suitable for controller-rendered manifests, that runs as root
// and recursively chowns every mount path to uid:gid.
//
// uid/gid MUST equal the main container's runAsUser/runAsGroup, otherwise the
// main (non-root) container still cannot write the volume. image should reuse
// the workload's own image so no extra image pull is needed; any image with a
// POSIX sh and chown works.
//
// See the package documentation for the PSS constraint — do not use this in a
// namespace that enforces the restricted Pod Security Standard.
func RootChownInitContainer(name, image string, uid, gid int64, mounts []ChownMount) map[string]any {
	paths := make([]string, 0, len(mounts))
	volumeMounts := make([]any, 0, len(mounts))
	for _, m := range mounts {
		paths = append(paths, m.MountPath)
		volumeMounts = append(volumeMounts, map[string]any{
			"name":      m.Name,
			"mountPath": m.MountPath,
		})
	}

	return map[string]any{
		"name":            name,
		"image":           image,
		"imagePullPolicy": "IfNotPresent",
		"securityContext": map[string]any{
			// Must be root to chown a volume the provisioner owns as a
			// different UID. Mirrors the master Hermes init-hermes-perms
			// container; admissible only outside restricted-PSS namespaces.
			"runAsUser":    int64(0),
			"runAsGroup":   int64(0),
			"runAsNonRoot": false,
		},
		"command":      []any{"sh", "-ec", ChownCommand(uid, gid, paths)},
		"volumeMounts": volumeMounts,
	}
}

// ChownCommand returns the shell command a root chown init container runs:
// a single recursive chown of every path to uid:gid. Exposed so YAML-template
// renderers (e.g. the master Hermes deployment) can share the exact semantics
// rather than re-deriving the string and drifting.
func ChownCommand(uid, gid int64, paths []string) string {
	return fmt.Sprintf("chown -R %d:%d %s", uid, gid, strings.Join(paths, " "))
}
