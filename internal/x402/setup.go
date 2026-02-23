package x402

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

const (
	x402Namespace     = "x402"
	pricingConfigMap  = "x402-pricing"
	x402SecretName    = "x402-secrets"
)

// Setup configures x402 pricing in the cluster by patching the ConfigMap
// and Secret. Stakater Reloader auto-restarts the verifier pod.
func Setup(cfg *config.Config, wallet, chain string) error {
	kubectlBin := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfig := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		return fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	// 1. Patch the Secret with the wallet address.
	fmt.Printf("Configuring x402: setting wallet address...\n")
	patchJSON := fmt.Sprintf(`{"stringData":{"WALLET_ADDRESS":"%s"}}`, wallet)
	if err := kubectlRun(kubectlBin, kubeconfig,
		"patch", "secret", x402SecretName, "-n", x402Namespace,
		"-p", patchJSON, "--type=merge"); err != nil {
		return fmt.Errorf("failed to patch x402 secret: %w", err)
	}

	// 2. Update the pricing ConfigMap with wallet and chain.
	fmt.Printf("Updating x402 pricing config...\n")
	pricingYAML := fmt.Sprintf(`wallet: "%s"
chain: "%s"
facilitatorURL: "https://facilitator.x402.rs"
verifyOnly: false
routes: []
`, wallet, chain)

	cmPatch := map[string]interface{}{
		"data": map[string]string{
			"pricing.yaml": pricingYAML,
		},
	}
	cmPatchJSON, err := json.Marshal(cmPatch)
	if err != nil {
		return fmt.Errorf("marshal pricing patch: %w", err)
	}

	if err := kubectlRun(kubectlBin, kubeconfig,
		"patch", "configmap", pricingConfigMap, "-n", x402Namespace,
		"-p", string(cmPatchJSON), "--type=merge"); err != nil {
		return fmt.Errorf("failed to patch x402 pricing: %w", err)
	}

	fmt.Printf("x402 configured: wallet=%s chain=%s\n", wallet, chain)
	return nil
}

// AddRoute adds a pricing route to the x402 ConfigMap.
func AddRoute(cfg *config.Config, pattern, price, description string) error {
	kubectlBin := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfig := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		return fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	// Read current pricing config.
	pricingCfg, err := GetPricingConfig(cfg)
	if err != nil {
		return fmt.Errorf("read pricing config: %w", err)
	}

	// Add the new route.
	pricingCfg.Routes = append(pricingCfg.Routes, RouteRule{
		Pattern:     pattern,
		Price:       price,
		Description: description,
	})

	// Re-serialize and patch.
	return patchPricingConfig(kubectlBin, kubeconfig, pricingCfg)
}

// GetPricingConfig reads the current x402 pricing ConfigMap from the cluster.
func GetPricingConfig(cfg *config.Config) (*PricingConfig, error) {
	kubectlBin := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfig := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		return nil, fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	raw, err := kubectlOutput(kubectlBin, kubeconfig,
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

func patchPricingConfig(kubectlBin, kubeconfig string, pcfg *PricingConfig) error {
	// Serialize pricing config as YAML.
	var sb strings.Builder
	fmt.Fprintf(&sb, "wallet: \"%s\"\n", pcfg.Wallet)
	fmt.Fprintf(&sb, "chain: \"%s\"\n", pcfg.Chain)
	fmt.Fprintf(&sb, "facilitatorURL: \"%s\"\n", pcfg.FacilitatorURL)
	fmt.Fprintf(&sb, "verifyOnly: %v\n", pcfg.VerifyOnly)

	if len(pcfg.Routes) == 0 {
		sb.WriteString("routes: []\n")
	} else {
		sb.WriteString("routes:\n")
		for _, r := range pcfg.Routes {
			fmt.Fprintf(&sb, "  - pattern: \"%s\"\n", r.Pattern)
			fmt.Fprintf(&sb, "    price: \"%s\"\n", r.Price)
			if r.Description != "" {
				fmt.Fprintf(&sb, "    description: \"%s\"\n", r.Description)
			}
		}
	}

	cmPatch := map[string]interface{}{
		"data": map[string]string{
			"pricing.yaml": sb.String(),
		},
	}
	cmPatchJSON, err := json.Marshal(cmPatch)
	if err != nil {
		return fmt.Errorf("marshal pricing patch: %w", err)
	}

	return kubectlRun(kubectlBin, kubeconfig,
		"patch", "configmap", pricingConfigMap, "-n", x402Namespace,
		"-p", string(cmPatchJSON), "--type=merge")
}

func kubectlRun(binary, kubeconfig string, args ...string) error {
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfig))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return fmt.Errorf("%w: %s", err, errMsg)
		}
		return err
	}
	return nil
}

func kubectlOutput(binary, kubeconfig string, args ...string) (string, error) {
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfig))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return "", fmt.Errorf("%w: %s", err, errMsg)
		}
		return "", err
	}
	return stdout.String(), nil
}
