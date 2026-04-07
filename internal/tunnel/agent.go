package tunnel

import (
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

	if currentURL, _ := readCurrentAgentBaseURL(overlayPath); currentURL == tunnelURL {
		fmt.Printf("✓ AGENT_BASE_URL already set to %s — skipping sync\n", tunnelURL)
		return nil
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

	return nil
}

func agentOverlayPath(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "applications", "openclaw", agentDeploymentID, "values-obol.yaml")
}

func readCurrentAgentBaseURL(overlayPath string) (string, error) {
	data, err := os.ReadFile(overlayPath)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.Contains(line, "name: AGENT_BASE_URL") {
			if i+1 < len(lines) && strings.Contains(lines[i+1], "value:") {
				return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i+1]), "value:")), nil
			}
		}
	}
	return "", nil
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

				out = append(out, "    value: "+tunnelURL)
			}

			continue
		}

		out = append(out, line)

		// Case 2: AGENT_BASE_URL not yet in the file — insert after REMOTE_SIGNER_URL.
		if !alreadyPresent && !inserted && strings.Contains(line, "value: http://remote-signer:9000") {
			out = append(out,
				"  - name: AGENT_BASE_URL",
				"    value: "+tunnelURL,
			)
			inserted = true
		}
	}

	// Case 3: Neither AGENT_BASE_URL nor REMOTE_SIGNER_URL found (unusual).
	if !inserted {
		out = append(out, "extraEnv:", "  - name: AGENT_BASE_URL\n    value: "+tunnelURL)
	}

	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o600) //nolint:gosec // G703: path from user's local config dir
}
