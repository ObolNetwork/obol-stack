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
// If facilitatorURL is empty, the default (https://facilitator.x402.rs) is used.
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
		facilitatorURL = "https://facilitator.x402.rs"
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

	fmt.Printf("x402 configured: wallet=%s chain=%s\n", wallet, chain)
	return nil
}

// AddRoute adds a pricing route to the x402 ConfigMap.
// Optional per-route payTo and network override the global config when set.
func AddRoute(cfg *config.Config, pattern, price, description string, opts ...RouteOption) error {
	if err := EnsureVerifier(cfg); err != nil {
		return fmt.Errorf("deploy x402 verifier: %w", err)
	}

	rule := RouteRule{
		Pattern:     pattern,
		Price:       price,
		Description: description,
	}
	for _, opt := range opts {
		opt(&rule)
	}

	pcfg, err := GetPricingConfig(cfg)
	if err != nil {
		return err
	}

	replaced := false
	for i := range pcfg.Routes {
		if sameRouteIdentity(pcfg.Routes[i], rule) {
			pcfg.Routes[i] = rule
			replaced = true
			break
		}
	}
	if !replaced {
		pcfg.Routes = append(pcfg.Routes, rule)
	}
	return WritePricingConfig(cfg, pcfg)
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

// WithUpstreamAuth sets the upstream Authorization header injected on success.
func WithUpstreamAuth(upstreamAuth string) RouteOption {
	return func(r *RouteRule) { r.UpstreamAuth = upstreamAuth }
}

// WithPriceMetadata records the source pricing model behind the enforced Price.
func WithPriceMetadata(model, perMTok string, approxTokensPerRequest int) RouteOption {
	return func(r *RouteRule) {
		r.PriceModel = model
		r.PerMTok = perMTok
		r.ApproxTokensPerRequest = approxTokensPerRequest
	}
}

// WithOfferInfo records the originating ServiceOffer identity.
func WithOfferInfo(namespace, name string) RouteOption {
	return func(r *RouteRule) {
		r.OfferNamespace = namespace
		r.OfferName = name
	}
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

// WritePricingConfig writes the pricing config to the cluster ConfigMap.
func WritePricingConfig(cfg *config.Config, pcfg *PricingConfig) error {
	bin, kc := kubectl.Paths(cfg)
	copy := *pcfg
	copy.Routes = nil
	return patchPricingConfig(bin, kc, &copy)
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

func DeleteStaticOfferRoute(cfg *config.Config, namespace, offerName string) error {
	if namespace == "" {
		namespace = x402Namespace
	}
	pcfg, err := GetPricingConfig(cfg)
	if err != nil {
		return err
	}

	filtered := pcfg.Routes[:0]
	for _, route := range pcfg.Routes {
		if route.OfferNamespace == namespace && route.OfferName == offerName {
			continue
		}
		filtered = append(filtered, route)
	}
	pcfg.Routes = filtered
	return WritePricingConfig(cfg, pcfg)
}

// DeletePaymentRoute is kept as a compatibility alias for the old static
// ConfigMap-backed route management path.
func DeletePaymentRoute(cfg *config.Config, namespace, offerName string) error {
	return DeleteStaticOfferRoute(cfg, namespace, offerName)
}

func sameRouteIdentity(left, right RouteRule) bool {
	if left.OfferNamespace != "" || right.OfferNamespace != "" || left.OfferName != "" || right.OfferName != "" {
		return left.OfferNamespace == right.OfferNamespace && left.OfferName == right.OfferName
	}
	return left.Pattern == right.Pattern
}
