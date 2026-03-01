package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

const agentID = "obol-agent"

// Init sets up the singleton obol-agent OpenClaw instance.
// It enforces a single agent by using a fixed deployment ID.
// After onboarding, it patches the monetize RBAC bindings
// to grant the agent's ServiceAccount monetization permissions,
// and injects HEARTBEAT.md to drive periodic reconciliation.
func Init(cfg *config.Config, u *ui.UI) error {
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
		u.Warn("obol-agent already exists, re-syncing...")
		opts.Force = true
	}

	if err := openclaw.Onboard(cfg, opts, u); err != nil {
		return fmt.Errorf("failed to onboard obol-agent: %w", err)
	}

	// Patch ClusterRoleBinding to add the agent's ServiceAccount.
	if err := patchMonetizeBinding(cfg, u); err != nil {
		return fmt.Errorf("failed to patch ClusterRoleBinding: %w", err)
	}

	// Inject HEARTBEAT.md for periodic reconciliation.
	if err := injectHeartbeatFile(cfg, u); err != nil {
		return fmt.Errorf("failed to inject HEARTBEAT.md: %w", err)
	}

	return nil
}

// patchMonetizeBinding adds the obol-agent's OpenClaw ServiceAccount
// as a subject on the monetize ClusterRoleBindings and x402 RoleBinding.
//
//	ClusterRoleBindings patched:
//	  openclaw-monetize-read-binding      (cluster-wide read)
//	  openclaw-monetize-workload-binding  (cluster-wide mutate)
//	RoleBindings patched:
//	  openclaw-x402-pricing-binding       (x402 namespace, pricing ConfigMap)
func patchMonetizeBinding(cfg *config.Config, u *ui.UI) error {
	namespace := fmt.Sprintf("openclaw-%s", agentID)

	subject := []map[string]interface{}{
		{
			"kind":      "ServiceAccount",
			"name":      "openclaw",
			"namespace": namespace,
		},
	}

	patch := []map[string]interface{}{
		{
			"op":    "replace",
			"path":  "/subjects",
			"value": subject,
		},
	}

	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	bin, kc := kubectl.Paths(cfg)
	patchArg := fmt.Sprintf("-p=%s", string(patchData))

	// Patch both ClusterRoleBindings.
	clusterBindings := []string{
		"openclaw-monetize-read-binding",
		"openclaw-monetize-workload-binding",
	}
	for _, name := range clusterBindings {
		if err := kubectl.RunSilent(bin, kc,
			"patch", "clusterrolebinding", name,
			"--type=json", patchArg,
		); err != nil {
			return fmt.Errorf("patch clusterrolebinding %s: %w", name, err)
		}
	}

	// Patch x402 namespace RoleBinding (non-fatal: x402 is lazily deployed
	// on first `obol sell` and may not exist yet).
	if err := kubectl.RunSilent(bin, kc,
		"patch", "rolebinding", "openclaw-x402-pricing-binding",
		"-n", "x402",
		"--type=json", patchArg,
	); err != nil {
		u.Warn("x402 namespace not yet deployed — pricing RBAC will be applied on first 'obol sell'")
	}

	u.Successf("RBAC bindings patched (SA: openclaw in %s)", namespace)
	return nil
}

// injectHeartbeatFile writes HEARTBEAT.md to the obol-agent's workspace path
// so OpenClaw runs monetize.py reconciliation on every heartbeat cycle.
// OpenClaw reads HEARTBEAT.md from the agent workspace directory
// (resolveAgentWorkspaceDir → /data/.openclaw/workspace/HEARTBEAT.md),
// NOT the root .openclaw directory.
func injectHeartbeatFile(cfg *config.Config, u *ui.UI) error {
	namespace := fmt.Sprintf("openclaw-%s", agentID)
	heartbeatDir := filepath.Join(cfg.DataDir, namespace, "openclaw-data", ".openclaw", "workspace")

	if err := os.MkdirAll(heartbeatDir, 0755); err != nil {
		return fmt.Errorf("failed to create heartbeat directory: %w", err)
	}

	content := `Run: python3 /data/.openclaw/skills/sell/scripts/monetize.py process --all
Report HEARTBEAT_OK if no pending offers. Otherwise report what was processed.
`

	heartbeatPath := filepath.Join(heartbeatDir, "HEARTBEAT.md")
	if err := os.WriteFile(heartbeatPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write HEARTBEAT.md: %w", err)
	}

	u.Successf("HEARTBEAT.md injected at %s", heartbeatPath)
	return nil
}
