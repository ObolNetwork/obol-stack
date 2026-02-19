package main

import (
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
	"github.com/urfave/cli/v2"
)

// bootstrapCommand creates a hidden command that initializes and starts the stack,
// waits for readiness, and opens the browser
func bootstrapCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:   "bootstrap",
		Usage:  "Initialize, start cluster, and open browser (hidden command for installer)",
		Hidden: true, // Hidden from help output
		Action: func(c *cli.Context) error {
			fmt.Println("Starting bootstrap process...")

			// Step 1: Initialize stack
			// Respect an existing backend choice (e.g., k3s) so bootstrap
			// doesn't silently switch back to k3d.
			backendName := stack.DetectExistingBackend(cfg)
			fmt.Println("Initializing stack configuration...")
			if err := stack.Init(cfg, false, backendName); err != nil {
				// Check if it's an "already exists" error - that's okay
				if !strings.Contains(err.Error(), "already exists") {
					return fmt.Errorf("bootstrap init failed: %w", err)
				}
				fmt.Println("Stack already initialized, continuing...")
			}
			fmt.Println("Stack initialized")

			// Step 2: Start stack
			fmt.Println("Starting Obol Stack...")
			if err := stack.Up(cfg); err != nil {
				return fmt.Errorf("bootstrap up failed: %w", err)
			}
			fmt.Println("Stack started")

			// Step 3: Wait for cluster readiness
			fmt.Println("Waiting for cluster to be ready...")
			if err := waitForClusterReady(cfg); err != nil {
				return fmt.Errorf("cluster readiness check failed: %w", err)
			}
			fmt.Println("Cluster is ready")

			// Step 4: Open browser
			url := "http://obol.stack"
			fmt.Printf("Opening browser to %s...\n", url)
			if err := openBrowser(url); err != nil {
				fmt.Printf("Failed to open browser automatically: %v\n", err)
				fmt.Printf("Please open your browser manually to: %s\n", url)
			} else {
				fmt.Printf("Browser opened to %s\n", url)
			}

			fmt.Println()
			fmt.Println("Bootstrap complete! Your Obol Stack is ready.")
			fmt.Println()
			fmt.Println("Next steps:")
			fmt.Println("  • View cluster: obol kubectl get pods --all-namespaces")
			fmt.Println("  • Manage cluster: obol k9s")
			fmt.Println("  • Stop cluster: obol stack down")
			fmt.Println()

			return nil
		},
	}
}

// waitForClusterReady polls the cluster until all critical pods are running
// and the nginx ingress is responding
func waitForClusterReady(cfg *config.Config) error {
	timeout := 20 * time.Minute
	pollInterval := 3 * time.Second
	deadline := time.Now().Add(timeout)

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")

	// Wait for kubeconfig to exist
	fmt.Println("Waiting for kubeconfig...")
	for time.Now().Before(deadline) {
		if _, err := os.Stat(kubeconfigPath); err == nil {
			break
		}
		time.Sleep(pollInterval)
	}

	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("kubeconfig not created within timeout")
	}

	// Wait for pods to be ready
	fmt.Println("Waiting for pods to be ready...")
	podsReady := false
	for time.Now().Before(deadline) {
		// Check if all pods in kube-system and default are running/completed
		cmd := exec.Command(kubectlPath, "get", "pods", "--all-namespaces", "-o", "jsonpath={.items[*].status.phase}")
		cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))

		output, err := cmd.Output()
		if err != nil {
			// kubectl might not be ready yet, continue polling
			time.Sleep(pollInterval)
			continue
		}

		// Check that all pods are Running or Succeeded
		phases := strings.Fields(string(output))
		allReady := true
		for _, phase := range phases {
			if phase != "Running" && phase != "Succeeded" {
				allReady = false
				break
			}
		}

		if allReady && len(phases) > 0 {
			podsReady = true
			break
		}

		time.Sleep(pollInterval)
	}

	if !podsReady {
		return fmt.Errorf("pods did not become ready within timeout")
	}

	fmt.Println("All pods are ready")

	// Wait for nginx ingress to respond
	fmt.Println("Waiting for ingress to respond...")
	ingressURL := "http://obol.stack:8080"
	ingressReady := false

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	for time.Now().Before(deadline) {
		resp, err := client.Get(ingressURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				// Any non-500 response means nginx is up (404/200/etc all fine)
				ingressReady = true
				break
			}
		}
		time.Sleep(pollInterval)
	}

	if !ingressReady {
		return fmt.Errorf("ingress did not respond within timeout")
	}

	fmt.Println("Ingress is responding")

	return nil
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
