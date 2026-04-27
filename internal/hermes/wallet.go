package hermes

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/google/uuid"
	"golang.org/x/crypto/scrypt"
	"golang.org/x/crypto/sha3"
)

type WalletInfo struct {
	Address      string `json:"address"`
	PublicKey    string `json:"publicKey"`
	KeystoreUUID string `json:"keystore_uuid"`
	KeystorePath string `json:"keystore_path"`
	CreatedAt    string `json:"createdAt"`
	Password     string `json:"-"`
}

type v3Keystore struct {
	Address string   `json:"address"`
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

const (
	scryptN     = 262144
	scryptR     = 8
	scryptP     = 1
	scryptDKLen = 32
)

func GenerateWallet(cfg *config.Config, id string, u *ui.UI) (*WalletInfo, error) {
	privKey, pubKey, err := generateKeypair()
	if err != nil {
		return nil, fmt.Errorf("key generation failed: %w", err)
	}

	address := addressFromPublicKey(pubKey)

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

func generateKeypair() (privKeyBytes []byte, pubKeyUncompressed []byte, err error) {
	privKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		return nil, nil, fmt.Errorf("secp256k1 key generation: %w", err)
	}

	privKeyBytes = privKey.Serialize()
	pubKeyUncompressed = privKey.PubKey().SerializeUncompressed()[1:]
	return privKeyBytes, pubKeyUncompressed, nil
}

func addressFromPublicKey(pubKey []byte) string {
	h := sha3.NewLegacyKeccak256()
	_, _ = h.Write(pubKey)
	hash := h.Sum(nil)
	return toChecksumAddress(hex.EncodeToString(hash[12:]))
}

func toChecksumAddress(addr string) string {
	addr = strings.ToLower(strings.TrimPrefix(addr, "0x"))
	hash := sha3.NewLegacyKeccak256()
	_, _ = hash.Write([]byte(addr))
	sum := hex.EncodeToString(hash.Sum(nil))

	var out strings.Builder
	out.WriteString("0x")
	for i, c := range addr {
		if c >= '0' && c <= '9' {
			out.WriteRune(c)
			continue
		}
		if sum[i] >= '8' {
			out.WriteRune(rune(strings.ToUpper(string(c))[0]))
		} else {
			out.WriteRune(c)
		}
	}

	return out.String()
}

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

func encryptToV3Keystore(privKey, pubKey []byte, password string) ([]byte, string, error) {
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, "", fmt.Errorf("salt generation: %w", err)
	}

	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return nil, "", fmt.Errorf("iv generation: %w", err)
	}

	dk, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, scryptDKLen)
	if err != nil {
		return nil, "", fmt.Errorf("scrypt: %w", err)
	}

	block, err := aes.NewCipher(dk[:16])
	if err != nil {
		return nil, "", fmt.Errorf("aes cipher: %w", err)
	}

	stream := cipher.NewCTR(block, iv)
	ciphertext := make([]byte, len(privKey))
	stream.XORKeyStream(ciphertext, privKey)

	macHasher := sha3.NewLegacyKeccak256()
	_, _ = macHasher.Write(dk[16:32])
	_, _ = macHasher.Write(ciphertext)
	mac := macHasher.Sum(nil)

	keystoreID := uuid.NewString()
	keystore := v3Keystore{
		Address: strings.TrimPrefix(addressFromPublicKey(pubKey), "0x"),
		Crypto: v3Crypto{
			Cipher:     "aes-128-ctr",
			CipherText: hex.EncodeToString(ciphertext),
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
			MAC: hex.EncodeToString(mac),
		},
		ID:      keystoreID,
		Version: 3,
	}

	raw, err := json.MarshalIndent(keystore, "", "  ")
	if err != nil {
		return nil, "", fmt.Errorf("marshal keystore: %w", err)
	}
	return raw, keystoreID, nil
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
