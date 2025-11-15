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
func Init(cfg *config.Config, googleAPIKey string) error {
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

	// Validate Google API key was provided
	if googleAPIKey == "" {
		l.Error("Google API key required")
		return fmt.Errorf("Google API key required via --google-api-key flag or GOOGLE_API_KEY environment variable")
	}

	l.Info("Initializing Obol Agent")
	l.Info("Creating Google API key secret for Obol Agent")

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
	secretCmd := exec.Command(kubectlPath, "--kubeconfig", kubeconfigPath, "create", "secret", "generic", "obol-agent-google-api-key", "--from-literal=GOOGLE_API_KEY="+googleAPIKey, "--namespace=agent", "--dry-run=client", "-o", "yaml")
	secretYAML, err := secretCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to generate secret manifest: %w", err)
	}
	applySecret := exec.CommandWithOutput(kubectlPath, "--kubeconfig", kubeconfigPath, "apply", "-f", "-")
	applySecret.SetStdin(strings.NewReader(string(secretYAML)))
	if err := applySecret.Run(); err != nil {
		return fmt.Errorf("failed to create Google API key secret: %w", err)
	}

	l.Success("Google API key secret created")
	l.Success("Obol Agent initialized successfully")
	l.Info("The Obol Agent deployment will now have access to Google API services")

	return nil
}
