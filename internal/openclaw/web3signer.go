package openclaw

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

const (
	web3signerChartVersion = "1.0.6"
	web3signerImageTag     = "25.12.0"
	web3signerReleaseName  = "web3signer"
	web3signerPort         = 9000

	keystoreAccountName = "obol-agent"
	keystorePasswordLen = 32
	foundryImage        = "ghcr.io/foundry-rs/foundry:stable"
)

// WalletKey holds the address and file locations for a generated wallet.
type WalletKey struct {
	Address      string // 0x-prefixed, 42 chars
	KeystoreFile string // absolute path to V3 JSON keystore on host
	PasswordFile string // absolute path to password file on host
}

// GenerateKeystoreViaCast creates an encrypted V3 keystore using Foundry's
// cast wallet new command. It tries the host-installed cast binary first,
// then falls back to running cast inside a Docker container.
func GenerateKeystoreViaCast(keysDir string) (*WalletKey, error) {
	if err := os.MkdirAll(keysDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create keys directory: %w", err)
	}

	// Generate random password and write to file. cast reads the password
	// from this file via --password-file (never passed on the command line).
	password, err := generateRandomPassword(keystorePasswordLen)
	if err != nil {
		return nil, fmt.Errorf("failed to generate password: %w", err)
	}

	passwordPath := filepath.Join(keysDir, "password.txt")
	if err := os.WriteFile(passwordPath, []byte(password), 0600); err != nil {
		return nil, fmt.Errorf("failed to write password file: %w", err)
	}

	// Try host cast binary
	if castPath, err := exec.LookPath("cast"); err == nil {
		address, err := runCastWalletNew(castPath, keysDir, passwordPath)
		if err == nil {
			keystoreFile := findKeystoreFile(keysDir)
			return &WalletKey{
				Address:      address,
				KeystoreFile: keystoreFile,
				PasswordFile: passwordPath,
			}, nil
		}
		fmt.Printf("  Warning: host cast failed: %v\n", err)
	}

	// Try Docker fallback
	if dockerPath, err := exec.LookPath("docker"); err == nil {
		address, err := runCastWalletNewDocker(dockerPath, keysDir, passwordPath)
		if err == nil {
			keystoreFile := findKeystoreFile(keysDir)
			return &WalletKey{
				Address:      address,
				KeystoreFile: keystoreFile,
				PasswordFile: passwordPath,
			}, nil
		}
		fmt.Printf("  Warning: docker fallback failed: %v\n", err)
	}

	// Clean up password file on total failure
	os.Remove(passwordPath)
	return nil, fmt.Errorf("could not generate keystore.\n" +
		"  Install Foundry: curl -L https://foundry.paradigm.xyz | bash && foundryup\n" +
		"  Or install Docker: https://docs.docker.com/get-docker/")
}

// runCastWalletNew runs cast wallet new on the host to create a V3 keystore.
// The password is read from a file, never passed on the command line.
func runCastWalletNew(castBin, keysDir, passwordFile string) (string, error) {
	cmd := exec.Command(castBin, "wallet", "new", keysDir, "--password-file", passwordFile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("cast wallet new: %w\n%s", err, output)
	}
	return parseAddressFromOutput(string(output))
}

// runCastWalletNewDocker runs cast wallet new inside a Foundry Docker container.
// The password file is mounted into the container at /keys/password.txt.
func runCastWalletNewDocker(dockerBin, keysDir, passwordFile string) (string, error) {
	absKeysDir, err := filepath.Abs(keysDir)
	if err != nil {
		return "", err
	}

	// password.txt is inside keysDir, so the single volume mount covers both
	containerPasswordPath := "/keys/" + filepath.Base(passwordFile)
	cmd := exec.Command(dockerBin, "run", "--rm",
		"-v", absKeysDir+":/keys",
		foundryImage,
		"cast", "wallet", "new", "/keys", "--password-file", containerPasswordPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker cast wallet new: %w\n%s", err, output)
	}
	return parseAddressFromOutput(string(output))
}

// parseAddressFromOutput extracts the 0x-prefixed Ethereum address from
// cast wallet new output.
var addressRe = regexp.MustCompile(`0x[0-9a-fA-F]{40}`)

func parseAddressFromOutput(output string) (string, error) {
	// Look for address in output lines (skip private key lines)
	for _, line := range strings.Split(output, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "private") {
			continue
		}
		if match := addressRe.FindString(line); match != "" {
			return match, nil
		}
	}
	return "", fmt.Errorf("could not find address in output:\n%s", output)
}

// findKeystoreFile finds the V3 keystore file in the keys directory.
// cast wallet import creates files named after the account (e.g. "obol-agent"),
// while cast wallet new creates "UTC--<timestamp>--<address>" files.
// We look for the known account name first, then fall back to any file
// containing valid V3 keystore JSON.
func findKeystoreFile(keysDir string) string {
	// Check for the well-known account name first
	knownPath := filepath.Join(keysDir, keystoreAccountName)
	if info, err := os.Stat(knownPath); err == nil && !info.IsDir() {
		return knownPath
	}

	// Fall back: scan for any file containing V3 keystore JSON
	entries, err := os.ReadDir(keysDir)
	if err != nil {
		return ""
	}

	var newest string
	var newestTime time.Time

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Skip known non-keystore files
		if name == "password.txt" || filepath.Ext(name) == ".yaml" || filepath.Ext(name) == ".ports" {
			continue
		}
		if !isV3Keystore(filepath.Join(keysDir, name)) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if newest == "" || info.ModTime().After(newestTime) {
			newest = filepath.Join(keysDir, name)
			newestTime = info.ModTime()
		}
	}
	return newest
}

// isV3Keystore returns true if the file at path contains valid V3 keystore JSON.
func isV3Keystore(path string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var ks struct {
		Version int `json:"version"`
	}
	return json.Unmarshal(content, &ks) == nil && ks.Version == 3
}

// generateRandomPassword creates a cryptographically random alphanumeric password.
func generateRandomPassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	max := big.NewInt(int64(len(charset)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = charset[n.Int64()]
	}
	return string(b), nil
}

// ProvisionKeyFiles writes the Web3Signer key configuration that references
// the V3 encrypted keystore. Web3Signer uses file-keystore type to read
// the encrypted key and password file at startup.
func ProvisionKeyFiles(keysDir string, wallet *WalletKey, label string) error {
	// Derive the container-relative paths. The host keysDir maps to
	// /data/keys/ inside the web3signer pod via the PVC mount.
	keystoreBasename := filepath.Base(wallet.KeystoreFile)
	yamlContent := fmt.Sprintf(`type: "file-keystore"
keyType: "SECP256K1"
keystoreFile: "/data/keys/%s"
keystorePasswordFile: "/data/keys/password.txt"
`, keystoreBasename)

	// Use a deterministic filename based on a short address prefix
	shortAddr := strings.TrimPrefix(wallet.Address, "0x")
	if len(shortAddr) > 8 {
		shortAddr = shortAddr[:8]
	}
	configFile := filepath.Join(keysDir, shortAddr+".yaml")
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
	PublicKey string `json:"publicKey,omitempty"`
	CreatedAt string `json:"createdAt"`
	Label     string `json:"label"`
}

// MetadataPayload is the JSON structure stored in the wallet-metadata ConfigMap.
type MetadataPayload struct {
	InstanceID string            `json:"instanceId"`
	Addresses  []MetadataAddress `json:"addresses"`
	Count      int               `json:"count"`
}

// ApplyMetadataConfigMap creates or updates the wallet-metadata ConfigMap
// in the instance namespace. The frontend reads this for display purposes.
func ApplyMetadataConfigMap(cfg *config.Config, id string, address string) error {
	namespace := fmt.Sprintf("%s-%s", appName, id)

	payload := MetadataPayload{
		InstanceID: id,
		Addresses: []MetadataAddress{
			{
				Address:   address,
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

	configMapYAML := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: wallet-metadata
  namespace: %s
  labels:
    app.kubernetes.io/component: wallet
    app.kubernetes.io/managed-by: obol
data:
  addresses.json: |
    %s
`, namespace, indentJSON(string(payloadJSON), 4))

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")

	cmd := exec.Command(kubectlPath, "apply", "-f", "-")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	cmd.Stdin = strings.NewReader(configMapYAML)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to apply wallet-metadata ConfigMap: %w", err)
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

// applyWalletMetadata reads the signing address from the provisioned keystore
// and creates the wallet-metadata ConfigMap. Called after helmfile sync
// when the namespace exists. Errors are non-fatal (printed as warnings).
func applyWalletMetadata(cfg *config.Config, id string) {
	keysDir := Web3SignerKeysPath(cfg, id)

	address := extractAddressFromKeystore(keysDir)
	if address == "" {
		fmt.Printf("  Warning: could not find wallet address in %s\n", keysDir)
		return
	}

	if err := ApplyMetadataConfigMap(cfg, id, address); err != nil {
		fmt.Printf("  Warning: could not create wallet-metadata ConfigMap: %v\n", err)
	} else {
		fmt.Printf("✓ Wallet metadata published (Agent address: %s)\n", address)
		fmt.Printf("  Back up your key: cp -r %s/ ~/obol-wallet-backup-%s/\n", keysDir, id)
		fmt.Println("  WARNING: This wallet feature is in alpha and may change rapidly.")
		fmt.Println("  Do not deposit mainnet funds you are not willing to lose.")
	}
}

// extractAddressFromKeystore reads V3 JSON keystore files in the directory
// and returns the address from the first one found. Keystore files may have
// any extension (cast wallet import uses no extension, cast wallet new uses
// UTC--timestamp--address format).
func extractAddressFromKeystore(keysDir string) string {
	// Check for the well-known account name first
	knownPath := filepath.Join(keysDir, keystoreAccountName)
	if addr := readAddressFromKeystoreFile(knownPath); addr != "" {
		return addr
	}

	// Fall back: scan all files
	entries, err := os.ReadDir(keysDir)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "password.txt" || filepath.Ext(name) == ".yaml" || filepath.Ext(name) == ".ports" {
			continue
		}
		if addr := readAddressFromKeystoreFile(filepath.Join(keysDir, name)); addr != "" {
			return addr
		}
	}
	return ""
}

// readAddressFromKeystoreFile reads a single file and extracts the address
// if it's a valid V3 keystore JSON.
func readAddressFromKeystoreFile(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var ks struct {
		Address string `json:"address"`
		Version int    `json:"version"`
	}
	if json.Unmarshal(content, &ks) != nil || ks.Version != 3 || ks.Address == "" {
		return ""
	}
	addr := ks.Address
	if !strings.HasPrefix(addr, "0x") {
		addr = "0x" + addr
	}
	return addr
}

// ensureWeb3Signer checks if the web3signer key and values file exist for
// an existing deployment. If not, it generates them.
func ensureWeb3Signer(cfg *config.Config, id, deploymentDir string) {
	valuesPath := filepath.Join(deploymentDir, "values-web3signer.yaml")
	keysDir := Web3SignerKeysPath(cfg, id)

	// Check if values and keystore already exist
	if _, err := os.Stat(valuesPath); err == nil {
		if findKeystoreFile(keysDir) != "" {
			return // Both values and keystore exist — nothing to do
		}
	}

	// Generate signing key via cast (V3 encrypted keystore)
	fmt.Println("\nProvisioning wallet for existing deployment...")
	wallet, err := GenerateKeystoreViaCast(keysDir)
	if err != nil {
		fmt.Printf("  Warning: could not generate keystore: %v\n", err)
		return
	}

	keyLabel := fmt.Sprintf("obol-agent-%s", id)
	if err := ProvisionKeyFiles(keysDir, wallet, keyLabel); err != nil {
		fmt.Printf("  Warning: could not provision key config: %v\n", err)
		return
	}
	fmt.Printf("  ✓ Agent wallet address: %s\n", wallet.Address)
	fmt.Printf("  Back up your key: cp -r %s/ ~/obol-wallet-backup-%s/\n", keysDir, id)

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
