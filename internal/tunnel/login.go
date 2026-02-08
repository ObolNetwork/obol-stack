package tunnel

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/stack"
)

type LoginOptions struct {
	Hostname string
}

// Login provisions a locally-managed tunnel using `cloudflared tunnel login` (browser auth),
// then writes the required credentials/config into Kubernetes and upgrades the cloudflared
// Helm release so the in-cluster connector runs the locally-managed tunnel.
//
// Docs:
// - Create a locally-managed tunnel: https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/local-management/create-local-tunnel/
// - Configuration file for published apps: https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/local-management/configuration-file/
// - `origincert` run parameter (locally-managed tunnels): https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/cloudflared-parameters/run-parameters/
func Login(cfg *config.Config, opts LoginOptions) error {
	hostname := normalizeHostname(opts.Hostname)
	if hostname == "" {
		return fmt.Errorf("--hostname is required (e.g. stack.example.com)")
	}

	// Stack must be running so we can write secrets/config to the cluster.
	kubeconfigPath, err := requireRunningStack(cfg)
	if err != nil {
		return err
	}

	stackID := stack.GetStackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack not initialized, run 'obol stack init' first")
	}
	tunnelName := fmt.Sprintf("obol-stack-%s", stackID)

	cloudflaredPath := filepath.Join(cfg.BinDir, "cloudflared")
	if _, err := os.Stat(cloudflaredPath); err != nil {
		var lookErr error
		cloudflaredPath, lookErr = exec.LookPath("cloudflared")
		if lookErr != nil {
			return fmt.Errorf("cloudflared not found. Install it and ensure it's in PATH (or place it in %s)", cfg.BinDir)
		}
	}

	fmt.Println("Authenticating cloudflared (browser)...")
	loginCmd := exec.Command(cloudflaredPath, "tunnel", "login")
	loginCmd.Stdin = os.Stdin
	loginCmd.Stdout = os.Stdout
	loginCmd.Stderr = os.Stderr
	if err := loginCmd.Run(); err != nil {
		return fmt.Errorf("cloudflared tunnel login failed: %w", err)
	}

	fmt.Printf("\nCreating tunnel: %s\n", tunnelName)
	if out, err := exec.Command(cloudflaredPath, "tunnel", "create", tunnelName).CombinedOutput(); err != nil {
		// "Already exists" is common if user re-runs. We'll recover by querying tunnel info.
		fmt.Printf("cloudflared tunnel create returned an error (continuing): %s\n", strings.TrimSpace(string(out)))
	}

	infoOut, err := exec.Command(cloudflaredPath, "tunnel", "info", tunnelName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cloudflared tunnel info failed: %w\n%s", err, strings.TrimSpace(string(infoOut)))
	}
	tunnelID, err := parseFirstUUID(string(infoOut))
	if err != nil {
		return fmt.Errorf("could not parse tunnel UUID from cloudflared tunnel info:\n%s", strings.TrimSpace(string(infoOut)))
	}

	cloudflaredDir := defaultCloudflaredDir()
	certPath := filepath.Join(cloudflaredDir, "cert.pem")
	credPath := filepath.Join(cloudflaredDir, tunnelID+".json")

	cert, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", certPath, err)
	}
	cred, err := os.ReadFile(credPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", credPath, err)
	}

	fmt.Printf("\nCreating DNS route for %s...\n", hostname)
	routeOut, err := exec.Command(cloudflaredPath, "tunnel", "route", "dns", tunnelName, hostname).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cloudflared tunnel route dns failed: %w\n%s", err, strings.TrimSpace(string(routeOut)))
	}

	if err := applyLocalManagedK8sResources(cfg, kubeconfigPath, hostname, tunnelID, cert, cred); err != nil {
		return err
	}

	// Re-render the chart so it flips from quick tunnel to locally-managed.
	if err := helmUpgradeCloudflared(cfg, kubeconfigPath); err != nil {
		return err
	}

	st, err := loadTunnelState(cfg)
	if err != nil {
		return fmt.Errorf("failed to read tunnel state: %w", err)
	}
	if st == nil {
		st = &tunnelState{}
	}
	st.Mode = "local"
	st.Hostname = hostname
	st.TunnelID = tunnelID
	st.TunnelName = tunnelName
	if err := saveTunnelState(cfg, st); err != nil {
		return fmt.Errorf("tunnel created, but failed to save local state: %w", err)
	}

	fmt.Println("\n✓ Tunnel login complete")
	fmt.Printf("Persistent URL: https://%s\n", hostname)
	fmt.Println("Tip: run 'obol tunnel status' to verify the connector is active.")
	return nil
}

func defaultCloudflaredDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".cloudflared"
	}
	return filepath.Join(home, ".cloudflared")
}

func parseFirstUUID(s string) (string, error) {
	re := regexp.MustCompile(`[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}`)
	if m := re.FindString(strings.ToLower(s)); m != "" {
		return m, nil
	}
	return "", fmt.Errorf("uuid not found")
}

func applyLocalManagedK8sResources(cfg *config.Config, kubeconfigPath, hostname, tunnelID string, certPEM, credJSON []byte) error {
	// Secret: account certificate + tunnel credentials (locally-managed tunnel requires origincert).
	secretYAML, err := buildLocalManagedSecretYAML(certPEM, credJSON)
	if err != nil {
		return err
	}
	if err := kubectlApplyManifest(cfg, kubeconfigPath, secretYAML); err != nil {
		return err
	}

	// ConfigMap: config.yml + tunnel_id used for command arg expansion.
	cfgYAML := buildLocalManagedConfigYAML(hostname, tunnelID)
	if err := kubectlApplyManifest(cfg, kubeconfigPath, cfgYAML); err != nil {
		return err
	}

	return nil
}

func buildLocalManagedSecretYAML(certPEM, credJSON []byte) ([]byte, error) {
	certB64 := base64.StdEncoding.EncodeToString(certPEM)
	credB64 := base64.StdEncoding.EncodeToString(credJSON)

	secret := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
data:
  cert.pem: %s
  credentials.json: %s
`, localManagedSecretName, tunnelNamespace, certB64, credB64)
	return []byte(secret), nil
}

func buildLocalManagedConfigYAML(hostname, tunnelID string) []byte {
	cfg := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  tunnel_id: %s
  config.yml: |
    tunnel: %s
    credentials-file: /etc/cloudflared/credentials.json

    ingress:
      - hostname: %s
        service: %s
      - service: http_status:404
`, localManagedConfigMapName, tunnelNamespace, tunnelID, tunnelID, hostname, tunnelServiceURL)
	return []byte(cfg)
}
