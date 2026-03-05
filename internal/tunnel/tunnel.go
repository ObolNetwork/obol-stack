package tunnel

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

const (
	tunnelNamespace     = "traefik"
	tunnelLabelSelector = "app.kubernetes.io/name=cloudflared"

	// cloudflared-tunnel-token is created by `obol tunnel provision`.
	tunnelTokenSecretName = "cloudflared-tunnel-token"
	tunnelTokenSecretKey  = "TUNNEL_TOKEN"
)

// Status displays the current tunnel status and URL.
func Status(cfg *config.Config, u *ui.UI) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Check if kubeconfig exists.
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("stack not running, use 'obol stack up' first")
	}

	st, _ := loadTunnelState(cfg)

	// Check pod status first.
	podStatus, err := getPodStatus(kubectlPath, kubeconfigPath)
	if err != nil {
		mode, url := tunnelModeAndURL(st)
		printStatusBox(u, mode, "not deployed", url, time.Now())
		u.Blank()
		u.Print("Troubleshooting:")
		u.Print("  - Start the stack: obol stack up")
		return nil
	}

	statusLabel := podStatus
	if podStatus == "running" {
		statusLabel = "active"
	}

	mode, url := tunnelModeAndURL(st)
	if mode == "quick" {
		// Quick tunnels only: try to get URL from logs.
		tunnelURL, err := GetTunnelURL(cfg)
		if err != nil {
			printStatusBox(u, mode, podStatus, "(not available)", time.Now())
			u.Blank()
			u.Print("Troubleshooting:")
			u.Print("  - Check logs: obol tunnel logs")
			u.Print("  - Restart tunnel: obol tunnel restart")
			return nil
		}
		url = tunnelURL
	}

	printStatusBox(u, mode, statusLabel, url, time.Now())
	u.Printf("Test with: curl %s/", url)

	// Auto-inject tunnel URL into obol-agent so registration JSON uses it.
	if url != "" && url != "(not available)" {
		if err := InjectBaseURL(cfg, url); err == nil {
			u.Dim("Agent base URL updated to " + url)
		}
	}

	return nil
}

// GetTunnelURL parses cloudflared logs to extract the quick tunnel URL.
func GetTunnelURL(cfg *config.Config) (string, error) {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	cmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"logs", "-n", tunnelNamespace,
		"-l", tunnelLabelSelector,
		"--tail=100",
	)

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get tunnel logs: %w", err)
	}

	if url, ok := parseQuickTunnelURL(string(output)); ok {
		return url, nil
	}

	// Back-compat: allow cfargotunnel.com to be detected too.
	re := regexp.MustCompile(`https://[a-z0-9-]+\.cfargotunnel\.com`)
	if url := re.FindString(string(output)); url != "" {
		return url, nil
	}

	return "", fmt.Errorf("tunnel URL not found in logs")
}

// InjectBaseURL sets AGENT_BASE_URL on the obol-agent deployment so that
// monetize.py uses the tunnel URL in registration JSON instead of obol.stack:8080.
func InjectBaseURL(cfg *config.Config, tunnelURL string) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	cmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"set", "env", "deployment/openclaw",
		"-n", "openclaw-obol-agent",
		fmt.Sprintf("AGENT_BASE_URL=%s", strings.TrimRight(tunnelURL, "/")),
	)
	return cmd.Run()
}

// Restart restarts the cloudflared deployment.
func Restart(cfg *config.Config, u *ui.UI) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Check if kubeconfig exists.
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("stack not running, use 'obol stack up' first")
	}

	cmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"rollout", "restart", "deployment/cloudflared",
		"-n", tunnelNamespace,
	)
	if err := u.Exec(ui.ExecConfig{
		Name: "Restarting cloudflared tunnel",
		Cmd:  cmd,
	}); err != nil {
		return fmt.Errorf("failed to restart tunnel: %w", err)
	}

	u.Blank()
	u.Print("Tunnel restarting...")
	u.Print("Run 'obol tunnel status' to see the URL once ready (may take 10-30 seconds).")

	return nil
}

// Logs displays cloudflared logs.
func Logs(cfg *config.Config, follow bool) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Check if kubeconfig exists.
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("stack not running, use 'obol stack up' first")
	}

	args := []string{
		"--kubeconfig", kubeconfigPath,
		"logs", "-n", tunnelNamespace,
		"-l", tunnelLabelSelector,
	}

	if follow {
		args = append(args, "-f")
	}

	cmd := exec.Command(kubectlPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	return cmd.Run()
}

// getPodStatus returns the status of the cloudflared pod.
func getPodStatus(kubectlPath, kubeconfigPath string) (string, error) {
	cmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"get", "pods", "-n", tunnelNamespace,
		"-l", tunnelLabelSelector,
		"-o", "jsonpath={.items[0].status.phase}",
	)

	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	status := strings.TrimSpace(string(output))
	if status == "" {
		return "", fmt.Errorf("no pods found")
	}

	return strings.ToLower(status), nil
}

// printStatusBox prints a formatted status box.
func printStatusBox(u *ui.UI, mode, status, url string, lastUpdated time.Time) {
	u.Blank()
	u.Bold("Cloudflare Tunnel Status")
	u.Print(strings.Repeat("─", 50))
	u.Detail("Mode", mode)
	u.Detail("Status", status)
	u.Detail("URL", url)
	u.Detail("Last Updated", lastUpdated.Format(time.RFC3339))
	u.Print(strings.Repeat("─", 50))
}

func parseQuickTunnelURL(logs string) (string, bool) {
	// Quick tunnel logs print a random *.trycloudflare.com URL.
	re := regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
	if url := re.FindString(logs); url != "" {
		return url, true
	}
	return "", false
}
