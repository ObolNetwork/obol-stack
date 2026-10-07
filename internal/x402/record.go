package x402

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"gopkg.in/yaml.v3"
)

// Record-on-write for seller pricing. `obol sell pricing` (Setup) writes the
// payTo wallet into the x402-secrets Secret and the x402-pricing ConfigMap,
// both of which live only in etcd — and x402-pricing is a helm-owned
// bootstrap object that a base-release sync re-renders with wallet: "".
// RecordedPricing mirrors the operator-set fields to
// $OBOL_CONFIG_DIR/x402/pricing.yaml so record replay (internal/replay)
// can re-impose them. Routes are NOT recorded: they are derived from
// ServiceOffers by the verifier's route source and come back when offers
// replay. authCaptureUnlock is helm-values-driven and survives syncs.

const recordedPricingVersion = 1

// RecordedPricing is the host-side record of `obol sell pricing`. Wallet is
// a public payTo address, not a secret.
type RecordedPricing struct {
	Version        int    `yaml:"version"`
	Wallet         string `yaml:"wallet"`
	Chain          string `yaml:"chain"`
	FacilitatorURL string `yaml:"facilitatorURL"`
	VerifyOnly     bool   `yaml:"verifyOnly"`
}

// RecordedPricingPath returns $OBOL_CONFIG_DIR/x402/pricing.yaml.
func RecordedPricingPath(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "x402", "pricing.yaml")
}

// SaveRecordedPricing writes the record atomically (0600 for consistency with
// the other records; nothing in it is secret).
func SaveRecordedPricing(cfg *config.Config, rec *RecordedPricing) error {
	if rec == nil {
		return fmt.Errorf("nil pricing record")
	}
	rec.Version = recordedPricingVersion
	path := RecordedPricingPath(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(rec)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadRecordedPricing reads the record. (nil, nil) when none exists.
func LoadRecordedPricing(cfg *config.Config) (*RecordedPricing, error) {
	path := RecordedPricingPath(cfg)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec RecordedPricing
	if err := yaml.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if rec.Version != recordedPricingVersion {
		return nil, fmt.Errorf("unsupported pricing record version %d in %s", rec.Version, path)
	}
	return &rec, nil
}

// kubeClient is the slice of kubectl the pricing reconcile needs; swapped
// for a fake in tests.
type kubeClient interface {
	Output(args ...string) (string, error)
	Run(args ...string) error
}

type kubectlClient struct{ bin, kubeconfig string }

func (k kubectlClient) Output(args ...string) (string, error) {
	return kubectl.Output(k.bin, k.kubeconfig, args...)
}

func (k kubectlClient) Run(args ...string) error {
	return kubectl.Run(k.bin, k.kubeconfig, args...)
}

var newKubeClient = func(cfg *config.Config) kubeClient {
	bin, kc := kubectl.Paths(cfg)
	return kubectlClient{bin: bin, kubeconfig: kc}
}

// ReconcileRecordedPricing re-applies the recorded pricing when the live
// ConfigMap/Secret disagree with it (fresh cluster, or a base-release sync
// that reset the bootstrap ConfigMap). Returns whether anything was patched.
// No record => (false, nil).
func ReconcileRecordedPricing(cfg *config.Config) (bool, error) {
	rec, err := LoadRecordedPricing(cfg)
	if err != nil || rec == nil {
		return false, err
	}
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return false, err
	}
	return reconcilePricing(newKubeClient(cfg), rec)
}

func reconcilePricing(kc kubeClient, rec *RecordedPricing) (bool, error) {
	raw, err := kc.Output("get", "configmap", pricingConfigMap, "-n", x402Namespace,
		"-o", `jsonpath={.data.pricing\.yaml}`)
	if err != nil {
		return false, fmt.Errorf("read %s/%s: %w", x402Namespace, pricingConfigMap, err)
	}
	var live PricingConfig
	if strings.TrimSpace(raw) != "" {
		if err := yaml.Unmarshal([]byte(raw), &live); err != nil {
			return false, fmt.Errorf("parse live pricing config: %w", err)
		}
	}

	liveWallet := ""
	if enc, err := kc.Output("get", "secret", x402SecretName, "-n", x402Namespace,
		"-o", `jsonpath={.data.WALLET_ADDRESS}`); err == nil {
		if dec, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(enc)); decErr == nil {
			liveWallet = string(dec)
		}
	}

	changed := false
	if !strings.EqualFold(liveWallet, rec.Wallet) {
		patch, err := json.Marshal(map[string]any{"stringData": map[string]string{"WALLET_ADDRESS": rec.Wallet}})
		if err != nil {
			return false, err
		}
		if err := kc.Run("patch", "secret", x402SecretName, "-n", x402Namespace,
			"-p", string(patch), "--type=merge"); err != nil {
			return false, fmt.Errorf("patch %s: %w", x402SecretName, err)
		}
		changed = true
	}

	if !strings.EqualFold(live.Wallet, rec.Wallet) || live.Chain != rec.Chain ||
		live.FacilitatorURL != rec.FacilitatorURL || live.VerifyOnly != rec.VerifyOnly {
		// Overlay only the recorded fields; keep live routes and
		// authCaptureUnlock untouched.
		live.Wallet = rec.Wallet
		live.Chain = rec.Chain
		live.FacilitatorURL = rec.FacilitatorURL
		live.VerifyOnly = rec.VerifyOnly
		patch, err := pricingConfigMapPatch(&live)
		if err != nil {
			return changed, err
		}
		if err := kc.Run("patch", "configmap", pricingConfigMap, "-n", x402Namespace,
			"-p", patch, "--type=merge"); err != nil {
			return changed, fmt.Errorf("patch %s: %w", pricingConfigMap, err)
		}
		changed = true
	}
	return changed, nil
}

// pricingConfigMapPatch renders the merge patch that replaces pricing.yaml
// and stamps the obol managed-by label.
func pricingConfigMapPatch(pcfg *PricingConfig) (string, error) {
	pricingBytes, err := yaml.Marshal(pcfg)
	if err != nil {
		return "", fmt.Errorf("marshal pricing config: %w", err)
	}
	patch, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"labels": map[string]string{kubectl.ManagedByLabel: kubectl.ManagedByObol},
		},
		"data": map[string]string{"pricing.yaml": string(pricingBytes)},
	})
	if err != nil {
		return "", fmt.Errorf("marshal pricing patch: %w", err)
	}
	return string(patch), nil
}
