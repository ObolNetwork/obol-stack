package x402

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	stackdefaults "github.com/ObolNetwork/obol-stack/internal/defaults"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/helmcmd"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"gopkg.in/yaml.v3"
)

// x402Manifest is the raw embedded x402.yaml. It is no longer applied
// directly via kubectl — helmfile renders the same file via the `base`
// release (see EnsureVerifier). Retained as a package-level value so
// shape/content tests can assert invariants about the embedded source.
var x402Manifest = mustReadX402Manifest()

func mustReadX402Manifest() []byte {
	data, err := embed.ReadInfrastructureFile("base/templates/x402.yaml")
	if err != nil {
		panic(fmt.Sprintf("read embedded x402 manifest: %v", err))
	}
	return data
}

const (
	x402Namespace    = "x402"
	pricingConfigMap = "x402-pricing"
	x402SecretName   = "x402-secrets"

	// DefaultFacilitatorURL is the Obol-operated x402 facilitator for payment
	// verification and settlement. Supports Base Mainnet and Base Sepolia.
	DefaultFacilitatorURL = "https://x402.gcp.obol.tech"

	// DefaultBuySellerURL is the Obol-operated demo seller used by
	// `obol buy inference` when no --seller is given.
	// TODO(buy): replace with the live URL once the default seller is provisioned.
	DefaultBuySellerURL = "https://demo-seller.obol.tech/services/default-paid"

	// DefaultBuySellerAgentID is the ERC-8004 tokenId the buyer expects to
	// see in the seller's /.well-known/agent-registration.json before signing.
	// Hard-fails on mismatch unless --no-verify-identity is passed.
	// TODO(buy): replace with the live agentId once the default seller is registered.
	DefaultBuySellerAgentID int64 = 0

	// DefaultBuySellerChain is the chain the default seller settles on.
	// Used only as a hint in error messages; the actual chain is taken
	// from the seller's 402 response by buy.py.
	DefaultBuySellerChain = "base-sepolia"

	// baseReleaseName matches the helmfile release in
	// internal/embed/infrastructure/helmfile.yaml whose `chart: ./base`
	// renders the x402 manifests. EnsureVerifier targets this release
	// via --selector so the verifier deployment is reconciled the same
	// way `obol stack up` deploys it — single source of truth.
	baseReleaseName = "base"
)

// EnsureVerifier deploys the x402 verifier subsystem if it doesn't exist.
// Idempotent — helmfile sync is safe to run multiple times.
//
// Historical note: this used to read embed.FS x402.yaml directly and
// `kubectl apply` it, which fought helmfile's field manager and forced
// us to duplicate the dev-mode image-pin rewrite (formerly in this file,
// now lives canonically in internal/defaults/defaults.go). Driving the
// deployment through helmfile against the already-populated
// $OBOL_CONFIG_DIR/defaults/ tree picks up the canonical dev rewrite
// for free and removes the entire footgun. See CLAUDE.md pitfall #9.
func EnsureVerifier(cfg *config.Config) error {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return err
	}

	// Refresh the defaults tree so the helmfile sync below reads the
	// most recent embedded manifests. Under OBOL_DEVELOPMENT=true this
	// also applies the canonical digest-pin -> :latest rewrite via
	// defaults.rewriteDevDigestPins so freshly built local images are
	// honored. No-op when the stamp is up to date.
	backendName := stackdefaults.DetectedBackendName(cfg)
	stackID := stackdefaults.StackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack ID not found, run 'obol stack init' first")
	}
	if _, err := stackdefaults.RefreshInfrastructureIfChanged(cfg, backendName, stackID); err != nil {
		return fmt.Errorf("refresh infrastructure defaults: %w", err)
	}

	if err := helmfileSyncBaseRelease(cfg); err != nil {
		return fmt.Errorf("helmfile sync %s: %w", baseReleaseName, err)
	}

	// Populate the CA bundle after deploying the verifier so TLS verification
	// of the facilitator works immediately. Idempotent — safe to call multiple times.
	bin, kc := kubectl.Paths(cfg)
	populateCABundle(bin, kc)
	return nil
}

// helmfileSyncBaseRelease runs `helmfile --selector name=base sync`
// against the defaults helmfile rendered into $OBOL_CONFIG_DIR/defaults.
// This is the same invocation pattern used by `internal/stack.syncDefaults`
// and `internal/update.ApplyUpgrades`, scoped to the single release that
// owns the x402 manifests.
func helmfileSyncBaseRelease(cfg *config.Config) error {
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	helmfilePath := filepath.Join(cfg.ConfigDir, "defaults", "helmfile.yaml")

	if _, err := os.Stat(helmfilePath); err != nil {
		return fmt.Errorf("defaults helmfile not found at %s (run 'obol stack init' first): %w", helmfilePath, err)
	}

	helmfileBin := filepath.Join(cfg.BinDir, "helmfile")
	helmBin := filepath.Join(cfg.BinDir, "helm")

	args := []string{
		"--file", helmfilePath,
		"--kubeconfig", kubeconfigPath,
		"--selector", "name=" + baseReleaseName,
		"sync",
	}
	args = append(args, helmcmd.SyncFlagsForVersion(helmBin)...)

	cmd := exec.Command(helmfileBin, args...)
	cmd.Env = append(os.Environ(),
		"KUBECONFIG="+kubeconfigPath,
		"STACK_DATA_DIR="+cfg.DataDir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// PopulateCABundle reads the host's CA certificate bundle and replaces the
// ca-certificates ConfigMap in the x402 namespace. Call this whenever the
// x402 verifier is deployed or updated without going through EnsureVerifier.
// Silently skips if no CA bundle is found on the host.
func PopulateCABundle(cfg *config.Config) {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return
	}
	bin, kc := kubectl.Paths(cfg)
	populateCABundle(bin, kc)
}

// Setup configures x402 pricing in the cluster by patching the ConfigMap
// and Secret. Stakater Reloader auto-restarts the verifier pod.
// If facilitatorURL is empty, the Obol-operated facilitator is used.
func Setup(cfg *config.Config, wallet, chain, facilitatorURL string) error {
	if err := ValidateWallet(wallet); err != nil {
		return err
	}
	if err := EnsureVerifier(cfg); err != nil {
		return fmt.Errorf("deploy x402 verifier: %w", err)
	}
	bin, kc := kubectl.Paths(cfg)

	// 1. Patch the Secret with the wallet address.
	fmt.Printf("Configuring x402: setting wallet address...\n")
	secretPatch := map[string]any{"stringData": map[string]string{"WALLET_ADDRESS": wallet}}
	patchJSON, err := json.Marshal(secretPatch)
	if err != nil {
		return fmt.Errorf("marshal secret patch: %w", err)
	}
	if err := kubectl.Run(bin, kc,
		"patch", "secret", x402SecretName, "-n", x402Namespace,
		"-p", string(patchJSON), "--type=merge"); err != nil {
		return fmt.Errorf("failed to patch x402 secret: %w", err)
	}

	// 2. Update the pricing ConfigMap with wallet, chain, and any existing
	// static/manual routes.
	fmt.Printf("Updating x402 pricing config...\n")
	if facilitatorURL == "" {
		facilitatorURL = DefaultFacilitatorURL
	}
	existingCfg, _ := GetPricingConfig(cfg)
	var existingRoutes []RouteRule
	if existingCfg != nil {
		existingRoutes = existingCfg.Routes
	}
	pricingCfg := &PricingConfig{
		Wallet:         wallet,
		Chain:          chain,
		FacilitatorURL: facilitatorURL,
		// ForwardAuth should verify only; settlement is performed downstream
		// after a successful paid upstream response.
		VerifyOnly: true,
		Routes:     existingRoutes,
	}
	if err := patchPricingConfig(bin, kc, pricingCfg); err != nil {
		return fmt.Errorf("failed to patch x402 pricing: %w", err)
	}

	fmt.Printf("x402 configured: wallet=%s chain=%s facilitator=%s\n", wallet, chain, facilitatorURL)
	return nil
}

// GetPricingConfig reads the current x402 pricing ConfigMap from the cluster.
func GetPricingConfig(cfg *config.Config) (*PricingConfig, error) {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return nil, err
	}
	bin, kc := kubectl.Paths(cfg)

	raw, err := kubectl.Output(bin, kc,
		"get", "configmap", pricingConfigMap, "-n", x402Namespace,
		"-o", `jsonpath={.data.pricing\.yaml}`)
	if err != nil {
		// x402 namespace/configmap doesn't exist yet — not an error, just no config.
		return &PricingConfig{}, nil
	}

	if strings.TrimSpace(raw) == "" {
		return &PricingConfig{}, nil
	}

	// Write to temp file and load via existing parser.
	tmpFile, err := os.CreateTemp("", "x402-pricing-*.yaml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(raw); err != nil {
		tmpFile.Close()
		return nil, err
	}
	tmpFile.Close()

	pcfg, err := LoadConfig(tmpFile.Name())
	if err != nil {
		return nil, err
	}
	return pcfg, nil
}

// populateCABundle reads the host's CA certificate bundle and replaces
// the ca-certificates ConfigMap in the x402 namespace. The x402-verifier
// image is distroless and ships without a CA store, so TLS verification
// of external facilitators fails without this.
//
// Uses "kubectl create --dry-run | kubectl replace" instead of "kubectl
// apply" because the macOS CA bundle (~290KB) exceeds the 262KB
// annotation limit that kubectl apply requires.
func populateCABundle(bin, kc string) {
	// Common CA bundle paths across Linux distros and macOS.
	candidates := []string{
		"/etc/ssl/certs/ca-certificates.crt", // Debian/Ubuntu
		"/etc/pki/tls/certs/ca-bundle.crt",   // RHEL/Fedora
		"/etc/ssl/cert.pem",                  // macOS / Alpine
	}
	var caPath string
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			caPath = path
			break
		}
	}
	if caPath == "" {
		return // no CA bundle found — skip silently
	}

	// Pipe through kubectl create --dry-run to generate the ConfigMap YAML,
	// then kubectl replace to apply it without the annotation size limit.
	if err := kubectl.PipeCommands(bin, kc,
		[]string{
			"create", "configmap", "ca-certificates", "-n", x402Namespace,
			"--from-file=ca-certificates.crt=" + caPath,
			"--dry-run=client", "-o", "yaml",
		},
		[]string{"replace", "-f", "-"}); err != nil {
		return
	}

	// Restart the verifier so it picks up the newly populated CA bundle.
	// The ConfigMap is mounted as a volume; Kubernetes may take 60-120s to
	// propagate changes, and we need TLS to work immediately for the
	// facilitator connection.
	_ = kubectl.RunSilent(bin, kc,
		"rollout", "restart", "deployment/x402-verifier", "-n", x402Namespace)
}

func patchPricingConfig(bin, kc string, pcfg *PricingConfig) error {
	pricingBytes, err := yaml.Marshal(pcfg)
	if err != nil {
		return fmt.Errorf("marshal pricing config: %w", err)
	}

	cmPatch := map[string]any{
		"data": map[string]string{
			"pricing.yaml": string(pricingBytes),
		},
	}
	cmPatchJSON, err := json.Marshal(cmPatch)
	if err != nil {
		return fmt.Errorf("marshal pricing patch: %w", err)
	}

	return kubectl.Run(bin, kc,
		"patch", "configmap", pricingConfigMap, "-n", x402Namespace,
		"-p", string(cmPatchJSON), "--type=merge")
}
