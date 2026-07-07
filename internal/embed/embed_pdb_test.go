package embed

import (
	"strings"
	"testing"
)

// TestPodDisruptionBudgets pins the PDB stance for the singleton services on
// user-facing paths. On multi-node clusters a `kubectl drain` would otherwise
// silently evict the only pod of the payment gate (x402-verifier → every
// /services/* route 5xxs) or the RPC front door (eRPC). minAvailable: 1 on a
// single replica deliberately blocks voluntary eviction until the operator
// deletes the pod explicitly — the same stance llm.yaml takes for litellm.
//
// Rollouts are NOT affected by PDBs; both deployments surge gaplessly via
// RollingUpdate maxUnavailable 0 (default rounding at 1 replica).
func TestPodDisruptionBudgets(t *testing.T) {
	tests := []struct {
		name         string
		file         string
		pdbName      string
		namespace    string
		wantSelector map[string]string
	}{
		{
			name:      "x402-verifier payment gate",
			file:      "base/templates/x402.yaml",
			pdbName:   "x402-verifier",
			namespace: "x402",
			wantSelector: map[string]string{
				"app": "x402-verifier",
			},
		},
		{
			name:      "erpc RPC front door",
			file:      "base/templates/erpc.yaml",
			pdbName:   "erpc",
			namespace: "erpc",
			// Must match the ethereum/erpc chart's selectorLabels, which the
			// base chart does not control — verified against the live
			// deployment's spec.selector.matchLabels.
			wantSelector: map[string]string{
				"app.kubernetes.io/name":     "erpc",
				"app.kubernetes.io/instance": "erpc",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := ReadInfrastructureFile(tt.file)
			if err != nil {
				t.Fatalf("ReadInfrastructureFile(%s): %v", tt.file, err)
			}

			pdb := findDocByName(multiDoc(data), "PodDisruptionBudget", tt.pdbName)
			if pdb == nil {
				t.Fatalf("PodDisruptionBudget %q missing from %s", tt.pdbName, tt.file)
			}
			if ns := nested(pdb, "metadata", "namespace"); ns != tt.namespace {
				t.Errorf("namespace = %v, want %s", ns, tt.namespace)
			}
			if ma := nested(pdb, "spec", "minAvailable"); ma != 1 {
				t.Errorf("minAvailable = %v, want 1", ma)
			}
			sel, ok := nested(pdb, "spec", "selector", "matchLabels").(map[string]any)
			if !ok {
				t.Fatal("PDB missing spec.selector.matchLabels")
			}
			for k, v := range tt.wantSelector {
				if sel[k] != v {
					t.Errorf("selector[%s] = %v, want %s", k, sel[k], v)
				}
			}
			if len(sel) != len(tt.wantSelector) {
				t.Errorf("selector has %d labels %v, want exactly %v — extra labels risk matching no pods", len(sel), sel, tt.wantSelector)
			}
		})
	}
}

// TestCloudflaredPDBTemplate pins the pre-existing tunnel-connector PDB: it
// must stay gated on an active tunnel (local/remote mode) with >1 replica,
// where minAvailable: 1 lets drains roll one connector at a time without
// dropping the public path.
func TestCloudflaredPDBTemplate(t *testing.T) {
	data, err := ReadInfrastructureFile("cloudflared/templates/pdb.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}
	raw := string(data)

	for _, want := range []string{
		"kind: PodDisruptionBudget",
		"minAvailable: 1",
		"app.kubernetes.io/name: cloudflared",
		"gt $persistentReplicas 1",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("cloudflared pdb.yaml missing %q", want)
		}
	}
}
