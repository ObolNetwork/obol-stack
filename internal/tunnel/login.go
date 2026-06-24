package tunnel

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

type LoginOptions struct {
	Hostname          string
	TransportProtocol string

	// OverwriteDNS passes --overwrite-dns to `cloudflared tunnel route dns`.
	// Without it, cloudflared refuses to replace an existing A/AAAA/CNAME
	// record at the hostname, so re-running the wizard after a prior attempt
	// fails with "An A, AAAA, or CNAME record with that host already exists"
	// (Cloudflare API error 1003).
	OverwriteDNS bool
}

// Login provisions a locally-managed tunnel using `cloudflared tunnel login` (browser auth),
// then writes the required credentials/config into Kubernetes and upgrades the cloudflared
// Helm release so the in-cluster connector runs the locally-managed tunnel.
//
// Docs:
// - Create a locally-managed tunnel: https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/local-management/create-local-tunnel/
// - Configuration file for published apps: https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/local-management/configuration-file/
// - `origincert` run parameter (locally-managed tunnels): https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/cloudflared-parameters/run-parameters/
func Login(cfg *config.Config, u *ui.UI, opts LoginOptions) error {
	hostname := normalizeHostname(opts.Hostname)
	if hostname == "" {
		return errors.New("--hostname is required (e.g. stack.example.com)")
	}
	transportProtocol, err := validateTunnelTransportProtocol(opts.TransportProtocol)
	if err != nil {
		return err
	}

	// Stack must be running so we can write secrets/config to the cluster.
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("stack not running, use 'obol stack up' first")
	}

	stackID := getStackID(cfg)
	if stackID == "" {
		return errors.New("stack not initialized, run 'obol stack init' first")
	}

	st, _ := loadTunnelState(cfg)
	tunnelName := desiredPersistentTunnelName(stackID, st, tunnelManagementLocal)

	cloudflaredPath, err := exec.LookPath("cloudflared")
	if err != nil {
		return errors.New("cloudflared not found in PATH. Install it first (e.g. 'brew install cloudflared' on macOS)")
	}

	u.Info("Authenticating cloudflared (browser)...")

	loginCmd := exec.Command(cloudflaredPath, "tunnel", "login")
	loginCmd.Stdin = os.Stdin
	loginCmd.Stdout = os.Stdout

	loginCmd.Stderr = os.Stderr
	if err := loginCmd.Run(); err != nil {
		return fmt.Errorf("cloudflared tunnel login failed: %w", err)
	}

	u.Infof("Creating tunnel: %s", tunnelName)

	if out, err := exec.Command(cloudflaredPath, "tunnel", "create", tunnelName).CombinedOutput(); err != nil {
		// "Already exists" is common if user re-runs. We'll recover by querying tunnel info.
		u.Warnf("cloudflared tunnel create returned an error (continuing): %s", strings.TrimSpace(string(out)))
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

	u.Infof("Creating DNS route for %s...", hostname)

	routeArgs := routeDNSArgs(tunnelName, hostname, opts.OverwriteDNS)
	routeOut, err := exec.Command(cloudflaredPath, routeArgs...).CombinedOutput()
	if err != nil {
		hint := ""
		if !opts.OverwriteDNS && strings.Contains(string(routeOut), "record with that host already exists") {
			hint = "\nhint: a record for this hostname already exists. Re-run with --overwrite-dns to replace it."
		}
		return fmt.Errorf("cloudflared tunnel route dns failed: %w\n%s%s", err, strings.TrimSpace(string(routeOut)), hint)
	}
	if err := verifyRoutedHostname(string(routeOut), hostname); err != nil {
		return err
	}

	if err := applyLocalManagedK8sResources(cfg, u, kubeconfigPath, hostname, tunnelID, cert, cred); err != nil {
		return err
	}
	if err := deleteRemoteManagedK8sResources(cfg, u, kubeconfigPath); err != nil {
		return err
	}
	if err := deleteRemoteTunnelToken(cfg); err != nil {
		return err
	}
	if err := applyManagementModeConfigMap(cfg, u, kubeconfigPath, tunnelManagementLocal, transportProtocol); err != nil {
		return err
	}

	// Re-render the chart so it flips from quick tunnel to locally-managed.
	if err := helmUpgradeCloudflared(cfg, u, kubeconfigPath); err != nil {
		return err
	}

	if st == nil {
		st = &tunnelState{}
	}

	st.ExposureMode = tunnelExposurePersistent
	st.ManagementMode = tunnelManagementLocal
	st.TransportProtocol = transportProtocol
	st.Hostname = hostname
	st.AccountID = ""
	st.ZoneID = ""
	st.TunnelID = tunnelID
	st.TunnelName = tunnelName
	if err := saveTunnelState(cfg, st); err != nil {
		return fmt.Errorf("tunnel created, but failed to save local state: %w", err)
	}

	tunnelURL := "https://" + hostname

	// Inject AGENT_BASE_URL into obol-agent overlay if deployed.
	if err := SyncAgentBaseURL(cfg, tunnelURL); err != nil {
		u.Warnf("could not sync AGENT_BASE_URL to obol-agent: %v", err)
	}

	// Write tunnel URL to ConfigMap so the frontend can read it.
	if err := SyncTunnelConfigMap(cfg, tunnelURL); err != nil {
		u.Warnf("could not sync tunnel URL to frontend ConfigMap: %v", err)
	}

	u.Blank()
	u.Success("Tunnel login complete")
	u.Printf("Persistent URL: https://%s", hostname)
	u.Print("Tip: run 'obol tunnel status' to verify the connector is active.")

	return nil
}

// routeDNSArgs builds the cloudflared argument vector for the
// `tunnel route dns` subcommand. When overwrite is true, --overwrite-dns is
// inserted between `dns` and the tunnel/hostname so cloudflared replaces an
// existing A/AAAA/CNAME record at the hostname instead of failing with API
// error 1003.
func routeDNSArgs(tunnelName, hostname string, overwrite bool) []string {
	args := []string{"tunnel", "route", "dns"}
	if overwrite {
		args = append(args, "--overwrite-dns")
	}
	return append(args, tunnelName, hostname)
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

	return "", errors.New("uuid not found")
}

// routedHostnameRegexps matches the two log shapes cloudflared emits on a
// successful `tunnel route dns` invocation:
//
//	... INF Added CNAME <hostname> which will route to this tunnel tunnelID=<UUID>
//	... INF <hostname> is already configured to route to your tunnel tunnelID=<UUID>
var routedHostnameRegexps = []*regexp.Regexp{
	regexp.MustCompile(`Added CNAME\s+(\S+)\s+which will route to this tunnel`),
	regexp.MustCompile(`(\S+)\s+is already configured to route to your tunnel`),
}

// verifyRoutedHostname parses cloudflared's combined stdout/stderr from
// `tunnel route dns` and returns an error when the hostname it actually routed
// differs from the requested hostname. This catches the silent zone-fallback
// failure mode where ~/.cloudflared/cert.pem is scoped to a Cloudflare account
// that does not own the requested zone, and cloudflared appends its default
// zone to the request (e.g. requested "inference.v1337.org" → routed
// "inference.v1337.org.humanresearch.ai").
//
// If neither known log pattern is found, the parser returns nil so a benign
// upstream log-format change does not break the happy path.
func verifyRoutedHostname(routedOutput, requestedHostname string) error {
	want := strings.TrimSpace(requestedHostname)
	if want == "" {
		return nil
	}

	for _, re := range routedHostnameRegexps {
		m := re.FindStringSubmatch(routedOutput)
		if len(m) < 2 {
			continue
		}

		got := strings.TrimSpace(m[1])
		if strings.EqualFold(got, want) {
			return nil
		}

		return fmt.Errorf(
			"cloudflared routed DNS to %q, not %q. Your ~/.cloudflared/cert.pem is scoped to a Cloudflare account that does not own the zone for %q. Move or delete the cert and re-run `obol tunnel login` to authorize a fresh cert against the correct account.",
			got, want, want,
		)
	}

	return nil
}

func applyLocalManagedK8sResources(cfg *config.Config, u *ui.UI, kubeconfigPath, hostname, tunnelID string, certPEM, credJSON []byte) error {
	// Secret: account certificate + tunnel credentials (locally-managed tunnel requires origincert).
	secretYAML := buildLocalManagedSecretYAML(certPEM, credJSON)

	if err := kubectlApply(cfg, u, kubeconfigPath, secretYAML); err != nil {
		return err
	}

	// ConfigMap: config.yml + tunnel_id used for command arg expansion.
	cfgYAML := buildLocalManagedConfigYAML(hostname, tunnelID)
	if err := kubectlApply(cfg, u, kubeconfigPath, cfgYAML); err != nil {
		return err
	}

	return nil
}

const (
	localManagedSecretName    = "cloudflared-local-credentials"
	localManagedConfigMapName = "cloudflared-local-config"
)

func buildLocalManagedSecretYAML(certPEM, credJSON []byte) []byte {
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

	return []byte(secret)
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
        service: http://traefik.traefik.svc.cluster.local:80
      - service: http_status:404
`, localManagedConfigMapName, tunnelNamespace, tunnelID, tunnelID, hostname)

	return []byte(cfg)
}

func kubectlApply(cfg *config.Config, u *ui.UI, kubeconfigPath string, manifest []byte) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")

	// Server-side apply: the server performs the merge, so kubectl skips the
	// client-side OpenAPI schema download (`/openapi/v2`). That endpoint is flaky
	// on freshly-started k3d clusters and returns EOF/timeouts even when normal
	// CRUD works, which would otherwise fail this apply. Matches the robust apply
	// path used elsewhere (openclaw, serviceoffer-controller).
	cmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"apply", "--server-side", "--force-conflicts", "-f", "-",
	)

	cmd.Stdin = bytes.NewReader(manifest)
	if err := u.Exec(ui.ExecConfig{
		Name: "Applying Kubernetes manifest",
		Cmd:  cmd,
	}); err != nil {
		return fmt.Errorf("kubectl apply failed: %w", err)
	}

	return nil
}
