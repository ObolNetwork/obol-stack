package tunnel

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
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
func Provision(cfg *config.Config, opts ProvisionOptions) error {
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
	kubeconfigPath, err := requireRunningStack(cfg)
	if err != nil {
		return err
	}

	stackID := getStackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack not initialized, run 'obol stack init' first")
	}
	tunnelName := fmt.Sprintf("obol-stack-%s", stackID)

	client := newCloudflareClient(opts.APIToken)

	// Try to reuse existing local state to keep the same tunnel ID.
	st, err := loadTunnelState(cfg)
	if err != nil {
		return fmt.Errorf("failed to read tunnel state: %w", err)
	}
	if st != nil && st.AccountID == opts.AccountID && st.ZoneID == opts.ZoneID && st.TunnelID != "" && st.TunnelName != "" {
		tunnelName = st.TunnelName
	}

	fmt.Println("Provisioning Cloudflare Tunnel (API)...")
	fmt.Printf("Hostname: %s\n", hostname)
	fmt.Printf("Tunnel:   %s\n", tunnelName)

	tunnelID := ""
	tunnelToken := ""

	if st != nil && st.AccountID == opts.AccountID && st.TunnelID != "" {
		tunnelID = st.TunnelID
		tok, err := client.GetTunnelToken(opts.AccountID, tunnelID)
		if err != nil {
			// If the tunnel no longer exists, create a new one.
			fmt.Printf("Existing tunnel token fetch failed (%v); creating a new tunnel...\n", err)
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

	if err := client.UpdateTunnelConfiguration(opts.AccountID, tunnelID, hostname, tunnelServiceURL); err != nil {
		return err
	}

	if err := client.UpsertTunnelDNSRecord(opts.ZoneID, hostname, tunnelID+".cfargotunnel.com"); err != nil {
		return err
	}

	if err := applyTunnelTokenSecret(cfg, kubeconfigPath, tunnelToken); err != nil {
		return err
	}

	// Ensure cloudflared switches to remotely-managed mode immediately (chart defaults to mode:auto).
	if err := helmUpgradeCloudflared(cfg, kubeconfigPath); err != nil {
		return err
	}

	if st == nil {
		st = &tunnelState{}
	}
	st.Mode = "remote"
	st.Hostname = hostname
	st.AccountID = opts.AccountID
	st.ZoneID = opts.ZoneID
	st.TunnelID = tunnelID
	st.TunnelName = tunnelName

	if err := saveTunnelState(cfg, st); err != nil {
		return fmt.Errorf("tunnel provisioned, but failed to save local state: %w", err)
	}

	fmt.Println("\n✓ Tunnel provisioned")
	fmt.Printf("Persistent URL: https://%s\n", hostname)
	fmt.Println("Tip: run 'obol tunnel status' to verify the connector is active.")
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

func applyTunnelTokenSecret(cfg *config.Config, kubeconfigPath, token string) error {
	secretYAML := buildRemoteManagedSecretYAML(token)
	return kubectlApplyManifest(cfg, kubeconfigPath, secretYAML)
}

func buildRemoteManagedSecretYAML(token string) []byte {
	tokenB64 := base64.StdEncoding.EncodeToString([]byte(token))
	secret := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
data:
  %s: %s
`, tunnelTokenSecretName, tunnelNamespace, tunnelTokenSecretKey, tokenB64)
	return []byte(secret)
}

func helmUpgradeCloudflared(cfg *config.Config, kubeconfigPath string) error {
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
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to upgrade cloudflared release: %w", err)
	}
	return nil
}
