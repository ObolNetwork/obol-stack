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

	// Patch x402 namespace RoleBinding.
	if err := kubectl.RunSilent(bin, kc,
		"patch", "rolebinding", "openclaw-x402-pricing-binding",
		"-n", "x402",
		"--type=json", patchArg,
	); err != nil {
		return fmt.Errorf("patch rolebinding openclaw-x402-pricing-binding: %w", err)
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

	content := `## Sell-Side: Reconcile ServiceOffers
Run: python3 /data/.openclaw/skills/sell/scripts/monetize.py process --all
Report what was processed. If all offers are Ready, move to the next section.

## Buy-Side: Discover and Buy Inference
You are an autonomous agent managing compute infrastructure. On each heartbeat:

1. DISCOVER: Search for registered agents offering inference services.
   Run: python3 /data/.openclaw/skills/discovery/scripts/discovery.py search --chain base-sepolia --limit 10
   For each discovered agent with x402Support=true, check if you already have that model available.

2. EVALUATE: Compare discovered pricing against your current providers.
   - Check your USDC balance: python3 /data/.openclaw/skills/buy-inference/scripts/buy.py balance
   - Check current purchased providers: python3 /data/.openclaw/skills/buy-inference/scripts/buy.py list
   - Only buy if: the discovered model is useful AND the price is reasonable AND you have sufficient USDC.

3. BUY: If a good deal is found and not already purchased:
   - Probe the endpoint: python3 /data/.openclaw/skills/buy-inference/scripts/buy.py probe <endpoint-url>
   - Buy access: python3 /data/.openclaw/skills/buy-inference/scripts/buy.py buy <name> --endpoint <url> --model <model-id> --count 10

4. REPORT: Summarize what you did — offers reconciled, agents discovered, purchases made, or HEARTBEAT_OK if nothing to do.

IMPORTANT: Do NOT buy on every heartbeat. Only buy when you discover a NEW agent you haven't purchased from yet.
If discovery fails (e.g. network error), that's OK — just report the error and move on.
`

	heartbeatPath := filepath.Join(heartbeatDir, "HEARTBEAT.md")
	if err := os.WriteFile(heartbeatPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write HEARTBEAT.md: %w", err)
	}

	u.Successf("HEARTBEAT.md injected at %s", heartbeatPath)
	return nil
}
