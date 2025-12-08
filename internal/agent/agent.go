package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/executor"
	"github.com/ObolNetwork/obol-stack/internal/logging"
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

	// Create logger and executor
	l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
		StateDir: cfg.StateDir,
		StackID:  stackID,
	})
	defer cleanup()

	exec := executor.New(l.Logger)
	defer exec.Close()

	// Validate Agent API key was provided
	if agentAPIKey == "" {
		l.Error("Agent API key required")
		return fmt.Errorf("agent API key required via --agent-api-key flag or AGENT_API_KEY environment variable. Navigate to https://aistudio.google.com/api-keys to create an API key for your Obol Agent")
	}

	l.Info("Initializing Obol Agent")
	l.Info("Creating API key secret for Obol Agent")

	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")

	// Create namespace (idempotent)
	nsCmd := exec.Command(kubectlPath, "--kubeconfig", kubeconfigPath, "create", "namespace", "agent", "--dry-run=client", "-o", "yaml")
	nsYAML, err := nsCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to generate namespace manifest: %w", err)
	}
	applyNs := exec.CommandWithOutput(kubectlPath, "--kubeconfig", kubeconfigPath, "apply", "-f", "-")
	applyNs.SetStdin(strings.NewReader(string(nsYAML)))
	if err := applyNs.Run(); err != nil {
		return fmt.Errorf("failed to create agent namespace: %w", err)
	}

	// Create secret (idempotent)
	secretCmd := exec.Command(kubectlPath, "--kubeconfig", kubeconfigPath, "create", "secret", "generic", "obol-agent-api-key", "--from-literal=AGENT_API_KEY="+agentAPIKey, "--namespace=agent", "--dry-run=client", "-o", "yaml")
	secretYAML, err := secretCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to generate secret manifest: %w", err)
	}
	applySecret := exec.CommandWithOutput(kubectlPath, "--kubeconfig", kubeconfigPath, "apply", "-f", "-")
	applySecret.SetStdin(strings.NewReader(string(secretYAML)))
	if err := applySecret.Run(); err != nil {
		return fmt.Errorf("failed to create Agent API key secret: %w", err)
	}

	l.Success("Agent API key secret created")
	l.Success("Obol Agent initialized successfully")

	return nil
}
