package hermes

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/keystore"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

type WalletInfo struct {
	Address      string `json:"address"`
	PublicKey    string `json:"publicKey"`
	KeystoreUUID string `json:"keystore_uuid"`
	KeystorePath string `json:"keystore_path"`
	CreatedAt    string `json:"createdAt"`
	Password     string `json:"-"`
}

func GenerateWallet(cfg *config.Config, id string, u *ui.UI) (*WalletInfo, error) {
	privKey, pubKey, err := keystore.GenerateKeypair()
	if err != nil {
		return nil, fmt.Errorf("key generation failed: %w", err)
	}
	defer keystore.Zero(privKey)

	address := keystore.AddressFromPublicKey(pubKey)

	password, err := keystore.RandomPassword(keystore.PasswordLength)
	if err != nil {
		return nil, fmt.Errorf("password generation failed: %w", err)
	}

	keystoreJSON, keystoreID, err := keystore.EncryptV3(privKey, pubKey, password)
	if err != nil {
		return nil, fmt.Errorf("keystore encryption failed: %w", err)
	}

	keystorePath, err := provisionKeystoreToVolume(cfg, id, keystoreID, keystoreJSON, u)
	if err != nil {
		return nil, fmt.Errorf("keystore provisioning failed: %w", err)
	}

	return &WalletInfo{
		Address:      address,
		PublicKey:    "0x04" + hex.EncodeToString(pubKey),
		KeystoreUUID: keystoreID,
		KeystorePath: keystorePath,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		Password:     password,
	}, nil
}

func provisionKeystoreToVolume(cfg *config.Config, id, keystoreID string, keystoreJSON []byte, u *ui.UI) (string, error) {
	dir := agentruntime.KeystoreVolumePath(cfg, agentruntime.Hermes, id)
	ensureVolumeWritable(cfg, dir, u)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create keystore directory: %w", err)
	}

	path := filepath.Join(dir, keystoreID+".json")
	if err := os.WriteFile(path, keystoreJSON, 0o600); err != nil {
		return "", fmt.Errorf("write keystore: %w", err)
	}

	fixRuntimeVolumeOwnership(cfg, dir, u)
	return path, nil
}

func generateRemoteSignerValues(wallet *WalletInfo) string {
	return fmt.Sprintf(`# Remote-signer configuration
# Managed by obol agent — do not edit manually.

keystorePassword:
  value: %q

persistence:
  enabled: true
  size: 100Mi

podSecurityContext:
  fsGroup: 1000
`, wallet.Password)
}

func walletMetadataPath(deploymentDir string) string {
	return filepath.Join(deploymentDir, "wallet.json")
}

func WriteWalletMetadata(deploymentDir string, wallet *WalletInfo) error {
	data, err := json.MarshalIndent(wallet, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal wallet metadata: %w", err)
	}
	return os.WriteFile(walletMetadataPath(deploymentDir), data, 0o600)
}

func ReadWalletMetadata(deploymentDir string) (*WalletInfo, error) {
	data, err := os.ReadFile(walletMetadataPath(deploymentDir))
	if err != nil {
		return nil, err
	}

	var wallet WalletInfo
	if err := json.Unmarshal(data, &wallet); err != nil {
		return nil, fmt.Errorf("unmarshal wallet metadata: %w", err)
	}
	return &wallet, nil
}

// applyWalletMetadataConfigMap creates or updates a wallet-metadata ConfigMap
// in the instance namespace. The frontend reads this to display wallet
// addresses on the agent card. Mirrors the OpenClaw helper at
// internal/openclaw/wallet.go so the frontend's getWalletMetadata works
// identically for either runtime. Must be called after helmfile sync (the
// namespace must exist).
func applyWalletMetadataConfigMap(cfg *config.Config, id, deploymentDir string) {
	wallet, err := ReadWalletMetadata(deploymentDir)
	if err != nil {
		return
	}

	namespace := agentruntime.Namespace(agentruntime.Hermes, id)
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	kubectlBinary := cfg.ToolPath("kubectl")

	addressesJSON := map[string]any{
		"instanceId": id,
		"addresses": []map[string]string{
			{
				"address":   wallet.Address,
				"publicKey": wallet.PublicKey,
				"createdAt": wallet.CreatedAt,
				"label":     "hermes-" + id,
			},
		},
		"count": 1,
	}

	addressesData, err := json.Marshal(addressesJSON)
	if err != nil {
		fmt.Printf("Warning: could not marshal wallet metadata: %v\n", err)
		return
	}

	manifest := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "wallet-metadata",
			"namespace": namespace,
			"labels": map[string]string{
				"app.kubernetes.io/component":  "remote-signer",
				"app.kubernetes.io/managed-by": "obol",
			},
		},
		"data": map[string]string{
			"addresses.json": string(addressesData),
		},
	}

	raw, err := json.Marshal(manifest)
	if err != nil {
		fmt.Printf("Warning: could not marshal ConfigMap: %v\n", err)
		return
	}

	cmd := exec.Command(kubectlBinary, "apply", "-f", "-")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	cmd.Stdin = bytes.NewReader(raw)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("Warning: could not apply wallet-metadata ConfigMap: %v\n%s", err, stderr.String())
	}
}

func ResolveWalletAddress(cfg *config.Config) (string, error) {
	ids, err := agentruntime.ListInstanceIDs(cfg, agentruntime.Hermes)
	if err != nil {
		return "", err
	}

	switch len(ids) {
	case 0:
		return "", fmt.Errorf("no Hermes instances found — run 'obol agent new --runtime hermes' first, or use --wallet")
	case 1:
		wallet, err := ReadWalletMetadata(DeploymentPath(cfg, ids[0]))
		if err != nil {
			return "", fmt.Errorf("wallet not found for instance %q: %w (use --wallet to specify manually)", ids[0], err)
		}
		return wallet.Address, nil
	default:
		var addrs []string
		for _, id := range ids {
			w, err := ReadWalletMetadata(DeploymentPath(cfg, id))
			if err != nil {
				continue
			}
			addrs = append(addrs, fmt.Sprintf("  %s (instance: %s)", w.Address, id))
		}
		return "", fmt.Errorf("multiple Hermes instances found, use --wallet to specify:\n%s", strings.Join(addrs, "\n"))
	}
}

func ResolveInstanceNamespace(cfg *config.Config) (string, error) {
	ids, err := agentruntime.ListInstanceIDs(cfg, agentruntime.Hermes)
	if err != nil {
		return "", err
	}

	switch len(ids) {
	case 0:
		return "", fmt.Errorf("no Hermes instances found — run 'obol agent new --runtime hermes' first")
	case 1:
		return agentruntime.Namespace(agentruntime.Hermes, ids[0]), nil
	default:
		return "", fmt.Errorf("multiple Hermes instances found (%s), specify an instance", strings.Join(ids, ", "))
	}
}

func ListWallets(cfg *config.Config, id string, u *ui.UI) error {
	var ids []string
	if id != "" {
		ids = []string{id}
	} else {
		var err error
		ids, err = agentruntime.ListInstanceIDs(cfg, agentruntime.Hermes)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			u.Info("No Hermes instances found")
			return nil
		}
	}

	found := false
	for _, instanceID := range ids {
		wallet, err := ReadWalletMetadata(DeploymentPath(cfg, instanceID))
		if err != nil {
			continue
		}
		found = true
		u.Detail("Instance", instanceID)
		u.Detail("  Address", wallet.Address)
		u.Detail("  Keystore UUID", wallet.KeystoreUUID)
		if wallet.CreatedAt != "" {
			u.Detail("  Created", wallet.CreatedAt)
		}
		u.Blank()
	}

	if !found {
		u.Info("No wallets found")
	}
	return nil
}
