package x402

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"gopkg.in/yaml.v3"
)

const (
	x402Namespace     = "x402"
	pricingConfigMap  = "x402-pricing"
	x402SecretName    = "x402-secrets"
)

// Setup configures x402 pricing in the cluster by patching the ConfigMap
// and Secret. Stakater Reloader auto-restarts the verifier pod.
// If facilitatorURL is empty, the default (https://facilitator.x402.rs) is used.
func Setup(cfg *config.Config, wallet, chain, facilitatorURL string) error {
	if err := ValidateWallet(wallet); err != nil {
		return err
	}
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return err
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

	// 2. Update the pricing ConfigMap with wallet and chain.
	fmt.Printf("Updating x402 pricing config...\n")
	if facilitatorURL == "" {
		facilitatorURL = "https://facilitator.x402.rs"
	}
	pricingCfg := &PricingConfig{
		Wallet:         wallet,
		Chain:          chain,
		FacilitatorURL: facilitatorURL,
		VerifyOnly:     false,
		Routes:         []RouteRule{},
	}
	if err := patchPricingConfig(bin, kc, pricingCfg); err != nil {
		return fmt.Errorf("failed to patch x402 pricing: %w", err)
	}

	fmt.Printf("x402 configured: wallet=%s chain=%s\n", wallet, chain)
	return nil
}

// AddRoute adds a pricing route to the x402 ConfigMap.
// Optional per-route payTo and network override the global config when set.
func AddRoute(cfg *config.Config, pattern, price, description string, opts ...RouteOption) error {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return err
	}

	// Read current pricing config.
	pricingCfg, err := GetPricingConfig(cfg)
	if err != nil {
		return fmt.Errorf("read pricing config: %w", err)
	}

	// Build the route rule.
	rule := RouteRule{
		Pattern:     pattern,
		Price:       price,
		Description: description,
	}
	for _, opt := range opts {
		opt(&rule)
	}

	pricingCfg.Routes = append(pricingCfg.Routes, rule)

	// Re-serialize and patch.
	bin, kc := kubectl.Paths(cfg)
	return patchPricingConfig(bin, kc, pricingCfg)
}

// RouteOption is a functional option for AddRoute.
type RouteOption func(*RouteRule)

// WithPayTo sets a per-route payTo address (overrides global wallet).
func WithPayTo(payTo string) RouteOption {
	return func(r *RouteRule) { r.PayTo = payTo }
}

// WithNetwork sets a per-route network (overrides global chain).
func WithNetwork(network string) RouteOption {
	return func(r *RouteRule) { r.Network = network }
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
		return nil, fmt.Errorf("read x402 pricing ConfigMap: %w", err)
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

	return LoadConfig(tmpFile.Name())
}

// WritePricingConfig writes the pricing config to the cluster ConfigMap.
func WritePricingConfig(cfg *config.Config, pcfg *PricingConfig) error {
	bin, kc := kubectl.Paths(cfg)
	return patchPricingConfig(bin, kc, pcfg)
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
