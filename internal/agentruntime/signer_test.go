package agentruntime

import (
	"encoding/json"
	"testing"
)

// TestRemoteSignerStrategyPatchArgs guards the post-sync pin of
// strategy.type=Recreate on the remote-signer deployment. The chart (0.3.3)
// cannot render spec.strategy, so this imperative patch is the only thing
// keeping a RWO-keystore singleton off RollingUpdate — where a surge pod on
// another node wedges on the volume attach. The patch must be a merge patch
// that pins type=Recreate AND carries an explicit rollingUpdate:null — the
// null is what removes the k8s-defaulted field the API would otherwise
// reject the type flip over.
func TestRemoteSignerStrategyPatchArgs(t *testing.T) {
	args := RemoteSignerStrategyPatchArgs("hermes-obol-agent")

	want := map[string]bool{
		"patch":                    false,
		"deployment/remote-signer": false,
		"hermes-obol-agent":        false,
		"--type=merge":             false,
	}
	var patchJSON string
	for i, a := range args {
		if _, ok := want[a]; ok {
			want[a] = true
		}
		if a == "-p" && i+1 < len(args) {
			patchJSON = args[i+1]
		}
	}
	for arg, seen := range want {
		if !seen {
			t.Errorf("RemoteSignerStrategyPatchArgs missing expected arg %q in %v", arg, args)
		}
	}
	if patchJSON == "" {
		t.Fatal("RemoteSignerStrategyPatchArgs missing -p <patch>")
	}

	var patch struct {
		Spec struct {
			Strategy map[string]any `json:"strategy"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(patchJSON), &patch); err != nil {
		t.Fatalf("patch is not valid JSON: %v\n%s", err, patchJSON)
	}
	if got := patch.Spec.Strategy["type"]; got != "Recreate" {
		t.Errorf("strategy.type = %v, want Recreate", got)
	}
	ru, present := patch.Spec.Strategy["rollingUpdate"]
	if !present || ru != nil {
		t.Errorf("strategy.rollingUpdate = %v (present=%v), want explicit null — it is what clears the k8s-defaulted block", ru, present)
	}
}
