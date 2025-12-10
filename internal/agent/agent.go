package agent

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/stack"
)

const (
	kubeconfigFile = "kubeconfig.yaml"
)

// Init initializes the Obol Agent with required secrets
func Init(cfg *config.Config, agentAPIKey string) error {
	kubeconfigPath := filepath.Join(cfg.ConfigDir, kubeconfigFile)

	// Check if kubeconfig exists (stack must be running)
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("stack not running, use 'obol stack up' first")
	}

	// Get stack ID for logging
	stackID := stack.GetStackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack ID not found, run 'obol stack init' first")
	}

	// If no API key provided via flag, try to read from stdin
	if agentAPIKey == "" {
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			// Data is being piped to stdin
			data, err := io.ReadAll(os.Stdin)
			if err == nil {
				agentAPIKey = strings.TrimSpace(string(data))
			}
		}
	}

	// Validate Agent API key was provided
	if agentAPIKey == "" {
		return fmt.Errorf("agent API key required via --agent-api-key flag or AGENT_API_KEY environment variable. Navigate to https://aistudio.google.com/api-keys to create an API key for your Obol Agent")
	}

	fmt.Println("Initializing Obol Agent")
	fmt.Printf("Stack ID: %s\n", stackID)
	fmt.Println("Creating API key secret for Obol Agent")

	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")

	// Create namespace (idempotent)
	nsCmd := exec.Command(kubectlPath, "--kubeconfig", kubeconfigPath, "create", "namespace", "agent", "--dry-run=client", "-o", "yaml")
	nsYAML, err := nsCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to generate namespace manifest: %w", err)
	}

	applyNs := exec.Command(kubectlPath, "--kubeconfig", kubeconfigPath, "apply", "-f", "-")
	applyNs.Stdin = strings.NewReader(string(nsYAML))
	applyNs.Stdout = os.Stdout
	applyNs.Stderr = os.Stderr
	if err := applyNs.Run(); err != nil {
		return fmt.Errorf("failed to create agent namespace: %w", err)
	}

	// Create secret (idempotent)
	secretCmd := exec.Command(kubectlPath, "--kubeconfig", kubeconfigPath, "create", "secret", "generic", "obol-agent-api-key", "--from-literal=AGENT_API_KEY="+agentAPIKey, "--namespace=agent", "--dry-run=client", "-o", "yaml")
	secretYAML, err := secretCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to generate secret manifest: %w", err)
	}

	applySecret := exec.Command(kubectlPath, "--kubeconfig", kubeconfigPath, "apply", "-f", "-")
	applySecret.Stdin = strings.NewReader(string(secretYAML))
	applySecret.Stdout = os.Stdout
	applySecret.Stderr = os.Stderr
	if err := applySecret.Run(); err != nil {
		return fmt.Errorf("failed to create Agent API key secret: %w", err)
	}

	fmt.Println("Agent API key secret created")
	fmt.Println("Obol Agent initialized successfully")

	return nil
}
