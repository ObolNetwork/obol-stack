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
)

const (
	tunnelNamespace  = "traefik"
	tunnelLabelSelector = "app.kubernetes.io/name=cloudflared"
)

// Status displays the current tunnel status and URL
func Status(cfg *config.Config) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Check if kubeconfig exists
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("stack not running, use 'obol stack up' first")
	}

	// Check pod status first
	podStatus, err := getPodStatus(kubectlPath, kubeconfigPath)
	if err != nil {
		printStatusBox("quick", "not deployed", "", time.Now())
		fmt.Println("\nTroubleshooting:")
		fmt.Println("  - Start the stack: obol stack up")
		return nil
	}

	// Try to get tunnel URL from logs
	url, err := GetTunnelURL(cfg)
	if err != nil {
		printStatusBox("quick", podStatus, "(not available)", time.Now())
		fmt.Println("\nTroubleshooting:")
		fmt.Println("  - Check logs: obol tunnel logs")
		fmt.Println("  - Restart tunnel: obol tunnel restart")
		return nil
	}

	printStatusBox("quick", "active", url, time.Now())
	fmt.Printf("\nTest with: curl %s/\n", url)

	return nil
}

// GetTunnelURL parses cloudflared logs to extract the quick tunnel URL
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

	// Parse URL from logs (quick tunnel uses cfargotunnel.com)
	re := regexp.MustCompile(`https://[a-z0-9-]+\.cfargotunnel\.com`)
	matches := re.FindString(string(output))
	if matches == "" {
		// Also try trycloudflare.com as fallback
		re = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
		matches = re.FindString(string(output))
	}
	if matches == "" {
		return "", fmt.Errorf("tunnel URL not found in logs")
	}

	return matches, nil
}

// Restart restarts the cloudflared deployment to get a new tunnel URL
func Restart(cfg *config.Config) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Check if kubeconfig exists
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("stack not running, use 'obol stack up' first")
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
	fmt.Println("Run 'obol tunnel status' to see the new URL once ready (may take 10-30 seconds).")

	return nil
}

// Logs displays cloudflared logs
func Logs(cfg *config.Config, follow bool) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Check if kubeconfig exists
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

// getPodStatus returns the status of the cloudflared pod
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

// printStatusBox prints a formatted status box
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
