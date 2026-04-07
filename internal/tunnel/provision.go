package tunnel

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// ProvisionOptions configures `obol tunnel provision`.
type ProvisionOptions struct {
	Hostname  string
	AccountID string
	ZoneID    string
	APIToken  string
}

// Provision provisions a persistent Cloudflare Tunnel routed via a proxied DNS record.
//
// Based on Cloudflare's "Create a tunnel (API)" guide:
// - POST /accounts/$ACCOUNT_ID/cfd_tunnel
// - PUT /accounts/$ACCOUNT_ID/cfd_tunnel/$TUNNEL_ID/configurations
// - POST /zones/$ZONE_ID/dns_records (proxied CNAME to <tunnel-id>.cfargotunnel.com)
func Provision(cfg *config.Config, u *ui.UI, opts ProvisionOptions) error {
	hostname := normalizeHostname(opts.Hostname)
	if hostname == "" {
		return fmt.Errorf("--hostname is required (e.g. stack.example.com)")
	}
	if opts.AccountID == "" {
		return fmt.Errorf("--account-id is required (or set CLOUDFLARE_ACCOUNT_ID)")
	}
	if opts.ZoneID == "" {
		return fmt.Errorf("--zone-id is required (or set CLOUDFLARE_ZONE_ID)")
	}
	if opts.APIToken == "" {
		return fmt.Errorf("--api-token is required (or set CLOUDFLARE_API_TOKEN)")
	}

	// Stack must be running so we can store the tunnel token in-cluster.
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("stack not running, use 'obol stack up' first")
	}

	stackID := getStackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack not initialized, run 'obol stack init' first")
	}
	tunnelName := fmt.Sprintf("obol-stack-%s", stackID)

	client := newCloudflareClient(opts.APIToken)

	// Try to reuse existing local state to keep the same tunnel ID.
	st, _ := loadTunnelState(cfg)
	if st != nil && st.AccountID == opts.AccountID && st.ZoneID == opts.ZoneID && st.TunnelID != "" && st.TunnelName != "" {
		tunnelName = st.TunnelName
	}

	u.Info("Provisioning Cloudflare Tunnel (API)...")
	u.Detail("Hostname", hostname)
	u.Detail("Tunnel", tunnelName)

	tunnelID := ""
	tunnelToken := ""

	if st != nil && st.AccountID == opts.AccountID && st.TunnelID != "" {
		tunnelID = st.TunnelID
		tok, err := client.GetTunnelToken(opts.AccountID, tunnelID)
		if err != nil {
			// If the tunnel no longer exists, create a new one.
			u.Warnf("Existing tunnel token fetch failed (%v); creating a new tunnel...", err)
			tunnelID = ""
		} else {
			tunnelToken = tok
		}
	}

	if tunnelID == "" {
		t, err := client.CreateTunnel(opts.AccountID, tunnelName)
		if err != nil {
			return err
		}
		tunnelID = t.ID
		tunnelToken = t.Token
	}

	if err := client.UpdateTunnelConfiguration(opts.AccountID, tunnelID, hostname, "http://traefik.traefik.svc.cluster.local:80"); err != nil {
		return err
	}

	if err := client.UpsertTunnelDNSRecord(opts.ZoneID, hostname, tunnelID+".cfargotunnel.com"); err != nil {
		return err
	}

	if err := applyTunnelTokenSecret(cfg, u, kubeconfigPath, tunnelToken); err != nil {
		return err
	}

	// Ensure cloudflared switches to remotely-managed mode immediately (chart defaults to mode:auto).
	if err := helmUpgradeCloudflared(cfg, u, kubeconfigPath); err != nil {
		return err
	}

	if st == nil {
		st = &tunnelState{}
	}
	st.Mode = "dns"
	st.Hostname = hostname
	st.AccountID = opts.AccountID
	st.ZoneID = opts.ZoneID
	st.TunnelID = tunnelID
	st.TunnelName = tunnelName

	if err := saveTunnelState(cfg, st); err != nil {
		return fmt.Errorf("tunnel provisioned, but failed to save local state: %w", err)
	}

	tunnelURL := fmt.Sprintf("https://%s", hostname)

	// Inject AGENT_BASE_URL into obol-agent overlay if deployed.
	if err := SyncAgentBaseURL(cfg, tunnelURL, u); err != nil {
		u.Warnf("could not sync AGENT_BASE_URL to obol-agent: %v", err)
	}

	// Write tunnel URL to ConfigMap so the frontend can read it.
	if err := SyncTunnelConfigMap(cfg, tunnelURL); err != nil {
		u.Warnf("could not sync tunnel URL to frontend ConfigMap: %v", err)
	}

	u.Blank()
	u.Success("Tunnel provisioned")
	u.Printf("Persistent URL: https://%s", hostname)
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
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")

	createCmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"-n", tunnelNamespace,
		"create", "secret", "generic", tunnelTokenSecretName,
		fmt.Sprintf("--from-literal=%s=%s", tunnelTokenSecretKey, token),
		"--dry-run=client",
		"-o", "yaml",
	)
	out, err := createCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to create secret manifest: %w", err)
	}

	applyCmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"apply", "-f", "-",
	)
	applyCmd.Stdin = bytes.NewReader(out)
	if err := u.Exec(ui.ExecConfig{
		Name: "Applying tunnel token secret",
		Cmd:  applyCmd,
	}); err != nil {
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

	// Run from the defaults dir so "./cloudflared" resolves correctly.
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
