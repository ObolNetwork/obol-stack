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

// ProvisionOptions configures `obol tunnel provision`.
type ProvisionOptions struct {
	Hostname          string
	AccountID         string
	ZoneID            string
	APIToken          string
	TransportProtocol string
}

type resolvedProvisionTarget struct {
	Hostname  string
	AccountID string
	ZoneID    string
	ZoneName  string
}

// Provision provisions a remotely managed persistent Cloudflare Tunnel routed via a proxied DNS record.
func Provision(cfg *config.Config, u *ui.UI, opts ProvisionOptions) error {
	hostname := normalizeHostname(opts.Hostname)
	if hostname == "" {
		return errors.New("--hostname is required (e.g. stack.example.com)")
	}
	if opts.APIToken == "" {
		return errors.New("--api-token is required (or set CLOUDFLARE_API_TOKEN)")
	}
	transportProtocol, err := validateTunnelTransportProtocol(opts.TransportProtocol)
	if err != nil {
		return err
	}

	// Stack must be running so we can store the tunnel token in-cluster.
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("stack not running, use 'obol stack up' first")
	}

	stackID := getStackID(cfg)
	if stackID == "" {
		return errors.New("stack not initialized, run 'obol stack init' first")
	}

	client := newCloudflareClient(opts.APIToken)
	target, err := resolveProvisionTarget(client, ProvisionOptions{
		Hostname:  hostname,
		AccountID: opts.AccountID,
		ZoneID:    opts.ZoneID,
		APIToken:  opts.APIToken,
	})
	if err != nil {
		return err
	}

	st, _ := loadTunnelState(cfg)
	tunnelName := desiredPersistentTunnelName(stackID, st, tunnelManagementRemote)
	if st != nil && st.Management() == tunnelManagementRemote && st.AccountID == target.AccountID && st.TunnelID != "" && st.TunnelName != "" {
		tunnelName = st.TunnelName
	}

	u.Info("Provisioning Cloudflare Tunnel (API)...")
	u.Detail("Hostname", target.Hostname)
	u.Detail("Account", target.AccountID)
	u.Detail("Zone", fmt.Sprintf("%s (%s)", target.ZoneName, target.ZoneID))
	u.Detail("Tunnel", tunnelName)

	tunnelID := ""
	tunnelToken := ""
	if st != nil && st.Management() == tunnelManagementRemote && st.AccountID == target.AccountID && st.TunnelID != "" {
		tunnelID = st.TunnelID
		tok, tokenErr := client.GetTunnelToken(target.AccountID, tunnelID)
		if tokenErr != nil {
			u.Warnf("Existing tunnel token fetch failed (%v); creating a new tunnel...", tokenErr)
			tunnelID = ""
		} else {
			tunnelToken = tok
		}
	}

	if tunnelID == "" {
		t, createErr := client.CreateTunnel(target.AccountID, tunnelName)
		if createErr != nil {
			return createErr
		}
		tunnelID = t.ID
		tunnelToken = t.Token
	}

	if err := client.UpdateTunnelConfiguration(target.AccountID, tunnelID, target.Hostname, "http://traefik.traefik.svc.cluster.local:80"); err != nil {
		return err
	}
	if err := client.UpsertTunnelDNSRecord(target.ZoneID, target.Hostname, tunnelID+".cfargotunnel.com"); err != nil {
		return err
	}
	if err := saveRemoteTunnelToken(cfg, tunnelToken); err != nil {
		return fmt.Errorf("save tunnel token locally: %w", err)
	}
	if err := applyTunnelTokenSecret(cfg, u, kubeconfigPath, tunnelToken); err != nil {
		return err
	}
	if err := deleteLocalManagedK8sResources(cfg, u, kubeconfigPath); err != nil {
		return err
	}
	if err := applyManagementModeConfigMap(cfg, u, kubeconfigPath, tunnelManagementRemote, transportProtocol); err != nil {
		return err
	}

	// Ensure cloudflared switches to remotely-managed mode immediately.
	if err := helmUpgradeCloudflared(cfg, u, kubeconfigPath); err != nil {
		return err
	}

	if st == nil {
		st = &tunnelState{}
	}
	st.ExposureMode = tunnelExposurePersistent
	st.ManagementMode = tunnelManagementRemote
	st.TransportProtocol = transportProtocol
	st.Hostname = target.Hostname
	st.AccountID = target.AccountID
	st.ZoneID = target.ZoneID
	st.TunnelID = tunnelID
	st.TunnelName = tunnelName
	if err := saveTunnelState(cfg, st); err != nil {
		return fmt.Errorf("tunnel provisioned, but failed to save local state: %w", err)
	}

	tunnelURL := "https://" + target.Hostname
	if err := SyncAgentBaseURL(cfg, tunnelURL); err != nil {
		u.Warnf("could not sync AGENT_BASE_URL to obol-agent: %v", err)
	}
	if err := SyncTunnelConfigMap(cfg, tunnelURL); err != nil {
		u.Warnf("could not sync tunnel URL to frontend ConfigMap: %v", err)
	}

	u.Blank()
	u.Success("Tunnel provisioned")
	u.Printf("Persistent URL: %s", tunnelURL)
	u.Print("Tip: run 'obol tunnel status' to verify the connector is active.")

	return nil
}

func resolveProvisionTarget(client *cloudflareClient, opts ProvisionOptions) (*resolvedProvisionTarget, error) {
	hostname := normalizeHostname(opts.Hostname)
	if hostname == "" {
		return nil, errors.New("hostname is required")
	}

	zoneName, err := extractZoneName(hostname)
	if err != nil {
		return nil, err
	}

	accountID := strings.TrimSpace(opts.AccountID)
	zoneID := strings.TrimSpace(opts.ZoneID)

	if zoneID == "" {
		zone, zoneErr := client.ResolveZoneForHostname(hostname)
		if zoneErr != nil {
			if errors.Is(zoneErr, errCloudflareZoneNotFound) {
				return nil, fmt.Errorf("could not resolve a Cloudflare zone for %s: %w. Either add the zone to Cloudflare first or use 'obol tunnel setup --register-domain'", hostname, zoneErr)
			}

			return nil, fmt.Errorf("cloudflare zone lookup failed for %s: %w", hostname, zoneErr)
		}
		zoneID = zone.ID
		zoneName = zone.Name
		if accountID == "" {
			accountID = zone.Account.ID
		}
	}

	if accountID == "" {
		resolvedAccountID, resolveErr := client.ResolveAccountID(accountID)
		if resolveErr != nil {
			return nil, resolveErr
		}
		accountID = resolvedAccountID
	}

	return &resolvedProvisionTarget{
		Hostname:  hostname,
		AccountID: accountID,
		ZoneID:    zoneID,
		ZoneName:  zoneName,
	}, nil
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
