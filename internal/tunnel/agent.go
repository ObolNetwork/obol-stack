package tunnel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

const agentDeploymentID = "obol-agent"

// SyncAgentBaseURL patches AGENT_BASE_URL in the obol-agent's values-obol.yaml
// and runs helmfile sync to apply the change. It is a no-op if the obol-agent
// deployment directory does not exist (agent not yet initialized).
func SyncAgentBaseURL(cfg *config.Config, tunnelURL string) error {
	overlayPath := agentOverlayPath(cfg)
	if _, err := os.Stat(overlayPath); os.IsNotExist(err) {
		return nil // agent not deployed yet — nothing to do
	}

	if err := patchAgentBaseURL(overlayPath, tunnelURL); err != nil {
		return fmt.Errorf("failed to patch values-obol.yaml: %w", err)
	}

	// Run helmfile sync to apply the change to the cluster.
	deploymentDir := filepath.Dir(overlayPath)
	helmfilePath := filepath.Join(deploymentDir, "helmfile.yaml")
	if _, err := os.Stat(helmfilePath); os.IsNotExist(err) {
		// Overlay exists but helmfile.yaml is missing — unusual, skip sync.
		fmt.Printf("⚠ AGENT_BASE_URL updated in values-obol.yaml but helmfile.yaml not found; run 'obol openclaw sync %s' manually.\n", agentDeploymentID)
		return nil
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		fmt.Printf("⚠ AGENT_BASE_URL updated but cluster not running; changes will apply on next 'obol openclaw sync %s'.\n", agentDeploymentID)
		return nil
	}

	helmfileBin := filepath.Join(cfg.BinDir, "helmfile")
	if _, err := os.Stat(helmfileBin); os.IsNotExist(err) {
		fmt.Printf("⚠ helmfile not found at %s; run 'obol openclaw sync %s' manually.\n", helmfileBin, agentDeploymentID)
		return nil
	}

	fmt.Printf("Syncing AGENT_BASE_URL=%s to obol-agent...\n", tunnelURL)
	cmd := exec.Command(helmfileBin, "-f", helmfilePath, "sync")
	cmd.Dir = deploymentDir
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("helmfile sync failed for obol-agent: %w", err)
	}

	fmt.Println("✓ AGENT_BASE_URL synced to obol-agent")

	// Helmfile sync renders the openclaw-config ConfigMap from the chart template,
	// which does not include agents.defaults.heartbeat.  Re-patch the ConfigMap so
	// the heartbeat interval is restored.  OpenClaw hot-reloads the change (~30-60s)
	// — no pod restart is needed.
	patchHeartbeatAfterSync(cfg, deploymentDir)

	return nil
}

// patchHeartbeatAfterSync re-injects agents.defaults.heartbeat into the
// openclaw-config ConfigMap after a helmfile sync reset it.  Mirrors the logic
// in internal/openclaw.patchHeartbeatConfig; kept here to avoid a circular
// import (openclaw imports tunnel).
//
// Non-fatal: prints a warning on failure and continues.
func patchHeartbeatAfterSync(cfg *config.Config, deploymentDir string) {
	// Read heartbeat interval from values-obol.yaml.
	valuesRaw, err := os.ReadFile(filepath.Join(deploymentDir, "values-obol.yaml"))
	if err != nil || !strings.Contains(string(valuesRaw), "heartbeat:") {
		return
	}
	var every, target string
	for _, line := range strings.Split(string(valuesRaw), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "every:") {
			every = strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "every:")), "\"'")
		}
		if strings.HasPrefix(t, "target:") {
			target = strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "target:")), "\"'")
		}
	}
	if every == "" {
		return
	}

	kubectlBin := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	namespace := "openclaw-" + agentDeploymentID
	env := append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	// Read current ConfigMap.
	getCmd := exec.Command(kubectlBin, "get", "configmap", "openclaw-config",
		"-n", namespace, "-o", "jsonpath={.data.openclaw\\.json}")
	getCmd.Env = env
	var outBuf bytes.Buffer
	getCmd.Stdout = &outBuf
	if err := getCmd.Run(); err != nil {
		fmt.Printf("⚠ could not read openclaw-config for heartbeat patch: %v\n", err)
		return
	}

	var cfgJSON map[string]interface{}
	if err := json.Unmarshal(outBuf.Bytes(), &cfgJSON); err != nil {
		fmt.Printf("⚠ could not parse openclaw.json for heartbeat patch: %v\n", err)
		return
	}

	// Inject heartbeat.
	agents, _ := cfgJSON["agents"].(map[string]interface{})
	if agents == nil {
		agents = map[string]interface{}{}
		cfgJSON["agents"] = agents
	}
	defaults, _ := agents["defaults"].(map[string]interface{})
	if defaults == nil {
		defaults = map[string]interface{}{}
		agents["defaults"] = defaults
	}
	hb := map[string]interface{}{"every": every}
	if target != "" {
		hb["target"] = target
	}
	defaults["heartbeat"] = hb

	patched, _ := json.MarshalIndent(cfgJSON, "", "  ")
	applyPayload, _ := json.Marshal(map[string]interface{}{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]interface{}{"name": "openclaw-config", "namespace": namespace},
		"data":     map[string]string{"openclaw.json": string(patched)},
	})

	applyCmd := exec.Command(kubectlBin, "apply", "-f", "-",
		"--server-side", "--field-manager=helm", "--force-conflicts")
	applyCmd.Env = env
	applyCmd.Stdin = bytes.NewReader(applyPayload)
	var applyErr bytes.Buffer
	applyCmd.Stderr = &applyErr
	if err := applyCmd.Run(); err != nil {
		fmt.Printf("⚠ heartbeat patch failed: %v\n%s\n", err, applyErr.String())
		return
	}
	fmt.Printf("✓ Heartbeat config re-applied after sync (every: %s)\n", every)
}

func agentOverlayPath(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "applications", "openclaw", agentDeploymentID, "values-obol.yaml")
}

// patchAgentBaseURL reads values-obol.yaml and ensures the extraEnv list
// contains an AGENT_BASE_URL entry with the given value. If the entry already
// exists it is updated in place; otherwise it is appended after the
// REMOTE_SIGNER_URL entry.
func patchAgentBaseURL(path, tunnelURL string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")

	// Pre-scan: check if AGENT_BASE_URL already exists.
	alreadyPresent := false
	for _, l := range lines {
		if strings.Contains(l, "name: AGENT_BASE_URL") {
			alreadyPresent = true
			break
		}
	}

	inserted := false
	var out []string

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Case 1: AGENT_BASE_URL already present — update its value line.
		if strings.Contains(line, "name: AGENT_BASE_URL") {
			inserted = true
			out = append(out, line)
			// The next line should be the value line — replace it.
			if i+1 < len(lines) && strings.Contains(lines[i+1], "value:") {
				i++
				out = append(out, fmt.Sprintf("    value: %s", tunnelURL))
			}
			continue
		}

		out = append(out, line)

		// Case 2: AGENT_BASE_URL not yet in the file — insert after REMOTE_SIGNER_URL.
		if !alreadyPresent && !inserted && strings.Contains(line, "value: http://remote-signer:9000") {
			out = append(out,
				"  - name: AGENT_BASE_URL",
				fmt.Sprintf("    value: %s", tunnelURL),
			)
			inserted = true
		}
	}

	// Case 3: Neither AGENT_BASE_URL nor REMOTE_SIGNER_URL found (unusual).
	if !inserted {
		out = append(out, "extraEnv:", fmt.Sprintf("  - name: AGENT_BASE_URL\n    value: %s", tunnelURL))
	}

	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0644)
}
