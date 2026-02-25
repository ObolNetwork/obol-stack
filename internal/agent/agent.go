package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
)

const agentID = "obol-agent"

// Init sets up the singleton obol-agent OpenClaw instance.
// It enforces a single agent by using a fixed deployment ID.
// After onboarding, it patches the openclaw-monetize ClusterRoleBinding
// to grant the agent's ServiceAccount monetization permissions,
// and injects HEARTBEAT.md to drive periodic reconciliation.
func Init(cfg *config.Config) error {
	// Check if obol-agent already exists.
	instances, err := openclaw.ListInstanceIDs(cfg)
	if err != nil {
		return fmt.Errorf("failed to list OpenClaw instances: %w", err)
	}

	exists := false
	for _, id := range instances {
		if id == agentID {
			exists = true
			break
		}
	}

	opts := openclaw.OnboardOptions{
		ID:          agentID,
		Sync:        true,
		Interactive: true,
		AgentMode:   true,
	}

	if exists {
		fmt.Println("obol-agent already exists, re-syncing...")
		opts.Force = true
	}

	if err := openclaw.Onboard(cfg, opts); err != nil {
		return fmt.Errorf("failed to onboard obol-agent: %w", err)
	}

	// Patch ClusterRoleBinding to add the agent's ServiceAccount.
	if err := patchMonetizeBinding(cfg); err != nil {
		fmt.Printf("Warning: could not patch ClusterRoleBinding: %v\n", err)
	}

	// Inject HEARTBEAT.md for periodic reconciliation.
	if err := injectHeartbeatFile(cfg); err != nil {
		fmt.Printf("Warning: could not inject HEARTBEAT.md: %v\n", err)
	}

	return nil
}

// patchMonetizeBinding adds the obol-agent's OpenClaw ServiceAccount
// as a subject on the openclaw-monetize-binding ClusterRoleBinding.
func patchMonetizeBinding(cfg *config.Config) error {
	namespace := fmt.Sprintf("openclaw-%s", agentID)

	patch := []map[string]interface{}{
		{
			"op":   "replace",
			"path": "/subjects",
			"value": []map[string]interface{}{
				{
					"kind":      "ServiceAccount",
					"name":      "openclaw",
					"namespace": namespace,
				},
			},
		},
	}

	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	cmd := exec.Command(kubectlBinary,
		"patch", "clusterrolebinding", "openclaw-monetize-binding",
		"--type=json",
		fmt.Sprintf("-p=%s", string(patchData)),
	)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%v: %s", err, stderr.String())
	}

	fmt.Printf("✓ ClusterRoleBinding openclaw-monetize-binding patched (SA: openclaw in %s)\n", namespace)
	return nil
}

// injectHeartbeatFile writes HEARTBEAT.md to the obol-agent's PVC path
// so OpenClaw runs monetize.py reconciliation on every heartbeat cycle.
func injectHeartbeatFile(cfg *config.Config) error {
	namespace := fmt.Sprintf("openclaw-%s", agentID)
	heartbeatDir := filepath.Join(cfg.DataDir, namespace, "openclaw-data", ".openclaw")

	if err := os.MkdirAll(heartbeatDir, 0755); err != nil {
		return fmt.Errorf("failed to create heartbeat directory: %w", err)
	}

	content := `Run: python3 /data/.openclaw/skills/monetize/scripts/monetize.py process --all
Report HEARTBEAT_OK if no pending offers. Otherwise report what was processed.
`

	heartbeatPath := filepath.Join(heartbeatDir, "HEARTBEAT.md")
	if err := os.WriteFile(heartbeatPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write HEARTBEAT.md: %w", err)
	}

	fmt.Printf("✓ HEARTBEAT.md injected at %s\n", heartbeatPath)
	return nil
}
