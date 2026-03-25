package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// DefaultInstanceID is the canonical OpenClaw instance that runs both
// user-facing inference and agent-mode monetize/heartbeat reconciliation.
const DefaultInstanceID = "obol-agent"

// Init patches the default OpenClaw instance with agent capabilities:
// monetize RBAC bindings and HEARTBEAT.md for periodic reconciliation.
// The actual OpenClaw deployment is created by openclaw.SetupDefault()
// during `obol stack up`; Init() adds the agent superpowers on top.
func Init(cfg *config.Config, u *ui.UI) error {
	// Patch ClusterRoleBinding to add the default instance's ServiceAccount.
	if err := patchMonetizeBinding(cfg, u); err != nil {
		return fmt.Errorf("failed to patch ClusterRoleBinding: %w", err)
	}

	// Inject HEARTBEAT.md for periodic reconciliation.
	if err := injectHeartbeatFile(cfg, u); err != nil {
		return fmt.Errorf("failed to inject HEARTBEAT.md: %w", err)
	}

	u.Success("Agent capabilities applied to default OpenClaw instance")

	return nil
}

// patchMonetizeBinding adds the default OpenClaw instance's ServiceAccount
// as a subject on the monetize ClusterRoleBindings and x402 RoleBinding.
//
//	ClusterRoleBindings patched:
//	  openclaw-monetize-read-binding      (cluster-wide read)
//	  openclaw-monetize-workload-binding  (cluster-wide mutate)
//	RoleBindings patched:
//	  openclaw-x402-pricing-binding       (x402 namespace, pricing ConfigMap)
func patchMonetizeBinding(cfg *config.Config, u *ui.UI) error {
	namespace := "openclaw-" + DefaultInstanceID

	subject := []map[string]any{
		{
			"kind":      "ServiceAccount",
			"name":      "openclaw",
			"namespace": namespace,
		},
	}

	patch := []map[string]any{
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
	patchArg := "-p=" + string(patchData)

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

// injectHeartbeatFile writes HEARTBEAT.md to the default instance's workspace
// so OpenClaw runs monetize.py reconciliation on every heartbeat cycle.
// OpenClaw reads HEARTBEAT.md from the agent workspace directory
// (resolveAgentWorkspaceDir → /data/.openclaw/workspace/HEARTBEAT.md),
// NOT the root .openclaw directory.
func injectHeartbeatFile(cfg *config.Config, u *ui.UI) error {
	namespace := "openclaw-" + DefaultInstanceID
	heartbeatDir := filepath.Join(cfg.DataDir, namespace, "openclaw-data", ".openclaw", "workspace")

	if err := os.MkdirAll(heartbeatDir, 0o755); err != nil {
		return fmt.Errorf("failed to create heartbeat directory: %w", err)
	}

	content := `Run this single command, then reply with ONLY its output (no commentary):
python3 /data/.openclaw/skills/sell/scripts/monetize.py process --all --quick
`

	heartbeatPath := filepath.Join(heartbeatDir, "HEARTBEAT.md")
	if err := os.WriteFile(heartbeatPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("failed to write HEARTBEAT.md: %w", err)
	}

	u.Successf("HEARTBEAT.md injected at %s", heartbeatPath)

	return nil
}
