package tunnel

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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
	"github.com/ObolNetwork/obol-stack/internal/version"
)

const (
	tunnelNamespace     = "traefik"
	tunnelLabelSelector = "app.kubernetes.io/name=cloudflared"

	// cloudflared-tunnel-token is created by `obol tunnel setup` (connector token).
	tunnelTokenSecretName = "cloudflared-tunnel-token"
	tunnelTokenSecretKey  = "TUNNEL_TOKEN"
)

// tunnelStatusResult is the JSON-serialisable result for `tunnel status`.
type tunnelStatusResult struct {
	Mode              string `json:"mode"`
	DisplayMode       string `json:"display_mode,omitempty"`
	ExposureMode      string `json:"exposure_mode,omitempty"`
	ManagementMode    string `json:"management_mode,omitempty"`
	TransportProtocol string `json:"transport_protocol,omitempty"`
	Status            string `json:"status"`
	URL               string `json:"url"`
	Hostname          string `json:"hostname,omitempty"`
	DesiredReplicas   int    `json:"desired_replicas,omitempty"`
	ReadyReplicas     int    `json:"ready_replicas,omitempty"`
	AvailableReplicas int    `json:"available_replicas,omitempty"`
	PodStatus         string `json:"pod_status,omitempty"`
	Uptime            string `json:"uptime,omitempty"`
	ConnectorStatus   string `json:"connector_status,omitempty"`
	ActiveConnections int    `json:"active_connections,omitempty"`

	// Connector-local probe results (cloudflared :2000 /ready + /metrics).
	ConnectorVersion string `json:"connector_version,omitempty"`
	RequestsServed   int64  `json:"requests_served,omitempty"`
	RequestErrors    int64  `json:"request_errors,omitempty"`

	// Public reachability probe (HTTP GET of the public URL).
	PublicReachable  bool `json:"public_reachable,omitempty"`
	PublicHTTPStatus int  `json:"public_http_status,omitempty"`

	LastUpdated string `json:"last_updated"`
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
			StartTime  string `json:"startTime"`
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
	StartedAt         time.Time
}

// connectorProbe captures the cloudflared connector's self-reported health from
// its in-cluster metrics endpoint (:2000). All fields are best-effort; a failed
// probe leaves the zero value and the caller degrades gracefully.
type connectorProbe struct {
	Reachable      bool
	ReadyConns     int
	Version        string
	RequestsServed int64
	RequestErrors  int64
}

// StatusOptions configures `tunnel status` presentation and probing.
type StatusOptions struct {
	// NoProbe skips both the connector metrics probe (kubectl port-forward to
	// cloudflared :2000) and the outbound public URL reachability check, keeping
	// `status` fully offline and fast.
	NoProbe bool
}

type RestartOptions struct {
	TransportProtocol string
}

// Status displays the current tunnel status and URL.
func Status(cfg *config.Config, u *ui.UI, opts StatusOptions) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("stack not running, use 'obol stack up' first")
	}

	now := time.Now()
	st, _ := loadTunnelState(cfg)
	mode, url := tunnelModeAndURL(st)
	result := tunnelStatusResult{
		Mode:              mode,
		DisplayMode:       st.DisplayMode(),
		ExposureMode:      tunnelExposureQuick,
		ManagementMode:    tunnelManagementQuick,
		TransportProtocol: tunnelTransportAuto,
		URL:               url,
		DesiredReplicas:   desiredRuntimeReplicas(st),
		LastUpdated:       now.Format(time.RFC3339),
	}
	if st != nil {
		result.ExposureMode = st.ExposureMode
		result.ManagementMode = st.Management()
		result.TransportProtocol = tunnelTransportProtocol(st)
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
		printStatusReport(u, result, now)
		u.Blank()
		if mode == tunnelExposureQuick {
			u.Print("The tunnel will start automatically when you sell a service.")
			u.Print("  Start manually:  obol tunnel restart")
			u.Print("  Permanent URL:   obol tunnel setup")
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
	if !runtime.StartedAt.IsZero() {
		result.Uptime = humanizeDuration(now.Sub(runtime.StartedAt))
	}

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

	// Connector health: prefer the connector's own /ready + /metrics endpoint
	// (works in every mode, no Cloudflare API token needed). Fall back to the
	// remote-management API path only when the local probe is skipped/unavailable
	// and a token happens to be present.
	if !opts.NoProbe {
		if probe := probeCloudflaredMetrics(cfg); probe.Reachable {
			result.ActiveConnections = probe.ReadyConns
			result.ConnectorVersion = probe.Version
			result.RequestsServed = probe.RequestsServed
			result.RequestErrors = probe.RequestErrors
			if probe.ReadyConns > 0 {
				result.ConnectorStatus = "connected"
			} else {
				result.ConnectorStatus = "waiting_for_connections"
			}
		}
	}
	if result.ConnectorStatus == "" && st != nil && st.IsPersistent() {
		result.ConnectorStatus = defaultPersistentConnectorStatus(st)
	}

	// Public reachability probe: GET the public URL root and assert HTTP < 400.
	publicURL := result.URL
	probeablePublicURL := strings.HasPrefix(publicURL, "https://") || strings.HasPrefix(publicURL, "http://")
	if !opts.NoProbe && probeablePublicURL {
		if code, ok := probePublicURL(publicURL); ok {
			result.PublicHTTPStatus = code
			result.PublicReachable = code < 400
		}
	}

	result.Status = summarizeTunnelStatus(result)
	if u.IsJSON() {
		return u.JSON(result)
	}

	printStatusReport(u, result, now)
	if probeablePublicURL {
		syncTunnelDependents(cfg, u, publicURL)
	}
	if result.Status != "active" {
		u.Blank()
		u.Print("Troubleshooting:")
		u.Print("  - Check logs:     obol tunnel logs")
		u.Print("  - Restart tunnel: obol tunnel restart")
	} else if mode == tunnelExposureQuick {
		u.Blank()
		u.Dim("This is a temporary URL — it changes on every restart.")
		u.Print("  Create a permanent URL: obol tunnel setup")
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

func defaultPersistentConnectorStatus(st *tunnelState) string {
	if st == nil || !st.IsPersistent() {
		return ""
	}
	if st.Management() == tunnelManagementRemote {
		return "not_probed"
	}
	return "managed-locally"
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

// WaitReady scales the cloudflared deployment to the desired replica count,
// waits for the deployment rollout, and returns the active public tunnel URL.
//
// For quick tunnels this polls pod logs for a public *.trycloudflare.com URL.
// For persistent tunnels this returns the configured hostname after rollout.
//
// Side effects on success: injects AGENT_BASE_URL into the agent deployment,
// writes the tunnel URL to the obol-frontend ConfigMap, and refreshes the
// storefront landing page for the public tunnel hostname.
func WaitReady(cfg *config.Config, u *ui.UI) (string, error) {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return "", errors.New("stack not running")
	}

	st, _ := loadTunnelState(cfg)
	desiredReplicas := desiredRuntimeReplicas(st)
	if runtime, err := getTunnelRuntimeHealth(kubectlPath, kubeconfigPath); err == nil && runtime.ReadyReplicas >= desiredReplicas && runtime.AvailableReplicas >= desiredReplicas {
		tunnelURL, err := currentTunnelURL(cfg, st)
		if err != nil {
			return "", err
		}
		syncTunnelDependents(cfg, u, tunnelURL)
		return tunnelURL, nil
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

	var tunnelURL string
	if st != nil && st.IsPersistent() {
		runtime, runtimeErr := getTunnelRuntimeHealth(kubectlPath, kubeconfigPath)
		if runtimeErr != nil {
			if rolloutErr != nil {
				return "", fmt.Errorf("cloudflared rollout failed and runtime health could not be rechecked: %w", rolloutErr)
			}
			return "", runtimeErr
		}
		if runtime.ReadyReplicas < desiredReplicas || runtime.AvailableReplicas < desiredReplicas {
			if rolloutErr != nil {
				return "", fmt.Errorf("cloudflared not ready within %s: deployment rollout failed (%w)", totalBudget, rolloutErr)
			}
			return "", fmt.Errorf("cloudflared not ready within %s: %d/%d replicas ready", totalBudget, runtime.ReadyReplicas, desiredReplicas)
		}
		var err error
		tunnelURL, err = currentTunnelURL(cfg, st)
		if err != nil {
			return "", err
		}
	} else {
		// Quick tunnels: poll pod logs for the public trycloudflare URL until the
		// remaining budget runs out. Even when the rollout above failed, we still
		// give the URL probe a brief grace window in case the pod is up but the
		// rollout watcher returned spuriously.
		for time.Now().Before(deadline) {
			if url, err := currentTunnelURL(cfg, st); err == nil && strings.HasPrefix(url, "https://") {
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
	}

	syncTunnelDependents(cfg, u, tunnelURL)

	return tunnelURL, nil
}

func syncTunnelDependents(cfg *config.Config, u *ui.UI, tunnelURL string) {
	if err := InjectBaseURL(cfg, tunnelURL); err == nil {
		u.Dim("Agent base URL updated to " + tunnelURL)
	}
	if err := SyncTunnelConfigMap(cfg, tunnelURL); err != nil {
		u.Dim("Could not sync tunnel URL to frontend ConfigMap: " + err.Error())
	}
	if err := CreateStorefront(cfg, storefrontHostnames(cfg, tunnelURL)...); err != nil {
		u.Dim("Could not create storefront: " + err.Error())
	}
}

func currentTunnelURL(cfg *config.Config, st *tunnelState) (string, error) {
	mode, url := tunnelModeAndURL(st)
	if mode != tunnelExposureQuick {
		if url == "" {
			return "", errors.New("persistent tunnel hostname is not configured")
		}
		return url, nil
	}

	url, err := GetTunnelURL(cfg)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(url, "https://") {
		return "", errors.New("quick tunnel URL is not yet available")
	}

	return url, nil
}

// EnsureRunning is the historical alias for WaitReady. New callers should
// prefer WaitReady directly; this is kept so existing call sites compile
// unchanged.
func EnsureRunning(cfg *config.Config, u *ui.UI) (string, error) {
	return WaitReady(cfg, u)
}

// IsQuickTunnelHealthy reports whether a quick (anonymous *.trycloudflare.com)
// tunnel is currently serving — pod is Running and a URL has been captured
// from its logs. Returns false for persistent (DNS) tunnels and for any
// failure mode (no kubeconfig, no pod, no URL).
//
// Used by `obol stack up` to skip the cloudflared chart sync when the URL
// would otherwise be invalidated. Persistent tunnels survive helmfile sync
// because the chart renders replicas: 1 for them; quick tunnels do not, so
// re-syncing the chart kills the running pod and rotates the URL.
func IsQuickTunnelHealthy(cfg *config.Config) bool {
	st, _ := loadTunnelState(cfg)
	if st != nil && st.IsPersistent() {
		return false // persistent tunnel — chart already keeps it alive
	}

	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return false
	}

	runtime, err := getTunnelRuntimeHealth(kubectlPath, kubeconfigPath)
	if err != nil {
		return false
	}
	if runtime.ReadyReplicas < quickReplicaCount || runtime.AvailableReplicas < quickReplicaCount || runtime.PodStatus == "" {
		return false
	}

	url, err := currentTunnelURL(cfg, st)
	if err != nil || !strings.HasPrefix(url, "https://") {
		return false
	}

	return true
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
	if st, _ := loadTunnelState(cfg); st != nil && st.IsPersistent() {
		return true
	}

	if currentURL == "" {
		return true
	}

	u.Blank()
	u.Warnf("Quick tunnel URL will be invalidated: %s", currentURL)
	u.Dim(fmt.Sprintf("  After `%s`, the next `obol sell http` brings up a fresh URL.", action))
	u.Dim("  Buyers using the old URL will see 530 errors.")
	u.Dim("  For a permanent URL: obol tunnel setup --hostname stack.example.com")

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
func Restart(cfg *config.Config, u *ui.UI, opts RestartOptions) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Check if kubeconfig exists.
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("stack not running, use 'obol stack up' first")
	}

	st, _ := loadTunnelState(cfg)
	transportOverrideSpecified := strings.TrimSpace(opts.TransportProtocol) != ""
	if transportOverrideSpecified {
		transportProtocol, err := validateTunnelTransportProtocol(opts.TransportProtocol)
		if err != nil {
			return err
		}
		if st == nil {
			st = &tunnelState{}
		}
		if !st.IsPersistent() {
			st.ExposureMode = tunnelExposureQuick
			st.ManagementMode = tunnelManagementQuick
			st.Hostname = ""
			st.AccountID = ""
			st.ZoneID = ""
		}
		st.TransportProtocol = transportProtocol
		if err := saveTunnelState(cfg, st); err != nil {
			return fmt.Errorf("save tunnel state: %w", err)
		}
	}

	if st != nil && st.IsPersistent() {
		if err := RestorePersistentResources(cfg, u); err != nil {
			return fmt.Errorf("restore persistent tunnel resources: %w", err)
		}
	} else {
		currentURL, _ := GetTunnelURL(cfg)
		if !ConfirmQuickTunnelLoss(cfg, u, currentURL, "obol tunnel restart") {
			u.Info("Aborted.")

			return nil
		}

		transportProtocol := tunnelTransportProtocol(st)
		if transportOverrideSpecified || transportProtocol != tunnelTransportAuto {
			if err := applyManagementModeConfigMap(cfg, u, kubeconfigPath, tunnelManagementQuick, transportProtocol); err != nil {
				return err
			}
			if err := helmUpgradeCloudflared(cfg, u, kubeconfigPath); err != nil {
				return fmt.Errorf("failed to apply cloudflared transport settings: %w", err)
			}
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
	// WaitReady also refreshes AGENT_BASE_URL, the frontend tunnel ConfigMap,
	// and the public storefront.
	newURL, err := WaitReady(cfg, u)
	if err != nil {
		return fmt.Errorf("tunnel restarted but new URL not captured: %w", err)
	}

	u.Blank()
	if st != nil && st.IsPersistent() {
		u.Successf("Persistent tunnel active: %s", newURL)
		u.Dim("  Resources restored and connector restarted.")
	} else {
		u.Successf("Tunnel restarted: %s", newURL)
		u.Dim("  /skill.md, /api/services.json, and the storefront now reflect the new URL.")
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
	var earliestStart time.Time
	for _, pod := range pods.Items {
		phase := strings.ToLower(strings.TrimSpace(pod.Status.Phase))
		if phase == "" {
			phase = "unknown"
		}
		phases = append(phases, phase)
		if ts := strings.TrimSpace(pod.Status.StartTime); ts != "" {
			if started, parseErr := time.Parse(time.RFC3339, ts); parseErr == nil {
				if earliestStart.IsZero() || started.Before(earliestStart) {
					earliestStart = started
				}
			}
		}
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
		StartedAt:         earliestStart,
	}, nil
}

// humanizeDuration renders a coarse, human-friendly uptime string.
func humanizeDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	default:
		days := int(d.Hours()) / 24
		h := int(d.Hours()) % 24
		if h == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd%dh", days, h)
	}
}

// humanTunnelMode maps the internal display mode to a friendly label that makes
// the permanent-vs-temporary distinction obvious. Both local- and remote-managed
// tunnels are permanent; only the quick tunnel is temporary.
func humanTunnelMode(displayMode string) string {
	switch displayMode {
	case "persistent-remote":
		return "Permanent (Cloudflare-managed)"
	case "persistent-local":
		return "Permanent (browser-managed)"
	case tunnelExposurePersistent:
		return "Permanent"
	default:
		return "Temporary (quick tunnel)"
	}
}

// printStatusReport renders the human-facing status. The default view stays
// concise; --verbose (u.IsVerbose) adds replica/pod internals and last-updated.
func printStatusReport(u *ui.UI, result tunnelStatusResult, lastUpdated time.Time) {
	u.Blank()
	u.Bold("Cloudflare Tunnel Status")
	u.Print(strings.Repeat("─", 50))
	u.Detail("Mode", humanTunnelMode(result.DisplayMode))
	u.Detail("Status", result.Status)
	if result.Hostname != "" {
		u.Detail("Hostname", result.Hostname)
	}
	u.Detail("URL", result.URL)
	if result.Uptime != "" {
		u.Detail("Uptime", result.Uptime)
	}
	if result.ConnectorStatus != "" {
		connector := result.ConnectorStatus
		if result.ActiveConnections > 0 {
			connector = fmt.Sprintf("%s (%d active)", connector, result.ActiveConnections)
		}
		u.Detail("Connector", connector)
	}
	if result.PublicHTTPStatus > 0 {
		reach := fmt.Sprintf("HTTP %d", result.PublicHTTPStatus)
		if result.PublicReachable {
			reach = "reachable (" + reach + ")"
		} else {
			reach = "unreachable (" + reach + ")"
		}
		u.Detail("Public check", reach)
	}
	if u.IsVerbose() {
		if result.ManagementMode != "" {
			u.Detail("Management", result.ManagementMode)
		}
		if result.TransportProtocol != "" {
			u.Detail("Transport", result.TransportProtocol)
		}
		if result.ConnectorVersion != "" {
			u.Detail("Connector version", result.ConnectorVersion)
		}
		if result.RequestsServed > 0 || result.RequestErrors > 0 {
			u.Detail("Requests served", fmt.Sprintf("%d (%d errors)", result.RequestsServed, result.RequestErrors))
		}
		if result.DesiredReplicas > 0 || result.ReadyReplicas > 0 {
			u.Detail("Replicas", fmt.Sprintf("%d ready / %d desired", result.ReadyReplicas, result.DesiredReplicas))
		}
		if result.PodStatus != "" {
			u.Detail("Pods", result.PodStatus)
		}
		u.Detail("Last updated", lastUpdated.Format(time.RFC3339))
	}
	u.Print(strings.Repeat("─", 50))
}

// probeCloudflaredMetrics port-forwards to the cloudflared connector's metrics
// endpoint (:2000) and reads /ready + /metrics for self-reported health. It is
// strictly best-effort: any failure returns a non-reachable zero value so the
// caller degrades gracefully. Works in every tunnel mode without a Cloudflare
// API token.
func probeCloudflaredMetrics(cfg *config.Config) connectorProbe {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	localPort, err := freeLocalPort()
	if err != nil {
		return connectorProbe{}
	}

	pf := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"port-forward", "-n", tunnelNamespace,
		"deployment/cloudflared",
		fmt.Sprintf("%d:2000", localPort),
	)
	if err := pf.Start(); err != nil {
		return connectorProbe{}
	}
	defer func() {
		_ = pf.Process.Kill()
		_ = pf.Wait()
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	base := fmt.Sprintf("http://127.0.0.1:%d", localPort)

	// Wait briefly for the forward to come up.
	var readyBody []byte
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, getErr := client.Get(base + "/ready")
		if getErr == nil {
			readyBody, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if len(readyBody) == 0 {
		return connectorProbe{}
	}

	probe := connectorProbe{Reachable: true}
	var ready struct {
		ReadyConnections int `json:"readyConnections"`
	}
	if json.Unmarshal(readyBody, &ready) == nil {
		probe.ReadyConns = ready.ReadyConnections
	}

	if resp, getErr := client.Get(base + "/metrics"); getErr == nil {
		metricsBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		parseCloudflaredMetrics(string(metricsBody), &probe)
	}

	return probe
}

// parseCloudflaredMetrics extracts a few friendly numbers from cloudflared's
// Prometheus text exposition. Unknown/absent metrics are silently ignored.
func parseCloudflaredMetrics(metrics string, probe *connectorProbe) {
	for _, line := range strings.Split(metrics, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "cloudflared_tunnel_total_requests"):
			probe.RequestsServed += metricLineValueInt(line)
		case strings.HasPrefix(line, "cloudflared_tunnel_request_errors"):
			probe.RequestErrors += metricLineValueInt(line)
		case strings.HasPrefix(line, "build_info") && probe.Version == "":
			probe.Version = metricLabelValue(line, "version")
		}
	}
}

func metricLineValueInt(line string) int64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	if f, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
		return int64(f)
	}
	return 0
}

func metricLabelValue(line, label string) string {
	re := regexp.MustCompile(label + `="([^"]*)"`)
	if m := re.FindStringSubmatch(line); len(m) == 2 {
		return m[1]
	}
	return ""
}

// probePublicURL issues a short GET against the public URL root and returns the
// HTTP status code. ok is false when the request could not be completed.
func probePublicURL(publicURL string) (int, bool) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(strings.TrimRight(publicURL, "/") + "/")
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	return resp.StatusCode, true
}

func freeLocalPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// SyncTunnelConfigMap creates or patches the obol-stack-config ConfigMap in the
// obol-frontend namespace with the current tunnel URL and the running Obol CLI
// version. The frontend reads this ConfigMap for the public tunnel URL and the
// footer version display. Server-side apply merges fields; call after
// obol-frontend exists (helmfile).
func SyncTunnelConfigMap(cfg *config.Config, tunnelURL string) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	manifest := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: obol-stack-config
  namespace: obol-frontend
data:
  tunnelURL: %q
  obolVersion: %q
`, strings.TrimRight(tunnelURL, "/"), version.Version)

	// Server-side apply avoids the flaky client-side /openapi/v2 download on k3d.
	cmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"apply", "--server-side", "--force-conflicts", "-f", "-",
	)

	cmd.Stdin = strings.NewReader(manifest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return nil
}

// SyncStackConfigVersion SSA-merges only obolVersion into
// obol-frontend/obol-stack-config, leaving tunnelURL untouched. Used on stack
// up when no tunnel sync runs yet (after infra deploy creates the namespace).
func SyncStackConfigVersion(cfg *config.Config) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	manifest := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: obol-stack-config
  namespace: obol-frontend
data:
  obolVersion: %q
`, version.Version)

	cmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"apply", "--server-side", "--force-conflicts", "-f", "-",
	)
	cmd.Stdin = strings.NewReader(manifest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply obol-stack-config failed: %w: %s", err, strings.TrimSpace(string(out)))
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
	if err := CreateStorefront(cfg, storefrontHostnames(cfg, tunnelURL)...); err != nil {
		u.Warnf("could not create storefront: %v", err)
	}

	return tunnelURL, nil
}

// RefreshStorefront re-applies the storefront's HTTPRoute against the
// tunnel's currently tracked hostnames, narrowing or tearing it down for any
// hostname that is now offer-bound. CreateStorefront only sees ServiceOffer
// bindings that already exist on the cluster at call time, so a hostname
// bound by a manifest applied AFTER the tunnel was last (re)created — e.g.
// `obol sell ... --hostname X` — needs this explicit follow-up to be
// reflected immediately, instead of shadowing the offer's route until some
// later, unrelated tunnel/sell invocation happens to run CreateStorefront
// again (Canary402).
//
// It no-ops quietly when there is no persistent tunnel/hostname state yet
// (e.g. a first `obol sell ... --hostname X --no-register` before any
// tunnel has ever been created) — CreateStorefront has nothing to publish
// in that case, and EnsureTunnelForSell reconciles the storefront once the
// tunnel comes up.
func RefreshStorefront(cfg *config.Config) error {
	hosts := storefrontHostnames(cfg, "")
	if len(hosts) == 0 {
		return nil
	}
	return CreateStorefront(cfg, hosts...)
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

// DeleteOptions configures `obol tunnel delete`.
type DeleteOptions struct {
	// Force skips the interactive confirmation prompt.
	Force bool
}

// DeleteResult is the JSON-serialisable result of Delete.
type DeleteResult struct {
	ManagementMode           string   `json:"management_mode"`
	DeletedHostnames         []string `json:"deleted_hostnames"`
	CloudflareTunnelDeleted  bool     `json:"cloudflare_tunnel_deleted"`
	DashboardCleanupRequired bool     `json:"dashboard_cleanup_required"`
}

// Delete tears down the persistent tunnel completely and reverts the connector
// to a default quick tunnel. It is the destructive counterpart to Stop (which
// only pauses the connector). Where Obol holds the credential — a local-managed
// (cert) tunnel — it deletes the Cloudflare tunnel directly. For a
// dashboard-managed (connector token) tunnel Obol holds no account-wide API
// token, so it cleans up the cluster side and prints the dashboard steps. DNS
// CNAME records are never deleted (no broad token by design) and are left to the
// operator with explicit instructions.
func Delete(cfg *config.Config, u *ui.UI, opts DeleteOptions) (*DeleteResult, error) {
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return nil, errors.New("stack not running, use 'obol stack up' first")
	}

	st, err := loadTunnelState(cfg)
	if err != nil {
		return nil, fmt.Errorf("load tunnel state: %w", err)
	}
	if st == nil || !st.IsPersistent() {
		return nil, errors.New("no permanent tunnel to delete (the current tunnel is quick or none); use 'obol tunnel stop' to pause a quick tunnel")
	}

	management := st.Management()
	hostnames := st.HostnameSet()

	if !opts.Force && u.IsTTY() {
		u.Blank()
		u.Bold("This permanently tears down the persistent tunnel serving:")
		for _, h := range hostnames {
			u.Dim("  - https://" + h)
		}
		u.Dim("The stack reverts to a default quick tunnel (new temporary URL).")
		if !u.Confirm("Delete this tunnel?", false) {
			return nil, errors.New("aborted")
		}
	}

	result := &DeleteResult{ManagementMode: management, DeletedHostnames: hostnames}

	// 1. Delete the Cloudflare-side tunnel where Obol holds the credential.
	switch management {
	case tunnelManagementLocal:
		if err := deleteLocalCloudflareTunnel(u, st.TunnelName, st.TunnelID); err != nil {
			// Non-fatal: keep cleaning the cluster even if the cloudflared delete
			// fails (e.g. the tunnel was already removed).
			u.Warnf("could not delete the Cloudflare tunnel (continuing cleanup): %v", err)
		} else {
			result.CloudflareTunnelDeleted = true
		}
	case tunnelManagementRemote:
		result.DashboardCleanupRequired = true
	}

	// 2. Remove in-cluster persistent resources + storefront + local token.
	if err := deleteLocalManagedK8sResources(cfg, u, kubeconfigPath); err != nil {
		u.Warnf("could not delete local-managed resources: %v", err)
	}
	if err := deleteRemoteManagedK8sResources(cfg, u, kubeconfigPath); err != nil {
		u.Warnf("could not delete remote-managed resources: %v", err)
	}
	if err := deleteRemoteTunnelToken(cfg); err != nil {
		u.Warnf("could not delete local tunnel token: %v", err)
	}
	if err := DeleteStorefront(cfg); err != nil {
		u.Warnf("could not delete storefront: %v", err)
	}

	// 3. Clear local state and revert the connector to the default quick tunnel
	//    (a persistent→quick mode change rolls the pods via helm upgrade).
	if err := deleteTunnelState(cfg); err != nil {
		u.Warnf("could not clear tunnel state: %v", err)
	}
	if err := applyManagementModeConfigMap(cfg, u, kubeconfigPath, tunnelManagementQuick, tunnelTransportAuto); err != nil {
		u.Warnf("could not reset connector to quick mode: %v", err)
	}
	if err := helmUpgradeCloudflared(cfg, u, kubeconfigPath); err != nil {
		u.Warnf("could not re-render cloudflared to quick mode: %v", err)
	}

	// 4. Tell the operator what Obol could not do without a broad credential.
	u.Blank()
	u.Success("Tunnel deleted — reverted to a default quick tunnel")
	u.Dim("  'obol tunnel status' shows the new temporary URL; 'obol tunnel stop' disables public access.")
	if management == tunnelManagementLocal {
		u.Blank()
		u.Dim("The Cloudflare tunnel was deleted. Its DNS CNAME record(s) still resolve —")
		u.Dim("delete them in the Cloudflare dashboard (DNS → Records):")
		for _, h := range hostnames {
			u.Dim("  - " + h)
		}
	} else {
		u.Blank()
		u.Bold("Finish teardown in the Cloudflare dashboard")
		u.Print("Obol holds no API token for a dashboard-managed tunnel, so delete it there:")
		u.Print("  https://one.dash.cloudflare.com → Networks → Tunnels → delete the tunnel,")
		u.Print("  then remove its Public Hostname(s) and DNS record(s).")
	}

	return result, nil
}

// deleteLocalCloudflareTunnel deletes the cert-scoped cloudflared tunnel. The -f
// flag cleans up any active connections so the delete succeeds even while the
// connector is still running.
func deleteLocalCloudflareTunnel(u *ui.UI, tunnelName, tunnelID string) error {
	cloudflaredPath, err := exec.LookPath("cloudflared")
	if err != nil {
		return errors.New("cloudflared not found in PATH")
	}

	ref := strings.TrimSpace(tunnelName)
	if ref == "" {
		ref = strings.TrimSpace(tunnelID) // cloudflared accepts the UUID as the tunnel ref
	}
	if ref == "" {
		return errors.New("local tunnel has no name or id to delete")
	}

	u.Infof("Deleting Cloudflare tunnel %s...", ref)
	if out, err := exec.Command(cloudflaredPath, "tunnel", "delete", "-f", ref).CombinedOutput(); err != nil {
		return fmt.Errorf("cloudflared tunnel delete failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return nil
}

// storefrontNamespace is where the storefront landing page resources live.
const storefrontNamespace = "traefik"

// storefrontHostnames returns the hostnames the public storefront should be
// published on: the full tracked set for a persistent tunnel, else the host
// parsed from tunnelURL (quick tunnels). Empty entries are dropped.
func storefrontHostnames(cfg *config.Config, tunnelURL string) []string {
	if st, _ := loadTunnelState(cfg); st != nil && st.IsPersistent() {
		if set := st.HostnameSet(); len(set) > 0 {
			return set
		}
	}
	if parsed, err := url.Parse(tunnelURL); err == nil {
		if h := parsed.Hostname(); h != "" {
			return []string{h}
		}
	}
	return nil
}

// offerBoundHostnames returns the set of hostnames claimed by ServiceOffers
// (spec.hostname), normalized, and any error from the query itself (cluster
// down, CRD missing, kubectl not found). A successful query with no offers
// bound returns an empty, non-nil map with a nil error — callers must not
// conflate that with a query failure (P1b): collapsing both into the same
// "nil" previously made CreateStorefront treat "can't tell" as "zero
// hostnames are bound" and reclaim every hostname, including live
// per-offer origins, under the catch-all.
func offerBoundHostnames(kubectlPath, kubeconfigPath string) (map[string]bool, error) {
	cmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"get", "serviceoffers.obol.org", "-A",
		"-o", `jsonpath={range .items[*]}{.spec.hostname}{"\n"}{end}`,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	bound := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if h := normalizeHostname(line); h != "" {
			bound[h] = true
		}
	}
	return bound, nil
}

// CreateStorefront creates (or updates) the public storefront landing page and
// publishes it at the root path of EVERY supplied hostname. Each argument may be
// a bare hostname or a full URL (scheme/path stripped); empty or duplicate
// entries are dropped. The single HTTPRoute lists all hostnames, so a second
// domain serves `/` without displacing the first.
func CreateStorefront(cfg *config.Config, hostnames ...string) error {
	hosts := normalizeHostnames(hostnames)
	if len(hosts) == 0 {
		return errors.New("CreateStorefront requires at least one hostname")
	}

	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Hostnames claimed by a ServiceOffer (spec.hostname) belong to that
	// offer's dedicated-origin route — the storefront catch-all must not
	// contest their root (Gateway API breaks PathPrefix-/ ties on route
	// age, i.e. silently).
	bound, err := offerBoundHostnames(kubectlPath, kubeconfigPath)
	if err != nil {
		// ponytail: fail safe (P1b) — we can't tell which hostnames are
		// offer-bound, so proceeding would risk the catch-all reclaiming a
		// live per-offer origin on what may be a transient kubectl error.
		// Leave the existing storefront route untouched instead of
		// guessing "zero hostnames are bound".
		fmt.Printf("   Storefront: could not query offer-bound hostnames (%v) — leaving existing storefront route unchanged\n", err)
		return nil
	}
	if len(bound) > 0 {
		kept := hosts[:0]
		for _, h := range hosts {
			if bound[h] {
				fmt.Printf("   Storefront: skipping %s (bound to a ServiceOffer via spec.hostname)\n", h)
				continue
			}
			kept = append(kept, h)
		}
		hosts = kept
		if len(hosts) == 0 {
			// Every tracked hostname is offer-bound: the storefront has
			// nothing left to serve at any hostname. Tear it down instead
			// of leaving the previously-applied HTTPRoute (with the now-
			// stale wider host list) on the cluster — Gateway API breaks
			// the resulting PathPrefix-/ tie by route age, so that stale
			// route would otherwise keep shadowing the offer's own
			// dedicated-origin route.
			return DeleteStorefront(cfg)
		}
	}

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
									{"name": "SERVICES_URL", "value": "http://obol-skill-md.x402.svc.cluster.local:8080"},
									// Bind Next on all interfaces so the kubelet probes
									// and Traefik reach it on the pod IP (the standalone
									// server otherwise defaults to localhost).
									{"name": "HOSTNAME", "value": "0.0.0.0"},
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
				"hostnames": hosts,
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

		// Server-side apply avoids the flaky client-side /openapi/v2 download on k3d.
		cmd := exec.Command(kubectlPath,
			"--kubeconfig", kubeconfigPath,
			"apply", "--server-side", "--force-conflicts", "-f", "-",
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
