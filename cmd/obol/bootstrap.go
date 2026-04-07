package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/stack"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/urfave/cli/v3"
)

// bootstrapCommand creates a hidden command that initializes and starts the stack,
// waits for readiness, and opens the browser
func bootstrapCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:   "bootstrap",
		Usage:  "Initialize, start cluster, and open browser (hidden command for installer)",
		Hidden: true,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			u.Info("Starting bootstrap process")

			// Step 1: Initialize stack
			backendName := stack.DetectExistingBackend(cfg)
			if err := stack.Init(cfg, u, false, backendName); err != nil {
				if !strings.Contains(err.Error(), "already exists") {
					return fmt.Errorf("bootstrap init failed: %w", err)
				}

				u.Warn("Stack already initialized, continuing")
			}

			// Step 2: Start stack
			if err := stack.Up(cfg, u, false); err != nil {
				return fmt.Errorf("bootstrap up failed: %w", err)
			}

			// Step 3: Wait for cluster readiness
			if err := waitForClusterReady(cfg, u); err != nil {
				return fmt.Errorf("cluster readiness check failed: %w", err)
			}

			// Step 4: Open browser
			url := "http://obol.stack"
			if resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(url); err != nil {
				url = "http://obol.stack:8080"
			} else {
				resp.Body.Close()
			}
			u.Infof("Opening browser to %s", url)

			if err := openBrowser(url); err != nil {
				u.Warnf("Failed to open browser: %v", err)
				u.Printf("  Please open manually: %s", url)
			}

			u.Blank()
			u.Bold("Bootstrap complete! Your Obol Stack is ready.")
			u.Blank()
			u.Print("Next steps:")
			u.Printf("  • View the stack interface at %s", url)
			u.Print("  • Create an Obol Agent: obol agent init")
			u.Print("  • View what's running from the terminal (press '0'): obol k9s")
			u.Print("  • Shut down the stack: obol stack down")
			u.Blank()

			return nil
		},
	}
}

// waitForClusterReady polls the cluster until all critical pods are running
// and the ingress is responding
func waitForClusterReady(cfg *config.Config, u *ui.UI) error {
	timeout := 20 * time.Minute
	pollInterval := 3 * time.Second
	deadline := time.Now().Add(timeout)

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")

	// Wait for kubeconfig to exist
	err := u.RunWithSpinner("Waiting for kubeconfig", func() error {
		for time.Now().Before(deadline) {
			if _, err := os.Stat(kubeconfigPath); err == nil {
				return nil
			}

			time.Sleep(pollInterval)
		}

		return errors.New("kubeconfig not created within timeout")
	})
	if err != nil {
		return err
	}

	// Wait for pods to be ready
	err = u.RunWithSpinner("Waiting for pods to be ready", func() error {
		for time.Now().Before(deadline) {
			cmd := exec.Command(kubectlPath, "get", "pods", "--all-namespaces", "-o", "jsonpath={.items[*].status.phase}")

			cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

			output, err := cmd.Output()
			if err != nil {
				time.Sleep(pollInterval)
				continue
			}

			phases := strings.Fields(string(output))
			allReady := true

			for _, phase := range phases {
				if phase != "Running" && phase != "Succeeded" {
					allReady = false
					break
				}
			}

			if allReady && len(phases) > 0 {
				return nil
			}

			time.Sleep(pollInterval)
		}

		return errors.New("pods did not become ready within timeout")
	})
	if err != nil {
		return err
	}

	// Wait for ingress to respond
	err = u.RunWithSpinner("Waiting for ingress to respond", func() error {
		ingressURL := "http://obol.stack:8080"

		client := &http.Client{Timeout: 5 * time.Second}
		for time.Now().Before(deadline) {
			resp, err := client.Get(ingressURL)
			if err == nil {
				resp.Body.Close()

				if resp.StatusCode < 500 {
					return nil
				}
			}

			time.Sleep(pollInterval)
		}

		return errors.New("ingress did not respond within timeout")
	})

	return err
}

// openBrowser opens the default browser to the specified URL
func openBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return cmd.Start()
}
