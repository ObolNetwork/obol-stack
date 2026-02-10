package openclaw

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/creack/pty/v2"
	"github.com/dustinkirkland/golang-petname"
	"golang.org/x/term"
)

const (
	appName       = "openclaw"
	defaultDomain = "obol.stack"
)

// ansiRe matches ANSI escape sequences (CSI and OSC).
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x1b]*\x1b\\`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// Embed the OpenClaw Helm chart from the shared charts directory.
// The chart source lives in internal/embed/charts/openclaw/ and is
// referenced here so the openclaw package owns its own chart lifecycle.
//
//go:embed all:chart
var chartFS embed.FS

// UpOptions contains options for the up command
type UpOptions struct {
	ID          string // Deployment ID (empty = generate petname)
	Force       bool   // Overwrite existing deployment
	Sync        bool   // Also run helmfile sync after install
	Interactive bool   // true = prompt for provider choice; false = silent defaults
	IsDefault   bool   // true = use fixed ID "default", idempotent on re-run
}

// SetupDefault deploys a default OpenClaw instance as part of stack setup.
// It is idempotent: if a "default" deployment already exists, it re-syncs.
func SetupDefault(cfg *config.Config) error {
	return Up(cfg, UpOptions{
		ID:        "default",
		Sync:      true,
		IsDefault: true,
	})
}

// Up creates and optionally deploys an OpenClaw instance
func Up(cfg *config.Config, opts UpOptions) error {
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
				imported, _ := DetectExistingConfig()
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
			imported, err = interactiveSetup(imported)
			if err != nil {
				return fmt.Errorf("interactive setup failed: %w", err)
			}
		}
	}

	if err := os.MkdirAll(deploymentDir, 0755); err != nil {
		return fmt.Errorf("failed to create deployment directory: %w", err)
	}

	// Copy embedded chart to deployment/chart/
	chartDir := filepath.Join(deploymentDir, "chart")
	if err := copyEmbeddedChart(chartDir); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to copy chart: %w", err)
	}

	// Write values.yaml from the embedded chart defaults
	defaultValues, err := chartFS.ReadFile("chart/values.yaml")
	if err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to read chart defaults: %w", err)
	}
	if err := os.WriteFile(filepath.Join(deploymentDir, "values.yaml"), defaultValues, 0644); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to write values.yaml: %w", err)
	}

	// Write Obol Stack overlay values (httpRoute, provider config, eRPC, skills)
	hostname := fmt.Sprintf("openclaw-%s.%s", id, defaultDomain)
	namespace := fmt.Sprintf("%s-%s", appName, id)
	overlay := generateOverlayValues(hostname, imported)
	if err := os.WriteFile(filepath.Join(deploymentDir, "values-obol.yaml"), []byte(overlay), 0644); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to write overlay values: %w", err)
	}

	// Generate helmfile.yaml referencing local chart
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
	fmt.Printf("  - chart/            Embedded OpenClaw Helm chart\n")
	fmt.Printf("  - values.yaml       Chart defaults (edit to customize)\n")
	fmt.Printf("  - values-obol.yaml  Obol Stack defaults (httpRoute, providers, eRPC)\n")
	fmt.Printf("  - helmfile.yaml     Deployment configuration\n")

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

	namespace := fmt.Sprintf("%s-%s", appName, id)
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
	Port int // local port override (0 = auto-select)
}

// Setup runs the OpenClaw onboard wizard against a deployed instance.
// It port-forwards to the gateway WebSocket and invokes the native openclaw CLI.
func Setup(cfg *config.Config, id string, opts SetupOptions) error {
	deploymentDir := deploymentPath(cfg, id)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s/%s\nRun 'obol openclaw up' first", appName, id)
	}

	openclawBin, err := findOpenClawBinary(cfg)
	if err != nil {
		return err
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

	fmt.Printf("Port-forward active: localhost:%d -> %s:18789\n\n", pf.localPort, namespace)

	wsURL := fmt.Sprintf("ws://localhost:%d", pf.localPort)
	cmd := exec.Command(openclawBin, "onboard",
		"--mode", "remote",
		"--remote-url", wsURL,
		"--remote-token", token)

	// Start in a PTY so the wizard gets a real terminal (colors, selectors).
	// We intercept PTY output to detect completion because the Node.js
	// process hangs after remote-mode (@clack/prompts leaves stdin open).
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("openclaw onboard failed to start: %w", err)
	}

	// ptyClosed tracks whether we have already closed ptmx to avoid double-close.
	ptyClosed := false
	closePTY := func() {
		if !ptyClosed {
			ptyClosed = true
			ptmx.Close()
		}
	}
	defer closePTY()

	if sz, err := pty.GetsizeFull(os.Stdin); err == nil {
		_ = pty.Setsize(ptmx, sz)
	}

	// Raw mode: forward keystrokes to the PTY without line-buffering.
	oldState, rawErr := term.MakeRaw(int(os.Stdin.Fd()))
	if rawErr == nil {
		defer term.Restore(int(os.Stdin.Fd()), oldState)
	}

	// Catch signals so we restore the terminal even on interrupt.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		closePTY()
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}()

	// Forward keystrokes from stdin to the PTY. This goroutine will
	// unblock and return once ptmx is closed (write returns error).
	go func() { _, _ = io.Copy(ptmx, os.Stdin) }()

	// Relay PTY -> stdout, watching for the completion marker.
	// We keep a sliding window of the last 512 bytes to avoid O(n^2) scans.
	// ANSI escape sequences are stripped before matching because @clack/prompts
	// renders with cursor movement and color codes in TTY mode.
	const markerWindowSize = 512
	const marker = "Remote gateway configured"
	var window bytes.Buffer
	buf := make([]byte, 4096)
	completed := false
	for {
		n, readErr := ptmx.Read(buf)
		if n > 0 {
			os.Stdout.Write(buf[:n])
			window.Write(buf[:n])
			// Trim window to a sliding tail to keep stripANSI cheap.
			if window.Len() > markerWindowSize*2 {
				tail := window.Bytes()[window.Len()-markerWindowSize:]
				window.Reset()
				window.Write(tail)
			}
			if strings.Contains(stripANSI(window.String()), marker) {
				completed = true
				break
			}
		}
		if readErr != nil {
			break
		}
	}

	// Clean shutdown sequence:
	//  1. Close the PTY master. This causes:
	//     - The io.Copy(ptmx, os.Stdin) goroutine to return (write to closed fd).
	//     - The child process to receive SIGHUP.
	//  2. Kill the child process to be certain it is dead.
	//  3. Wait to reap the zombie. With the PTY closed, this returns promptly.
	closePTY()
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
	_ = cmd.Wait()

	if completed {
		fmt.Printf("\r\nSetup complete! Open dashboard: obol openclaw dashboard %s\r\n", id)
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

// deploymentPath returns the path to a deployment directory
func deploymentPath(cfg *config.Config, id string) string {
	return filepath.Join(cfg.ConfigDir, "applications", appName, id)
}

// copyEmbeddedChart extracts the embedded chart FS to destDir
func copyEmbeddedChart(destDir string) error {
	return fs.WalkDir(chartFS, "chart", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "chart" {
			return nil
		}

		relPath := strings.TrimPrefix(path, "chart/")
		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		data, err := chartFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read embedded %s: %w", path, err)
		}
		return os.WriteFile(destPath, data, 0644)
	})
}

// generateOverlayValues creates the Obol Stack-specific values overlay.
// If imported is non-nil, provider/channel config from the import is used
// instead of the default Ollama configuration.
func generateOverlayValues(hostname string, imported *ImportResult) string {
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
		b.WriteString(importedOverlay)
	} else {
		b.WriteString(`# Route agent traffic to in-cluster Ollama
openclaw:
  agentModel: ollama/glm-4.7-flash

# Default model provider: in-cluster Ollama
models:
  ollama:
    enabled: true
    baseUrl: http://ollama.llm.svc.cluster.local:11434/v1
    api: openai-completions
    apiKeyEnvVar: OLLAMA_API_KEY
    apiKeyValue: ollama-local
    models:
      - id: glm-4.7-flash
        name: GLM-4.7 Flash

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

	return b.String()
}

// interactiveSetup prompts the user for provider configuration.
// If imported is non-nil, offers to use the detected config.
func interactiveSetup(imported *ImportResult) (*ImportResult, error) {
	reader := bufio.NewReader(os.Stdin)

	if imported != nil {
		fmt.Print("\nUse detected configuration? [Y/n]: ")
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "" || line == "y" || line == "yes" {
			fmt.Println("Using detected configuration.")
			return imported, nil
		}
	}

	fmt.Println("\nSelect a model provider:")
	fmt.Println("  [1] Ollama (default, runs in-cluster)")
	fmt.Println("  [2] OpenAI")
	fmt.Println("  [3] Anthropic")
	fmt.Print("\nChoice [1]: ")

	line, _ := reader.ReadString('\n')
	choice := strings.TrimSpace(line)
	if choice == "" {
		choice = "1"
	}

	switch choice {
	case "1":
		// Ollama defaults — return nil so generateOverlayValues uses built-in defaults
		fmt.Println("Using Ollama (in-cluster) as default provider.")
		return nil, nil
	case "2":
		return promptForProvider(reader, "openai", "OpenAI", "https://api.openai.com/v1", "", "gpt-4o", "GPT-4o")
	case "3":
		return promptForProvider(reader, "anthropic", "Anthropic", "https://api.anthropic.com/v1", "anthropic", "claude-sonnet-4-5-20250929", "Claude Sonnet 4.5")
	default:
		fmt.Printf("Unknown choice '%s', using Ollama defaults.\n", choice)
		return nil, nil
	}
}

// promptForProvider asks for an API key and builds an ImportResult for a single provider
func promptForProvider(reader *bufio.Reader, name, display, baseURL, api, modelID, modelName string) (*ImportResult, error) {
	fmt.Printf("\n%s API key: ", display)
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("%s API key is required", display)
	}

	agentModel := fmt.Sprintf("%s/%s", name, modelID)

	return &ImportResult{
		AgentModel: agentModel,
		Providers: []ImportedProvider{
			{
				Name:    name,
				BaseURL: baseURL,
				API:     api,
				APIKey:  apiKey,
				Models: []ImportedModel{
					{ID: modelID, Name: modelName},
				},
			},
		},
	}, nil
}

// generateHelmfile creates a helmfile.yaml referencing the local chart
func generateHelmfile(id, namespace string) string {
	return fmt.Sprintf(`# OpenClaw instance: %s
# Managed by obol openclaw

releases:
  - name: openclaw
    namespace: %s
    createNamespace: true
    chart: ./chart
    values:
      - values.yaml
      - values-obol.yaml
`, id, namespace)
}
