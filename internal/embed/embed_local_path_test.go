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
