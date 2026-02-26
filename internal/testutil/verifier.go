package testutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
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
	currentYAML, err := kubectlExecOutput(kubectlBin, kubeconfig, "get", "cm", "x402-pricing",
		"-n", "x402", "-o", `jsonpath={.data.pricing\.yaml}`)
	if err != nil {
		t.Fatalf("read x402-pricing ConfigMap: %v", err)
	}

	// Save original ConfigMap JSON for restore.
	originalJSON, err := kubectlExecOutput(kubectlBin, kubeconfig, "get", "cm", "x402-pricing",
		"-n", "x402", "-o", "json")
	if err != nil {
		t.Fatalf("read x402-pricing ConfigMap (json): %v", err)
	}

	// Replace the facilitatorURL in the pricing YAML.
	updated := currentYAML
	for _, line := range strings.Split(currentYAML, "\n") {
		if strings.Contains(line, "facilitatorURL:") {
			updated = strings.Replace(updated, line, fmt.Sprintf(`facilitatorURL: "%s"`, newURL), 1)
			break
		}
	}

	// Patch the ConfigMap with the new facilitator URL.
	patchJSON, _ := json.Marshal(map[string]any{
		"data": map[string]string{
			"pricing.yaml": updated,
		},
	})
	if err := kubectlExecRun(kubectlBin, kubeconfig, "patch", "cm", "x402-pricing", "-n", "x402",
		"--type=merge", fmt.Sprintf("-p=%s", string(patchJSON))); err != nil {
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
	var cm struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(originalJSON), &cm); err != nil {
		t.Logf("Warning: could not restore x402-pricing ConfigMap: %v", err)
		return
	}

	patchJSON, _ := json.Marshal(map[string]any{
		"data": cm.Data,
	})

	if err := kubectlExecRun(kubectlBin, kubeconfig, "patch", "cm", "x402-pricing", "-n", "x402",
		"--type=merge", fmt.Sprintf("-p=%s", string(patchJSON))); err != nil {
		t.Logf("Warning: could not restore x402-pricing ConfigMap: %v", err)
	} else {
		t.Log("Restored original x402-pricing ConfigMap")
	}
}

// waitForVerifierRestart restarts the x402-verifier deployment and waits
// for the new pod to start with the expected facilitator URL in its logs.
func waitForVerifierRestart(t *testing.T, kubectlBin, kubeconfig, expectedURL string) {
	t.Helper()

	// Force restart so the verifier picks up the new ConfigMap immediately.
	if err := kubectlExecRun(kubectlBin, kubeconfig, "rollout", "restart",
		"deploy/x402-verifier", "-n", "x402"); err != nil {
		t.Fatalf("rollout restart x402-verifier: %v", err)
	}

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		logs, err := kubectlExecOutput(kubectlBin, kubeconfig, "logs", "deploy/x402-verifier",
			"-n", "x402", "--tail=10")
		if err == nil && strings.Contains(logs, expectedURL) {
			t.Log("x402-verifier restarted with updated facilitator URL")
			return
		}
		time.Sleep(3 * time.Second)
	}
	t.Log("Warning: did not confirm verifier restart with new URL (continuing anyway)")
}

// kubectlExecRun runs a kubectl command, returning an error if it fails.
func kubectlExecRun(binary, kubeconfig string, args ...string) error {
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfig))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return fmt.Errorf("%w: %s", err, errMsg)
		}
		return err
	}
	return nil
}

// kubectlExecOutput runs a kubectl command and returns its stdout.
func kubectlExecOutput(binary, kubeconfig string, args ...string) (string, error) {
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfig))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return "", fmt.Errorf("%w: %s", err, errMsg)
		}
		return "", err
	}
	return stdout.String(), nil
}
