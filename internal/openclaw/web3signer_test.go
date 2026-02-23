package openclaw

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestProvisionKeyFiles(t *testing.T) {
	dir := t.TempDir()

	// Create a fake keystore file (simulating what cast wallet new creates)
	keystoreFile := filepath.Join(dir, "obol-agent")
	if err := os.WriteFile(keystoreFile, []byte(`{"address":"abcdef1234567890abcdef1234567890abcdef12","crypto":{},"id":"test","version":3}`), 0600); err != nil {
		t.Fatal(err)
	}

	wallet := &WalletKey{
		Address:      "0xabcdef1234567890abcdef1234567890abcdef12",
		KeystoreFile: keystoreFile,
		PasswordFile: filepath.Join(dir, "password.txt"),
	}

	err := ProvisionKeyFiles(dir, wallet, "test-agent")
	if err != nil {
		t.Fatalf("ProvisionKeyFiles() error: %v", err)
	}

	// Check .yaml key config file exists with file-keystore reference
	yamlFile := filepath.Join(dir, "abcdef12.yaml")
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
	if !strings.Contains(yaml, `type: "file-keystore"`) {
		t.Error("yaml should contain type: file-keystore")
	}
	if !strings.Contains(yaml, `keystoreFile: "/data/keys/obol-agent"`) {
		t.Error("yaml should reference keystore file by name")
	}
	if !strings.Contains(yaml, `keystorePasswordFile: "/data/keys/password.txt"`) {
		t.Error("yaml should reference password file at container path")
	}
	if !strings.Contains(yaml, `keyType: "SECP256K1"`) {
		t.Error("yaml should specify SECP256K1 key type")
	}
}

func TestFindKeystoreFile_AccountName(t *testing.T) {
	dir := t.TempDir()

	// cast wallet import creates files named after the account
	keystorePath := filepath.Join(dir, "obol-agent")
	if err := os.WriteFile(keystorePath, []byte(`{"version":3}`), 0600); err != nil {
		t.Fatal(err)
	}

	got := findKeystoreFile(dir)
	if got != keystorePath {
		t.Errorf("findKeystoreFile() = %q, want %q", got, keystorePath)
	}
}

func TestFindKeystoreFile_SkipsNonKeystore(t *testing.T) {
	dir := t.TempDir()

	// Write files that should be skipped
	os.WriteFile(filepath.Join(dir, "password.txt"), []byte("pw"), 0600)
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("type: file-keystore"), 0600)

	got := findKeystoreFile(dir)
	if got != "" {
		t.Errorf("findKeystoreFile() should return empty for non-keystore files, got %q", got)
	}
}

func TestExtractAddressFromKeystore(t *testing.T) {
	dir := t.TempDir()

	keystore := `{"address":"2a94386c1e32628b15d155a387f3ca2d406d7cb3","crypto":{},"id":"test","version":3}`
	if err := os.WriteFile(filepath.Join(dir, "obol-agent"), []byte(keystore), 0600); err != nil {
		t.Fatal(err)
	}

	address := extractAddressFromKeystore(dir)
	if address != "0x2a94386c1e32628b15d155a387f3ca2d406d7cb3" {
		t.Errorf("extractAddressFromKeystore() = %q, want 0x-prefixed address", address)
	}
}

func TestExtractAddressFromKeystore_Empty(t *testing.T) {
	dir := t.TempDir()
	address := extractAddressFromKeystore(dir)
	if address != "" {
		t.Errorf("extractAddressFromKeystore() on empty dir = %q, want empty", address)
	}
}

func TestParseAddressFromOutput(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{
			name: "cast wallet new output",
			output: `Created new encrypted keystore file: /tmp/keys/UTC--2026-02-22--abcdef
Address: 0x2a94386c1e32628b15d155a387f3cA2D406d7Cb3`,
			want: "0x2a94386c1e32628b15d155a387f3cA2D406d7Cb3",
		},
		{
			name: "skips private key line",
			output: `Private key: 0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab
Address: 0x1234567890abcdef1234567890abcdef12345678`,
			want: "0x1234567890abcdef1234567890abcdef12345678",
		},
		{
			name:    "no address found",
			output:  "some random output",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAddressFromOutput(tt.output)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("parseAddressFromOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateRandomPassword(t *testing.T) {
	pw1, err := generateRandomPassword(32)
	if err != nil {
		t.Fatalf("generateRandomPassword() error: %v", err)
	}
	if len(pw1) != 32 {
		t.Errorf("password length = %d, want 32", len(pw1))
	}

	pw2, _ := generateRandomPassword(32)
	if pw1 == pw2 {
		t.Error("two generated passwords should not be identical")
	}
}

func TestGenerateWeb3SignerValues(t *testing.T) {
	values := generateWeb3SignerValues("my-agent")

	if !strings.Contains(values, "my-agent") {
		t.Error("values should reference the instance ID")
	}
	if !strings.Contains(values, "eth1") {
		t.Error("values should use eth1 subcommand")
	}
	if !strings.Contains(values, "type: ClusterIP") {
		t.Error("values should use ClusterIP service type")
	}
	if !strings.Contains(values, web3signerImageTag) {
		t.Errorf("values should pin image tag %s", web3signerImageTag)
	}
}

func TestMetadataPayload_JSON(t *testing.T) {
	payload := MetadataPayload{
		InstanceID: "test-id",
		Addresses: []MetadataAddress{
			{
				Address:   "0x1234567890abcdef1234567890abcdef12345678",
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
}

func TestGenerateHelmfile_IncludesWeb3Signer(t *testing.T) {
	helmfile := generateHelmfile("my-id", "openclaw-my-id")

	if !strings.Contains(helmfile, "name: obol") {
		t.Error("helmfile should have obol repo")
	}
	if !strings.Contains(helmfile, "name: ethereum") {
		t.Error("helmfile should have ethereum repo")
	}
	if !strings.Contains(helmfile, "name: openclaw") {
		t.Error("helmfile should have openclaw release")
	}
	if !strings.Contains(helmfile, "name: web3signer") {
		t.Error("helmfile should have web3signer release")
	}
	if strings.Count(helmfile, "namespace: openclaw-my-id") != 2 {
		t.Error("both releases should target the same namespace")
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

func TestIndentJSON(t *testing.T) {
	input := `{
  "foo": "bar",
  "baz": 1
}`
	result := indentJSON(input, 4)
	lines := strings.Split(result, "\n")

	if lines[0] != "{" {
		t.Errorf("first line should be '{', got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "    ") {
		t.Errorf("second line should have 4-space indent, got %q", lines[1])
	}
}
