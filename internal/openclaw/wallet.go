package openclaw

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"golang.org/x/crypto/scrypt"
	"golang.org/x/crypto/sha3"
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

// v3 keystore types matching Web3 Secret Storage Definition v3.
type v3Keystore struct {
	Address string   `json:"address"` // hex address without 0x prefix
	Crypto  v3Crypto `json:"crypto"`
	ID      string   `json:"id"`
	Version int      `json:"version"`
}

type v3Crypto struct {
	Cipher       string       `json:"cipher"`
	CipherText   string       `json:"ciphertext"`
	CipherParams cipherParams `json:"cipherparams"`
	KDF          string       `json:"kdf"`
	KDFParams    kdfParams    `json:"kdfparams"`
	MAC          string       `json:"mac"`
}

type cipherParams struct {
	IV string `json:"iv"`
}

type kdfParams struct {
	DKLen int    `json:"dklen"`
	N     int    `json:"n"`
	R     int    `json:"r"`
	P     int    `json:"p"`
	Salt  string `json:"salt"`
}

// scrypt parameters matching go-ethereum defaults.
const (
	scryptN     = 262144
	scryptR     = 8
	scryptP     = 1
	scryptDKLen = 32
)

// GenerateWallet creates a new secp256k1 signing key, encrypts it as a V3
// keystore, and provisions it to the host-side PVC path for the remote-signer.
func GenerateWallet(cfg *config.Config, id string, u *ui.UI) (*WalletInfo, error) {
	privKey, pubKey, err := generateKeypair()
	if err != nil {
		return nil, fmt.Errorf("key generation failed: %w", err)
	}
	defer zeroBytes(privKey)

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
	defer zeroBytes(privKey)

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

// KeystoreMaterial bundles the in-memory output of GenerateKeystoreInMemory:
// a freshly minted secp256k1 wallet encrypted as a Web3 V3 keystore plus
// the password needed to decrypt it. Used by callers that persist the
// keystore via mechanisms other than the host-side PVC (e.g. a K8s
// Secret created by an in-cluster controller).
type KeystoreMaterial struct {
	Address      string // EIP-55 checksummed
	PublicKey    string // 0x04 || X || Y (130 hex chars)
	KeystoreUUID string // V3 keystore "id" field
	KeystoreJSON []byte // Encrypted V3 keystore document
	Password     string // Random 32-char password
}

// GenerateKeystoreInMemory mints a fresh wallet keypair, V3-encrypts it
// in-memory, and returns everything the caller needs to persist the
// keystore wherever they want. Mirrors GenerateWallet's crypto path
// without the host-side disk write — useful for in-cluster controllers
// that materialise the keystore into a Secret rather than a PVC.
func GenerateKeystoreInMemory() (*KeystoreMaterial, error) {
	privKey, pubKey, err := generateKeypair()
	if err != nil {
		return nil, fmt.Errorf("key generation failed: %w", err)
	}
	defer zeroBytes(privKey)

	password, err := generateRandomPassword(32)
	if err != nil {
		return nil, fmt.Errorf("password generation failed: %w", err)
	}
	keystoreJSON, keystoreID, err := encryptToV3Keystore(privKey, pubKey, password)
	if err != nil {
		return nil, fmt.Errorf("keystore encryption failed: %w", err)
	}

	return &KeystoreMaterial{
		Address:      addressFromPublicKey(pubKey),
		PublicKey:    "0x04" + hex.EncodeToString(pubKey),
		KeystoreUUID: keystoreID,
		KeystoreJSON: keystoreJSON,
		Password:     password,
	}, nil
}

func provisionWalletFromKeyMaterial(cfg *config.Config, id string, privKey, pubKey []byte, address string, u *ui.UI) (*WalletInfo, error) {
	if len(privKey) != 32 {
		return nil, errors.New("private key must be 32 bytes")
	}
	if len(pubKey) != 64 {
		return nil, errors.New("public key must be 64 bytes without prefix")
	}
	if address == "" {
		address = addressFromPublicKey(pubKey)
	}

	password, err := generateRandomPassword(32)
	if err != nil {
		return nil, fmt.Errorf("password generation failed: %w", err)
	}

	keystoreJSON, keystoreID, err := encryptToV3Keystore(privKey, pubKey, password)
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

// generateKeypair creates a random secp256k1 private key using crypto/rand.
// Returns the 32-byte private key and 64-byte uncompressed public key (without 0x04 prefix).
func generateKeypair() (privKeyBytes []byte, pubKeyUncompressed []byte, err error) {
	privKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		return nil, nil, fmt.Errorf("secp256k1 key generation: %w", err)
	}

	privKeyBytes = privKey.Serialize() // 32 bytes

	// Uncompressed public key: 04 || X || Y (65 bytes).
	// For Ethereum address derivation we need X || Y (64 bytes, no prefix).
	pubKeyUncompressed = privKey.PubKey().SerializeUncompressed()[1:]

	return privKeyBytes, pubKeyUncompressed, nil
}

// addressFromPublicKey computes the Ethereum address from a 64-byte
// uncompressed public key (without the 0x04 prefix).
// Returns the EIP-55 checksummed address with 0x prefix.
func addressFromPublicKey(pubKey []byte) string {
	h := sha3.NewLegacyKeccak256()
	h.Write(pubKey)
	hash := h.Sum(nil)
	rawAddr := hex.EncodeToString(hash[12:]) // last 20 bytes

	return toChecksumAddress(rawAddr)
}

// toChecksumAddress applies EIP-55 mixed-case checksum encoding.
func toChecksumAddress(addr string) string {
	addr = strings.ToLower(addr)
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(addr))
	hash := hex.EncodeToString(h.Sum(nil))

	var result strings.Builder
	result.WriteString("0x")

	for i, c := range addr {
		if c >= '0' && c <= '9' {
			result.WriteRune(c)
		} else {
			// If the corresponding hex digit in the hash is >= 8, uppercase it.
			nibble := hash[i]
			if nibble >= '8' {
				result.WriteRune(c - 32) // lowercase to uppercase
			} else {
				result.WriteRune(c)
			}
		}
	}

	return result.String()
}

// encryptToV3Keystore encrypts a private key using the Web3 Secret Storage v3
// format: scrypt KDF (N=262144, r=8, p=1) + AES-128-CTR.
// Returns the JSON-encoded keystore and the keystore UUID.
func encryptToV3Keystore(privKey, pubKey []byte, password string) ([]byte, string, error) {
	// Generate random salt (32 bytes) and IV (16 bytes).
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, "", fmt.Errorf("salt generation: %w", err)
	}

	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return nil, "", fmt.Errorf("iv generation: %w", err)
	}

	// Derive key via scrypt.
	derivedKey, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, scryptDKLen)
	if err != nil {
		return nil, "", fmt.Errorf("scrypt key derivation: %w", err)
	}

	// Encrypt private key with AES-128-CTR (first 16 bytes of derived key).
	block, err := aes.NewCipher(derivedKey[:16])
	if err != nil {
		return nil, "", fmt.Errorf("aes cipher: %w", err)
	}

	cipherText := make([]byte, len(privKey))
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(cipherText, privKey)

	// MAC = Keccak-256(derivedKey[16:32] || cipherText).
	mac := sha3.NewLegacyKeccak256()
	mac.Write(derivedKey[16:32])
	mac.Write(cipherText)
	macHash := mac.Sum(nil)

	// Derive address from public key (without 0x prefix, lowercase).
	address := addressFromPublicKey(pubKey)
	address = strings.TrimPrefix(strings.ToLower(address), "0x")

	keystoreID := uuid.New().String()

	ks := v3Keystore{
		Address: address,
		Crypto: v3Crypto{
			Cipher:     "aes-128-ctr",
			CipherText: hex.EncodeToString(cipherText),
			CipherParams: cipherParams{
				IV: hex.EncodeToString(iv),
			},
			KDF: "scrypt",
			KDFParams: kdfParams{
				DKLen: scryptDKLen,
				N:     scryptN,
				R:     scryptR,
				P:     scryptP,
				Salt:  hex.EncodeToString(salt),
			},
			MAC: hex.EncodeToString(macHash),
		},
		ID:      keystoreID,
		Version: 3,
	}

	data, err := json.MarshalIndent(ks, "", "  ")
	if err != nil {
		return nil, "", fmt.Errorf("json marshal: %w", err)
	}

	return data, keystoreID, nil
}

// decryptV3Keystore decrypts a V3 keystore JSON to recover the private key.
// Used for testing round-trip correctness.
func decryptV3Keystore(keystoreJSON []byte, password string) ([]byte, error) {
	var ks v3Keystore
	if err := json.Unmarshal(keystoreJSON, &ks); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}

	salt, err := hex.DecodeString(ks.Crypto.KDFParams.Salt)
	if err != nil {
		return nil, fmt.Errorf("decode salt: %w", err)
	}

	iv, err := hex.DecodeString(ks.Crypto.CipherParams.IV)
	if err != nil {
		return nil, fmt.Errorf("decode iv: %w", err)
	}

	cipherText, err := hex.DecodeString(ks.Crypto.CipherText)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}

	storedMAC, err := hex.DecodeString(ks.Crypto.MAC)
	if err != nil {
		return nil, fmt.Errorf("decode mac: %w", err)
	}

	derivedKey, err := scrypt.Key([]byte(password), salt, ks.Crypto.KDFParams.N, ks.Crypto.KDFParams.R, ks.Crypto.KDFParams.P, ks.Crypto.KDFParams.DKLen)
	if err != nil {
		return nil, fmt.Errorf("scrypt: %w", err)
	}

	// Verify MAC.
	mac := sha3.NewLegacyKeccak256()
	mac.Write(derivedKey[16:32])
	mac.Write(cipherText)

	computedMAC := mac.Sum(nil)
	if !hmacEqual(computedMAC, storedMAC) {
		return nil, errors.New("MAC mismatch: wrong password or corrupted keystore")
	}

	// Decrypt.
	block, err := aes.NewCipher(derivedKey[:16])
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}

	plaintext := make([]byte, len(cipherText))
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(plaintext, cipherText)

	return plaintext, nil
}

// hmacEqual compares two byte slices in constant time.
func hmacEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}

	var result byte
	for i := range a {
		result |= a[i] ^ b[i]
	}

	return result == 0
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// generateRandomPassword creates a cryptographically random password using
// alphanumeric characters (a-z, A-Z, 0-9).
func generateRandomPassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	charsetLen := big.NewInt(int64(len(charset)))

	result := make([]byte, length)
	for i := range result {
		n, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			return "", fmt.Errorf("random int: %w", err)
		}

		result[i] = charset[n.Int64()]
	}

	return string(result), nil
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
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

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
