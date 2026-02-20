package tee

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// ErrNotSupported is returned by verification functions for stub quotes.
var ErrNotSupported = fmt.Errorf("tee: verification not supported for stub quotes")

// ExtractUserData extracts the user_data field from a quote regardless of
// TEE type. The TEE type is auto-detected from the quote header.
//
// For stub quotes, user_data is the hex-decoded "user_data" JSON field.
// For real TEE quotes (TDX/SNP/Nitro), this will parse the native format.
func ExtractUserData(quote []byte) ([]byte, TEEType, error) {
	// Try stub format first (JSON with "type":"stub").
	var stubDoc struct {
		Type     string `json:"type"`
		UserData string `json:"user_data"`
	}
	if err := json.Unmarshal(quote, &stubDoc); err == nil && stubDoc.Type == "stub" {
		ud, err := hex.DecodeString(stubDoc.UserData)
		if err != nil {
			return nil, TEETypeStub, fmt.Errorf("tee: invalid stub user_data hex: %w", err)
		}
		return ud, TEETypeStub, nil
	}

	// TODO(phase-2b): Detect and parse TDX DCAP quote header.
	// TDX quotes start with a 48-byte header: version(2) + attestation_key_type(2) + ...
	// The reportData (user_data) is at a known offset in the TD Report body.

	// TODO(phase-2b): Detect and parse SNP attestation report.
	// SNP reports are exactly 1184 bytes. user_data is at offset 0x50 (80), 64 bytes.

	// TODO(phase-2b): Detect and parse Nitro CBOR attestation document.
	// Nitro docs are CBOR-encoded with a known COSE_Sign1 structure.
	// user_data is in the payload map under key "user_data".

	return nil, "", fmt.Errorf("tee: unrecognised quote format (%d bytes)", len(quote))
}

// VerifyBinding checks that a quote's user_data matches the expected
// binding of pubkey + modelHash:
//
//	SHA256(pubkeyBytes || modelHashBytes) == extracted user_data
//
// This is the fundamental verification step that any client should perform.
func VerifyBinding(quote, pubkey []byte, modelHash string) error {
	userData, teeType, err := ExtractUserData(quote)
	if err != nil {
		return err
	}

	expected, err := UserData(pubkey, modelHash)
	if err != nil {
		return err
	}

	if !bytesEqual(userData, expected) {
		return fmt.Errorf("tee: user_data mismatch (tee_type=%s): quote does not bind to pubkey+model", teeType)
	}

	return nil
}

// VerifyTDX parses and verifies a TDX DCAP quote against Intel's public
// PCK certificates. Returns the parsed TD measurements if valid.
func VerifyTDX(quote []byte, expectedUserData []byte) error {
	// TODO(phase-2b): Implement TDX DCAP quote verification.
	//
	// Steps:
	// 1. Parse DCAP quote structure (header + body + signature)
	// 2. Extract signing cert chain from quote
	// 3. Verify cert chain against Intel SGX root CA
	// 4. Verify ECDSA signature over TD Report
	// 5. Compare reportData[0:32] with expectedUserData
	// 6. Return parsed measurements (MRTD, MRCONFIGID, RTMRs)
	return fmt.Errorf("tee: TDX verification not yet implemented")
}

// VerifySNP parses and verifies an AMD SEV-SNP attestation report against
// AMD's VCEK certificate chain.
func VerifySNP(report []byte, expectedUserData []byte) error {
	// TODO(phase-2b): Implement SNP report verification.
	return fmt.Errorf("tee: SNP verification not yet implemented")
}

// VerifyNitro verifies an AWS Nitro attestation document against the
// Nitro CA certificate chain.
func VerifyNitro(doc []byte, expectedUserData []byte) error {
	// TODO(phase-2b): Implement Nitro document verification.
	return fmt.Errorf("tee: Nitro verification not yet implemented")
}

// ComputeModelHash returns the hex-encoded SHA-256 of the given model
// identifier string. This is a convenience for callers that don't have
// a pre-computed hash of the model weights.
func ComputeModelHash(modelID string) string {
	h := sha256.Sum256([]byte(modelID))
	return hex.EncodeToString(h[:])
}

// bytesEqual is a constant-time-ish comparison (we're not protecting against
// timing attacks here — the quote is public data — but it's cleaner).
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
