package tunnel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// RestorePersistentResources rehydrates the in-cluster cloudflared resources from
// local state so persistent tunnels survive stack recreation.
func RestorePersistentResources(cfg *config.Config, u *ui.UI) error {
	st, err := loadTunnelState(cfg)
	if err != nil {
		return fmt.Errorf("load tunnel state: %w", err)
	}

	if st == nil || !st.IsPersistent() || st.Hostname == "" {
		return nil
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("stack not running, use 'obol stack up' first")
	}

	switch st.Management() {
	case tunnelManagementLocal:
		return restoreLocalManagedResources(cfg, u, kubeconfigPath, st)
	case tunnelManagementRemote:
		return restoreRemoteManagedResources(cfg, u, kubeconfigPath, st)
	default:
		return nil
	}
}

func restoreLocalManagedResources(cfg *config.Config, u *ui.UI, kubeconfigPath string, st *tunnelState) error {
	if st.TunnelID == "" {
		return errors.New("persistent local tunnel is missing tunnel_id; rerun 'obol tunnel login --hostname <host>'")
	}

	cloudflaredDir := defaultCloudflaredDir()
	certPath := filepath.Join(cloudflaredDir, "cert.pem")
	credPath := filepath.Join(cloudflaredDir, st.TunnelID+".json")

	cert, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("read local cloudflared cert %s: %w", certPath, err)
	}

	cred, err := os.ReadFile(credPath)
	if err != nil {
		return fmt.Errorf("read local cloudflared credentials %s: %w", credPath, err)
	}

	if err := applyLocalManagedK8sResources(cfg, u, kubeconfigPath, st.Hostname, st.TunnelID, cert, cred); err != nil {
		return err
	}
	if err := deleteRemoteManagedK8sResources(cfg, u, kubeconfigPath); err != nil {
		return err
	}
	if err := deleteRemoteTunnelToken(cfg); err != nil {
		return err
	}
	if err := applyManagementModeConfigMap(cfg, u, kubeconfigPath, tunnelManagementLocal); err != nil {
		return err
	}

	return helmUpgradeCloudflared(cfg, u, kubeconfigPath)
}

func restoreRemoteManagedResources(cfg *config.Config, u *ui.UI, kubeconfigPath string, st *tunnelState) error {
	token, err := loadRemoteTunnelToken(cfg)
	if err != nil {
		return fmt.Errorf("load stored tunnel token: %w", err)
	}

	if token == "" && st.AccountID != "" && st.TunnelID != "" {
		if apiToken := os.Getenv("CLOUDFLARE_API_TOKEN"); apiToken != "" {
			client := newCloudflareClient(apiToken)
			token, err = client.GetTunnelToken(st.AccountID, st.TunnelID)
			if err == nil && token != "" {
				_ = saveRemoteTunnelToken(cfg, token)
			}
		}
	}

	if token == "" {
		return errors.New("persistent remote tunnel token is unavailable locally; rerun 'obol tunnel provision --hostname <host>' or set CLOUDFLARE_API_TOKEN and retry")
	}

	if err := applyTunnelTokenSecret(cfg, u, kubeconfigPath, token); err != nil {
		return err
	}
	if err := deleteLocalManagedK8sResources(cfg, u, kubeconfigPath); err != nil {
		return err
	}
	if err := applyManagementModeConfigMap(cfg, u, kubeconfigPath, tunnelManagementRemote); err != nil {
		return err
	}

	return helmUpgradeCloudflared(cfg, u, kubeconfigPath)
}
