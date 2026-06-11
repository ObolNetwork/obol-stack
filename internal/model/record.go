package model

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"gopkg.in/yaml.v3"
)

// Record-on-write for LiteLLM model configuration. `obol model
// setup|prefer|remove` mutate the litellm-config ConfigMap and
// litellm-secrets Secret, which only live in etcd — without a host-side
// record, cluster recreation silently loses the operator's model routing
// (plans/stack-export-import.md, Phase 2).
//
// RecordState is called by the explicit `obol model ...` commands AFTER a
// successful mutation (never from stack-up auto-configuration, which would
// overwrite operator intent with auto-detected defaults). ReconcileRecorded
// runs during `obol stack up` after autoConfigureLLM and re-imposes the
// recorded model list: recorded entries win (including their order — the
// head of model_list is the agents' default model), anything else currently
// in the ConfigMap (chart's paid/* catch-all, newly auto-detected models) is
// appended after.

// recordVersion is the on-disk format version of the recorded model state.
const recordVersion = 1

// RecordedModelState is the host-side record of operator-applied LiteLLM
// configuration. Secrets hold provider API keys in plaintext, matching the
// existing convention for values-remote-signer.yaml (0600 in ConfigDir).
type RecordedModelState struct {
	Version   int               `yaml:"version"`
	ModelList []ModelEntry      `yaml:"model_list"`
	Secrets   map[string]string `yaml:"secrets,omitempty"`
}

func recordedModelPath(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "llm", "recorded-models.yaml")
}

// RecordState snapshots the live model_list (minus paid/* entries, which are
// controller/purchase-derived and must not survive recreation) plus the
// provider API keys it references into the host-side record. Best-effort:
// failures warn but never fail the command that just succeeded.
func RecordState(cfg *config.Config, u *ui.UI) {
	kubectlBinary, kubeconfigPath := kubectl.Paths(cfg)

	raw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath={.data.config\\.yaml}")
	if err != nil {
		u.Warnf("Could not record model config to disk (will not survive cluster recreation): %v", err)
		return
	}
	var litellmConfig LiteLLMConfig
	if err := yaml.Unmarshal([]byte(raw), &litellmConfig); err != nil {
		u.Warnf("Could not record model config to disk: %v", err)
		return
	}

	state := &RecordedModelState{
		Version:   recordVersion,
		ModelList: filterRecordableEntries(litellmConfig.ModelList),
	}
	state.Secrets = readReferencedSecrets(cfg, secretEnvVarsFromEntries(state.ModelList))

	if err := writeRecordedModelState(cfg, state); err != nil {
		u.Warnf("Could not record model config to disk: %v", err)
	}
}

// ReconcileRecorded re-applies the recorded model state to the cluster.
// Called from `obol stack up` after auto-configuration so operator intent
// (entries + order) wins over auto-detected defaults. No record on disk
// means nothing to do.
func ReconcileRecorded(cfg *config.Config, u *ui.UI) {
	state, err := readRecordedModelState(cfg)
	if err != nil {
		u.Warnf("Could not read recorded model config: %v", err)
		return
	}
	if state == nil || len(state.ModelList) == 0 {
		return
	}
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return
	}

	kubectlBinary, kubeconfigPath := kubectl.Paths(cfg)
	raw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath={.data.config\\.yaml}")
	if err != nil {
		u.Warnf("Could not reconcile recorded models: %v", err)
		return
	}
	var litellmConfig LiteLLMConfig
	if err := yaml.Unmarshal([]byte(raw), &litellmConfig); err != nil {
		u.Warnf("Could not reconcile recorded models: %v", err)
		return
	}

	merged := MergeRecordedModelList(state.ModelList, litellmConfig.ModelList)
	configChanged := !reflect.DeepEqual(litellmConfig.ModelList, merged)
	if configChanged {
		litellmConfig.ModelList = merged
		if err := writeLiteLLMConfig(kubectlBinary, kubeconfigPath, &litellmConfig); err != nil {
			u.Warnf("Could not reconcile recorded models: %v", err)
			return
		}
	}

	secretsChanged, err := applyRecordedSecrets(cfg, state.Secrets)
	if err != nil {
		u.Warnf("Could not reconcile recorded provider keys: %v", err)
	}

	switch {
	case secretsChanged:
		// Env vars only refresh on pod restart; the ConfigMap change (if
		// any) rides along on the same rollout.
		u.Infof("Restoring recorded model config (%d models, provider keys)", len(state.ModelList))
		if err := RestartLiteLLM(cfg, u, "recorded model config"); err != nil {
			u.Warnf("LiteLLM restart after model reconcile failed: %v", err)
		}
	case configChanged:
		// Reloader restarts LiteLLM on litellm-config changes since rc14;
		// no manual rollout needed for a ConfigMap-only change.
		u.Infof("Restored recorded model list (%d models)", len(state.ModelList))
	}
}

// filterRecordableEntries drops paid/* routes: both the chart-managed
// catch-all and per-purchase paid/<model> entries, which point at buyer
// sidecar state that intentionally does not survive recreation.
func filterRecordableEntries(entries []ModelEntry) []ModelEntry {
	var out []ModelEntry
	for _, e := range entries {
		if strings.HasPrefix(e.ModelName, "paid/") {
			continue
		}
		out = append(out, e)
	}
	return out
}

// MergeRecordedModelList builds the reconciled model_list: recorded entries
// first (their order is operator intent — head = default model), then any
// current entries not named in the record (chart catch-alls, auto-detected
// models) in their existing relative order.
func MergeRecordedModelList(recorded, current []ModelEntry) []ModelEntry {
	out := make([]ModelEntry, 0, len(recorded)+len(current))
	seen := make(map[string]bool, len(recorded))
	for _, e := range recorded {
		if seen[e.ModelName] {
			continue
		}
		seen[e.ModelName] = true
		out = append(out, e)
	}
	for _, e := range current {
		if seen[e.ModelName] {
			continue
		}
		seen[e.ModelName] = true
		out = append(out, e)
	}
	return out
}

// secretEnvVarsFromEntries collects the env var names referenced by
// model_list entries via LiteLLM's "os.environ/<NAME>" api_key convention.
func secretEnvVarsFromEntries(entries []ModelEntry) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		name, ok := strings.CutPrefix(e.LiteLLMParams.APIKey, "os.environ/")
		if !ok || name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// readReferencedSecrets fetches the named keys from litellm-secrets.
// Missing keys or an unreadable Secret simply shrink the result.
func readReferencedSecrets(cfg *config.Config, envVars []string) map[string]string {
	if len(envVars) == 0 {
		return nil
	}
	kubectlBinary, kubeconfigPath := kubectl.Paths(cfg)
	raw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "secret", secretName, "-n", namespace, "-o", "json")
	if err != nil {
		return nil
	}
	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &secret); err != nil {
		return nil
	}
	out := map[string]string{}
	for _, name := range envVars {
		enc, ok := secret.Data[name]
		if !ok {
			continue
		}
		val, err := base64.StdEncoding.DecodeString(enc)
		if err != nil || len(val) == 0 {
			continue
		}
		out[name] = string(val)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// applyRecordedSecrets patches litellm-secrets with any recorded keys whose
// live value differs. Returns whether a patch was applied.
func applyRecordedSecrets(cfg *config.Config, secrets map[string]string) (bool, error) {
	if len(secrets) == 0 {
		return false, nil
	}
	current := readReferencedSecrets(cfg, mapKeys(secrets))
	stringData := map[string]string{}
	for k, v := range secrets {
		if current[k] != v {
			stringData[k] = v
		}
	}
	if len(stringData) == 0 {
		return false, nil
	}
	patch, err := json.Marshal(map[string]any{"stringData": stringData})
	if err != nil {
		return false, err
	}
	kubectlBinary, kubeconfigPath := kubectl.Paths(cfg)
	if err := kubectl.Run(kubectlBinary, kubeconfigPath,
		"patch", "secret", secretName, "-n", namespace,
		"-p", string(patch), "--type=merge"); err != nil {
		return false, err
	}
	return true, nil
}

func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// writeLiteLLMConfig replaces config.yaml in the ConfigMap wholesale
// (unlike patchLiteLLMConfig's merge-by-name, reconcile must impose order).
func writeLiteLLMConfig(kubectlBinary, kubeconfigPath string, litellmConfig *LiteLLMConfig) error {
	updated, err := yaml.Marshal(litellmConfig)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	escapedYAML, err := json.Marshal(string(updated))
	if err != nil {
		return fmt.Errorf("escape YAML: %w", err)
	}
	patchJSON := fmt.Sprintf(`{"data":{"config.yaml":%s}}`, escapedYAML)
	return kubectl.Run(kubectlBinary, kubeconfigPath,
		"patch", "configmap", configMapName, "-n", namespace,
		"-p", patchJSON, "--type=merge", "--field-manager=helm")
}

func readRecordedModelState(cfg *config.Config) (*RecordedModelState, error) {
	data, err := os.ReadFile(recordedModelPath(cfg))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state RecordedModelState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse %s: %w", recordedModelPath(cfg), err)
	}
	if state.Version != recordVersion {
		return nil, fmt.Errorf("unsupported recorded-models version %d", state.Version)
	}
	return &state, nil
}

func writeRecordedModelState(cfg *config.Config, state *RecordedModelState) error {
	path := recordedModelPath(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(state)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
