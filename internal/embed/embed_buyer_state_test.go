package embed

import "testing"

// TestBuyerStatePVC asserts that x402-buyer's /state is backed by a PVC
// (not an emptyDir), that the buyer runs as its own Recreate Deployment so
// the RWO PVC is remounted without overlap, and that the litellm Deployment
// is stateless with RollingUpdate maxUnavailable: 0 (zero-downtime rollouts,
// issue #321).
//
// Regression (emptyDir): lost consumed.json on every pod restart, causing
// the buyer to re-spend already-consumed auths from the ConfigMap pool
// and cascading into facilitator 400s ("nonce already used") until a
// manual `buy.py process --all` reseeded.
// Regression (sidecar coupling): while the buyer lived in the litellm pod,
// the RWO PVC forced litellm onto Recreate — every rollout (Reloader secret
// change, image bump) was a full inference gap.
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

	// The x402-buyer Deployment owns the PVC mount; litellm must not.
	buyerDep := findDocByName(docs, "Deployment", "x402-buyer")
	if buyerDep == nil {
		t.Fatal("x402-buyer Deployment missing from llm.yaml (buyer split, issue #321)")
	}

	volumes, ok := nested(buyerDep, "spec", "template", "spec", "volumes").([]any)
	if !ok {
		t.Fatal("x402-buyer Deployment has no volumes")
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
		t.Fatal("x402-buyer Deployment missing 'x402-buyer-state' volume")
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

	// Buyer strategy must be Recreate: the new pod waits for the old pod to
	// release the RWO PVC before mounting, and consumed-auth state must
	// have exactly one writer (double-spend protection).
	if strat := nested(buyerDep, "spec", "strategy", "type"); strat != "Recreate" {
		t.Errorf("x402-buyer Deployment strategy.type = %v, want Recreate (RWO PVC + single-writer auth state)", strat)
	}

	if policy := nested(buyerDep, "spec", "template", "spec", "securityContext", "fsGroupChangePolicy"); policy != "OnRootMismatch" {
		t.Errorf("x402-buyer pod fsGroupChangePolicy = %v, want OnRootMismatch", policy)
	}

	// The buyer pod must keep UID/GID 1000 while hostPath PVs from
	// <= v0.10.0-rc12 clusters remain in support: those PVs ignore fsGroup
	// (kubelet skips ownership management on hostPath) and hold a
	// consumed.json written 0600 by UID 1000 — a 65532 buyer crashloops
	// on `load state` and takes every paid/* route down. On fresh local-type
	// PVs the explicit UID is harmless.
	if u := nested(buyerDep, "spec", "template", "spec", "securityContext", "runAsUser"); u != 1000 {
		t.Errorf("x402-buyer pod securityContext.runAsUser = %v, want 1000 (legacy hostPath-PV state compat)", u)
	}
	if g := nested(buyerDep, "spec", "template", "spec", "securityContext", "runAsGroup"); g != 1000 {
		t.Errorf("x402-buyer pod securityContext.runAsGroup = %v, want 1000 (legacy hostPath-PV state compat)", g)
	}
	if nr := nested(buyerDep, "spec", "template", "spec", "securityContext", "runAsNonRoot"); nr != true {
		t.Errorf("x402-buyer pod securityContext.runAsNonRoot = %v, want true (restricted PSS)", nr)
	}

	// litellm must be stateless: no buyer container, no PVC volume, and
	// RollingUpdate with maxUnavailable: 0 so rollouts never gap inference.
	dep := findDocByName(docs, "Deployment", "litellm")
	if dep == nil {
		t.Fatal("litellm Deployment missing from llm.yaml")
	}
	if liteVols, ok := nested(dep, "spec", "template", "spec", "volumes").([]any); ok {
		for _, v := range liteVols {
			vm, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if _, isPVC := vm["persistentVolumeClaim"]; isPVC {
				t.Errorf("litellm Deployment mounts PVC volume %v — litellm must stay stateless for RollingUpdate maxUnavailable:0", vm["name"])
			}
		}
	}
	containers, ok := nested(dep, "spec", "template", "spec", "containers").([]any)
	if !ok {
		t.Fatal("litellm Deployment has no containers")
	}
	for _, c := range containers {
		cm, ok := c.(map[string]any)
		if ok && cm["name"] == "x402-buyer" {
			t.Error("x402-buyer container found in litellm Deployment — the buyer must run as its own Deployment (issue #321)")
		}
	}
	if strat := nested(dep, "spec", "strategy", "type"); strat != "RollingUpdate" {
		t.Errorf("litellm Deployment strategy.type = %v, want RollingUpdate (zero-downtime rollouts)", strat)
	}
	if mu := nested(dep, "spec", "strategy", "rollingUpdate", "maxUnavailable"); mu != 0 {
		t.Errorf("litellm Deployment rollingUpdate.maxUnavailable = %v, want 0 (a new pod must be Ready before the old one terminates)", mu)
	}
	if ms := nested(dep, "spec", "strategy", "rollingUpdate", "maxSurge"); ms != 1 {
		t.Errorf("litellm Deployment rollingUpdate.maxSurge = %v, want 1", ms)
	}
}
