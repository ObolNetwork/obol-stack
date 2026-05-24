package hermes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"gopkg.in/yaml.v3"
)

const (
	bitwardenConfigFileName   = "bitwarden.yaml"
	bitwardenEnvSecretName    = "hermes-env"
	defaultBitwardenTokenEnv  = "BWS_ACCESS_TOKEN"
	defaultBitwardenCacheTTL  = 300
	defaultBitwardenServerEnv = "BWS_SERVER_URL"
)

// BitwardenConfig is Obol's non-secret metadata for Hermes' native
// secrets.bitwarden config block. The bootstrap token itself lives only in the
// per-agent hermes-env Secret.
type BitwardenConfig struct {
	Enabled          bool   `yaml:"enabled" json:"enabled"`
	ProjectID        string `yaml:"project_id" json:"project_id"`
	ServerURL        string `yaml:"server_url,omitempty" json:"server_url,omitempty"`
	AccessTokenEnv   string `yaml:"access_token_env" json:"access_token_env"`
	CacheTTLSeconds  int    `yaml:"cache_ttl_seconds" json:"cache_ttl_seconds"`
	OverrideExisting bool   `yaml:"override_existing" json:"override_existing"`
	AutoInstall      bool   `yaml:"auto_install" json:"auto_install"`
}

type BitwardenSetupOptions struct {
	AccessToken     string
	ProjectID       string
	ServerURL       string
	AccessTokenEnv  string
	CacheTTLSeconds int
}

type BitwardenStatus struct {
	Enabled          bool   `json:"enabled"`
	ProjectID        string `json:"project_id,omitempty"`
	ServerURL        string `json:"server_url,omitempty"`
	AccessTokenEnv   string `json:"access_token_env"`
	MetadataPath     string `json:"metadata_path"`
	EnvSecretExists  bool   `json:"env_secret_exists"`
	TokenKeyPresent  bool   `json:"token_key_present"`
	ServerURLPresent bool   `json:"server_url_present"`
}

func DefaultBitwardenConfig() BitwardenConfig {
	return BitwardenConfig{
		AccessTokenEnv:   defaultBitwardenTokenEnv,
		CacheTTLSeconds:  defaultBitwardenCacheTTL,
		OverrideExisting: true,
		AutoInstall:      true,
	}
}

func (c BitwardenConfig) normalized() BitwardenConfig {
	def := DefaultBitwardenConfig()
	if strings.TrimSpace(c.AccessTokenEnv) == "" {
		c.AccessTokenEnv = def.AccessTokenEnv
	}
	if c.CacheTTLSeconds <= 0 {
		c.CacheTTLSeconds = def.CacheTTLSeconds
	}
	return c
}

func bitwardenConfigPath(deploymentDir string) string {
	return filepath.Join(deploymentDir, bitwardenConfigFileName)
}

func LoadBitwardenConfig(deploymentDir string) (BitwardenConfig, bool, error) {
	path := bitwardenConfigPath(deploymentDir)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultBitwardenConfig(), false, nil
		}
		return BitwardenConfig{}, false, fmt.Errorf("read Bitwarden config: %w", err)
	}
	var cfg BitwardenConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return BitwardenConfig{}, false, fmt.Errorf("parse Bitwarden config: %w", err)
	}
	return cfg.normalized(), true, nil
}

func saveBitwardenConfig(deploymentDir string, cfg BitwardenConfig) error {
	if err := os.MkdirAll(deploymentDir, 0o755); err != nil {
		return fmt.Errorf("create deployment dir: %w", err)
	}
	raw, err := yaml.Marshal(cfg.normalized())
	if err != nil {
		return fmt.Errorf("marshal Bitwarden config: %w", err)
	}
	if err := os.WriteFile(bitwardenConfigPath(deploymentDir), raw, 0o600); err != nil {
		return fmt.Errorf("write Bitwarden config: %w", err)
	}
	return nil
}

func SetupBitwarden(cfg *config.Config, id string, opts BitwardenSetupOptions, u *ui.UI) error {
	deploymentDir := DeploymentPath(cfg, id)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: hermes/%s", id)
	}

	bw := DefaultBitwardenConfig()
	bw.Enabled = true
	bw.ProjectID = strings.TrimSpace(opts.ProjectID)
	bw.ServerURL = strings.TrimSpace(opts.ServerURL)
	if strings.TrimSpace(opts.AccessTokenEnv) != "" {
		bw.AccessTokenEnv = strings.TrimSpace(opts.AccessTokenEnv)
	}
	if opts.CacheTTLSeconds > 0 {
		bw.CacheTTLSeconds = opts.CacheTTLSeconds
	}

	token := strings.TrimSpace(opts.AccessToken)
	if token == "" {
		return errors.New("Bitwarden access token is required")
	}
	if bw.ProjectID == "" {
		return errors.New("Bitwarden project ID is required")
	}

	if err := saveBitwardenConfig(deploymentDir, bw); err != nil {
		return err
	}

	u.Info("Syncing Hermes deployment")
	if err := Sync(cfg, id, u); err != nil {
		return err
	}

	if err := applyBitwardenEnvSecret(cfg, id, bw, token); err != nil {
		return err
	}
	u.Successf("Updated %s/%s", agentruntime.Namespace(agentruntime.Hermes, id), bitwardenEnvSecretName)

	return restartHermesDeployment(cfg, id, u)
}

func DisableBitwarden(cfg *config.Config, id string, u *ui.UI) error {
	deploymentDir := DeploymentPath(cfg, id)
	bw, _, err := LoadBitwardenConfig(deploymentDir)
	if err != nil {
		return err
	}
	bw.Enabled = false
	if err := saveBitwardenConfig(deploymentDir, bw); err != nil {
		return err
	}
	if err := Sync(cfg, id, u); err != nil {
		return err
	}
	return restartHermesDeployment(cfg, id, u)
}

func GetBitwardenStatus(cfg *config.Config, id string) (BitwardenStatus, error) {
	deploymentDir := DeploymentPath(cfg, id)
	bw, _, err := LoadBitwardenConfig(deploymentDir)
	if err != nil {
		return BitwardenStatus{}, err
	}
	exists, hasToken, hasServer, err := bitwardenEnvSecretPresence(cfg, id, bw.AccessTokenEnv)
	if err != nil {
		return BitwardenStatus{}, err
	}
	return BitwardenStatus{
		Enabled:          bw.Enabled,
		ProjectID:        bw.ProjectID,
		ServerURL:        bw.ServerURL,
		AccessTokenEnv:   bw.AccessTokenEnv,
		MetadataPath:     bitwardenConfigPath(deploymentDir),
		EnvSecretExists:  exists,
		TokenKeyPresent:  hasToken,
		ServerURLPresent: hasServer,
	}, nil
}

func FetchBitwardenSecretForAgent(ctx context.Context, cfg *config.Config, id, key string) (string, error) {
	deploymentDir := DeploymentPath(cfg, id)
	bw, _, err := LoadBitwardenConfig(deploymentDir)
	if err != nil {
		return "", err
	}
	if !bw.Enabled {
		return "", fmt.Errorf("Bitwarden is not enabled for hermes/%s", id)
	}
	if strings.TrimSpace(bw.ProjectID) == "" {
		return "", fmt.Errorf("Bitwarden project ID is not configured for hermes/%s", id)
	}
	token, err := readBitwardenBootstrapToken(cfg, id, bw.AccessTokenEnv)
	if err != nil {
		return "", err
	}
	secrets, err := fetchBitwardenSecrets(ctx, bw, token)
	if err != nil {
		return "", err
	}
	value := secrets[key]
	if value == "" {
		return "", fmt.Errorf("Bitwarden secret %q not found in configured project", key)
	}
	return value, nil
}

func fetchBitwardenSecrets(ctx context.Context, bw BitwardenConfig, token string) (map[string]string, error) {
	bw = bw.normalized()
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("Bitwarden access token is required")
	}
	if strings.TrimSpace(bw.ProjectID) == "" {
		return nil, errors.New("Bitwarden project ID is required")
	}

	bin := strings.TrimSpace(os.Getenv("OBOL_BWS_BIN"))
	if bin == "" {
		bin = "bws"
	}

	cmd := exec.CommandContext(ctx, bin, "secret", "list", bw.ProjectID, "--output", "json")
	cmd.Env = append(os.Environ(),
		"NO_COLOR=1",
		defaultBitwardenTokenEnv+"="+token,
		bw.AccessTokenEnv+"="+token,
	)
	if strings.TrimSpace(bw.ServerURL) != "" {
		cmd.Env = append(cmd.Env, defaultBitwardenServerEnv+"="+strings.TrimSpace(bw.ServerURL))
	}

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("bws secret list failed: %w", err)
	}

	var items []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("parse bws secret list output: %w", err)
	}
	secrets := make(map[string]string, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Key) == "" {
			continue
		}
		secrets[item.Key] = item.Value
	}
	return secrets, nil
}

func applyBitwardenEnvSecret(cfg *config.Config, id string, bw BitwardenConfig, token string) error {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return err
	}
	ns := agentruntime.Namespace(agentruntime.Hermes, id)
	stringData := map[string]string{bw.AccessTokenEnv: token}
	if strings.TrimSpace(bw.ServerURL) != "" {
		stringData[defaultBitwardenServerEnv] = strings.TrimSpace(bw.ServerURL)
	}
	manifest := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      bitwardenEnvSecretName,
			"namespace": ns,
			"labels": map[string]string{
				"app.kubernetes.io/name":       "hermes",
				"app.kubernetes.io/managed-by": "obol",
			},
		},
		"type":       "Opaque",
		"stringData": stringData,
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal Bitwarden env Secret: %w", err)
	}
	bin, kc := kubectl.Paths(cfg)
	if _, err := kubectl.ApplyOutput(bin, kc, raw); err != nil {
		return fmt.Errorf("apply Bitwarden env Secret: %w", err)
	}
	return nil
}

func bitwardenEnvSecretPresence(cfg *config.Config, id, tokenKey string) (exists, hasToken, hasServer bool, err error) {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return false, false, false, err
	}
	ns := agentruntime.Namespace(agentruntime.Hermes, id)
	bin, kc := kubectl.Paths(cfg)
	raw, err := kubectl.Output(bin, kc, "get", "secret", bitwardenEnvSecretName, "-n", ns, "-o", "json", "--ignore-not-found")
	if err != nil {
		return false, false, false, err
	}
	if strings.TrimSpace(raw) == "" {
		return false, false, false, nil
	}
	data, err := parseSecretData([]byte(raw))
	if err != nil {
		return false, false, false, err
	}
	return true, strings.TrimSpace(data[tokenKey]) != "", strings.TrimSpace(data[defaultBitwardenServerEnv]) != "", nil
}

func readBitwardenBootstrapToken(cfg *config.Config, id, tokenKey string) (string, error) {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return "", err
	}
	ns := agentruntime.Namespace(agentruntime.Hermes, id)
	bin, kc := kubectl.Paths(cfg)
	raw, err := kubectl.Output(bin, kc, "get", "secret", bitwardenEnvSecretName, "-n", ns, "-o", "json")
	if err != nil {
		return "", fmt.Errorf("read %s/%s: %w", ns, bitwardenEnvSecretName, err)
	}
	data, err := parseSecretData([]byte(raw))
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(data[tokenKey])
	if token == "" {
		return "", fmt.Errorf("%s/%s is missing %s", ns, bitwardenEnvSecretName, tokenKey)
	}
	return token, nil
}

func parseSecretData(raw []byte) (map[string]string, error) {
	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(raw, &secret); err != nil {
		return nil, fmt.Errorf("parse Secret JSON: %w", err)
	}
	out := make(map[string]string, len(secret.Data))
	for key, value := range secret.Data {
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			out[key] = value
			continue
		}
		out[key] = string(decoded)
	}
	return out, nil
}

func restartHermesDeployment(cfg *config.Config, id string, u *ui.UI) error {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return err
	}
	ns := agentruntime.Namespace(agentruntime.Hermes, id)
	bin, kc := kubectl.Paths(cfg)
	u.Info("Restarting Hermes")
	if err := kubectl.Run(bin, kc, "rollout", "restart", "deployment/hermes", "-n", ns); err != nil {
		return fmt.Errorf("restart Hermes: %w", err)
	}
	if err := kubectl.Run(bin, kc, "rollout", "status", "deployment/hermes", "-n", ns, "--timeout=120s"); err != nil {
		return fmt.Errorf("Hermes rollout not confirmed: %w", err)
	}
	u.Success("Hermes restarted")
	return nil
}
