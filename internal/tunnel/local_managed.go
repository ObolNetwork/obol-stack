package tunnel

// Locally-managed tunnels (created by the removed `obol tunnel login` /
// `tunnel setup --management local`) are still restored on `stack up` so an
// existing tunnel keeps serving after upgrading. Nothing here creates one or
// changes its hostnames; move it to a connector token with `obol tunnel setup`.

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// errLocalManagedNoChanges is returned for changes a local-managed tunnel can
// no longer make (they needed the host cloudflared login).
var errLocalManagedNoChanges = errors.New("this tunnel was created with 'obol tunnel login', which is no longer supported. " +
	"Create the tunnel in the Cloudflare dashboard (Networks → Tunnels), then run: obol tunnel setup <connector-token> --hostname <host>")

func defaultCloudflaredDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".cloudflared"
	}

	return filepath.Join(home, ".cloudflared")
}

func applyLocalManagedK8sResources(cfg *config.Config, u *ui.UI, kubeconfigPath string, hostnames []string, tunnelID string, certPEM, credJSON []byte) error {
	// Secret: account certificate + tunnel credentials (locally-managed tunnel requires origincert).
	secretYAML := buildLocalManagedSecretYAML(certPEM, credJSON)

	if err := kubectlApply(cfg, u, kubeconfigPath, secretYAML); err != nil {
		return err
	}

	// ConfigMap: config.yml + tunnel_id used for command arg expansion.
	cfgYAML := buildLocalManagedConfigYAML(hostnames, tunnelID)
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

// buildLocalManagedConfigYAML renders the cloudflared-local-config ConfigMap with
// one ingress rule per public hostname (all routed to the same in-cluster Traefik
// service), terminated by the mandatory catch-all http_status:404 rule. Adding or
// removing a hostname re-renders over the full set, so a second domain never
// displaces the first.
func buildLocalManagedConfigYAML(hostnames []string, tunnelID string) []byte {
	hostnames = normalizeHostnames(hostnames)

	var ingress strings.Builder
	for _, h := range hostnames {
		ingress.WriteString("      - hostname: ")
		ingress.WriteString(h)
		ingress.WriteString("\n        service: http://traefik.traefik.svc.cluster.local:80\n")
	}
	// Mandatory catch-all: cloudflared rejects an ingress list without one.
	ingress.WriteString("      - service: http_status:404\n")

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
%s`, localManagedConfigMapName, tunnelNamespace, tunnelID, tunnelID, ingress.String())

	return []byte(cfg)
}

// normalizeHostnames normalizes and de-duplicates a slice of hostnames,
// preserving first-seen order (so the primary stays at index 0).
func normalizeHostnames(in []string) []string {
	out := make([]string, 0, len(in))
	for _, h := range in {
		out = appendHostname(out, h)
	}

	return out
}

func kubectlApply(cfg *config.Config, u *ui.UI, kubeconfigPath string, manifest []byte) error {
	kubectlPath := cfg.ToolPath("kubectl")

	// Server-side apply: the server performs the merge, so kubectl skips the
	// client-side OpenAPI schema download (`/openapi/v2`). That endpoint is flaky
	// on freshly-started k3d clusters and returns EOF/timeouts even when normal
	// CRUD works, which would otherwise fail this apply. Matches the robust apply
	// path used elsewhere (serviceoffer-controller).
	cmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"apply", "--server-side", "--force-conflicts", "-f", "-",
	)

	cmd.Stdin = bytes.NewReader(manifest)

	err := u.Exec(ui.ExecConfig{
		Name:        "Applying Kubernetes manifest",
		Cmd:         cmd,
		Interactive: false,
	})
	if err != nil {
		return fmt.Errorf("kubectl apply failed: %w", err)
	}

	return nil
}
