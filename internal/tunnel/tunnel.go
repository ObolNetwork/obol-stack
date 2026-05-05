package tunnel

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/images"
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
// tunnelStatusResult is the JSON-serialisable result for `tunnel status`.
type tunnelStatusResult struct {
	Mode        string `json:"mode"`
	Status      string `json:"status"`
	URL         string `json:"url"`
	LastUpdated string `json:"last_updated"`
}

// Status displays the current tunnel status and URL.
func Status(cfg *config.Config, u *ui.UI) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Check if kubeconfig exists.
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("stack not running, use 'obol stack up' first")
	}

	st, _ := loadTunnelState(cfg)

	// Check pod status first.
	podStatus, err := getPodStatus(kubectlPath, kubeconfigPath)
	if err != nil {
		mode, url := tunnelModeAndURL(st)
		if mode == "quick" {
			// Quick tunnel is dormant — activates on first `obol sell`.
			if u.IsJSON() {
				return u.JSON(tunnelStatusResult{Mode: "quick", Status: "dormant", URL: "(activates on 'obol sell')", LastUpdated: time.Now().Format(time.RFC3339)})
			}
			printStatusBox(u, "quick", "dormant", "(activates on 'obol sell')", time.Now())
			u.Blank()
			u.Print("The tunnel will start automatically when you sell a service.")
			u.Print("  Start manually: obol tunnel restart")
			u.Print("  Persistent URL: obol tunnel login --hostname stack.example.com")

			return nil
		}
		if u.IsJSON() {
			return u.JSON(tunnelStatusResult{Mode: mode, Status: "not running", URL: url, LastUpdated: time.Now().Format(time.RFC3339)})
		}
		printStatusBox(u, mode, "not running", url, time.Now())
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
			if u.IsJSON() {
				return u.JSON(tunnelStatusResult{Mode: mode, Status: podStatus, URL: "(not available)", LastUpdated: time.Now().Format(time.RFC3339)})
			}
			printStatusBox(u, mode, podStatus, "(not available)", time.Now())
			u.Blank()
			u.Print("Troubleshooting:")
			u.Print("  - Check logs: obol tunnel logs")
			u.Print("  - Restart tunnel: obol tunnel restart")

			return nil //nolint:nilerr // URL unavailable is a display-only issue; show troubleshooting hints instead
		}

		url = tunnelURL
	}

	if u.IsJSON() {
		return u.JSON(tunnelStatusResult{Mode: mode, Status: statusLabel, URL: url, LastUpdated: time.Now().Format(time.RFC3339)})
	}

	printStatusBox(u, mode, statusLabel, url, time.Now())
	u.Printf("Test with: curl %s/", url)

	// Auto-inject tunnel URL into obol-agent so registration JSON uses it.
	if url != "" && url != "(not available)" {
		if err := InjectBaseURL(cfg, url); err == nil {
			u.Dim("Agent base URL updated to " + url)
		}
		// Write tunnel URL to ConfigMap so the frontend can read it.
		if err := SyncTunnelConfigMap(cfg, url); err != nil {
			u.Dim("Could not sync tunnel URL to frontend ConfigMap: " + err.Error())
		}
	}

	return nil
}

// InjectBaseURL sets AGENT_BASE_URL on the default Hermes deployment so that
// monetize.py uses the tunnel URL in registration JSON.
func InjectBaseURL(cfg *config.Config, tunnelURL string) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	desc := agentruntime.Describe(agentruntime.Hermes)

	cmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"set", "env", "deployment/"+desc.ServiceName,
		"-n", agentruntime.Namespace(agentruntime.Hermes, agentruntime.DefaultInstanceID),
		"AGENT_BASE_URL="+strings.TrimRight(tunnelURL, "/"),
	)

	return cmd.Run()
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

	return "", errors.New("tunnel URL not found in logs")
}

// defaultWaitReadyTimeout is the upper-bound budget for both the cloudflared
// rollout and the trycloudflare URL appearing in pod logs. Override with
// FLOW_TUNNEL_TIMEOUT (a duration like "90s" or a positive integer of seconds).
const defaultWaitReadyTimeout = 5 * time.Minute

// waitReadyTimeout returns the configured WaitReady budget, honouring the
// FLOW_TUNNEL_TIMEOUT environment variable. Falls back to defaultWaitReadyTimeout
// when unset or unparseable.
func waitReadyTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("FLOW_TUNNEL_TIMEOUT"))
	if raw == "" {
		return defaultWaitReadyTimeout
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return defaultWaitReadyTimeout
}

// WaitReady scales the cloudflared deployment up if needed, then polls until
// BOTH the deployment rollout is complete AND a public *.trycloudflare.com URL
// has been captured from the pod logs. The budget is bounded by
// waitReadyTimeout (defaultWaitReadyTimeout / FLOW_TUNNEL_TIMEOUT). On timeout
// it returns an error that names both subjects (deployment + URL) so callers
// can distinguish a half-baked tunnel from a missing one.
//
// Side effects on success: injects AGENT_BASE_URL into the agent deployment and
// writes the tunnel URL to the obol-frontend ConfigMap consumed by the
// serviceoffer-controller for ERC-8004 registration metadata.
func WaitReady(cfg *config.Config, u *ui.UI) (string, error) {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return "", errors.New("stack not running")
	}

	// Fast path: tunnel pod is already running and exposing a URL.
	if podStatus, err := getPodStatus(kubectlPath, kubeconfigPath); err == nil && podStatus == "running" {
		if url, err := GetTunnelURL(cfg); err == nil {
			return url, nil
		}
	}

	// Scale to 1 replica (idempotent).
	scaleCmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"scale", "deployment/cloudflared",
		"-n", tunnelNamespace,
		"--replicas=1",
	)
	if err := scaleCmd.Run(); err != nil {
		return "", fmt.Errorf("failed to scale cloudflared: %w", err)
	}

	totalBudget := waitReadyTimeout()
	deadline := time.Now().Add(totalBudget)

	// Stage 1: wait for the deployment rollout.
	rolloutTimeout := totalBudget
	if rolloutTimeout > 5*time.Minute {
		rolloutTimeout = 5 * time.Minute
	}
	waitCmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"rollout", "status", "deployment/cloudflared",
		"-n", tunnelNamespace,
		fmt.Sprintf("--timeout=%ds", int(rolloutTimeout.Seconds())),
	)
	rolloutErr := u.Exec(ui.ExecConfig{
		Name: "Waiting for cloudflared rollout",
		Cmd:  waitCmd,
	})

	// Stage 2: poll the tunnel pod logs for the trycloudflare URL until the
	// remaining budget runs out. Even when the rollout above failed, we still
	// give the URL probe a brief grace window in case the pod is up but the
	// rollout watcher returned spuriously.
	var tunnelURL string
	for time.Now().Before(deadline) {
		if url, err := GetTunnelURL(cfg); err == nil && strings.HasPrefix(url, "https://") {
			tunnelURL = url
			break
		}
		time.Sleep(5 * time.Second)
	}

	if tunnelURL == "" {
		if rolloutErr != nil {
			return "", fmt.Errorf("cloudflared not ready within %s: deployment rollout failed (%w) and no public *.trycloudflare.com URL captured", totalBudget, rolloutErr)
		}
		return "", fmt.Errorf("cloudflared not ready within %s: deployment is rolled out but no public *.trycloudflare.com URL captured from pod logs", totalBudget)
	}

	// URL captured: propagate to agent + frontend ConfigMap. Best-effort.
	if err := InjectBaseURL(cfg, tunnelURL); err == nil {
		u.Dim("Agent base URL updated to " + tunnelURL)
	}
	if err := SyncTunnelConfigMap(cfg, tunnelURL); err != nil {
		u.Dim("Could not sync tunnel URL to frontend ConfigMap: " + err.Error())
	}

	return tunnelURL, nil
}

// EnsureRunning is the historical alias for WaitReady. New callers should
// prefer WaitReady directly; this is kept so existing call sites compile
// unchanged.
func EnsureRunning(cfg *config.Config, u *ui.UI) (string, error) {
	return WaitReady(cfg, u)
}

// ConfirmQuickTunnelLoss warns the user when a destructive action is about to
// invalidate an active quick tunnel URL, and asks whether to proceed. Returns
// true when the caller should continue.
//
// Quick tunnels get a fresh *.trycloudflare.com URL on every cluster recreate
// or `obol tunnel restart`, so anyone who bookmarked or registered the old URL
// will see 530 errors until they re-discover via /skill.md. Persistent (DNS)
// tunnels are stable across these events and skip the warning.
//
// Pass currentURL as discovered from the running cloudflared pod (or "" when
// none). In non-interactive sessions, Confirm returns its default (true), so
// automation and CI flows print the warning but do not block.
func ConfirmQuickTunnelLoss(cfg *config.Config, u *ui.UI, currentURL, action string) bool {
	if st, _ := loadTunnelState(cfg); st != nil && st.Hostname != "" {
		return true
	}

	if currentURL == "" {
		return true
	}

	u.Blank()
	u.Warnf("Quick tunnel URL will be invalidated: %s", currentURL)
	u.Dim(fmt.Sprintf("  After `%s`, the next `obol sell http` brings up a fresh URL.", action))
	u.Dim("  Buyers using the old URL will see 530 errors.")
	u.Dim("  For a permanent URL: obol tunnel login --hostname stack.example.com")

	return u.Confirm("Continue?", true)
}

// Restart restarts the cloudflared deployment and propagates the new tunnel
// URL to dependent resources (obol-stack-config ConfigMap, agent overlay,
// storefront HTTPRoute hostname pin). Quick tunnels get a new URL on every
// restart, so dependents must be refreshed or sell flows break:
//   - skill.md / services.json embed the stale base URL until the controller
//     observes the ConfigMap change
//   - the storefront HTTPRoute is hostname-pinned; without an update it points
//     at the old tunnel hostname and traffic to the new hostname's `/` falls
//     through to the frontend catch-all
func Restart(cfg *config.Config, u *ui.UI) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Check if kubeconfig exists.
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("stack not running, use 'obol stack up' first")
	}

	currentURL, _ := GetTunnelURL(cfg)
	if !ConfirmQuickTunnelLoss(cfg, u, currentURL, "obol tunnel restart") {
		u.Info("Aborted.")

		return nil
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

	// Wait for the rollout to complete BEFORE asking for the URL. Otherwise
	// WaitReady's fast path may pick up the OLD pod's logs (still running
	// during the rolling update) and return the stale URL.
	rolloutCmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"rollout", "status", "deployment/cloudflared",
		"-n", tunnelNamespace,
		"--timeout=120s",
	)
	if err := u.Exec(ui.ExecConfig{
		Name: "Waiting for new cloudflared pod",
		Cmd:  rolloutCmd,
	}); err != nil {
		return fmt.Errorf("rollout did not complete: %w", err)
	}

	// Capture the new URL and update everything that needs the base URL.
	// WaitReady also calls InjectBaseURL + SyncTunnelConfigMap.
	newURL, err := WaitReady(cfg, u)
	if err != nil {
		return fmt.Errorf("tunnel restarted but new URL not captured: %w", err)
	}

	// Refresh the storefront HTTPRoute (hostname-pinned to the tunnel domain).
	if err := CreateStorefront(cfg, newURL); err != nil {
		u.Warnf("could not refresh storefront for new URL: %v", err)
	}

	u.Blank()
	u.Successf("Tunnel restarted: %s", newURL)
	u.Dim("  /skill.md, /api/services.json, and the storefront now reflect the new URL.")

	return nil
}

// Logs displays cloudflared logs.
func Logs(cfg *config.Config, follow bool) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Check if kubeconfig exists.
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("stack not running, use 'obol stack up' first")
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
		return "", errors.New("no pods found")
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

// SyncTunnelConfigMap creates or patches the obol-stack-config ConfigMap in the
// obol-frontend namespace with the current tunnel URL. The frontend reads this
// ConfigMap to construct the correct dashboard URL.
func SyncTunnelConfigMap(cfg *config.Config, tunnelURL string) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Ensure the namespace exists (non-fatal if it doesn't).
	_ = exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"create", "namespace", "obol-frontend",
		"--dry-run=client", "-o", "yaml",
	).Run()

	manifest := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: obol-stack-config
  namespace: obol-frontend
data:
  tunnelURL: %s
`, strings.TrimRight(tunnelURL, "/"))

	cmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"apply", "-f", "-",
	)

	cmd.Stdin = strings.NewReader(manifest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return nil
}

// EnsureTunnelForSell ensures the tunnel is running and propagates the URL to
// the public service discovery surfaces needed by seller flows. It updates the
// frontend ConfigMap and storefront, but deliberately avoids syncing the
// obol-agent overlay. The agent overlay should be updated by explicit tunnel
// provisioning/login flows, not every ServiceOffer mutation.
func EnsureTunnelForSell(cfg *config.Config, u *ui.UI) (string, error) {
	tunnelURL, err := EnsureRunning(cfg, u)
	if err != nil {
		return "", err
	}
	// EnsureRunning already calls InjectBaseURL + SyncTunnelConfigMap.
	// Create the storefront landing page for the tunnel hostname.
	if err := CreateStorefront(cfg, tunnelURL); err != nil {
		u.Warnf("could not create storefront: %v", err)
	}

	return tunnelURL, nil
}

// Stop scales the cloudflared deployment to 0 replicas.
func Stop(cfg *config.Config, u *ui.UI) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return nil // stack not running, nothing to stop
	}

	cmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"scale", "deployment/cloudflared",
		"-n", tunnelNamespace,
		"--replicas=0",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to scale cloudflared to 0: %w: %s", err, strings.TrimSpace(string(out)))
	}

	u.Success("Tunnel stopped")

	return nil
}

// storefrontNamespace is where the storefront landing page resources live.
const storefrontNamespace = "traefik"

// CreateStorefront creates (or updates) a simple HTML landing page served at
// the tunnel hostname's root path. This uses the same busybox-httpd + ConfigMap
// pattern as the .well-known registration in monetize.py.
func CreateStorefront(cfg *config.Config, tunnelURL string) error {
	parsed, err := url.Parse(tunnelURL)
	if err != nil {
		return fmt.Errorf("invalid tunnel URL: %w", err)
	}

	hostname := parsed.Hostname()

	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	labels := map[string]string{"app": "tunnel-storefront"}

	// Build the resources for the public storefront.
	resources := []map[string]any{
		// Deployment: Next.js public storefront image.
		{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name":      "tunnel-storefront",
				"namespace": storefrontNamespace,
			},
			"spec": map[string]any{
				"replicas": 1,
				"selector": map[string]any{
					"matchLabels": labels,
				},
				"template": map[string]any{
					"metadata": map[string]any{
						"labels": labels,
					},
					"spec": map[string]any{
						"containers": []map[string]any{
							{
								"name":            "storefront",
								"image":           images.Resolve("ghcr.io/obolnetwork/obol-stack-public-storefront"),
								"imagePullPolicy": "IfNotPresent",
								"ports": []map[string]any{
									{"containerPort": 3000, "name": "http"},
								},
								"env": []map[string]string{
									{"name": "SERVICES_URL", "value": "http://obol-skill-md.x402.svc:8080"},
								},
								// Next.js SSR `/` cold renders can take >1s (the
								// implicit livenessProbe timeoutSeconds default).
								// Use a startupProbe to absorb the warm-up window
								// and only flip liveness on once the app is up,
								// then keep liveness loose enough that a slow SSR
								// doesn't kill an otherwise-healthy pod.
								"startupProbe": map[string]any{
									"httpGet": map[string]any{
										"path": "/",
										"port": "http",
									},
									"periodSeconds":    5,
									"failureThreshold": 30, // up to 150s to warm
									"timeoutSeconds":   5,
								},
								"livenessProbe": map[string]any{
									"httpGet": map[string]any{
										"path": "/",
										"port": "http",
									},
									"periodSeconds":    30,
									"timeoutSeconds":   5,
									"failureThreshold": 3,
								},
								"resources": map[string]any{
									"requests": map[string]string{"cpu": "10m", "memory": "32Mi"},
									"limits":   map[string]string{"cpu": "100m", "memory": "128Mi"},
								},
							},
						},
					},
				},
			},
		},
		// Service
		{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]any{
				"name":      "tunnel-storefront",
				"namespace": storefrontNamespace,
			},
			"spec": map[string]any{
				"selector": labels,
				"ports": []map[string]any{
					{"port": 3000, "targetPort": 3000, "name": "http"},
				},
			},
		},
		// HTTPRoute: tunnel hostname → storefront (more specific than frontend catch-all).
		{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata": map[string]any{
				"name":      "tunnel-storefront",
				"namespace": storefrontNamespace,
			},
			"spec": map[string]any{
				"hostnames": []string{hostname},
				"parentRefs": []map[string]any{
					{
						"name":        "traefik-gateway",
						"namespace":   "traefik",
						"sectionName": "web",
					},
				},
				"rules": []map[string]any{
					{
						"matches": []map[string]any{
							{"path": map[string]string{"type": "PathPrefix", "value": "/"}},
						},
						"backendRefs": []map[string]any{
							{
								"name": "tunnel-storefront",
								"port": 3000,
							},
						},
					},
				},
			},
		},
	}

	// Apply each resource via kubectl apply.
	for _, res := range resources {
		data, err := json.Marshal(res)
		if err != nil {
			return fmt.Errorf("failed to marshal resource: %w", err)
		}

		cmd := exec.Command(kubectlPath,
			"--kubeconfig", kubeconfigPath,
			"apply", "-f", "-",
		)

		cmd.Stdin = strings.NewReader(string(data))
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to apply storefront resource: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}

	return nil
}

// DeleteStorefront removes the storefront landing page resources.
func DeleteStorefront(cfg *config.Config) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return nil
	}

	for _, resource := range []string{
		"httproute/tunnel-storefront",
		"service/tunnel-storefront",
		"deployment/tunnel-storefront",
		"configmap/tunnel-storefront",
	} {
		cmd := exec.Command(kubectlPath,
			"--kubeconfig", kubeconfigPath,
			"delete", resource,
			"-n", storefrontNamespace,
			"--ignore-not-found",
		)
		_ = cmd.Run() // best-effort cleanup
	}

	return nil
}

func parseQuickTunnelURL(logs string) (string, bool) {
	// Quick tunnel logs print a random *.trycloudflare.com URL.
	re := regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
	matches := re.FindAllString(logs, -1)
	if len(matches) > 0 {
		return matches[len(matches)-1], true
	}

	return "", false
}
