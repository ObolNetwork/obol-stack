package testutil

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/kubectl"
)

// PatchVerifierFacilitator patches the x402-pricing ConfigMap to use the given
// facilitator URL, restarts the x402-verifier deployment, waits for the new pod
// to log the updated URL, and registers t.Cleanup to restore the original
// ConfigMap contents.
//
// kubectlBin is the absolute path to the kubectl binary.
// kubeconfig is the absolute path to the kubeconfig file.
// newURL is the facilitator URL to inject (e.g. "http://host.docker.internal:54321").
func PatchVerifierFacilitator(t *testing.T, kubectlBin, kubeconfig, newURL string) {
	t.Helper()

	// Read current pricing YAML from ConfigMap.
	currentYAML, err := kubectl.Output(kubectlBin, kubeconfig, "get", "cm", "x402-pricing",
		"-n", "x402", "-o", `jsonpath={.data.pricing\.yaml}`)
	if err != nil {
		t.Fatalf("read x402-pricing ConfigMap: %v", err)
	}

	// Save original ConfigMap JSON for restore.
	originalJSON, err := kubectl.Output(kubectlBin, kubeconfig, "get", "cm", "x402-pricing",
		"-n", "x402", "-o", "json")
	if err != nil {
		t.Fatalf("read x402-pricing ConfigMap (json): %v", err)
	}

	// Replace the facilitatorURL in the pricing YAML.
	updated := currentYAML
	for line := range strings.SplitSeq(currentYAML, "\n") {
		if strings.Contains(line, "facilitatorURL:") {
			updated = strings.Replace(updated, line, fmt.Sprintf(`facilitatorURL: "%s"`, newURL), 1)
			break
		}
	}

	// Patch the ConfigMap with the new facilitator URL.
	patchJSON, _ := json.Marshal(map[string]any{ //nolint:errchkjson // map[string]any is safe, keys/values are controlled
		"data": map[string]string{
			"pricing.yaml": updated,
		},
	})
	if err := kubectl.RunSilent(kubectlBin, kubeconfig, "patch", "cm", "x402-pricing", "-n", "x402",
		"--type=merge", "-p="+string(patchJSON)); err != nil {
		t.Fatalf("patch x402-pricing ConfigMap: %v", err)
	}

	t.Logf("Patched x402-pricing facilitatorURL → %s", newURL)

	// Register cleanup to restore original ConfigMap.
	t.Cleanup(func() {
		restoreVerifierConfigMap(t, kubectlBin, kubeconfig, originalJSON)
	})

	// Restart verifier and wait for it to log the new URL.
	waitForVerifierRestart(t, kubectlBin, kubeconfig, newURL)
}

// restoreVerifierConfigMap restores the x402-pricing ConfigMap from a JSON snapshot.
func restoreVerifierConfigMap(t *testing.T, kubectlBin, kubeconfig, originalJSON string) {
	t.Helper()

	var cm struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(originalJSON), &cm); err != nil {
		t.Logf("Warning: could not restore x402-pricing ConfigMap: %v", err)
		return
	}

	patchJSON, _ := json.Marshal(map[string]any{ //nolint:errchkjson // map[string]any is safe, keys/values are controlled
		"data": cm.Data,
	})

	if err := kubectl.RunSilent(kubectlBin, kubeconfig, "patch", "cm", "x402-pricing", "-n", "x402",
		"--type=merge", "-p="+string(patchJSON)); err != nil {
		t.Logf("Warning: could not restore x402-pricing ConfigMap: %v", err)
	} else {
		t.Log("Restored original x402-pricing ConfigMap")
	}
}

// waitForVerifierRestart restarts the x402-verifier deployment and waits
// for the rollout to complete (pod passes /readyz) and the new pod to log
// the expected facilitator URL.
func waitForVerifierRestart(t *testing.T, kubectlBin, kubeconfig, expectedURL string) {
	t.Helper()

	// Force restart so the verifier picks up the new ConfigMap immediately.
	if err := kubectl.RunSilent(kubectlBin, kubeconfig, "rollout", "restart",
		"deploy/x402-verifier", "-n", "x402"); err != nil {
		t.Fatalf("rollout restart x402-verifier: %v", err)
	}

	// Wait for rollout to complete — this blocks until the new pod passes its
	// /readyz probe, which returns 200 only after pricing config is loaded.
	if err := kubectl.RunSilent(kubectlBin, kubeconfig, "rollout", "status",
		"deploy/x402-verifier", "-n", "x402", "--timeout=60s"); err != nil {
		t.Logf("Warning: rollout status timed out: %v (continuing anyway)", err)
	}

	// Confirm the new pod has the expected facilitator URL in its logs.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		logs, err := kubectl.Output(kubectlBin, kubeconfig, "logs", "deploy/x402-verifier",
			"-n", "x402", "--tail=10")
		if err == nil && strings.Contains(logs, expectedURL) {
			t.Log("x402-verifier restarted with updated facilitator URL")
			return
		}

		time.Sleep(2 * time.Second)
	}

	t.Log("Warning: did not confirm verifier restart with new URL (continuing anyway)")
}
