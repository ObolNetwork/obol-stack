package openclaw

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/keystore"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// WalletInfo holds generated wallet metadata returned from GenerateWallet.
type WalletInfo struct {
	Address      string `json:"address"`       // 0x-prefixed Ethereum address
	PublicKey    string `json:"publicKey"`     // 0x-prefixed uncompressed public key (130 hex chars)
	KeystoreUUID string `json:"keystore_uuid"` // UUID of the V3 keystore file
	KeystorePath string `json:"keystore_path"` // Absolute host path to keystore JSON
	CreatedAt    string `json:"createdAt"`     // ISO 8601 timestamp
	Password     string `json:"-"`             // Keystore password (not serialized)
}

// GenerateWallet creates a new secp256k1 signing key, encrypts it as a V3
// keystore, and provisions it to the host-side PVC path for the remote-signer.
func GenerateWallet(cfg *config.Config, id string, u *ui.UI) (*WalletInfo, error) {
	privKey, pubKey, err := keystore.GenerateKeypair()
	if err != nil {
		return nil, fmt.Errorf("key generation failed: %w", err)
	}
	defer keystore.Zero(privKey)

	return provisionWalletFromKeyMaterial(cfg, id, privKey, pubKey, "", u)
}

// ImportWalletFromPrivateKey provisions an existing Ethereum private key as the
// remote-signer wallet for an OpenClaw instance.
func ImportWalletFromPrivateKey(cfg *config.Config, id, privateKeyHex string, u *ui.UI) (*WalletInfo, error) {
	privateKeyHex = strings.TrimSpace(strings.TrimPrefix(privateKeyHex, "0x"))
	if privateKeyHex == "" {
		return nil, errors.New("private key is empty")
	}

	key, err := ethcrypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	privKey := ethcrypto.FromECDSA(key)
	defer keystore.Zero(privKey)

	pubKeyWithPrefix := ethcrypto.FromECDSAPub(&key.PublicKey)
	if len(privKey) != 32 || len(pubKeyWithPrefix) != 65 || pubKeyWithPrefix[0] != 0x04 {
		return nil, errors.New("invalid private key material")
	}

	return provisionWalletFromKeyMaterial(
		cfg,
		id,
		privKey,
		pubKeyWithPrefix[1:],
		ethcrypto.PubkeyToAddress(key.PublicKey).Hex(),
		u,
	)
}

func provisionWalletFromKeyMaterial(cfg *config.Config, id string, privKey, pubKey []byte, address string, u *ui.UI) (*WalletInfo, error) {
	if len(privKey) != 32 {
		return nil, errors.New("private key must be 32 bytes")
	}
	if len(pubKey) != 64 {
		return nil, errors.New("public key must be 64 bytes without prefix")
	}
	if address == "" {
		address = keystore.AddressFromPublicKey(pubKey)
	}

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

// KeystoreVolumePath returns the host-side path where the remote-signer's
// PVC stores keystores. This follows the local-path-provisioner pattern:
// $DATA_DIR/<namespace>/<pvc-name>/
func KeystoreVolumePath(cfg *config.Config, id string) string {
	namespace := fmt.Sprintf("%s-%s", appName, id)
	return filepath.Join(cfg.DataDir, namespace, "remote-signer-keystores")
}

// provisionKeystoreToVolume writes the V3 keystore JSON to the host-side PVC
// path before the remote-signer pod starts. Returns the absolute path to the
// written keystore file.
func provisionKeystoreToVolume(cfg *config.Config, id, keystoreID string, keystoreJSON []byte, u *ui.UI) (string, error) {
	dir := KeystoreVolumePath(cfg, id)

	// On k3d, the local-path-provisioner inside the container may have already
	// created parent directories as root, making them root-owned on the host.
	// Pre-create and chown inside the k3d node so the host-side CLI can write.
	ensureVolumeWritable(cfg, dir, u)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create keystore directory: %w", err)
	}

	filename := keystoreID + ".json"

	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, keystoreJSON, 0o600); err != nil {
		return "", fmt.Errorf("write keystore: %w", err)
	}

	// Re-chown to UID 1000 so the remote-signer pod can read the keystore.
	fixVolumeOwnership(cfg, dir, u)
	return path, nil
}

// generateRemoteSignerValues emits the values-remote-signer.yaml content
// for the remote-signer Helm release.
func generateRemoteSignerValues(wallet *WalletInfo) string {
	return fmt.Sprintf(`# Remote-signer configuration
# Managed by obol openclaw — do not edit manually.

keystorePassword:
  value: %q

# The signer is a singleton over a ReadWriteOnce keystore PVC: a RollingUpdate
# surge pod can wedge on the volume attach on multi-node clusters and briefly
# double-runs the signer over the same keystore on any cluster. Recreate is the
# chart default as of remote-signer 0.4.0; pinned explicitly here so the intent
# is visible and robust to a future chart-default change.
strategy:
  type: Recreate

persistence:
  enabled: true
  size: 100Mi

# Ensure the pod's volumes are group-owned by GID 1000 so the remote-signer
# process (UID 1000) can read and write the keystore PVC.
podSecurityContext:
  fsGroup: 1000
`, wallet.Password)
}

// walletMetadataPath returns the path to the wallet.json metadata file
// in the deployment directory.
func walletMetadataPath(deploymentDir string) string {
	return filepath.Join(deploymentDir, "wallet.json")
}

// WriteWalletMetadata writes the wallet address and UUID to a JSON file
// in the deployment directory for re-sync and display purposes.
func WriteWalletMetadata(deploymentDir string, wallet *WalletInfo) error {
	data, err := json.MarshalIndent(wallet, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal wallet metadata: %w", err)
	}

	return os.WriteFile(walletMetadataPath(deploymentDir), data, 0o600)
}

// ReadWalletMetadata reads existing wallet metadata from the deployment directory.
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

// ensureWallet checks if wallet files exist for a deployment. If not
// (e.g., a pre-wallet deployment), it generates and provisions them.
// This is called during doSync to handle upgrades gracefully.
func ensureWallet(cfg *config.Config, id, deploymentDir string, u *ui.UI) {
	// Check if wallet metadata already exists.
	if _, err := os.Stat(walletMetadataPath(deploymentDir)); err == nil {
		return // wallet already provisioned
	}

	// Check if values-remote-signer.yaml exists (written during onboard).
	valuesPath := filepath.Join(deploymentDir, "values-remote-signer.yaml")
	if _, err := os.Stat(valuesPath); err == nil {
		return // values exist, wallet was provisioned
	}

	// No wallet yet — generate one.
	u.Info("Generating Ethereum wallet for this instance...")
	wallet, err := GenerateWallet(cfg, id, u)
	if err != nil {
		u.Warnf("Could not generate wallet: %v", err)
		return
	}

	values := generateRemoteSignerValues(wallet)
	if err := os.WriteFile(valuesPath, []byte(values), 0o600); err != nil {
		fmt.Printf("Warning: could not write remote-signer values: %v\n", err)
		return
	}

	if err := WriteWalletMetadata(deploymentDir, wallet); err != nil {
		fmt.Printf("Warning: could not write wallet metadata: %v\n", err)
		return
	}

	fmt.Printf("  Wallet address: %s\n", wallet.Address)
}

// applyWalletMetadataConfigMap creates or updates a wallet-metadata ConfigMap
// in the instance namespace. The frontend reads this to display wallet addresses.
// Must be called after helmfile sync (namespace must exist).
func applyWalletMetadataConfigMap(cfg *config.Config, id, deploymentDir string) {
	wallet, err := ReadWalletMetadata(deploymentDir)
	if err != nil {
		return // no wallet metadata, nothing to apply
	}

	namespace := fmt.Sprintf("%s-%s", appName, id)
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	kubectlBinary := cfg.ToolPath("kubectl")

	// Build addresses.json matching the frontend's WalletMetadata type.
	addressesJSON := map[string]any{
		"instanceId": id,
		"addresses": []map[string]string{
			{
				"address":   wallet.Address,
				"publicKey": wallet.PublicKey,
				"createdAt": wallet.CreatedAt,
				"label":     "obol-agent-" + id,
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
