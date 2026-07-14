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

	// DefaultBuySellerURL is the public Obol-operated paid-inference
	// storefront used by `obol buy inference` when no seller URL is given.
	// The host CLI reads /api/services.json from this base URL and picks an
	// offer; pass a /services/<name> URL to bypass the catalog walk.
	DefaultBuySellerURL = "https://inference.v1337.org/"

	// DefaultBuySellerAgentID is the ERC-8004 tokenId the buyer expects to
	// see in the seller's /.well-known/agent-registration.json before
	// signing. 0 means "no expected id" — identity verification is opt-in
	// today via --expected-agent-id; the default flow trusts the URL.
	DefaultBuySellerAgentID int64 = 0

	// DefaultBuySellerChain is the chain the default seller settles on.
	// Used only as a hint in error messages; the actual chain is taken
	// from the seller's 402 response by buy.py.
	DefaultBuySellerChain = "base"

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
	// most recent embedded manifests. This also rewrites stack-owned
	// :__OBOL_IMAGE__ placeholders via internal/images (dev tag or
	// GitCommit@digest). No-op when the stamp is up to date.
	backendName := stackdefaults.DetectedBackendName(cfg)
	stackID := stackdefaults.StackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack ID not found, run 'obol stack init' first")
	}
	if _, err := stackdefaults.RefreshInfrastructureIfChanged(cfg, backendName, stackID); err != nil {
		return fmt.Errorf("refresh infrastructure defaults: %w", err)
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	snapshots, err := preserveMutableRuntimeConfigMaps(cfg, kubeconfigPath)
	if err != nil {
		return fmt.Errorf("snapshot mutable runtime configmaps: %w", err)
	}

	if err := helmfileSyncBaseRelease(cfg); err != nil {
		return fmt.Errorf("helmfile sync %s: %w", baseReleaseName, err)
	}
	if err := restoreMutableRuntimeConfigMaps(cfg, kubeconfigPath, snapshots); err != nil {
		return fmt.Errorf("restore mutable runtime configmaps: %w", err)
	}

	// Populate the CA bundle after deploying the verifier so TLS verification
	// of the facilitator works immediately. Idempotent — safe to call multiple times.
	bin, kc := kubectl.Paths(cfg)
	populateCABundle(bin, kc)
	return nil
}

type mutableConfigMapSnapshot struct {
	Name      string
	Namespace string
	Data      map[string]string
}

var mutableRuntimeConfigMaps = []mutableConfigMapSnapshot{
	{Name: "litellm-config", Namespace: "llm"},
	{Name: "x402-buyer-config", Namespace: "llm"},
	{Name: "x402-buyer-auths", Namespace: "llm"},
}

// preserveMutableRuntimeConfigMaps snapshots ConfigMaps whose data is mutated
// at runtime by `obol model setup`, PurchaseRequest reconciliation, or the
// buyer auth-pool flow. `EnsureVerifier` must sync the base release so the
// verifier uses canonical Helm ownership, but the base chart contains only
// bootstrap defaults for these objects. Without this snapshot/restore pass,
// `obol x402 setup` can erase configured models and buyer auth state.
func preserveMutableRuntimeConfigMaps(cfg *config.Config, kubeconfigPath string) ([]mutableConfigMapSnapshot, error) {
	out := make([]mutableConfigMapSnapshot, 0, len(mutableRuntimeConfigMaps))
	for _, item := range mutableRuntimeConfigMaps {
		data, found, err := readConfigMapData(cfg, kubeconfigPath, item.Namespace, item.Name)
		if err != nil {
			return nil, err
		}
		if !found || len(data) == 0 {
			continue
		}
		out = append(out, mutableConfigMapSnapshot{Name: item.Name, Namespace: item.Namespace, Data: data})
	}
	return out, nil
}

func restoreMutableRuntimeConfigMaps(cfg *config.Config, kubeconfigPath string, snapshots []mutableConfigMapSnapshot) error {
	for _, snap := range snapshots {
		current, _, err := readConfigMapData(cfg, kubeconfigPath, snap.Namespace, snap.Name)
		if err != nil {
			return err
		}
		data, err := mergeRuntimeConfigMapData(snap.Name, current, snap.Data)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			continue
		}
		manifest, err := configMapDataManifest(snap.Namespace, snap.Name, data)
		if err != nil {
			return err
		}
		if err := kubectl.ApplyServerSideForceConflicts(filepath.Join(cfg.BinDir, "kubectl"), kubeconfigPath, manifest, "helm"); err != nil {
			return err
		}
	}
	return nil
}

func readConfigMapData(cfg *config.Config, kubeconfigPath, namespace, name string) (map[string]string, bool, error) {
	raw, err := kubectl.Output(filepath.Join(cfg.BinDir, "kubectl"), kubeconfigPath,
		"get", "configmap", name, "-n", namespace, "-o", "json")
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "NotFound") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("get configmap %s/%s: %w", namespace, name, err)
	}
	var obj struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, false, fmt.Errorf("parse configmap %s/%s: %w", namespace, name, err)
	}
	return obj.Data, true, nil
}

func mergeRuntimeConfigMapData(name string, current, previous map[string]string) (map[string]string, error) {
	if name == "litellm-config" {
		currentRaw := current["config.yaml"]
		previousRaw := previous["config.yaml"]
		if strings.TrimSpace(previousRaw) == "" {
			return current, nil
		}
		if strings.TrimSpace(currentRaw) == "" {
			return previous, nil
		}
		merged, err := mergeLiteLLMConfig(currentRaw, previousRaw)
		if err != nil {
			return nil, err
		}
		out := copyStringMap(current)
		out["config.yaml"] = merged
		return out, nil
	}

	out := copyStringMap(previous)
	for k, v := range current {
		out[k] = v
	}
	return out, nil
}

func mergeLiteLLMConfig(currentRaw, previousRaw string) (string, error) {
	var current map[string]any
	if err := yaml.Unmarshal([]byte(currentRaw), &current); err != nil {
		return "", fmt.Errorf("parse current LiteLLM config: %w", err)
	}
	if current == nil {
		current = map[string]any{}
	}

	var previous map[string]any
	if err := yaml.Unmarshal([]byte(previousRaw), &previous); err != nil {
		return "", fmt.Errorf("parse previous LiteLLM config: %w", err)
	}
	if previous == nil {
		previous = map[string]any{}
	}

	merged := copyAnyMap(previous)
	for key, value := range current {
		merged[key] = value
	}

	models, err := mergeLiteLLMModelLists(current["model_list"], previous["model_list"])
	if err != nil {
		return "", err
	}
	if len(models) > 0 {
		merged["model_list"] = models
	}

	for _, key := range []string{"general_settings", "litellm_settings"} {
		if liteLLMValueEmpty(current[key]) && !liteLLMValueEmpty(previous[key]) {
			merged[key] = previous[key]
		}
	}

	mergedRaw, err := yaml.Marshal(merged)
	if err != nil {
		return "", fmt.Errorf("serialize merged LiteLLM config: %w", err)
	}
	return string(mergedRaw), nil
}

func mergeLiteLLMModelLists(currentRaw, previousRaw any) ([]any, error) {
	current, err := liteLLMModelList(currentRaw)
	if err != nil {
		return nil, fmt.Errorf("parse current LiteLLM model_list: %w", err)
	}
	previous, err := liteLLMModelList(previousRaw)
	if err != nil {
		return nil, fmt.Errorf("parse previous LiteLLM model_list: %w", err)
	}

	merged := append([]any{}, current...)
	byName := make(map[string]bool, len(current))
	for _, entry := range current {
		if name := liteLLMModelName(entry); name != "" {
			byName[name] = true
		}
	}
	for _, entry := range previous {
		name := liteLLMModelName(entry)
		if name == "" {
			continue
		}
		if byName[name] {
			continue
		}
		byName[name] = true
		merged = append(merged, entry)
	}
	return merged, nil
}

func liteLLMModelList(value any) ([]any, error) {
	if value == nil {
		return nil, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("expected sequence, got %T", value)
	}
	return list, nil
}

func liteLLMModelName(entry any) string {
	switch typed := entry.(type) {
	case map[string]any:
		if name, ok := typed["model_name"].(string); ok {
			return strings.TrimSpace(name)
		}
	case map[any]any:
		if name, ok := typed["model_name"].(string); ok {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

func liteLLMValueEmpty(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	case map[any]any:
		return len(typed) == 0
	default:
		return false
	}
}

func configMapDataManifest(namespace, name string, data map[string]string) ([]byte, error) {
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]string{
			"name":      name,
			"namespace": namespace,
		},
		"data": data,
	}
	return yaml.Marshal(obj)
}

func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
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
