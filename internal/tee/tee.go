// Package tee provides Linux Trusted Execution Environment (TEE) key
// management and attestation for Intel TDX, AMD SEV-SNP, and AWS Nitro
// Enclaves. Keys implement the enclave.Key interface so the inference
// gateway can use TEE-backed keys interchangeably with macOS Secure
// Enclave keys.
//
// On platforms without TEE hardware (or when no build tag is set), a
// software stub backend generates standard in-process P-256 keys with
// dummy attestation quotes. This allows development and CI testing on
// any machine.
//
// Build with -tags tdx, -tags snp, or -tags nitro to enable real
// hardware backends. Without any of these tags the stub is used
// automatically.
package tee

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/enclave"
)

// TEEType identifies the hardware TEE backend in use.
type TEEType string

const (
	TEETypeTDX   TEEType = "tdx"
	TEETypeSNP   TEEType = "snp"
	TEETypeNitro TEEType = "nitro"
	TEETypeStub  TEEType = "stub" // software fallback, dev mode
)

// ValidTEETypes returns the list of accepted --tee flag values.
func ValidTEETypes() []TEEType {
	return []TEEType{TEETypeTDX, TEETypeSNP, TEETypeNitro, TEETypeStub}
}

// ParseTEEType validates a string as a TEEType.
func ParseTEEType(s string) (TEEType, error) {
	switch TEEType(s) {
	case TEETypeTDX, TEETypeSNP, TEETypeNitro, TEETypeStub:
		return TEEType(s), nil
	default:
		return "", fmt.Errorf("tee: unknown TEE type %q (valid: tdx, snp, nitro, stub)", s)
	}
}

// AttestationReport is returned by the /v1/attestation endpoint and
// contains the TEE quote plus metadata needed for client-side verification.
type AttestationReport struct {
	TEEType   TEEType `json:"tee_type"`
	Pubkey    string  `json:"pubkey"`     // hex-encoded 65-byte uncompressed P-256
	ModelHash string  `json:"model_hash"` // hex SHA-256 of model weights/ID
	Quote     []byte  `json:"quote"`      // raw TEE quote bytes (base64 in JSON)
	Timestamp int64   `json:"timestamp"`  // Unix seconds
}

// UserData computes the attestation user_data binding:
//
//	SHA256(pubkeyBytes || modelHashBytes)
//
// where pubkeyBytes is the 65-byte uncompressed SEC1 public key and
// modelHashBytes is the raw 32-byte SHA-256 of the model.
func UserData(pubkey []byte, modelHash string) ([]byte, error) {
	hashBytes, err := hex.DecodeString(modelHash)
	if err != nil {
		return nil, fmt.Errorf("tee: invalid model hash hex: %w", err)
	}
	h := sha256.New()
	h.Write(pubkey)
	h.Write(hashBytes)
	return h.Sum(nil), nil
}

// Attest generates an attestation report for the given key and model.
// The report binds the key's public key and the model hash into the
// TEE quote's user_data field.
//
// The key must have been created by tee.NewKey(); passing any other
// enclave.Key implementation returns an error.
func Attest(key enclave.Key, modelHash string) (*AttestationReport, error) {
	tk, ok := key.(*teeKey)
	if !ok {
		return nil, fmt.Errorf("tee: Attest requires a TEE-backed key (got %T)", key)
	}

	userData, err := UserData(tk.PublicKeyBytes(), modelHash)
	if err != nil {
		return nil, err
	}

	quote, err := tk.backend.attest(userData)
	if err != nil {
		return nil, fmt.Errorf("tee: attestation failed: %w", err)
	}

	return &AttestationReport{
		TEEType:   tk.teeType,
		Pubkey:    hex.EncodeToString(tk.PublicKeyBytes()),
		ModelHash: modelHash,
		Quote:     quote,
		Timestamp: time.Now().Unix(),
	}, nil
}

// MarshalReport encodes a report as indented JSON.
func MarshalReport(r *AttestationReport) ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}
