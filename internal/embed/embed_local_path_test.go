package embed

import (
	"strings"
	"testing"
)

// TestLocalPathProvisionerGroupWritable asserts that the local-path
// provisioner's setup script makes newly provisioned volumes group-writable
// with the setgid bit, in addition to chowning them to 1000:1000.
//
// Rationale: local-path PVs are hostPath-typed, so pod fsGroup is a no-op and
// the directory mode this script sets is the only ownership a non-root
// workload can rely on. Group-write + setgid lets workloads that share GID
// 1000 (or are given runAsGroup: 1000) write the volume without a root chown
// init — the restricted-PSS-safe path used by the x402-buyer. Regressing this
// back to 0755 silently re-breaks any such workload. See
// plans/volume-permission-hardening.md.
func TestLocalPathProvisionerGroupWritable(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/local-path.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}
	text := string(data)

	for _, want := range []string{
		`mkdir -m 0775 -p "${VOL_DIR}"`,
		`chown -R 1000:1000 "${VOL_DIR}"`,
		`chmod 2775 "${VOL_DIR}"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("local-path setup script missing %q", want)
		}
	}

	// The old 0755 mode must not linger — it is the regression to guard against.
	if strings.Contains(text, `mkdir -m 0755 -p "${VOL_DIR}"`) {
		t.Error("local-path setup still uses mkdir -m 0755 — volumes will not be group-writable")
	}
}
