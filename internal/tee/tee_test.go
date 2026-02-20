package tee

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/enclave"
)

const testModelHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" // SHA256("")

func TestNewKey_StubBackend(t *testing.T) {
	key, err := NewKey("com.obol.test.tee", testModelHash)
	if err != nil {
		t.Fatalf("NewKey failed: %v", err)
	}

	// Check public key shape: 65 bytes, 0x04 prefix.
	pub := key.PublicKeyBytes()
	if len(pub) != 65 {
		t.Fatalf("expected 65-byte public key, got %d", len(pub))
	}
	if pub[0] != 0x04 {
		t.Fatalf("expected 0x04 prefix, got 0x%02x", pub[0])
	}

	// Check tag and persistence.
	if key.Tag() != "com.obol.test.tee" {
		t.Errorf("expected tag %q, got %q", "com.obol.test.tee", key.Tag())
	}
	if !key.Persistent() {
		t.Error("expected Persistent() == true")
	}
}

func TestNewKey_UniqueKeys(t *testing.T) {
	k1, err := NewKey("com.obol.test.k1", testModelHash)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := NewKey("com.obol.test.k2", testModelHash)
	if err != nil {
		t.Fatal(err)
	}

	pub1 := hex.EncodeToString(k1.PublicKeyBytes())
	pub2 := hex.EncodeToString(k2.PublicKeyBytes())
	if pub1 == pub2 {
		t.Error("two separately generated keys should have different public keys")
	}
}

func TestECIES_RoundTrip(t *testing.T) {
	key, err := NewKey("com.obol.test.ecies", testModelHash)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte(`{"model":"llama3","messages":[{"role":"user","content":"hello"}]}`)

	// Encrypt using the public key (same as client would do).
	ct, err := enclave.Encrypt(key.PublicKeyBytes(), plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Ciphertext should be longer than plaintext (version + ephPub + nonce + tag).
	if len(ct) <= len(plaintext) {
		t.Fatalf("ciphertext (%d bytes) should be longer than plaintext (%d bytes)", len(ct), len(plaintext))
	}

	// Decrypt via the TEE key.
	decrypted, err := key.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if string(decrypted) != string(plaintext) {
		t.Fatalf("round-trip mismatch:\n  got:  %s\n  want: %s", string(decrypted), string(plaintext))
	}
}

func TestECIES_DecryptBadVersion(t *testing.T) {
	key, err := NewKey("com.obol.test.badver", testModelHash)
	if err != nil {
		t.Fatal(err)
	}

	ct, err := enclave.Encrypt(key.PublicKeyBytes(), []byte("test"))
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt the version byte.
	ct[0] = 0xFF
	_, err = key.Decrypt(ct)
	if err == nil {
		t.Error("expected error for bad version, got nil")
	}
}

func TestECIES_DecryptTruncated(t *testing.T) {
	key, err := NewKey("com.obol.test.trunc", testModelHash)
	if err != nil {
		t.Fatal(err)
	}

	_, err = key.Decrypt([]byte{0x01, 0x02, 0x03})
	if err == nil {
		t.Error("expected error for truncated ciphertext, got nil")
	}
}

func TestAttest_Stub(t *testing.T) {
	key, err := NewKey("com.obol.test.attest", testModelHash)
	if err != nil {
		t.Fatal(err)
	}

	report, err := Attest(key, testModelHash)
	if err != nil {
		t.Fatalf("Attest failed: %v", err)
	}

	// Verify report fields.
	if report.TEEType != TEETypeStub {
		t.Errorf("expected tee_type %q, got %q", TEETypeStub, report.TEEType)
	}
	if report.Pubkey != hex.EncodeToString(key.PublicKeyBytes()) {
		t.Error("report pubkey doesn't match key's public key")
	}
	if report.ModelHash != testModelHash {
		t.Errorf("report model_hash = %q, want %q", report.ModelHash, testModelHash)
	}
	if report.Timestamp == 0 {
		t.Error("expected non-zero timestamp")
	}
	if len(report.Quote) == 0 {
		t.Error("expected non-empty quote")
	}

	// Parse the stub quote and verify user_data.
	var stubDoc struct {
		Type     string `json:"type"`
		UserData string `json:"user_data"`
	}
	if err := json.Unmarshal(report.Quote, &stubDoc); err != nil {
		t.Fatalf("failed to parse stub quote: %v", err)
	}
	if stubDoc.Type != "stub" {
		t.Errorf("stub quote type = %q, want %q", stubDoc.Type, "stub")
	}

	// Verify user_data = SHA256(pubkey || modelHashBytes).
	modelHashBytes, _ := hex.DecodeString(testModelHash)
	h := sha256.New()
	h.Write(key.PublicKeyBytes())
	h.Write(modelHashBytes)
	expectedUD := hex.EncodeToString(h.Sum(nil))
	if stubDoc.UserData != expectedUD {
		t.Errorf("user_data mismatch:\n  got:  %s\n  want: %s", stubDoc.UserData, expectedUD)
	}
}

func TestVerifyBinding_Stub(t *testing.T) {
	key, err := NewKey("com.obol.test.verify", testModelHash)
	if err != nil {
		t.Fatal(err)
	}

	report, err := Attest(key, testModelHash)
	if err != nil {
		t.Fatal(err)
	}

	// Should pass verification.
	if err := VerifyBinding(report.Quote, key.PublicKeyBytes(), testModelHash); err != nil {
		t.Fatalf("VerifyBinding failed: %v", err)
	}
}

func TestVerifyBinding_WrongModel(t *testing.T) {
	key, err := NewKey("com.obol.test.wrongmodel", testModelHash)
	if err != nil {
		t.Fatal(err)
	}

	report, err := Attest(key, testModelHash)
	if err != nil {
		t.Fatal(err)
	}

	// Wrong model hash should fail.
	wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"
	err = VerifyBinding(report.Quote, key.PublicKeyBytes(), wrongHash)
	if err == nil {
		t.Error("expected error for wrong model hash, got nil")
	}
}

func TestExtractUserData_StubRoundTrip(t *testing.T) {
	key, err := NewKey("com.obol.test.extract", testModelHash)
	if err != nil {
		t.Fatal(err)
	}

	report, err := Attest(key, testModelHash)
	if err != nil {
		t.Fatal(err)
	}

	ud, teeType, err := ExtractUserData(report.Quote)
	if err != nil {
		t.Fatalf("ExtractUserData failed: %v", err)
	}
	if teeType != TEETypeStub {
		t.Errorf("expected tee type %q, got %q", TEETypeStub, teeType)
	}

	expected, _ := UserData(key.PublicKeyBytes(), testModelHash)
	if !bytesEqual(ud, expected) {
		t.Error("extracted user_data doesn't match expected")
	}
}

func TestSign_Stub(t *testing.T) {
	key, err := NewKey("com.obol.test.sign", testModelHash)
	if err != nil {
		t.Fatal(err)
	}

	digest := sha256.Sum256([]byte("test message"))
	sig, err := key.Sign(digest[:])
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}
	if len(sig) == 0 {
		t.Error("expected non-empty signature")
	}

	// DER signature should start with 0x30 (SEQUENCE).
	if sig[0] != 0x30 {
		t.Errorf("expected DER SEQUENCE tag 0x30, got 0x%02x", sig[0])
	}
}

func TestDelete_Stub(t *testing.T) {
	key, err := NewKey("com.obol.test.delete", testModelHash)
	if err != nil {
		t.Fatal(err)
	}

	if err := key.Delete(); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestParseTEEType(t *testing.T) {
	tests := []struct {
		input string
		want  TEEType
		ok    bool
	}{
		{"tdx", TEETypeTDX, true},
		{"snp", TEETypeSNP, true},
		{"nitro", TEETypeNitro, true},
		{"stub", TEETypeStub, true},
		{"unknown", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		got, err := ParseTEEType(tt.input)
		if tt.ok && err != nil {
			t.Errorf("ParseTEEType(%q) unexpected error: %v", tt.input, err)
		} else if !tt.ok && err == nil {
			t.Errorf("ParseTEEType(%q) expected error, got %q", tt.input, got)
		} else if tt.ok && got != tt.want {
			t.Errorf("ParseTEEType(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestComputeModelHash(t *testing.T) {
	hash := ComputeModelHash("llama3")
	if len(hash) != 64 {
		t.Errorf("expected 64 hex chars, got %d", len(hash))
	}
	// Should be deterministic.
	hash2 := ComputeModelHash("llama3")
	if hash != hash2 {
		t.Error("ComputeModelHash should be deterministic")
	}
	// Different input should produce different hash.
	hash3 := ComputeModelHash("gpt-4")
	if hash == hash3 {
		t.Error("different inputs should produce different hashes")
	}
}

func TestMarshalReport(t *testing.T) {
	key, err := NewKey("com.obol.test.marshal", testModelHash)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Attest(key, testModelHash)
	if err != nil {
		t.Fatal(err)
	}

	data, err := MarshalReport(report)
	if err != nil {
		t.Fatalf("MarshalReport failed: %v", err)
	}

	// Should be valid JSON.
	var parsed AttestationReport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal report JSON: %v", err)
	}
	if parsed.TEEType != TEETypeStub {
		t.Errorf("round-trip tee_type = %q, want %q", parsed.TEEType, TEETypeStub)
	}
}
