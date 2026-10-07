package embed

import (
	"strings"
	"testing"
)

// TestLocalPathProvisionerUsesLocalPV asserts that local-path-provisioner
// creates local PVs, not hostPath PVs. Kubernetes can apply pod fsGroup
// ownership to local PVs, which is the lean path that avoids per-workload
// root chown init containers and UID 1000 alignment.
func TestLocalPathProvisionerUsesLocalPV(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/local-path.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}
	text := string(data)

	for _, want := range []string{
		`defaultVolumeType: local`,
		`mkdir -m 0770 -p "${VOL_DIR}"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("local-path setup script missing %q", want)
		}
	}

	for _, banned := range []string{
		`chown -R 1000:1000 "${VOL_DIR}"`,
		`chmod 2775 "${VOL_DIR}"`,
		`mkdir -m 0755 -p "${VOL_DIR}"`,
	} {
		if strings.Contains(text, banned) {
			t.Errorf("local-path setup still contains legacy hostPath workaround %q", banned)
		}
	}
}

// TestLocalPathStorageClassKeepsNamespacePVCLayout pins the on-disk PVC
// layout ($DATA_DIR/<namespace>/<pvc-name>) that host-side wallet, backup and
// export code depends on, and the provisioner opt-out that layout needs:
// local-path-provisioner >= v0.0.33 rejects a pathPattern that equals
// <namespace>/<pvc-name> unless allowUnsafePathPattern is set, which left
// every PVC Pending after the v0.0.30 -> v0.0.37 bump.
func TestLocalPathStorageClassKeepsNamespacePVCLayout(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/local-path.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"pathPattern: '{{`{{ .PVC.Namespace }}/{{ .PVC.Name }}`}}'",
		`allowUnsafePathPattern: "true"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("local-path StorageClass missing %q", want)
		}
	}
}
