package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

	// Ensure the openclaw-config ConfigMap has heartbeat config and that the
	// pod is running with it loaded.  This is needed both for fresh clusters
	// (where doSync ran before the pod started, so the patch didn't take
	// effect) and for "already running" clusters where doSync was never called
	// this session.  ensureHeartbeatActive is idempotent: if heartbeat is
	// already in the ConfigMap and the pod is healthy, it does nothing.
	if err := ensureHeartbeatActive(cfg, u); err != nil {
		// Non-fatal: log and continue.  The heartbeat may still work if the
		// ConfigMap was already correct from a previous run.
		u.Warnf("could not ensure heartbeat config: %v", err)
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
	namespace := fmt.Sprintf("openclaw-%s", DefaultInstanceID)

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

// injectHeartbeatFile writes HEARTBEAT.md to the default instance's workspace
// so OpenClaw runs monetize.py reconciliation on every heartbeat cycle.
// OpenClaw reads HEARTBEAT.md from the agent workspace directory
// (resolveAgentWorkspaceDir → /data/.openclaw/workspace/HEARTBEAT.md),
// NOT the root .openclaw directory.
func injectHeartbeatFile(cfg *config.Config, u *ui.UI) error {
	namespace := fmt.Sprintf("openclaw-%s", DefaultInstanceID)
	heartbeatDir := filepath.Join(cfg.DataDir, namespace, "openclaw-data", ".openclaw", "workspace")

	if err := os.MkdirAll(heartbeatDir, 0755); err != nil {
		return fmt.Errorf("failed to create heartbeat directory: %w", err)
	}

	content := `Run this single command, then reply with ONLY its output (no commentary):
python3 /data/.openclaw/skills/sell/scripts/monetize.py process --all --quick
`

	heartbeatPath := filepath.Join(heartbeatDir, "HEARTBEAT.md")
	if err := os.WriteFile(heartbeatPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write HEARTBEAT.md: %w", err)
	}

	u.Successf("HEARTBEAT.md injected at %s", heartbeatPath)
	return nil
}

// ensureHeartbeatActive guarantees that:
//  1. The openclaw-config ConfigMap contains agents.defaults.heartbeat (every: 5m).
//  2. The openclaw pod is restarted when the ConfigMap was missing the field,
//     so the heartbeat scheduler is activated on the next pod startup.
//
// Idempotent: if heartbeat is already present and the pod is healthy, no
// restart is performed.
func ensureHeartbeatActive(cfg *config.Config, u *ui.UI) error {
	namespace := fmt.Sprintf("openclaw-%s", DefaultInstanceID)
	kubectlBin := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	env := append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))

	// Read current ConfigMap.
	getCmd := exec.Command(kubectlBin,
		"get", "configmap", "openclaw-config",
		"-n", namespace,
		"-o", "jsonpath={.data.openclaw\\.json}")
	getCmd.Env = env
	var outBuf bytes.Buffer
	getCmd.Stdout = &outBuf
	if err := getCmd.Run(); err != nil {
		return fmt.Errorf("read openclaw-config: %w", err)
	}

	var cfgJSON map[string]interface{}
	if err := json.Unmarshal(outBuf.Bytes(), &cfgJSON); err != nil {
		return fmt.Errorf("parse openclaw.json: %w", err)
	}

	// Check whether heartbeat is already present.
	agents, _ := cfgJSON["agents"].(map[string]interface{})
	defaults, _ := agents["defaults"].(map[string]interface{})
	_, alreadySet := defaults["heartbeat"]
	if alreadySet {
		u.Success("Heartbeat config already active")
		return nil
	}

	// Inject heartbeat.
	if agents == nil {
		agents = map[string]interface{}{}
		cfgJSON["agents"] = agents
	}
	if defaults == nil {
		defaults = map[string]interface{}{}
		agents["defaults"] = defaults
	}
	defaults["heartbeat"] = map[string]interface{}{
		"every":  "5m",
		"target": "none",
	}

	patched, err := json.MarshalIndent(cfgJSON, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal patched config: %w", err)
	}

	applyPayload := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]interface{}{
			"name":      "openclaw-config",
			"namespace": namespace,
		},
		"data": map[string]string{
			"openclaw.json": string(patched),
		},
	}
	applyRaw, _ := json.Marshal(applyPayload)

	applyCmd := exec.Command(kubectlBin,
		"apply", "-f", "-",
		"--server-side", "--field-manager=helm", "--force-conflicts")
	applyCmd.Env = env
	applyCmd.Stdin = bytes.NewReader(applyRaw)
	var applyErr bytes.Buffer
	applyCmd.Stderr = &applyErr
	if err := applyCmd.Run(); err != nil {
		return fmt.Errorf("patch heartbeat config: %w\n%s", err, applyErr.String())
	}

	// OpenClaw watches for ConfigMap file changes and hot-reloads config.
	// No pod restart is needed: the running pod will detect the update within
	// ~30-60s and apply [reload] config hot reload, switching the heartbeat
	// interval to 5m immediately without losing the running pod or its state.
	u.Success("Heartbeat config injected — OpenClaw hot reload will activate it (every 5m)")
	return nil
}
