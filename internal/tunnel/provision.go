package tunnel

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// TokenProvisionOptions configures the dashboard-managed (connector token) path.
type TokenProvisionOptions struct {
	Hostname          string
	ConnectorToken    string
	TransportProtocol string
}

// ProvisionWithToken wires up a permanent, dashboard-managed tunnel from a
// Cloudflare connector token (the value from Networks → Tunnels). It performs no
// Cloudflare API calls: the user has already created the tunnel and its Public
// Hostname route in the dashboard. We simply store the token as the in-cluster
// connector secret and run cloudflared in remote-managed mode — runtime-identical
// to the API-provisioned path, but with a least-privilege, single-tunnel
// credential instead of an account-wide API token.
func ProvisionWithToken(cfg *config.Config, u *ui.UI, opts TokenProvisionOptions) error {
	hostname := normalizeHostname(opts.Hostname)
	if hostname == "" {
		return errors.New("--hostname is required (e.g. stack.example.com)")
	}
	transportProtocol, err := validateTunnelTransportProtocol(opts.TransportProtocol)
	if err != nil {
		return err
	}

	claims, err := parseConnectorToken(opts.ConnectorToken)
	if err != nil {
		return fmt.Errorf("invalid connector token: %w", err)
	}
	connectorToken := strings.TrimSpace(opts.ConnectorToken)

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("stack not running, use 'obol stack up' first")
	}

	u.Info("Configuring dashboard-managed Cloudflare Tunnel...")
	u.Detail("Hostname", hostname)
	u.Detail("Tunnel", claims.TunnelID)

	if err := saveRemoteTunnelToken(cfg, connectorToken); err != nil {
		return fmt.Errorf("save tunnel token locally: %w", err)
	}
	if err := applyTunnelTokenSecret(cfg, u, kubeconfigPath, connectorToken); err != nil {
		return err
	}
	if err := deleteLocalManagedK8sResources(cfg, u, kubeconfigPath); err != nil {
		return err
	}
	if err := applyManagementModeConfigMap(cfg, u, kubeconfigPath, tunnelManagementRemote, transportProtocol); err != nil {
		return err
	}
	if err := helmUpgradeCloudflared(cfg, u, kubeconfigPath); err != nil {
		return err
	}

	st, _ := loadTunnelState(cfg)
	if st == nil {
		st = &tunnelState{}
	}
	st.ExposureMode = tunnelExposurePersistent
	st.ManagementMode = tunnelManagementRemote
	st.TransportProtocol = transportProtocol
	st.Hostname = hostname
	st.AccountID = claims.AccountTag
	st.ZoneID = "" // DNS is managed in the dashboard, not by us.
	st.TunnelID = claims.TunnelID
	st.TunnelName = ""
	if err := saveTunnelState(cfg, st); err != nil {
		return fmt.Errorf("tunnel configured, but failed to save local state: %w", err)
	}

	tunnelURL := "https://" + hostname
	if err := SyncAgentBaseURL(cfg, tunnelURL); err != nil {
		u.Warnf("could not sync AGENT_BASE_URL to obol-agent: %v", err)
	}
	if err := SyncTunnelConfigMap(cfg, tunnelURL); err != nil {
		u.Warnf("could not sync tunnel URL to frontend ConfigMap: %v", err)
	}

	u.Blank()
	u.Success("Tunnel configured")
	u.Printf("Permanent URL: %s", tunnelURL)
	u.Blank()
	u.Print("In the Cloudflare dashboard, make sure this tunnel's Public Hostname routes to:")
	u.Dim("  Service: http://traefik.traefik.svc.cluster.local:80")
	u.Dim(fmt.Sprintf("  Hostname: %s", hostname))
	u.Print("Tip: run 'obol tunnel status' to verify the connector is active.")

	return nil
}

func normalizeHostname(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")

	// Strip any path/query fragments users accidentally paste.
	if idx := strings.IndexByte(s, '/'); idx >= 0 {
		s = s[:idx]
	}
	if idx := strings.IndexByte(s, '?'); idx >= 0 {
		s = s[:idx]
	}
	if idx := strings.IndexByte(s, '#'); idx >= 0 {
		s = s[:idx]
	}

	return strings.ToLower(s)
}

func applyTunnelTokenSecret(cfg *config.Config, u *ui.UI, kubeconfigPath, token string) error {
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  %s: %s
`, tunnelTokenSecretName, tunnelNamespace, tunnelTokenSecretKey, token)

	if err := kubectlApply(cfg, u, kubeconfigPath, []byte(manifest)); err != nil {
		return fmt.Errorf("failed to apply tunnel token secret: %w", err)
	}

	return nil
}

func helmUpgradeCloudflared(cfg *config.Config, u *ui.UI, kubeconfigPath string) error {
	helmPath := filepath.Join(cfg.BinDir, "helm")
	defaultsDir := filepath.Join(cfg.ConfigDir, "defaults")

	if _, err := os.Stat(helmPath); os.IsNotExist(err) {
		return fmt.Errorf("helm not found at %s", helmPath)
	}
	if _, err := os.Stat(filepath.Join(defaultsDir, "cloudflared", "Chart.yaml")); os.IsNotExist(err) {
		return fmt.Errorf("cloudflared chart not found in %s (re-run 'obol stack init --force' to refresh defaults)", defaultsDir)
	}

	cmd := exec.Command(helmPath,
		"--kubeconfig", kubeconfigPath,
		"upgrade",
		"--install",
		"cloudflared",
		"./cloudflared",
		"--namespace", tunnelNamespace,
		"--wait",
		"--timeout", "2m",
	)

	cmd.Dir = defaultsDir
	if err := u.Exec(ui.ExecConfig{
		Name: "Upgrading cloudflared Helm release",
		Cmd:  cmd,
	}); err != nil {
		return fmt.Errorf("failed to upgrade cloudflared release: %w", err)
	}

	return nil
}
