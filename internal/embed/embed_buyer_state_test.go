package embed

import (
	"fmt"
	"testing"
)

// TestBuyerStatePVC asserts that x402-buyer's /state is backed by a PVC
// (not an emptyDir), and that the litellm Deployment uses the Recreate
// strategy so the RWO PVC can be remounted without overlap.
//
// Regression: emptyDir lost consumed.json on every pod restart, causing
// the buyer to re-spend already-consumed auths from the ConfigMap pool
// and cascading into facilitator 400s ("nonce already used") until a
// manual `buy.py process --all` reseeded.
func TestBuyerStatePVC(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/llm.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	docs := multiDoc(data)

	// PVC must exist in the llm namespace with RWO + local-path storage class.
	pvc := findDocByName(docs, "PersistentVolumeClaim", "x402-buyer-state")
	if pvc == nil {
		t.Fatal("PersistentVolumeClaim 'x402-buyer-state' missing from llm.yaml")
	}

	if ns := nested(pvc, "metadata", "namespace"); ns != "llm" {
		t.Errorf("PVC namespace = %v, want llm", ns)
	}

	modes, ok := nested(pvc, "spec", "accessModes").([]any)
	if !ok || len(modes) != 1 || modes[0] != "ReadWriteOnce" {
		t.Errorf("PVC accessModes = %v, want [ReadWriteOnce]", modes)
	}

	if sc := nested(pvc, "spec", "storageClassName"); sc != "local-path" {
		t.Errorf("PVC storageClassName = %v, want local-path", sc)
	}

	if storage := nested(pvc, "spec", "resources", "requests", "storage"); storage == nil {
		t.Error("PVC missing spec.resources.requests.storage")
	}

	// litellm Deployment volume entry must reference the PVC, not emptyDir.
	dep := findDocByName(docs, "Deployment", "litellm")
	if dep == nil {
		t.Fatal("litellm Deployment missing from llm.yaml")
	}

	volumes, ok := nested(dep, "spec", "template", "spec", "volumes").([]any)
	if !ok {
		t.Fatal("litellm Deployment has no volumes")
	}

	var stateVolume map[string]any
	for _, v := range volumes {
		vm, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if vm["name"] == "x402-buyer-state" {
			stateVolume = vm
			break
		}
	}

	if stateVolume == nil {
		t.Fatal("litellm Deployment missing 'x402-buyer-state' volume")
	}

	if _, isEmptyDir := stateVolume["emptyDir"]; isEmptyDir {
		t.Error("x402-buyer-state is still emptyDir — must be persistentVolumeClaim to survive pod restarts")
	}

	pvcRef, ok := stateVolume["persistentVolumeClaim"].(map[string]any)
	if !ok {
		t.Fatal("x402-buyer-state volume is not backed by persistentVolumeClaim")
	}

	if claim := pvcRef["claimName"]; claim != "x402-buyer-state" {
		t.Errorf("persistentVolumeClaim.claimName = %v, want x402-buyer-state", claim)
	}

	// Strategy must be Recreate so the new pod waits for the old pod to
	// release the RWO PVC before mounting. RollingUpdate with maxSurge>0
	// would block indefinitely.
	if strat := nested(dep, "spec", "strategy", "type"); strat != "Recreate" {
		t.Errorf("litellm Deployment strategy.type = %v, want Recreate (RWO PVC cannot be co-mounted during surge)", strat)
	}

	// The x402-buyer sidecar must run as UID/GID 1000 to match the owner
	// local-path provisions the state PVC with. fsGroup is a no-op on
	// hostPath-backed local-path volumes and a root chown init is forbidden
	// in the restricted-PSS llm namespace, so UID alignment is the only way
	// the sidecar can write /state/consumed.json. If this regresses to 65532,
	// the buyer silently fails to persist consumed auths on the first paid
	// call (Permission denied on a 1000:1000-owned dir) — the same re-spend
	// cascade the PVC was introduced to prevent.
	containers, ok := nested(dep, "spec", "template", "spec", "containers").([]any)
	if !ok {
		t.Fatal("litellm Deployment has no containers")
	}
	var buyer map[string]any
	for _, c := range containers {
		cm, ok := c.(map[string]any)
		if ok && cm["name"] == "x402-buyer" {
			buyer = cm
			break
		}
	}
	if buyer == nil {
		t.Fatal("x402-buyer container missing from litellm Deployment")
	}
	// Compare via fmt to avoid depending on yaml.v3's exact numeric type.
	if u := nested(buyer, "securityContext", "runAsUser"); fmt.Sprintf("%v", u) != "1000" {
		t.Errorf("x402-buyer securityContext.runAsUser = %v, want 1000 (must match local-path owner of x402-buyer-state PVC)", u)
	}
	if g := nested(buyer, "securityContext", "runAsGroup"); fmt.Sprintf("%v", g) != "1000" {
		t.Errorf("x402-buyer securityContext.runAsGroup = %v, want 1000", g)
	}
	if nr := nested(buyer, "securityContext", "runAsNonRoot"); nr != true {
		t.Errorf("x402-buyer securityContext.runAsNonRoot = %v, want true (restricted PSS)", nr)
	}
}
