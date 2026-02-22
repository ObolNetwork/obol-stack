package openclaw

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestGenerateSigningKey(t *testing.T) {
	key, err := GenerateSigningKey()
	if err != nil {
		t.Fatalf("GenerateSigningKey() error: %v", err)
	}

	// Private key should be 64 hex chars (32 bytes)
	if len(key.PrivateKeyHex) != 64 {
		t.Errorf("PrivateKeyHex length = %d, want 64", len(key.PrivateKeyHex))
	}
	if _, err := hex.DecodeString(key.PrivateKeyHex); err != nil {
		t.Errorf("PrivateKeyHex is not valid hex: %v", err)
	}

	// Public key should be 0x-prefixed, 132 chars (0x + 130 hex = 65 bytes uncompressed)
	if !strings.HasPrefix(key.PublicKeyHex, "0x") {
		t.Errorf("PublicKeyHex should start with 0x, got: %s", key.PublicKeyHex[:4])
	}
	if len(key.PublicKeyHex) != 132 {
		t.Errorf("PublicKeyHex length = %d, want 132 (0x + 130 hex chars)", len(key.PublicKeyHex))
	}

	// Address should be 0x-prefixed, 42 chars (0x + 40 hex = 20 bytes)
	if !strings.HasPrefix(key.Address, "0x") {
		t.Errorf("Address should start with 0x, got: %s", key.Address[:4])
	}
	if len(key.Address) != 42 {
		t.Errorf("Address length = %d, want 42", len(key.Address))
	}

	// KeyID should be 8 hex chars (4 bytes)
	if len(key.KeyID) != 8 {
		t.Errorf("KeyID length = %d, want 8", len(key.KeyID))
	}
}

func TestGenerateSigningKey_Unique(t *testing.T) {
	key1, err := GenerateSigningKey()
	if err != nil {
		t.Fatalf("first GenerateSigningKey() error: %v", err)
	}
	key2, err := GenerateSigningKey()
	if err != nil {
		t.Fatalf("second GenerateSigningKey() error: %v", err)
	}

	if key1.PrivateKeyHex == key2.PrivateKeyHex {
		t.Error("two generated keys should not have the same private key")
	}
	if key1.Address == key2.Address {
		t.Error("two generated keys should not have the same address")
	}
	if key1.KeyID == key2.KeyID {
		t.Error("two generated keys should not have the same key ID")
	}
}

func TestProvisionKeyFiles(t *testing.T) {
	dir := t.TempDir()

	key := &Web3SignerKey{
		PrivateKeyHex: "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		PublicKeyHex:  "0x04" + strings.Repeat("ab", 64),
		Address:       "0x" + strings.Repeat("cd", 20),
		KeyID:         "testkey1",
	}

	err := ProvisionKeyFiles(dir, key, "test-agent")
	if err != nil {
		t.Fatalf("ProvisionKeyFiles() error: %v", err)
	}

	// Check .yaml key config file exists with inline private key
	yamlFile := filepath.Join(dir, "testkey1.yaml")
	info, err := os.Stat(yamlFile)
	if err != nil {
		t.Fatalf("failed to stat yaml key config: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("yaml file permissions = %o, want 0600", perm)
	}

	yamlContent, err := os.ReadFile(yamlFile)
	if err != nil {
		t.Fatalf("failed to read yaml key config: %v", err)
	}
	yaml := string(yamlContent)
	if !strings.Contains(yaml, `type: "file-raw"`) {
		t.Error("yaml should contain type: file-raw")
	}
	if !strings.Contains(yaml, `privateKey: "0x`+key.PrivateKeyHex+`"`) {
		t.Error("yaml should contain inline private key with 0x prefix")
	}
	if !strings.Contains(yaml, `keyType: "SECP256K1"`) {
		t.Error("yaml should specify SECP256K1 key type")
	}
}

func TestProvisionKeyFiles_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "keys")

	key := &Web3SignerKey{
		PrivateKeyHex: strings.Repeat("ab", 32),
		KeyID:         "k1",
	}

	err := ProvisionKeyFiles(dir, key, "test")
	if err != nil {
		t.Fatalf("ProvisionKeyFiles() should create nested dirs: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "k1.yaml")); os.IsNotExist(err) {
		t.Error("key config not created in nested directory")
	}
}

func TestGenerateWeb3SignerValues(t *testing.T) {
	values := generateWeb3SignerValues("my-agent")

	// Should contain the instance ID
	if !strings.Contains(values, "my-agent") {
		t.Error("values should reference the instance ID")
	}

	// Should use customCommand with eth1 subcommand (not the chart's default eth2)
	if !strings.Contains(values, "customCommand:") {
		t.Error("values should use customCommand to override default eth2 command")
	}
	if !strings.Contains(values, "eth1") {
		t.Error("values should use eth1 subcommand")
	}

	// Should disable slashing protection DB (PostgreSQL)
	if !strings.Contains(values, "slashingprotectiondb:") || !strings.Contains(values, "enabled: false") {
		t.Error("values should disable slashingprotectiondb (PostgreSQL)")
	}

	// Should use ClusterIP service
	if !strings.Contains(values, "type: ClusterIP") {
		t.Error("values should use ClusterIP service type")
	}

	// Should pin the image tag
	if !strings.Contains(values, web3signerImageTag) {
		t.Errorf("values should pin image tag %s", web3signerImageTag)
	}

	// Should disable ingress
	if !strings.Contains(values, "ingress:") {
		t.Error("values should have ingress section")
	}
}

func TestMetadataPayload_JSON(t *testing.T) {
	payload := MetadataPayload{
		InstanceID: "test-id",
		Addresses: []MetadataAddress{
			{
				Address:   "0x1234567890abcdef1234567890abcdef12345678",
				PublicKey: "0x04abcd",
				CreatedAt: "2026-02-20T14:30:00Z",
				Label:     "obol-agent-test-id",
			},
		},
		Count: 1,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal metadata: %v", err)
	}

	var decoded MetadataPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal metadata: %v", err)
	}

	if decoded.InstanceID != "test-id" {
		t.Errorf("InstanceID = %q, want %q", decoded.InstanceID, "test-id")
	}
	if decoded.Count != 1 {
		t.Errorf("Count = %d, want 1", decoded.Count)
	}
	if len(decoded.Addresses) != 1 {
		t.Fatalf("Addresses length = %d, want 1", len(decoded.Addresses))
	}
	if decoded.Addresses[0].Address != "0x1234567890abcdef1234567890abcdef12345678" {
		t.Errorf("Address = %q, want full address", decoded.Addresses[0].Address)
	}
}

func TestGenerateHelmfile_IncludesWeb3Signer(t *testing.T) {
	helmfile := generateHelmfile("my-id", "openclaw-my-id")

	// Should have both repos
	if !strings.Contains(helmfile, "name: obol") {
		t.Error("helmfile should have obol repo")
	}
	if !strings.Contains(helmfile, "name: ethereum") {
		t.Error("helmfile should have ethereum repo")
	}
	if !strings.Contains(helmfile, "ethpandaops.github.io/ethereum-helm-charts") {
		t.Error("helmfile should reference ethpandaops helm charts")
	}

	// Should have both releases
	if !strings.Contains(helmfile, "name: openclaw") {
		t.Error("helmfile should have openclaw release")
	}
	if !strings.Contains(helmfile, "name: web3signer") {
		t.Error("helmfile should have web3signer release")
	}

	// Both releases should target the same namespace
	if strings.Count(helmfile, "namespace: openclaw-my-id") != 2 {
		t.Error("both releases should target the same namespace openclaw-my-id")
	}

	// Should reference web3signer values file
	if !strings.Contains(helmfile, "values-web3signer.yaml") {
		t.Error("helmfile should reference values-web3signer.yaml")
	}

	// Should reference the pinned chart version
	if !strings.Contains(helmfile, web3signerChartVersion) {
		t.Errorf("helmfile should pin web3signer chart version %s", web3signerChartVersion)
	}
}

func TestWeb3SignerKeysPath(t *testing.T) {
	cfg := &config.Config{
		DataDir: "/home/user/.local/share/obol",
	}

	path := Web3SignerKeysPath(cfg, "my-agent")
	expected := "/home/user/.local/share/obol/openclaw-my-agent/storage-web3signer-0/keys"
	if path != expected {
		t.Errorf("Web3SignerKeysPath() = %q, want %q", path, expected)
	}
}

func TestExtractPrivateKeyFromYAML(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "standard format",
			content: `type: "file-raw"
keyType: "SECP256K1"
privateKey: "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
`,
			want: "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		},
		{
			name: "single quotes",
			content: `type: "file-raw"
privateKey: '0xdeadbeef'
`,
			want: "deadbeef",
		},
		{
			name:    "no privateKey field",
			content: `type: "file-raw"`,
			want:    "",
		},
		{
			name:    "empty content",
			content: "",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPrivateKeyFromYAML(tt.content)
			if got != tt.want {
				t.Errorf("extractPrivateKeyFromYAML() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIndentJSON(t *testing.T) {
	input := `{
  "foo": "bar",
  "baz": 1
}`
	result := indentJSON(input, 4)
	lines := strings.Split(result, "\n")

	// First line should not be indented (already at right level in YAML)
	if lines[0] != "{" {
		t.Errorf("first line should be '{', got %q", lines[0])
	}
	// Subsequent lines should have 4-space indent
	if !strings.HasPrefix(lines[1], "    ") {
		t.Errorf("second line should have 4-space indent, got %q", lines[1])
	}
}
