package openclaw

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"golang.org/x/crypto/sha3"
)

const (
	web3signerChartVersion = "1.0.6"
	web3signerImageTag     = "25.12.0"
	web3signerReleaseName  = "web3signer"
	web3signerPort         = 9000
)

// Web3SignerKey holds the generated key material and derived identifiers.
type Web3SignerKey struct {
	PrivateKeyHex string // 64 hex chars (32 bytes)
	PublicKeyHex  string // 130 hex chars (65 bytes, uncompressed with 04 prefix)
	Address       string // 0x-prefixed, 42 chars
	KeyID         string // short identifier used in filenames
}

// GenerateSigningKey creates a new SECP256K1 private key and derives
// the Ethereum address from it. The key is suitable for Web3Signer's
// file-raw key type.
func GenerateSigningKey() (*Web3SignerKey, error) {
	privKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate secp256k1 key: %w", err)
	}

	privBytes := privKey.Serialize()                     // 32 bytes
	pubBytes := privKey.PubKey().SerializeUncompressed() // 65 bytes: 04 || x || y

	// Ethereum address: keccak256(pubkey_without_prefix)[12:]
	hash := sha3.NewLegacyKeccak256()
	hash.Write(pubBytes[1:]) // skip 0x04 prefix
	addrBytes := hash.Sum(nil)[12:]

	// Generate a short key ID from randomness
	idBytes := make([]byte, 4)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, fmt.Errorf("failed to generate key ID: %w", err)
	}

	return &Web3SignerKey{
		PrivateKeyHex: hex.EncodeToString(privBytes),
		PublicKeyHex:  "0x" + hex.EncodeToString(pubBytes),
		Address:       "0x" + hex.EncodeToString(addrBytes),
		KeyID:         hex.EncodeToString(idBytes),
	}, nil
}

// ProvisionKeyFiles writes the private key and Web3Signer TOML config
// to the host-side PVC path so that Web3Signer can load them on startup.
func ProvisionKeyFiles(keysDir string, key *Web3SignerKey, label string) error {
	if err := os.MkdirAll(keysDir, 0755); err != nil {
		return fmt.Errorf("failed to create keys directory: %w", err)
	}

	// Write Web3Signer YAML key config with the private key inline.
	// v25+ scans for .yaml files and expects the private key as a
	// 0x-prefixed hex value in the `privateKey` field.
	yamlContent := fmt.Sprintf(`type: "file-raw"
keyType: "SECP256K1"
privateKey: "0x%s"
`, key.PrivateKeyHex)

	configFile := filepath.Join(keysDir, key.KeyID+".yaml")
	if err := os.WriteFile(configFile, []byte(yamlContent), 0600); err != nil {
		return fmt.Errorf("failed to write key config: %w", err)
	}

	return nil
}

// Web3SignerKeysPath returns the host-side directory where Web3Signer
// key files are provisioned. The chart creates a StatefulSet with a PVC
// named "storage-web3signer-0" which the local-path-provisioner maps to
// $DATA_DIR/<namespace>/storage-web3signer-0/ on the host. This path
// appears as /data/ inside the web3signer pod.
func Web3SignerKeysPath(cfg *config.Config, id string) string {
	namespace := fmt.Sprintf("%s-%s", appName, id)
	return filepath.Join(cfg.DataDir, namespace, "storage-web3signer-0", "keys")
}

// MetadataAddress represents a single signing address in the ConfigMap.
type MetadataAddress struct {
	Address   string `json:"address"`
	PublicKey string `json:"publicKey"`
	CreatedAt string `json:"createdAt"`
	Label     string `json:"label"`
}

// MetadataPayload is the JSON structure stored in the web3signer-metadata ConfigMap.
type MetadataPayload struct {
	InstanceID string            `json:"instanceId"`
	Addresses  []MetadataAddress `json:"addresses"`
	Count      int               `json:"count"`
}

// ApplyMetadataConfigMap creates or updates the web3signer-metadata ConfigMap
// in the instance namespace. The frontend reads this for display purposes.
func ApplyMetadataConfigMap(cfg *config.Config, id string, key *Web3SignerKey) error {
	namespace := fmt.Sprintf("%s-%s", appName, id)

	payload := MetadataPayload{
		InstanceID: id,
		Addresses: []MetadataAddress{
			{
				Address:   key.Address,
				PublicKey: key.PublicKeyHex,
				CreatedAt: time.Now().UTC().Format(time.RFC3339),
				Label:     fmt.Sprintf("obol-agent-%s", id),
			},
		},
		Count: 1,
	}

	payloadJSON, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Build ConfigMap YAML
	configMapYAML := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: web3signer-metadata
  namespace: %s
  labels:
    app.kubernetes.io/component: web3signer
    app.kubernetes.io/managed-by: obol
data:
  addresses.json: |
    %s
`, namespace, indentJSON(string(payloadJSON), 4))

	// Apply via kubectl
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")

	cmd := exec.Command(kubectlPath, "apply", "-f", "-")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	cmd.Stdin = strings.NewReader(configMapYAML)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to apply web3signer-metadata ConfigMap: %w", err)
	}

	return nil
}

// indentJSON re-indents a JSON string with the given number of leading spaces
// on each line (for embedding in YAML).
func indentJSON(s string, spaces int) string {
	prefix := ""
	for i := 0; i < spaces; i++ {
		prefix += " "
	}
	result := ""
	for i, line := range splitLines(s) {
		if i == 0 {
			result += line
		} else {
			result += "\n" + prefix + line
		}
	}
	return result
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// applyWeb3SignerMetadata reads the signing key from the provisioned key files
// and creates the web3signer-metadata ConfigMap. Called after helmfile sync
// when the namespace exists. Errors are non-fatal (printed as warnings).
func applyWeb3SignerMetadata(cfg *config.Config, id string) {
	keysDir := Web3SignerKeysPath(cfg, id)

	// Find the .hex key file to reconstruct the key info
	entries, err := os.ReadDir(keysDir)
	if err != nil {
		fmt.Printf("  Warning: could not read web3signer keys directory: %v\n", err)
		return
	}

	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".hex" {
			continue
		}
		keyID := strings.TrimSuffix(entry.Name(), ".hex")

		privHex, err := os.ReadFile(filepath.Join(keysDir, entry.Name()))
		if err != nil {
			fmt.Printf("  Warning: could not read key file: %v\n", err)
			continue
		}

		privBytes, err := hex.DecodeString(strings.TrimSpace(string(privHex)))
		if err != nil {
			fmt.Printf("  Warning: invalid key hex: %v\n", err)
			continue
		}

		// Derive address from private key
		privKey := secp256k1.PrivKeyFromBytes(privBytes)
		pubBytes := privKey.PubKey().SerializeUncompressed()

		hash := sha3.NewLegacyKeccak256()
		hash.Write(pubBytes[1:])
		addrBytes := hash.Sum(nil)[12:]

		key := &Web3SignerKey{
			PublicKeyHex: "0x" + hex.EncodeToString(pubBytes),
			Address:      "0x" + hex.EncodeToString(addrBytes),
			KeyID:        keyID,
		}

		if err := ApplyMetadataConfigMap(cfg, id, key); err != nil {
			fmt.Printf("  Warning: could not create web3signer-metadata ConfigMap: %v\n", err)
		} else {
			fmt.Printf("  ✓ Web3Signer metadata published (address: %s)\n", key.Address)
		}
		return // only process the first key
	}
}

// ensureWeb3Signer checks if the web3signer key and values file exist for
// an existing deployment. If not, it generates them. This handles the case
// where an existing deployment (created before web3signer was added) is
// re-synced — the helmfile now references web3signer but the key/values
// haven't been provisioned yet.
func ensureWeb3Signer(cfg *config.Config, id, deploymentDir string) {
	valuesPath := filepath.Join(deploymentDir, "values-web3signer.yaml")
	keysDir := Web3SignerKeysPath(cfg, id)

	// Check if values file already exists
	if _, err := os.Stat(valuesPath); err == nil {
		// Values exist — check if keys also exist
		if entries, err := os.ReadDir(keysDir); err == nil {
			for _, e := range entries {
				if filepath.Ext(e.Name()) == ".hex" {
					return // Both values and key exist — nothing to do
				}
			}
		}
	}

	// Generate signing key
	fmt.Println("\nProvisioning Web3Signer for existing deployment...")
	signingKey, err := GenerateSigningKey()
	if err != nil {
		fmt.Printf("  Warning: could not generate signing key: %v\n", err)
		return
	}

	keyLabel := fmt.Sprintf("obol-agent-%s", id)
	if err := ProvisionKeyFiles(keysDir, signingKey, keyLabel); err != nil {
		fmt.Printf("  Warning: could not provision signing key: %v\n", err)
		return
	}
	fmt.Printf("  ✓ Agent wallet address: %s\n", signingKey.Address)
	fmt.Printf("  Back up your key: cp %s/%s.yaml ~/obol-wallet-backup-%s.yaml\n", keysDir, signingKey.KeyID, id)

	// Write values file
	web3signerValues := generateWeb3SignerValues(id)
	if err := os.WriteFile(valuesPath, []byte(web3signerValues), 0644); err != nil {
		fmt.Printf("  Warning: could not write web3signer values: %v\n", err)
		return
	}
	fmt.Println("  ✓ Web3Signer values written")
}

// generateWeb3SignerValues creates the values-web3signer.yaml content
// for the Web3Signer Helm release.
func generateWeb3SignerValues(id string) string {
	return fmt.Sprintf(`# Web3Signer configuration for OpenClaw instance: %s
# Managed by obol openclaw — do not edit manually.

replicas: 1

image:
  repository: consensys/web3signer
  tag: "%s"

# Override the default command to use eth1 mode instead of eth2.
# The chart's _cmd.tpl hardcodes "eth2" as the subcommand — we need "eth1"
# for SECP256K1 execution-layer signing.
# Override the config template to set data-path to /data/keys so that
# web3signer only scans our key YAML files, not chart's config.yaml.
config: |
  data-path: "/data/keys"
  http-listen-port: {{ .Values.httpPort }}
  http-listen-host: 0.0.0.0
  http-host-allowlist: "*"

customCommand:
  - sh
  - -ac
  - |
    exec /opt/web3signer/bin/web3signer \
      --config-file=/data/config.yaml \
      --key-config-path=/data/keys \
      eth1 --chain-id=1

# Key storage via chart's built-in persistence.
# Keys are pre-provisioned by 'obol agent init' to the host-path PVC.
persistence:
  enabled: true
  size: 100Mi
  accessModes:
    - ReadWriteOnce

# Slashing protection DB (PostgreSQL) is not needed for ETH1 file-based keys.
# The chart's dependency condition is 'slashingprotectiondb.enabled'.
slashingprotectiondb:
  enabled: false

# ClusterIP only — no external exposure.
service:
  type: ClusterIP

# No ingress — web3signer is namespace-internal only.
ingress:
  enabled: false

resources:
  requests:
    cpu: 100m
    memory: 256Mi
  limits:
    cpu: 500m
    memory: 512Mi
`, id, web3signerImageTag)
}
