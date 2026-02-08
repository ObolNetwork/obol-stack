package tunnel

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// Status displays the current tunnel status and URL.
func Status(cfg *config.Config) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath, err := requireRunningStack(cfg)
	if err != nil {
		return err
	}

	st, err := loadTunnelState(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to read tunnel state: %v\n", err)
		st = nil
	}

	// Check pod status first.
	podStatus, err := getPodStatus(kubectlPath, kubeconfigPath)
	if err != nil {
		mode, url := tunnelModeAndURL(st)
		printStatusBox(mode, "not deployed", url, time.Now())
		fmt.Println("\nTroubleshooting:")
		fmt.Println("  - Start the stack: obol stack up")
		return nil
	}

	statusLabel := podStatus
	if podStatus == "running" {
		statusLabel = "active"
	}

	mode, url := tunnelModeAndURL(st)
	if mode == "quick" {
		// Quick tunnels only: try to get URL from logs.
		u, err := GetTunnelURL(cfg)
		if err != nil {
			printStatusBox(mode, podStatus, "(not available)", time.Now())
			fmt.Println("\nTroubleshooting:")
			fmt.Println("  - Check logs: obol tunnel logs")
			fmt.Println("  - Restart tunnel: obol tunnel restart")
			return nil
		}
		url = u
	}

	printStatusBox(mode, statusLabel, url, time.Now())
	fmt.Printf("\nTest with: curl %s/\n", url)

	return nil
}

// GetTunnelURL parses cloudflared logs to extract the quick tunnel URL.
func GetTunnelURL(cfg *config.Config) (string, error) {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := stackKubeconfigPath(cfg)

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

// Restart restarts the cloudflared deployment.
func Restart(cfg *config.Config) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath, err := requireRunningStack(cfg)
	if err != nil {
		return err
	}

	fmt.Println("Restarting cloudflared tunnel...")

	cmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"rollout", "restart", "deployment/cloudflared",
		"-n", tunnelNamespace,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to restart tunnel: %w", err)
	}

	fmt.Println("\nTunnel restarting...")
	fmt.Println("Run 'obol tunnel status' to see the URL once ready (may take 10-30 seconds).")

	return nil
}

// Logs displays cloudflared logs.
func Logs(cfg *config.Config, follow bool) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath, err := requireRunningStack(cfg)
	if err != nil {
		return err
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
func printStatusBox(mode, status, url string, lastUpdated time.Time) {
	fmt.Println()
	fmt.Println("Cloudflare Tunnel Status")
	fmt.Println(strings.Repeat("─", 50))
	fmt.Printf("Mode:         %s\n", mode)
	fmt.Printf("Status:       %s\n", status)
	fmt.Printf("URL:          %s\n", url)
	fmt.Printf("Last Updated: %s\n", lastUpdated.Format(time.RFC3339))
	fmt.Println(strings.Repeat("─", 50))
}

func parseQuickTunnelURL(logs string) (string, bool) {
	// Quick tunnel logs print a random *.trycloudflare.com URL.
	re := regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
	if url := re.FindString(logs); url != "" {
		return url, true
	}
	return "", false
}

func stackKubeconfigPath(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
}

func requireRunningStack(cfg *config.Config) (kubeconfigPath string, err error) {
	kubeconfigPath = stackKubeconfigPath(cfg)
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return "", fmt.Errorf("stack not running, use 'obol stack up' first")
	}
	return kubeconfigPath, nil
}

func kubectlApplyManifest(cfg *config.Config, kubeconfigPath string, manifest []byte) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")

	cmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"apply", "-f", "-",
	)
	cmd.Stdin = bytes.NewReader(manifest)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl apply failed: %w", err)
	}
	return nil
}
