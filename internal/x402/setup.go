package x402

import (
	"fmt"
	"os"
	"strings"

	"encoding/json"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"gopkg.in/yaml.v3"
)

const (
	x402Namespace    = "x402"
	pricingConfigMap = "x402-pricing"
	x402SecretName   = "x402-secrets"

	// DefaultFacilitatorURL is the Obol-operated x402 facilitator for payment
	// verification and settlement. Supports Base Mainnet and Base Sepolia.
	DefaultFacilitatorURL = "https://x402.gcp.obol.tech"
)

var x402Manifest = mustReadX402Manifest()

func mustReadX402Manifest() []byte {
	data, err := embed.ReadInfrastructureFile("base/templates/x402.yaml")
	if err != nil {
		panic(fmt.Sprintf("read embedded x402 manifest: %v", err))
	}
	return data
}

// EnsureVerifier deploys the x402 verifier subsystem if it doesn't exist.
// Idempotent — kubectl apply is safe to run multiple times.
func EnsureVerifier(cfg *config.Config) error {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return err
	}
	bin, kc := kubectl.Paths(cfg)

	fmt.Println("Applying x402 payment components...")
	return kubectl.Apply(bin, kc, x402Manifest)
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

	// Populate the CA certificates bundle from the host so the distroless
	// verifier image can TLS-verify the facilitator.
	populateCABundle(bin, kc)

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
		VerifyOnly:     false,
		Routes:         existingRoutes,
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

// populateCABundle reads the host's CA certificate bundle and patches
// it into the ca-certificates ConfigMap in the x402 namespace. The
// x402-verifier image is distroless and ships without a CA store, so
// TLS verification of external facilitators fails without this.
func populateCABundle(bin, kc string) {
	// Common CA bundle paths across Linux distros and macOS.
	candidates := []string{
		"/etc/ssl/certs/ca-certificates.crt", // Debian/Ubuntu
		"/etc/pki/tls/certs/ca-bundle.crt",   // RHEL/Fedora
		"/etc/ssl/cert.pem",                   // macOS / Alpine
	}
	var caData []byte
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			caData = data
			break
		}
	}
	if len(caData) == 0 {
		return // no CA bundle found — skip silently
	}

	patch := map[string]any{"data": map[string]string{"ca-certificates.crt": string(caData)}}
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return
	}
	_ = kubectl.RunSilent(bin, kc,
		"patch", "configmap", "ca-certificates", "-n", x402Namespace,
		"-p", string(patchJSON), "--type=merge")
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

