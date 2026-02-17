package openclaw

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/llm"
	petname "github.com/dustinkirkland/golang-petname"
)

// CloudProviderInfo holds the cloud provider selection from interactive setup.
// This is used to configure llmspy with the API key separately from the
// OpenClaw overlay (which routes through llmspy).
type CloudProviderInfo struct {
	Name    string // "anthropic" or "openai"
	APIKey  string
	ModelID string // e.g. "claude-sonnet-4-5-20250929"
	Display string // e.g. "Claude Sonnet 4.5"
}

const (
	appName                 = "openclaw"
	defaultDomain           = "obol.stack"
	userSecretsFileName     = "values-obol.secrets.json"
	userSecretsK8sSecretRef = "openclaw-user-secrets"
	// chartVersion pins the openclaw Helm chart version from the obol repo.
	// renovate: datasource=helm depName=openclaw registryUrl=https://obolnetwork.github.io/helm-charts/
	chartVersion = "0.1.0"
)

// OnboardOptions contains options for the onboard command
type OnboardOptions struct {
	ID          string // Deployment ID (empty = generate petname)
	Force       bool   // Overwrite existing deployment
	Sync        bool   // Also run helmfile sync after install
	Interactive bool   // true = prompt for provider choice; false = silent defaults
	IsDefault   bool   // true = use fixed ID "default", idempotent on re-run
}

// SetupDefault deploys a default OpenClaw instance as part of stack setup.
// It is idempotent: if a "default" deployment already exists, it re-syncs.
// When Ollama is not detected on the host and no existing ~/.openclaw config
// is found, it skips provider setup gracefully so the user can configure
// later with `obol openclaw setup`.
func SetupDefault(cfg *config.Config) error {
	// Check whether the default deployment already exists (re-sync path).
	// If it does, proceed unconditionally — the overlay was already written.
	deploymentDir := deploymentPath(cfg, "default")
	if _, err := os.Stat(deploymentDir); err == nil {
		// Existing deployment — always re-sync regardless of Ollama status.
		return Onboard(cfg, OnboardOptions{
			ID:        "default",
			Sync:      true,
			IsDefault: true,
		})
	}

	// Check if there is an existing ~/.openclaw config with providers
	imported, importErr := DetectExistingConfig()
	if importErr != nil {
		fmt.Printf("  Warning: could not read existing config: %v\n", importErr)
	}
	hasImportedProviders := imported != nil && len(imported.Providers) > 0

	// If no imported providers, check Ollama availability for the default overlay
	if !hasImportedProviders {
		ollamaAvailable := detectOllama()
		if ollamaAvailable {
			fmt.Printf("  ✓ Ollama detected at %s\n", ollamaEndpoint())
		} else {
			fmt.Printf("  ⚠ Ollama not detected on host (%s)\n", ollamaEndpoint())
			fmt.Println("  Skipping default OpenClaw provider setup.")
			fmt.Println("  Run 'obol openclaw setup default' to configure a provider later.")
			return nil
		}
	}

	return Onboard(cfg, OnboardOptions{
		ID:        "default",
		Sync:      true,
		IsDefault: true,
	})
}

// Onboard creates and optionally deploys an OpenClaw instance
func Onboard(cfg *config.Config, opts OnboardOptions) error {
	id := opts.ID
	if opts.IsDefault {
		id = "default"
	}
	if id == "" {
		id = petname.Generate(2, "-")
		fmt.Printf("Generated deployment ID: %s\n", id)
	} else {
		fmt.Printf("Using deployment ID: %s\n", id)
	}

	deploymentDir := deploymentPath(cfg, id)

	// Idempotent re-run for default deployment: just re-sync
	if opts.IsDefault && !opts.Force {
		if _, err := os.Stat(deploymentDir); err == nil {
			fmt.Println("Default OpenClaw instance already configured, re-syncing...")
			if opts.Sync {
				if err := doSync(cfg, id); err != nil {
					return err
				}
				// Import workspace on re-sync too
				imported, importErr := DetectExistingConfig()
				if importErr != nil {
					fmt.Printf("Warning: could not read existing config: %v\n", importErr)
				}
				if imported != nil && imported.WorkspaceDir != "" {
					copyWorkspaceToPod(cfg, id, imported.WorkspaceDir)
				}
				return nil
			}
			return nil
		}
	}

	if _, err := os.Stat(deploymentDir); err == nil {
		if !opts.Force && !opts.IsDefault {
			return fmt.Errorf("deployment already exists: %s/%s\n"+
				"Directory: %s\n"+
				"Use --force or -f to overwrite", appName, id, deploymentDir)
		}
		fmt.Printf("WARNING: Overwriting existing deployment at %s\n", deploymentDir)
	}

	// Detect existing ~/.openclaw config
	imported, err := DetectExistingConfig()
	if err != nil {
		fmt.Printf("Warning: failed to read existing config: %v\n", err)
	}
	if imported != nil {
		PrintImportSummary(imported)
	}

	// Interactive setup: auto-skip prompts when existing config has providers
	if opts.Interactive {
		if imported != nil && len(imported.Providers) > 0 {
			fmt.Println("\nUsing detected configuration from ~/.openclaw/")
		} else {
			var cloudProvider *CloudProviderInfo
			imported, cloudProvider, err = interactiveSetup(imported)
			if err != nil {
				return fmt.Errorf("interactive setup failed: %w", err)
			}
			// Push cloud API key to llmspy if a cloud provider was selected
			if cloudProvider != nil {
				if llmErr := llm.ConfigureLLMSpy(cfg, cloudProvider.Name, cloudProvider.APIKey); llmErr != nil {
					return fmt.Errorf("failed to configure llmspy: %w", llmErr)
				}
			}
		}
	}

	if err := os.MkdirAll(deploymentDir, 0755); err != nil {
		return fmt.Errorf("failed to create deployment directory: %w", err)
	}

	// Write Obol Stack overlay values (httpRoute, provider config, eRPC, skills)
	hostname := fmt.Sprintf("openclaw-%s.%s", id, defaultDomain)
	namespace := fmt.Sprintf("%s-%s", appName, id)
	secretData := collectSensitiveData(imported)
	if err := writeUserSecretsFile(deploymentDir, secretData); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to write OpenClaw secrets metadata: %w", err)
	}
	overlay := generateOverlayValues(hostname, imported, len(secretData) > 0)
	if err := os.WriteFile(filepath.Join(deploymentDir, "values-obol.yaml"), []byte(overlay), 0644); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to write overlay values: %w", err)
	}

	// Generate helmfile.yaml referencing obol/openclaw from the published Helm repo
	helmfileContent := generateHelmfile(id, namespace)
	if err := os.WriteFile(filepath.Join(deploymentDir, "helmfile.yaml"), []byte(helmfileContent), 0644); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to write helmfile.yaml: %w", err)
	}

	fmt.Printf("\n✓ OpenClaw instance configured!\n")
	fmt.Printf("  Deployment: %s/%s\n", appName, id)
	fmt.Printf("  Namespace:  %s\n", namespace)
	fmt.Printf("  Hostname:   %s\n", hostname)
	fmt.Printf("  Location:   %s\n", deploymentDir)
	fmt.Printf("\nFiles created:\n")
	fmt.Printf("  - values-obol.yaml  Obol Stack overlay (httpRoute, providers, eRPC)\n")
	fmt.Printf("  - helmfile.yaml     Deployment configuration (chart: obol/openclaw v%s)\n", chartVersion)
	if len(secretData) > 0 {
		fmt.Printf("  - %s  Local secret values (used to create %s in-cluster)\n", userSecretsFileName, userSecretsK8sSecretRef)
	}

	if opts.Sync {
		fmt.Printf("\nDeploying to cluster...\n\n")
		if err := doSync(cfg, id); err != nil {
			return err
		}
		// Copy workspace files into the pod after sync succeeds
		if imported != nil && imported.WorkspaceDir != "" {
			copyWorkspaceToPod(cfg, id, imported.WorkspaceDir)
		}
		return nil
	}

	fmt.Printf("\nTo deploy: obol openclaw sync %s\n", id)
	return nil
}

// Sync deploys or updates an OpenClaw instance
func Sync(cfg *config.Config, id string) error {
	return doSync(cfg, id)
}

func doSync(cfg *config.Config, id string) error {
	deploymentDir := deploymentPath(cfg, id)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s/%s\nDirectory: %s", appName, id, deploymentDir)
	}

	helmfilePath := filepath.Join(deploymentDir, "helmfile.yaml")
	if _, err := os.Stat(helmfilePath); os.IsNotExist(err) {
		return fmt.Errorf("helmfile.yaml not found in: %s", deploymentDir)
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	helmfileBinary := filepath.Join(cfg.BinDir, "helmfile")
	if _, err := os.Stat(helmfileBinary); os.IsNotExist(err) {
		return fmt.Errorf("helmfile not found at %s", helmfileBinary)
	}
	namespace := fmt.Sprintf("%s-%s", appName, id)

	if err := applyUserSecretsIfPresent(cfg, namespace, deploymentDir); err != nil {
		return fmt.Errorf("failed to sync OpenClaw user secrets: %w", err)
	}

	fmt.Printf("Syncing OpenClaw: %s/%s\n", appName, id)
	fmt.Printf("Deployment directory: %s\n", deploymentDir)
	fmt.Printf("Running helmfile sync...\n\n")

	cmd := exec.Command(helmfileBinary, "-f", helmfilePath, "sync")
	cmd.Dir = deploymentDir
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath),
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("helmfile sync failed: %w", err)
	}

	hostname := fmt.Sprintf("openclaw-%s.%s", id, defaultDomain)
	fmt.Printf("\n✓ OpenClaw synced successfully!\n")
	fmt.Printf("  Namespace: %s\n", namespace)
	fmt.Printf("  URL:       http://%s\n", hostname)
	fmt.Printf("\nRetrieve gateway token:\n")
	fmt.Printf("  obol openclaw token %s\n", id)
	fmt.Printf("\nPort-forward fallback:\n")
	fmt.Printf("  obol kubectl -n %s port-forward svc/openclaw 18789:18789\n", namespace)

	return nil
}

func writeUserSecretsFile(deploymentDir string, secretData map[string]string) error {
	path := filepath.Join(deploymentDir, userSecretsFileName)
	if len(secretData) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	payload, err := json.MarshalIndent(secretData, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0600)
}

func loadUserSecretsFile(deploymentDir string) (map[string]string, error) {
	path := filepath.Join(deploymentDir, userSecretsFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out map[string]string
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", userSecretsFileName, err)
	}
	return out, nil
}

func applyUserSecretsIfPresent(cfg *config.Config, namespace, deploymentDir string) error {
	secretData, err := loadUserSecretsFile(deploymentDir)
	if err != nil {
		return err
	}
	if len(secretData) == 0 {
		return nil
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	if err := ensureNamespaceExists(kubectlBinary, kubeconfigPath, namespace); err != nil {
		return err
	}

	manifest := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]string{
			"name":      userSecretsK8sSecretRef,
			"namespace": namespace,
		},
		"type":       "Opaque",
		"stringData": secretData,
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}

	cmd := exec.Command(kubectlBinary, "apply", "-f", "-")
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w\n%s", err, stderr.String())
	}
	return nil
}

func ensureNamespaceExists(kubectlBinary, kubeconfigPath, namespace string) error {
	getCmd := exec.Command(kubectlBinary, "get", "namespace", namespace)
	getCmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	if err := getCmd.Run(); err == nil {
		return nil
	}

	createCmd := exec.Command(kubectlBinary, "create", "namespace", namespace)
	createCmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	var stderr bytes.Buffer
	createCmd.Stderr = &stderr
	if err := createCmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "AlreadyExists") {
			return nil
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// copyWorkspaceToPod copies the local workspace directory into the OpenClaw pod's PVC.
// This is non-fatal: failures print a warning and continue.
func copyWorkspaceToPod(cfg *config.Config, id, workspaceDir string) {
	namespace := fmt.Sprintf("%s-%s", appName, id)
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	fmt.Printf("\nImporting workspace from %s...\n", workspaceDir)

	// Wait for pod to be ready
	podName, err := waitForPod(kubectlBinary, kubeconfigPath, namespace, 60)
	if err != nil {
		fmt.Printf("Warning: could not find ready pod, skipping workspace import: %v\n", err)
		return
	}

	// kubectl cp <src>/. <pod>:/data/.openclaw/workspace/ -n <namespace>
	dest := fmt.Sprintf("%s:/data/.openclaw/workspace/", podName)
	src := workspaceDir + "/."
	cmd := exec.Command(kubectlBinary, "cp", src, dest, "-n", namespace)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("Warning: workspace copy failed: %v\n%s", err, stderr.String())
		return
	}

	fmt.Printf("Imported workspace into pod %s\n", podName)
}

// waitForPod polls for a Running pod matching the openclaw label and returns its name.
// Returns an error if no ready pod is found within timeoutSec seconds.
func waitForPod(kubectlBinary, kubeconfigPath, namespace string, timeoutSec int) (string, error) {
	labelSelector := fmt.Sprintf("app.kubernetes.io/name=%s", appName)

	for i := 0; i < timeoutSec; i += 3 {
		cmd := exec.Command(kubectlBinary, "get", "pods",
			"-n", namespace,
			"-l", labelSelector,
			"-o", "jsonpath={.items[?(@.status.phase=='Running')].metadata.name}",
		)
		cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Run()

		podName := strings.TrimSpace(stdout.String())
		if podName != "" {
			// If multiple pods, take the first
			if idx := strings.Index(podName, " "); idx > 0 {
				podName = podName[:idx]
			}
			return podName, nil
		}

		time.Sleep(3 * time.Second)
	}

	return "", fmt.Errorf("timed out waiting for pod in namespace %s", namespace)
}

// getToken retrieves the gateway token for an OpenClaw instance as a string.
func getToken(cfg *config.Config, id string) (string, error) {
	namespace := fmt.Sprintf("%s-%s", appName, id)

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return "", fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	cmd := exec.Command(kubectlBinary, "get", "secret", "-n", namespace,
		"-l", fmt.Sprintf("app.kubernetes.io/name=%s", appName),
		"-o", "json")
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to get secret: %w\n%s", err, stderr.String())
	}

	var secretList struct {
		Items []struct {
			Data map[string]string `json:"data"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &secretList); err != nil {
		return "", fmt.Errorf("failed to parse secret: %w", err)
	}

	if len(secretList.Items) == 0 {
		return "", fmt.Errorf("no secrets found in namespace %s. Is OpenClaw deployed?", namespace)
	}

	for _, item := range secretList.Items {
		if encoded, ok := item.Data["OPENCLAW_GATEWAY_TOKEN"]; ok {
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return "", fmt.Errorf("failed to decode token: %w", err)
			}
			return string(decoded), nil
		}
	}

	return "", fmt.Errorf("OPENCLAW_GATEWAY_TOKEN not found in namespace %s secrets", namespace)
}

// Token retrieves the gateway token for an OpenClaw instance and prints it.
func Token(cfg *config.Config, id string) error {
	token, err := getToken(cfg, id)
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", token)
	return nil
}

// findOpenClawBinary locates the openclaw CLI binary.
// Search order: PATH, then cfg.BinDir.
func findOpenClawBinary(cfg *config.Config) (string, error) {
	if p, err := exec.LookPath("openclaw"); err == nil {
		return p, nil
	}
	candidate := filepath.Join(cfg.BinDir, "openclaw")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	return "", fmt.Errorf("openclaw CLI not found.\n\nInstall with one of:\n  obolup.sh                                    (re-run bootstrap installer)\n  curl -fsSL https://openclaw.ai/install.sh | bash\n  npm install -g openclaw                      (requires Node.js 22+)")
}

// portForwarder manages a background kubectl port-forward process.
type portForwarder struct {
	cmd       *exec.Cmd
	localPort int
	done      chan error
	cancel    context.CancelFunc
}

// startPortForward launches kubectl port-forward in the background and waits
// until it reports the forwarding address on stdout.
func startPortForward(cfg *config.Config, namespace string, localPort int) (*portForwarder, error) {
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	portArg := fmt.Sprintf("%d:18789", localPort)
	if localPort == 0 {
		portArg = ":18789"
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, kubectlBinary, "port-forward",
		fmt.Sprintf("svc/%s", appName), portArg, "-n", namespace)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))

	// kubectl prints "Forwarding from ..." to stdout (not stderr)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start port-forward: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// Parse the "Forwarding from 127.0.0.1:<port>" line from stdout
	parsedPort := make(chan int, 1)
	parseErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			// kubectl prints: "Forwarding from 127.0.0.1:<port> -> 18789"
			if strings.Contains(line, "Forwarding from") {
				parts := strings.Split(line, ":")
				if len(parts) >= 2 {
					portPart := strings.Fields(parts[len(parts)-1])[0]
					var p int
					if _, err := fmt.Sscanf(portPart, "%d", &p); err == nil {
						parsedPort <- p
						// Continue draining to prevent pipe blocking
						io.Copy(io.Discard, stdoutPipe)
						return
					}
				}
			}
		}
		parseErr <- fmt.Errorf("port-forward exited without reporting a local port")
	}()

	select {
	case p := <-parsedPort:
		return &portForwarder{cmd: cmd, localPort: p, done: done, cancel: cancel}, nil
	case err := <-parseErr:
		cancel()
		return nil, err
	case err := <-done:
		cancel()
		if err != nil {
			return nil, fmt.Errorf("port-forward process exited unexpectedly: %w", err)
		}
		return nil, fmt.Errorf("port-forward process exited unexpectedly")
	case <-time.After(30 * time.Second):
		cancel()
		return nil, fmt.Errorf("timed out waiting for port-forward to become ready")
	}
}

// Stop terminates the port-forward process gracefully.
func (pf *portForwarder) Stop() {
	pf.cancel()
	select {
	case <-pf.done:
	case <-time.After(5 * time.Second):
		if pf.cmd.Process != nil {
			pf.cmd.Process.Kill()
		}
	}
}

// SetupOptions contains options for the setup command.
type SetupOptions struct {
	Port int // kept for backward compat; currently unused
}

// Setup reconfigures model providers for a deployed OpenClaw instance.
// It runs the interactive provider prompt, regenerates the overlay values,
// and syncs via helmfile so the pod picks up the new configuration.
func Setup(cfg *config.Config, id string, _ SetupOptions) error {
	deploymentDir := deploymentPath(cfg, id)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s/%s\nRun 'obol openclaw up' first", appName, id)
	}

	// Always show the provider prompt — that's the whole point of setup.
	imported, cloudProvider, err := interactiveSetup(nil)
	if err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Push cloud API key to llmspy if a cloud provider was selected
	if cloudProvider != nil {
		if llmErr := llm.ConfigureLLMSpy(cfg, cloudProvider.Name, cloudProvider.APIKey); llmErr != nil {
			return fmt.Errorf("failed to configure llmspy: %w", llmErr)
		}
	}

	// Regenerate helmfile to pick up any chart version bumps
	namespace := fmt.Sprintf("%s-%s", appName, id)
	helmfileContent := generateHelmfile(id, namespace)
	if err := os.WriteFile(filepath.Join(deploymentDir, "helmfile.yaml"), []byte(helmfileContent), 0644); err != nil {
		return fmt.Errorf("failed to write helmfile.yaml: %w", err)
	}

	// Regenerate overlay values with the selected provider
	hostname := fmt.Sprintf("openclaw-%s.%s", id, defaultDomain)
	secretData := collectSensitiveData(imported)
	if err := writeUserSecretsFile(deploymentDir, secretData); err != nil {
		return fmt.Errorf("failed to write OpenClaw secrets metadata: %w", err)
	}
	overlay := generateOverlayValues(hostname, imported, len(secretData) > 0)
	overlayPath := filepath.Join(deploymentDir, "values-obol.yaml")
	if err := os.WriteFile(overlayPath, []byte(overlay), 0644); err != nil {
		return fmt.Errorf("failed to write overlay values: %w", err)
	}

	fmt.Printf("\nApplying configuration...\n\n")
	if err := doSync(cfg, id); err != nil {
		return err
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	fmt.Printf("\nWaiting for pod to be ready...\n")
	if _, err := waitForPod(kubectlBinary, kubeconfigPath, namespace, 90); err != nil {
		fmt.Printf("Warning: pod not ready yet: %v\n", err)
		fmt.Println("The deployment may still be rolling out. Check with: obol kubectl get pods -n", namespace)
	} else {
		fmt.Printf("\n✓ Setup complete!\n")
		fmt.Printf("  Open dashboard: obol openclaw dashboard %s\n", id)
	}
	return nil
}

// DashboardOptions contains options for the dashboard command.
type DashboardOptions struct {
	Port      int
	NoBrowser bool
}

// Dashboard port-forwards to the OpenClaw instance and opens the web dashboard.
// The onReady callback is invoked with the dashboard URL; the CLI layer uses it
// to open a browser.
func Dashboard(cfg *config.Config, id string, opts DashboardOptions, onReady func(url string)) error {
	deploymentDir := deploymentPath(cfg, id)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s/%s\nRun 'obol openclaw up' first", appName, id)
	}

	token, err := getToken(cfg, id)
	if err != nil {
		return err
	}

	namespace := fmt.Sprintf("%s-%s", appName, id)
	fmt.Printf("Starting port-forward to %s...\n", namespace)

	pf, err := startPortForward(cfg, namespace, opts.Port)
	if err != nil {
		return fmt.Errorf("port-forward failed: %w", err)
	}
	defer pf.Stop()

	dashboardURL := fmt.Sprintf("http://localhost:%d/#token=%s", pf.localPort, token)
	fmt.Printf("Port-forward active: localhost:%d -> %s:18789\n", pf.localPort, namespace)
	fmt.Printf("\nDashboard URL: %s\n", dashboardURL)
	fmt.Printf("Gateway token: %s\n", token)
	fmt.Printf("\nPress Ctrl+C to stop.\n")

	if onReady != nil {
		onReady(dashboardURL)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case <-sigCh:
		fmt.Printf("\nShutting down...\n")
	case err := <-pf.done:
		if err != nil {
			return fmt.Errorf("port-forward died unexpectedly: %w", err)
		}
	}

	return nil
}

// List displays installed OpenClaw instances
func List(cfg *config.Config) error {
	appsDir := filepath.Join(cfg.ConfigDir, "applications", appName)

	if _, err := os.Stat(appsDir); os.IsNotExist(err) {
		fmt.Println("No OpenClaw instances installed")
		fmt.Println("\nTo create one: obol openclaw up")
		return nil
	}

	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	if len(entries) == 0 {
		fmt.Println("No OpenClaw instances installed")
		return nil
	}

	fmt.Println("OpenClaw instances:")
	fmt.Println()

	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		namespace := fmt.Sprintf("%s-%s", appName, id)
		hostname := fmt.Sprintf("openclaw-%s.%s", id, defaultDomain)
		fmt.Printf("  %s\n", id)
		fmt.Printf("    Namespace: %s\n", namespace)
		fmt.Printf("    URL:       http://%s\n", hostname)
		fmt.Println()
		count++
	}

	fmt.Printf("Total: %d instance(s)\n", count)
	return nil
}

// Delete removes an OpenClaw instance
func Delete(cfg *config.Config, id string, force bool) error {
	namespace := fmt.Sprintf("%s-%s", appName, id)
	deploymentDir := deploymentPath(cfg, id)

	fmt.Printf("Deleting OpenClaw: %s/%s\n", appName, id)
	fmt.Printf("Namespace: %s\n", namespace)

	configExists := false
	if _, err := os.Stat(deploymentDir); err == nil {
		configExists = true
	}

	namespaceExists := false
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); err == nil {
		kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
		cmd := exec.Command(kubectlBinary, "get", "namespace", namespace)
		cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
		if err := cmd.Run(); err == nil {
			namespaceExists = true
		}
	}

	if !namespaceExists && !configExists {
		return fmt.Errorf("instance not found: %s", id)
	}

	fmt.Println("\nResources to be deleted:")
	if namespaceExists {
		fmt.Printf("  [x] Kubernetes namespace: %s\n", namespace)
	} else {
		fmt.Printf("  [ ] Kubernetes namespace: %s (not found)\n", namespace)
	}
	if configExists {
		fmt.Printf("  [x] Configuration: %s\n", deploymentDir)
	}

	if !force {
		fmt.Print("\nProceed with deletion? [y/N]: ")
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
			fmt.Println("Deletion cancelled")
			return nil
		}
	}

	if namespaceExists {
		fmt.Printf("\nDeleting namespace %s...\n", namespace)
		kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
		cmd := exec.Command(kubectlBinary, "delete", "namespace", namespace,
			"--force", "--grace-period=0")
		cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to delete namespace: %w", err)
		}
		fmt.Println("Namespace deleted")
	}

	if configExists {
		fmt.Printf("Deleting configuration...\n")
		if err := os.RemoveAll(deploymentDir); err != nil {
			return fmt.Errorf("failed to delete config directory: %w", err)
		}
		fmt.Println("Configuration deleted")

		parentDir := filepath.Join(cfg.ConfigDir, "applications", appName)
		entries, err := os.ReadDir(parentDir)
		if err == nil && len(entries) == 0 {
			os.Remove(parentDir)
		}
	}

	fmt.Printf("\n✓ OpenClaw %s deleted successfully!\n", id)
	return nil
}

// SkillsSync packages a local skills directory into a ConfigMap and rolls the deployment
func SkillsSync(cfg *config.Config, id, skillsDir string) error {
	namespace := fmt.Sprintf("%s-%s", appName, id)

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		return fmt.Errorf("skills directory not found: %s", skillsDir)
	}

	configMapName := fmt.Sprintf("openclaw-%s-skills", id)
	archiveKey := "skills.tgz"

	fmt.Printf("Packaging skills from %s...\n", skillsDir)

	var archiveBuf bytes.Buffer
	tarCmd := exec.Command("tar", "-czf", "-", "-C", skillsDir, ".")
	tarCmd.Stdout = &archiveBuf
	var tarStderr bytes.Buffer
	tarCmd.Stderr = &tarStderr
	if err := tarCmd.Run(); err != nil {
		return fmt.Errorf("failed to create skills archive: %w\n%s", err, tarStderr.String())
	}

	tmpFile, err := os.CreateTemp("", "openclaw-skills-*.tgz")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(archiveBuf.Bytes()); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write archive: %w", err)
	}
	tmpFile.Close()

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	delCmd := exec.Command(kubectlBinary, "delete", "configmap", configMapName,
		"-n", namespace, "--ignore-not-found")
	delCmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	delCmd.Run()

	fmt.Printf("Creating ConfigMap %s in namespace %s...\n", configMapName, namespace)
	createCmd := exec.Command(kubectlBinary, "create", "configmap", configMapName,
		"-n", namespace,
		fmt.Sprintf("--from-file=%s=%s", archiveKey, tmpFile.Name()))
	createCmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	var createStderr bytes.Buffer
	createCmd.Stderr = &createStderr
	if err := createCmd.Run(); err != nil {
		return fmt.Errorf("failed to create ConfigMap: %w\n%s", err, createStderr.String())
	}

	fmt.Printf("✓ Skills ConfigMap updated: %s\n", configMapName)
	fmt.Printf("\nTo apply, re-sync: obol openclaw sync %s\n", id)
	return nil
}

// remoteCapableCommands lists openclaw subcommands that support --url and --token flags.
var remoteCapableCommands = map[string]bool{
	"gateway": true,
	"acp":     true,
	"browser": true,
	"logs":    true,
}

// CLI runs an openclaw CLI command against a deployed instance.
// Commands that support --url/--token are executed locally with a port-forward;
// others are executed via kubectl exec into the pod.
func CLI(cfg *config.Config, id string, args []string) error {
	deploymentDir := deploymentPath(cfg, id)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s/%s\nRun 'obol openclaw up' first", appName, id)
	}

	namespace := fmt.Sprintf("%s-%s", appName, id)

	if len(args) == 0 {
		return fmt.Errorf("no openclaw command specified\n\nExamples:\n"+
			"  obol openclaw cli %s -- gateway health\n"+
			"  obol openclaw cli %s -- gateway call config.get\n"+
			"  obol openclaw cli %s -- doctor", id, id, id)
	}

	// Determine if the command supports --url/--token (remote-capable)
	firstArg := args[0]
	if remoteCapableCommands[firstArg] {
		return cliViaPortForward(cfg, id, namespace, args)
	}
	return cliViaKubectlExec(cfg, namespace, args)
}

// cliViaPortForward runs an openclaw command locally with port-forward + --url/--token.
func cliViaPortForward(cfg *config.Config, id, namespace string, args []string) error {
	openclawBinary, err := findOpenClawBinary(cfg)
	if err != nil {
		return err
	}

	token, err := getToken(cfg, id)
	if err != nil {
		return fmt.Errorf("failed to get gateway token: %w", err)
	}

	pf, err := startPortForward(cfg, namespace, 0)
	if err != nil {
		return fmt.Errorf("port-forward failed: %w", err)
	}
	defer pf.Stop()

	// Append --url and --token to the args
	wsURL := fmt.Sprintf("ws://localhost:%d", pf.localPort)
	fullArgs := append(args, "--url", wsURL, "--token", token)

	cmd := exec.Command(openclawBinary, fullArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Handle signals to clean up port-forward
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		<-sigCh
		pf.Stop()
	}()

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				os.Exit(status.ExitStatus())
			}
		}
		return err
	}
	return nil
}

// cliViaKubectlExec runs an openclaw command inside the pod via kubectl exec.
func cliViaKubectlExec(cfg *config.Config, namespace string, args []string) error {
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	// Build: kubectl exec -it -n <ns> deploy/openclaw -- node openclaw.mjs <args>
	// The pod runs `node openclaw.mjs` (no standalone binary in PATH).
	execArgs := []string{
		"exec", "-it",
		"-n", namespace,
		"deploy/openclaw",
		"--",
		"node", "openclaw.mjs",
	}
	execArgs = append(execArgs, args...)

	cmd := exec.Command(kubectlBinary, execArgs...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				os.Exit(status.ExitStatus())
			}
		}
		return err
	}
	return nil
}

// deploymentPath returns the path to a deployment directory
func deploymentPath(cfg *config.Config, id string) string {
	return filepath.Join(cfg.ConfigDir, "applications", appName, id)
}

// generateOverlayValues creates the Obol Stack-specific values overlay.
// If imported is non-nil, provider/channel config from the import is used
// instead of the default Ollama configuration.
func generateOverlayValues(hostname string, imported *ImportResult, useExternalSecrets bool) string {
	var b strings.Builder

	b.WriteString(`# Obol Stack overlay values for OpenClaw
# This file contains stack-specific defaults. Edit to customize.

# Enable Gateway API HTTPRoute for stack routing
httpRoute:
  enabled: true
  hostnames:
`)
	b.WriteString(fmt.Sprintf("    - %s\n", hostname))
	b.WriteString(`  parentRefs:
    - name: traefik-gateway
      namespace: traefik
      sectionName: web

# SA needs API token mount for K8s read access
serviceAccount:
  automount: true

# Read-only RBAC for K8s API (pods, services, deployments, etc.)
rbac:
  create: true

`)

	// Provider and agent model configuration
	importedOverlay := TranslateToOverlayYAML(imported)
	if importedOverlay != "" {
		b.WriteString("# Imported from ~/.openclaw/openclaw.json\n")
		// Inject gateway controlUi settings for Traefik reverse proxy.
		// allowInsecureAuth is required because the browser accesses OpenClaw via
		// http://<instance>.obol.stack (non-localhost HTTP), where crypto.subtle is
		// unavailable. Without it, the gateway rejects with 1008 "requires HTTPS or
		// localhost (secure context)". Token auth is still enforced.
		if strings.Contains(importedOverlay, "openclaw:\n") {
			importedOverlay = strings.Replace(importedOverlay, "openclaw:\n", "openclaw:\n  gateway:\n    controlUi:\n      allowInsecureAuth: true\n", 1)
		} else {
			b.WriteString("openclaw:\n  gateway:\n    controlUi:\n      allowInsecureAuth: true\n\n")
		}
		b.WriteString(importedOverlay)
	} else {
		b.WriteString(`# Route agent traffic to in-cluster Ollama via llmspy proxy
openclaw:
  agentModel: ollama/gpt-oss:120b-cloud
  gateway:
    # Allow control UI over HTTP behind Traefik (local dev stack).
    # Required: browser on non-localhost HTTP has no crypto.subtle,
    # so device identity is unavailable. Token auth is still enforced.
    controlUi:
      allowInsecureAuth: true

# Default model provider: in-cluster Ollama (routed through llmspy)
# apiKeyValue is a dummy placeholder — Ollama does not require auth.
# It is safe to inline here (unlike real cloud keys, which go to secrets).
models:
  ollama:
    enabled: true
    baseUrl: http://llmspy.llm.svc.cluster.local:8000/v1
    apiKeyEnvVar: OLLAMA_API_KEY
    apiKeyValue: ollama-local
    models:
      - id: gpt-oss:120b-cloud
        name: GPT-OSS 120B Cloud

`)
	}

	b.WriteString(`# eRPC integration
erpc:
  url: http://erpc.erpc.svc.cluster.local:4000/rpc

# Skills: chart creates a default empty ConfigMap; populate with obol openclaw skills sync
skills:
  enabled: true
  createDefault: true

# Agent init Job (enable to bootstrap workspace on first deploy)
initJob:
  enabled: false
`)

	if useExternalSecrets {
		b.WriteString(`
# Load instance-local credentials (provider/channel tokens) from a dedicated Secret
secrets:
  extraEnvFromSecrets:
    - ` + userSecretsK8sSecretRef + `
`)
	}

	return b.String()
}

// ollamaEndpoint returns the base URL where host Ollama should be reachable.
// It respects the OLLAMA_HOST environment variable, falling back to http://localhost:11434.
func ollamaEndpoint() string {
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		// OLLAMA_HOST may be just "host:port" or a full URL.
		if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
			host = "http://" + host
		}
		return strings.TrimRight(host, "/")
	}
	return "http://localhost:11434"
}

// detectOllama checks whether Ollama is reachable on the host machine by
// hitting the /api/tags endpoint with a short timeout. Returns true if the
// server responds with HTTP 200.
func detectOllama() bool {
	endpoint := ollamaEndpoint()
	tagsURL, err := url.JoinPath(endpoint, "api", "tags")
	if err != nil {
		return false
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(tagsURL)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// interactiveSetup prompts the user for provider configuration.
// If imported is non-nil, offers to use the detected config.
// Returns the ImportResult for overlay generation, and optionally a CloudProviderInfo
// when a cloud provider was selected (so the caller can configure llmspy).
func interactiveSetup(imported *ImportResult) (*ImportResult, *CloudProviderInfo, error) {
	reader := bufio.NewReader(os.Stdin)

	if imported != nil {
		fmt.Print("\nUse detected configuration? [Y/n]: ")
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "" || line == "y" || line == "yes" {
			fmt.Println("Using detected configuration.")
			return imported, nil, nil
		}
	}

	// Detect Ollama on the host to decide whether to offer it as an option
	ollamaAvailable := detectOllama()
	if ollamaAvailable {
		fmt.Printf("  ✓ Ollama detected at %s\n", ollamaEndpoint())
	} else {
		fmt.Printf("  ⚠ Ollama not detected on host (%s)\n", ollamaEndpoint())
	}

	if ollamaAvailable {
		fmt.Println("\nSelect a model provider:")
		fmt.Println("  [1] Local Ollama via the Obol model gateway (default)")
		fmt.Println("  [2] Anthropic API key via the Obol model gateway")
		fmt.Println("  [3] OpenAI API key via the Obol model gateway")
		fmt.Println("  [4] Direct Anthropic API key to the Openclaw gateway")
		fmt.Println("  [5] Direct OpenAI API key to Openclaw gateway")
		fmt.Println("  [6] Custom OpenAI-compatible endpoint to the Openclaw gateway")
		fmt.Print("\nChoice [1]: ")

		line, _ := reader.ReadString('\n')
		choice := strings.TrimSpace(line)
		if choice == "" {
			choice = "1"
		}

		switch choice {
		case "1":
			fmt.Println("Using global Ollama route via llmspy.")
			return nil, nil, nil
		case "2":
			cloud, err := promptForCloudProvider(reader, "anthropic", "Anthropic", "claude-opus-4-6", "Claude Opus 4.6")
			if err != nil {
				return nil, nil, err
			}
			result := buildLLMSpyRoutedOverlay(cloud)
			return result, cloud, nil
		case "3":
			cloud, err := promptForCloudProvider(reader, "openai", "OpenAI", "gpt-5.2", "GPT-5.2")
			if err != nil {
				return nil, nil, err
			}
			result := buildLLMSpyRoutedOverlay(cloud)
			return result, cloud, nil
		case "4":
			result, err := promptForDirectProvider(reader, "anthropic", "Anthropic", "https://api.anthropic.com/v1", "anthropic-messages", "ANTHROPIC_API_KEY", "claude-opus-4-6", "Claude Opus 4.6")
			if err != nil {
				return nil, nil, err
			}
			return result, nil, nil
		case "5":
			result, err := promptForDirectProvider(reader, "openai", "OpenAI", "https://api.openai.com/v1", "openai-completions", "OPENAI_API_KEY", "gpt-5.2", "GPT-5.2")
			if err != nil {
				return nil, nil, err
			}
			return result, nil, nil
		case "6":
			result, err := promptForCustomProvider(reader)
			if err != nil {
				return nil, nil, err
			}
			return result, nil, nil
		default:
			fmt.Printf("Unknown choice '%s', using global Ollama route.\n", choice)
			return nil, nil, nil
		}
	}

	// Ollama not available — offer cloud/global and direct overrides
	fmt.Println("\nSelect a remote model provider:")
	fmt.Println("  [1] Anthropic API key via the Obol model gateway")
	fmt.Println("  [2] OpenAI API key via the Obol model gateway")
	fmt.Println("  [3] Direct Anthropic API key to the Openclaw gateway")
	fmt.Println("  [4] Direct OpenAI API key to Openclaw gateway")
	fmt.Println("  [5] Custom OpenAI-compatible endpoint to the Openclaw gateway")
	fmt.Print("\nChoice [1]: ")

	line, _ := reader.ReadString('\n')
	choice := strings.TrimSpace(line)
	if choice == "" {
		choice = "1"
	}

	switch choice {
	case "1":
		cloud, err := promptForCloudProvider(reader, "anthropic", "Anthropic", "claude-opus-4-6", "Claude Opus 4.6")
		if err != nil {
			return nil, nil, err
		}
		result := buildLLMSpyRoutedOverlay(cloud)
		return result, cloud, nil
	case "2":
		cloud, err := promptForCloudProvider(reader, "openai", "OpenAI", "gpt-5.2", "GPT-5.2")
		if err != nil {
			return nil, nil, err
		}
		result := buildLLMSpyRoutedOverlay(cloud)
		return result, cloud, nil
	case "3":
		result, err := promptForDirectProvider(reader, "anthropic", "Anthropic", "https://api.anthropic.com/v1", "anthropic-messages", "ANTHROPIC_API_KEY", "claude-opus-4-6", "Claude Opus 4.6")
		if err != nil {
			return nil, nil, err
		}
		return result, nil, nil
	case "4":
		result, err := promptForDirectProvider(reader, "openai", "OpenAI", "https://api.openai.com/v1", "openai-completions", "OPENAI_API_KEY", "gpt-5.2", "GPT-5.2")
		if err != nil {
			return nil, nil, err
		}
		return result, nil, nil
	case "5":
		result, err := promptForCustomProvider(reader)
		if err != nil {
			return nil, nil, err
		}
		return result, nil, nil
	default:
		return nil, nil, fmt.Errorf("unknown choice '%s'; please select a valid model provider", choice)
	}
}

// promptForCloudProvider asks for an API key and returns cloud provider info.
// The actual overlay (ImportResult) is built separately via buildLLMSpyRoutedOverlay.
func promptForCloudProvider(reader *bufio.Reader, name, display, modelID, modelName string) (*CloudProviderInfo, error) {
	fmt.Printf("\n%s API key: ", display)
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("%s API key is required", display)
	}

	return &CloudProviderInfo{
		Name:    name,
		APIKey:  apiKey,
		ModelID: modelID,
		Display: modelName,
	}, nil
}

// promptForDirectProvider asks for direct-provider settings for an instance-local override.
func promptForDirectProvider(reader *bufio.Reader, providerName, display, defaultBaseURL, defaultAPI, defaultAPIKeyEnvVar, defaultModelID, defaultModelName string) (*ImportResult, error) {
	fmt.Printf("\n%s API key (instance-local): ", display)
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("%s API key is required", display)
	}

	fmt.Printf("%s model ID [%s]: ", display, defaultModelID)
	modelID, _ := reader.ReadString('\n')
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		modelID = defaultModelID
	}

	fmt.Printf("%s model display name [%s]: ", display, defaultModelName)
	modelName, _ := reader.ReadString('\n')
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		modelName = defaultModelName
	}

	fmt.Printf("%s base URL [%s]: ", display, defaultBaseURL)
	baseURL, _ := reader.ReadString('\n')
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	return buildDirectProviderOverlay(providerName, baseURL, defaultAPI, defaultAPIKeyEnvVar, modelID, modelName, apiKey), nil
}

// promptForCustomProvider asks for an OpenAI-compatible custom endpoint override.
func promptForCustomProvider(reader *bufio.Reader) (*ImportResult, error) {
	fmt.Printf("\nCustom base URL (OpenAI-compatible, e.g. https://example.com/v1): ")
	baseURL, _ := reader.ReadString('\n')
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("custom base URL is required")
	}

	fmt.Printf("Custom model ID: ")
	modelID, _ := reader.ReadString('\n')
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil, fmt.Errorf("custom model ID is required")
	}

	fmt.Printf("Custom model display name [%s]: ", modelID)
	modelName, _ := reader.ReadString('\n')
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		modelName = modelID
	}

	fmt.Printf("Custom API type [openai-completions]: ")
	apiType, _ := reader.ReadString('\n')
	apiType = strings.TrimSpace(apiType)
	if apiType == "" {
		apiType = "openai-completions"
	}

	fmt.Printf("API key env var [OPENAI_API_KEY]: ")
	apiKeyEnvVar, _ := reader.ReadString('\n')
	apiKeyEnvVar = strings.TrimSpace(apiKeyEnvVar)
	if apiKeyEnvVar == "" {
		apiKeyEnvVar = "OPENAI_API_KEY"
	}

	fmt.Printf("API key (optional, leave empty to configure later): ")
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		fmt.Println("  Note: no API key provided; set it later via the OpenClaw user secret.")
	}

	// Custom endpoints use the "openai" slot because the Helm chart only iterates
	// a hardcoded provider list (ollama, anthropic, openai). Any other name would
	// be silently dropped. OpenAI-compatible is the most generic fit.
	return buildDirectProviderOverlay("openai", baseURL, apiType, apiKeyEnvVar, modelID, modelName, apiKey), nil
}

// buildLLMSpyRoutedOverlay creates an ImportResult that routes a cloud model
// through the llmspy proxy. OpenClaw sees an "ollama" provider pointing at the
// cluster-wide llmspy gateway, with the cloud model in its model list. We reuse
// the "ollama" provider name because the remote Helm chart only iterates a
// hardcoded list (ollama, anthropic, openai) — using a custom name would cause
// the provider to be silently dropped from the rendered config.
// The actual cloud providers are disabled in OpenClaw — llmspy handles upstream
// routing based on the bare model ID.
func buildLLMSpyRoutedOverlay(cloud *CloudProviderInfo) *ImportResult {
	return &ImportResult{
		AgentModel: "ollama/" + cloud.ModelID,
		Providers: []ImportedProvider{
			{
				Name:         "ollama",
				BaseURL:      "http://llmspy.llm.svc.cluster.local:8000/v1",
				API:          "openai-completions",
				APIKeyEnvVar: "OLLAMA_API_KEY",
				APIKey:       "ollama-local",
				Models: []ImportedModel{
					{ID: cloud.ModelID, Name: cloud.Display},
				},
			},
			{Name: "anthropic", Disabled: true},
			{Name: "openai", Disabled: true},
		},
	}
}

// buildDirectProviderOverlay creates an instance-local direct provider configuration.
// Provider name must be one of anthropic/openai/ollama due current chart constraints.
func buildDirectProviderOverlay(providerName, baseURL, api, apiKeyEnvVar, modelID, modelName, apiKey string) *ImportResult {
	var agentPrefix string
	switch providerName {
	case "anthropic":
		agentPrefix = "anthropic"
	case "openai":
		agentPrefix = "openai"
	default:
		agentPrefix = providerName
	}

	providers := []ImportedProvider{
		{Name: "anthropic", Disabled: providerName != "anthropic"},
		{Name: "openai", Disabled: providerName != "openai"},
		{Name: "ollama", Disabled: providerName != "ollama"},
	}
	for i := range providers {
		if providers[i].Name != providerName {
			continue
		}
		providers[i].Disabled = false
		providers[i].BaseURL = baseURL
		providers[i].API = api
		providers[i].APIKeyEnvVar = apiKeyEnvVar
		providers[i].APIKey = apiKey
		providers[i].Models = []ImportedModel{{ID: modelID, Name: modelName}}
	}

	return &ImportResult{
		AgentModel: agentPrefix + "/" + modelID,
		Providers:  providers,
	}
}

// collectSensitiveData extracts literal secrets from imported config and strips
// them from the in-memory overlay data so values-obol.yaml does not persist them.
// NOTE: This mutates imported in-place (zeroes APIKey/BotToken fields). The caller
// must call this BEFORE generating the overlay YAML.
func collectSensitiveData(imported *ImportResult) map[string]string {
	if imported == nil {
		return nil
	}

	secretData := make(map[string]string)

	for i := range imported.Providers {
		p := &imported.Providers[i]
		if p.APIKey == "" {
			continue
		}
		envVar := p.APIKeyEnvVar
		if envVar == "" {
			envVar = defaultProviderAPIKeyEnvVar(p.Name)
			p.APIKeyEnvVar = envVar
		}
		secretData[envVar] = p.APIKey
		p.APIKey = ""
	}

	if imported.Channels.Telegram != nil && imported.Channels.Telegram.BotToken != "" {
		secretData["TELEGRAM_BOT_TOKEN"] = imported.Channels.Telegram.BotToken
		imported.Channels.Telegram.BotToken = ""
	}
	if imported.Channels.Discord != nil && imported.Channels.Discord.BotToken != "" {
		secretData["DISCORD_BOT_TOKEN"] = imported.Channels.Discord.BotToken
		imported.Channels.Discord.BotToken = ""
	}
	if imported.Channels.Slack != nil {
		if imported.Channels.Slack.BotToken != "" {
			secretData["SLACK_BOT_TOKEN"] = imported.Channels.Slack.BotToken
			imported.Channels.Slack.BotToken = ""
		}
		if imported.Channels.Slack.AppToken != "" {
			secretData["SLACK_APP_TOKEN"] = imported.Channels.Slack.AppToken
			imported.Channels.Slack.AppToken = ""
		}
	}

	if len(secretData) == 0 {
		return nil
	}
	return secretData
}

// generateHelmfile creates a helmfile.yaml referencing the published obol/openclaw chart.
func generateHelmfile(id, namespace string) string {
	return fmt.Sprintf(`# OpenClaw instance: %s
# Managed by obol openclaw

repositories:
  - name: obol
    url: https://obolnetwork.github.io/helm-charts/

releases:
  - name: openclaw
    namespace: %s
    createNamespace: true
    chart: obol/openclaw
    version: %s
    values:
      - values-obol.yaml
`, id, namespace, chartVersion)
}
