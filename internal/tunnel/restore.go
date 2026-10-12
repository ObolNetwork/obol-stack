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
		u.Warn("This tunnel was created with 'obol tunnel login', which was removed in v0.16. It keeps")
		u.Warn("  running, but its hostnames can't be changed. Move it to a connector token from the")
		u.Warn("  Cloudflare dashboard: obol tunnel setup <connector-token> --hostname " + st.Hostname)

		return restoreLocalManagedResources(cfg, u, kubeconfigPath, st)
	case tunnelManagementRemote:
		return restoreRemoteManagedResources(cfg, u, kubeconfigPath, st)
	default:
		return nil
	}
}

func restoreLocalManagedResources(cfg *config.Config, u *ui.UI, kubeconfigPath string, st *tunnelState) error {
	if st.TunnelID == "" {
		return errLocalManagedNoChanges
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

	if err := applyLocalManagedK8sResources(cfg, u, kubeconfigPath, st.HostnameSet(), st.TunnelID, cert, cred); err != nil {
		return err
	}
	if err := deleteRemoteManagedK8sResources(cfg, u, kubeconfigPath); err != nil {
		return err
	}
	if err := deleteRemoteTunnelToken(cfg); err != nil {
		return err
	}
	if err := applyManagementModeConfigMap(cfg, u, kubeconfigPath, tunnelManagementLocal, tunnelTransportProtocol(st)); err != nil {
		return err
	}

	return helmUpgradeCloudflared(cfg, u, kubeconfigPath)
}

func restoreRemoteManagedResources(cfg *config.Config, u *ui.UI, kubeconfigPath string, st *tunnelState) error {
	token, err := loadRemoteTunnelToken(cfg)
	if err != nil {
		return fmt.Errorf("load stored tunnel token: %w", err)
	}

	if token == "" {
		return errors.New("persistent tunnel token is unavailable locally; rerun 'obol tunnel setup <connector-token>' to reconfigure the tunnel")
	}

	if err := applyTunnelTokenSecret(cfg, u, kubeconfigPath, token); err != nil {
		return err
	}
	if err := deleteLocalManagedK8sResources(cfg, u, kubeconfigPath); err != nil {
		return err
	}
	if err := applyManagementModeConfigMap(cfg, u, kubeconfigPath, tunnelManagementRemote, tunnelTransportProtocol(st)); err != nil {
		return err
	}

	return helmUpgradeCloudflared(cfg, u, kubeconfigPath)
}
