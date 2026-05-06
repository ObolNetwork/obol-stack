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
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

const (
	tunnelNamespace     = "traefik"
	tunnelLabelSelector = "app.kubernetes.io/name=cloudflared"

	// cloudflared-tunnel-token is created by `obol tunnel provision`.
	tunnelTokenSecretName = "cloudflared-tunnel-token"
	tunnelTokenSecretKey  = "TUNNEL_TOKEN"
)

// tunnelStatusResult is the JSON-serialisable result for `tunnel status`.
type tunnelStatusResult struct {
	Mode              string `json:"mode"`
	ExposureMode      string `json:"exposure_mode,omitempty"`
	ManagementMode    string `json:"management_mode,omitempty"`
	Status            string `json:"status"`
	URL               string `json:"url"`
	Hostname          string `json:"hostname,omitempty"`
	DesiredReplicas   int    `json:"desired_replicas,omitempty"`
	ReadyReplicas     int    `json:"ready_replicas,omitempty"`
	AvailableReplicas int    `json:"available_replicas,omitempty"`
	PodStatus         string `json:"pod_status,omitempty"`
	ConnectorStatus   string `json:"connector_status,omitempty"`
	ActiveConnections int    `json:"active_connections,omitempty"`
	LastUpdated       string `json:"last_updated"`
}

type deploymentReplicaStatus struct {
	Spec struct {
		Replicas *int32 `json:"replicas,omitempty"`
	} `json:"spec"`
	Status struct {
		Replicas          int32 `json:"replicas,omitempty"`
		ReadyReplicas     int32 `json:"readyReplicas,omitempty"`
		AvailableReplicas int32 `json:"availableReplicas,omitempty"`
	} `json:"status"`
}

type podStatusList struct {
	Items []struct {
		Status struct {
			Phase      string `json:"phase"`
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

type tunnelRuntimeHealth struct {
	DesiredReplicas   int
	ReadyReplicas     int
	AvailableReplicas int
	PodStatus         string
}

// Status displays the current tunnel status and URL.
func Status(cfg *config.Config, u *ui.UI) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("stack not running, use 'obol stack up' first")
	}

	now := time.Now()
	st, _ := loadTunnelState(cfg)
	mode, url := tunnelModeAndURL(st)
	result := tunnelStatusResult{
		Mode:            mode,
		ExposureMode:    tunnelExposureQuick,
		ManagementMode:  tunnelManagementQuick,
		URL:             url,
		DesiredReplicas: desiredRuntimeReplicas(st),
		LastUpdated:     now.Format(time.RFC3339),
	}
	if st != nil {
		result.ExposureMode = st.ExposureMode
		result.ManagementMode = st.Management()
		result.Hostname = st.Hostname
	}
	if result.ExposureMode == "" {
		result.ExposureMode = tunnelExposureQuick
	}
	if result.ManagementMode == "" {
		result.ManagementMode = tunnelManagementQuick
	}

	runtime, err := getTunnelRuntimeHealth(kubectlPath, kubeconfigPath)
	if err != nil {
		if mode == tunnelExposureQuick {
			result.Status = "dormant"
			result.URL = "(activates on 'obol sell')"
		} else {
			result.Status = "not running"
		}
		if u.IsJSON() {
			return u.JSON(result)
		}
		printDetailedStatusBox(u, result, now)
		u.Blank()
		if mode == tunnelExposureQuick {
			u.Print("The tunnel will start automatically when you sell a service.")
			u.Print("  Start manually: obol tunnel restart")
			u.Print("  Persistent URL: obol tunnel setup --hostname stack.example.com")
		} else {
			u.Print("Troubleshooting:")
			u.Print("  - Start the stack: obol stack up")
			u.Print("  - Restore persistent tunnel resources: obol tunnel restart")
		}
		return nil
	}

	result.DesiredReplicas = runtime.DesiredReplicas
	result.ReadyReplicas = runtime.ReadyReplicas
	result.AvailableReplicas = runtime.AvailableReplicas
	result.PodStatus = runtime.PodStatus

	if mode == tunnelExposureQuick {
		tunnelURL, quickErr := GetTunnelURL(cfg)
		if quickErr == nil {
			result.URL = tunnelURL
		} else {
			result.URL = "(not available)"
		}
	} else if result.URL == "" && result.Hostname != "" {
		result.URL = "https://" + result.Hostname
	}

	if st != nil && st.Management() == tunnelManagementRemote && st.AccountID != "" && st.TunnelID != "" {
		result.ConnectorStatus = "unknown"
		if apiToken := strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN")); apiToken != "" {
			if tunnelInfo, tunnelErr := newCloudflareClient(apiToken).GetTunnel(st.AccountID, st.TunnelID); tunnelErr == nil {
				result.ActiveConnections = len(tunnelInfo.Connections)
				if result.ActiveConnections > 0 {
					result.ConnectorStatus = "connected"
				} else {
					result.ConnectorStatus = "waiting_for_connections"
				}
			}
		}
	} else if st != nil && st.IsPersistent() {
		result.ConnectorStatus = "managed-locally"
	}

	result.Status = summarizeTunnelStatus(result)
	if u.IsJSON() {
		return u.JSON(result)
	}

	printDetailedStatusBox(u, result, now)
	if result.URL != "" && result.URL != "(not available)" && result.URL != "(activates on 'obol sell')" {
		u.Printf("Test with: curl %s/", result.URL)
		if err := InjectBaseURL(cfg, result.URL); err == nil {
			u.Dim("Agent base URL updated to " + result.URL)
		}
		if err := SyncTunnelConfigMap(cfg, result.URL); err != nil {
			u.Dim("Could not sync tunnel URL to frontend ConfigMap: " + err.Error())
		}
	}
	if result.Status != "active" {
		u.Blank()
		u.Print("Troubleshooting:")
		u.Print("  - Check logs: obol tunnel logs")
		u.Print("  - Restart tunnel: obol tunnel restart")
	}

	return nil
}

func summarizeTunnelStatus(result tunnelStatusResult) string {
	if result.DesiredReplicas == 0 {
		if result.Mode == tunnelExposureQuick {
			return "dormant"
		}
		return "stopped"
	}
	if result.ReadyReplicas == 0 {
		return "starting"
	}
	if result.ReadyReplicas < result.DesiredReplicas || result.AvailableReplicas < result.DesiredReplicas {
		return "degraded"
	}
	if result.Mode == tunnelExposureQuick && (result.URL == "" || result.URL == "(not available)") {
		return "starting"
	}
	if result.ManagementMode == tunnelManagementRemote && result.ConnectorStatus == "waiting_for_connections" {
		return "degraded"
	}
	return "active"
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

// EnsureRunning scales the cloudflared deployment to the desired replica count,
// waits for the deployment to be ready, and returns the tunnel URL once available.
func EnsureRunning(cfg *config.Config, u *ui.UI) (string, error) {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return "", errors.New("stack not running")
	}

	st, _ := loadTunnelState(cfg)
	desiredReplicas := desiredRuntimeReplicas(st)
	if runtime, err := getTunnelRuntimeHealth(kubectlPath, kubeconfigPath); err == nil && runtime.ReadyReplicas >= desiredReplicas && runtime.AvailableReplicas >= desiredReplicas {
		return currentTunnelURL(cfg, st)
	}

	scaleCmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"scale", "deployment/cloudflared",
		"-n", tunnelNamespace,
		fmt.Sprintf("--replicas=%d", desiredReplicas),
	)
	if err := scaleCmd.Run(); err != nil {
		return "", fmt.Errorf("failed to scale cloudflared: %w", err)
	}

	waitCmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"rollout", "status", "deployment/cloudflared",
		"-n", tunnelNamespace,
		"--timeout=90s",
	)
	if err := u.Exec(ui.ExecConfig{
		Name: "Starting Cloudflare tunnel",
		Cmd:  waitCmd,
	}); err != nil {
		return "", fmt.Errorf("cloudflared rollout failed: %w", err)
	}

	tunnelURL, err := currentTunnelURL(cfg, st)
	if err != nil {
		return "", err
	}
	if err := InjectBaseURL(cfg, tunnelURL); err == nil {
		u.Dim("Agent base URL updated to " + tunnelURL)
	}
	if err := SyncTunnelConfigMap(cfg, tunnelURL); err != nil {
		u.Dim("Could not sync tunnel URL to frontend ConfigMap: " + err.Error())
	}
	if err := CreateStorefront(cfg, tunnelURL); err != nil {
		u.Dim("Could not create storefront: " + err.Error())
	}

	return tunnelURL, nil
}

func currentTunnelURL(cfg *config.Config, st *tunnelState) (string, error) {
	mode, url := tunnelModeAndURL(st)
	if mode != tunnelExposureQuick {
		if url == "" {
			return "", errors.New("persistent tunnel hostname is not configured")
		}
		return url, nil
	}

	var tunnelURL string
	for range 20 {
		time.Sleep(time.Second)
		if url, err := GetTunnelURL(cfg); err == nil {
			tunnelURL = url
			break
		}
	}
	if tunnelURL == "" {
		return "", errors.New("tunnel started but URL not available yet — run 'obol tunnel status' in a few seconds")
	}
	return tunnelURL, nil
}

// Restart restarts the cloudflared deployment.
func Restart(cfg *config.Config, u *ui.UI) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Check if kubeconfig exists.
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("stack not running, use 'obol stack up' first")
	}

	st, _ := loadTunnelState(cfg)
	if st != nil && st.IsPersistent() {
		if err := RestorePersistentResources(cfg, u); err != nil {
			return fmt.Errorf("restore persistent tunnel resources: %w", err)
		}
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

	u.Blank()
	if st != nil && st.IsPersistent() {
		u.Print("Persistent tunnel resources restored and connector restarted.")
		u.Print("Run 'obol tunnel status' to verify the hostname is active.")
	} else {
		u.Print("Tunnel restarting...")
		u.Print("Run 'obol tunnel status' to see the URL once ready (may take 10-30 seconds).")
	}

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
	runtime, err := getTunnelRuntimeHealth(kubectlPath, kubeconfigPath)
	if err != nil {
		return "", err
	}
	if runtime.PodStatus == "" {
		return "", errors.New("no pods found")
	}
	return runtime.PodStatus, nil
}

func getTunnelRuntimeHealth(kubectlPath, kubeconfigPath string) (*tunnelRuntimeHealth, error) {
	depCmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"get", "deployment/cloudflared",
		"-n", tunnelNamespace,
		"-o", "json",
	)
	depOut, err := depCmd.Output()
	if err != nil {
		return nil, err
	}

	var deployment deploymentReplicaStatus
	if err := json.Unmarshal(depOut, &deployment); err != nil {
		return nil, fmt.Errorf("parse cloudflared deployment status: %w", err)
	}

	podsCmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"get", "pods", "-n", tunnelNamespace,
		"-l", tunnelLabelSelector,
		"-o", "json",
	)
	podsOut, err := podsCmd.Output()
	if err != nil {
		return nil, err
	}

	var pods podStatusList
	if err := json.Unmarshal(podsOut, &pods); err != nil {
		return nil, fmt.Errorf("parse cloudflared pod status: %w", err)
	}

	desiredReplicas := int(deployment.Status.Replicas)
	if deployment.Spec.Replicas != nil {
		desiredReplicas = int(*deployment.Spec.Replicas)
	}

	phases := make([]string, 0, len(pods.Items))
	for _, pod := range pods.Items {
		phase := strings.ToLower(strings.TrimSpace(pod.Status.Phase))
		if phase == "" {
			phase = "unknown"
		}
		phases = append(phases, phase)
	}
	podStatus := ""
	if len(phases) > 0 {
		podStatus = fmt.Sprintf("%d pod(s): %s", len(phases), strings.Join(phases, ","))
	}

	return &tunnelRuntimeHealth{
		DesiredReplicas:   desiredReplicas,
		ReadyReplicas:     int(deployment.Status.ReadyReplicas),
		AvailableReplicas: int(deployment.Status.AvailableReplicas),
		PodStatus:         podStatus,
	}, nil
}

func printDetailedStatusBox(u *ui.UI, result tunnelStatusResult, lastUpdated time.Time) {
	u.Blank()
	u.Bold("Cloudflare Tunnel Status")
	u.Print(strings.Repeat("─", 50))
	u.Detail("Mode", result.Mode)
	if result.ManagementMode != "" {
		u.Detail("Management", result.ManagementMode)
	}
	u.Detail("Status", result.Status)
	if result.Hostname != "" {
		u.Detail("Hostname", result.Hostname)
	}
	u.Detail("URL", result.URL)
	if result.DesiredReplicas > 0 || result.ReadyReplicas > 0 {
		u.Detail("Replicas", fmt.Sprintf("%d ready / %d desired", result.ReadyReplicas, result.DesiredReplicas))
	}
	if result.PodStatus != "" {
		u.Detail("Pods", result.PodStatus)
	}
	if result.ConnectorStatus != "" {
		connector := result.ConnectorStatus
		if result.ActiveConnections > 0 {
			connector = fmt.Sprintf("%s (%d active)", connector, result.ActiveConnections)
		}
		u.Detail("Connectors", connector)
	}
	u.Detail("Last Updated", lastUpdated.Format(time.RFC3339))
	u.Print(strings.Repeat("─", 50))
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

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Obol Stack</title>
  <style>
    body { font-family: system-ui, sans-serif; max-width: 640px; margin: 2rem auto; padding: 0 1rem; color: #1a1a1a; }
    a { color: #0066cc; }
    code { background: #f0f0f0; padding: 0.2em 0.4em; border-radius: 3px; }
    .services { margin-top: 1.5rem; }
  </style>
</head>
<body>
  <h1>Obol Stack</h1>
  <p>This node sells services via <a href="https://www.x402.org">x402</a> micropayments.</p>
  <div class="services">
    <h2>Available Services</h2>
    <p>See the machine-readable catalog: <a href="%s/skill.md">/skill.md</a></p>
    <p>Agent registration: <a href="%s/.well-known/agent-registration.json">/.well-known/agent-registration.json</a></p>
  </div>
</body>
</html>`, tunnelURL, tunnelURL)

	// Build the resources as a multi-document YAML manifest.
	resources := []map[string]any{
		// ConfigMap with HTML content + httpd mime config.
		{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":      "tunnel-storefront",
				"namespace": storefrontNamespace,
			},
			"data": map[string]string{
				"index.html": html,
				"httpd.conf": "",
				"mime.types": "text/html\thtml htm\n",
			},
		},
		// Deployment: busybox httpd serving the ConfigMap.
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
					"matchLabels": map[string]string{"app": "tunnel-storefront"},
				},
				"template": map[string]any{
					"metadata": map[string]any{
						"labels": map[string]string{"app": "tunnel-storefront"},
					},
					"spec": map[string]any{
						"containers": []map[string]any{
							{
								"name":    "httpd",
								"image":   "busybox:1.37",
								"command": []string{"httpd", "-f", "-p", "8080", "-h", "/www"},
								"ports": []map[string]any{
									{"containerPort": 8080},
								},
								"volumeMounts": []map[string]any{
									{"name": "html", "mountPath": "/www"},
								},
								"resources": map[string]any{
									"requests": map[string]string{"cpu": "5m", "memory": "8Mi"},
									"limits":   map[string]string{"cpu": "20m", "memory": "16Mi"},
								},
							},
						},
						"volumes": []map[string]any{
							{
								"name": "html",
								"configMap": map[string]any{
									"name": "tunnel-storefront",
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
				"selector": map[string]string{"app": "tunnel-storefront"},
				"ports": []map[string]any{
					{"port": 8080, "targetPort": 8080},
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
								"port": 8080,
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
	if url := re.FindString(logs); url != "" {
		return url, true
	}

	return "", false
}
