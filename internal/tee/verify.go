package tee

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sevabi "github.com/google/go-sev-guest/abi"
	sevpb "github.com/google/go-sev-guest/proto/sevsnp"
	sevvalidate "github.com/google/go-sev-guest/validate"
	sevverify "github.com/google/go-sev-guest/verify"
	tdxabi "github.com/google/go-tdx-guest/abi"
	tdxpb "github.com/google/go-tdx-guest/proto/tdx"
	tdxvalidate "github.com/google/go-tdx-guest/validate"
	tdxverify "github.com/google/go-tdx-guest/verify"
	"github.com/hf/nitrite"
)

// ExtractUserData extracts the user_data field from a quote regardless of
// TEE type. The TEE type is auto-detected from the quote header.
//
// For stub quotes, user_data is the hex-decoded "user_data" JSON field.
// For real TEE quotes, the native format is parsed.
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

	// AMD SEV-SNP: report is exactly 1184 bytes, reportData at offset 0x050.
	if len(quote) == sevabi.ReportSize {
		reportData := quote[0x050 : 0x050+sevabi.ReportDataSize]
		// reportData is 64 bytes; our user_data is a 32-byte SHA-256 in the
		// first 32 bytes (remaining 32 are zero-padded).
		return reportData[:32], TEETypeSNP, nil
	}

	// Intel TDX: DCAP quote v4 starts with version=4 (uint16 LE) and
	// teeType=0x00000081 at offset 4.
	if len(quote) > 48 {
		version := binary.LittleEndian.Uint16(quote[0:2])

		teeType := binary.LittleEndian.Uint32(quote[4:8])
		if version == 4 && teeType == 0x00000081 {
			// reportData is in the TD Quote Body at offset 568 from quote
			// start (48-byte header + 520 bytes into body).
			const reportDataOffset = 0x238 // 568
			if len(quote) > reportDataOffset+64 {
				reportData := quote[reportDataOffset : reportDataOffset+64]
				return reportData[:32], TEETypeTDX, nil
			}
		}
	}

	// AWS Nitro: CBOR-encoded COSE_Sign1 (starts with CBOR tag 18 = 0xD2).
	if len(quote) > 4 && quote[0] == 0xD2 {
		result, err := nitrite.Verify(quote, nitrite.VerifyOptions{
			CurrentTime: time.Now(),
		})
		if err == nil && result.Document != nil && result.Document.UserData != nil {
			return result.Document.UserData, TEETypeNitro, nil
		}
		// If verification fails, still try to extract user_data from the
		// payload for binding checks (caller will verify separately).
		// Fall through to error.
	}

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

// VerifyTDX parses and cryptographically verifies a TDX DCAP quote v4.
//
// Verification steps:
//  1. Parse the raw bytes into a QuoteV4 protobuf
//  2. Validate the PCK certificate chain (Intel Root CA → Intermediate → PCK Leaf)
//  3. Verify the ECDSA-256 signature over Header + TDQuoteBody
//  4. Optionally fetch Intel PCS collateral for TCB/QE identity checks
//  5. Validate reportData matches expectedUserData
func VerifyTDX(quote []byte, expectedUserData []byte) error {
	// Parse raw quote bytes into protobuf.
	quoteAny, err := tdxabi.QuoteToProto(quote)
	if err != nil {
		return fmt.Errorf("tee/tdx: parse quote: %w", err)
	}

	quoteV4, ok := quoteAny.(*tdxpb.QuoteV4)
	if !ok {
		return fmt.Errorf("tee/tdx: unexpected quote format (expected QuoteV4, got %T)", quoteAny)
	}

	// Cryptographic verification: PCK cert chain + ECDSA signature.
	// DefaultOptions does NOT fetch collateral from Intel PCS (offline-capable).
	verifyOpts := tdxverify.DefaultOptions()
	if err := tdxverify.TdxQuote(quoteAny, verifyOpts); err != nil {
		return fmt.Errorf("tee/tdx: signature verification failed: %w", err)
	}

	// Policy validation: check reportData matches expected binding.
	if expectedUserData != nil {
		valOpts := &tdxvalidate.Options{
			TdQuoteBodyOptions: tdxvalidate.TdQuoteBodyOptions{
				ReportData: padTo64(expectedUserData),
			},
		}
		if err := tdxvalidate.TdxQuote(quoteAny, valOpts); err != nil {
			return fmt.Errorf("tee/tdx: policy validation failed: %w", err)
		}
	}

	_ = quoteV4 // available for callers who need measurements (MrTd, RTMRs)

	return nil
}

// VerifySNP parses and cryptographically verifies an AMD SEV-SNP attestation
// report against AMD's VCEK/VLEK certificate chain.
//
// Verification steps:
//  1. Parse the raw 1184-byte report into a protobuf
//  2. Validate the certificate chain (AMD ARK → ASK → VCEK)
//  3. Verify ECDSA-P384-SHA384 signature over report bytes 0x000–0x29F
//  4. Validate reportData matches expectedUserData
//  5. Enforce policy: no debug mode
func VerifySNP(report []byte, expectedUserData []byte) error {
	// Parse raw report.
	if len(report) < sevabi.ReportSize {
		return fmt.Errorf("tee/snp: report too short (%d bytes, need %d)", len(report), sevabi.ReportSize)
	}

	protoReport, err := sevabi.ReportToProto(report[:sevabi.ReportSize])
	if err != nil {
		return fmt.Errorf("tee/snp: parse report: %w", err)
	}

	// If the report includes a certificate table (extended report), parse
	// the full attestation. Otherwise, go-sev-guest will fetch VCEK from
	// AMD KDS using the chip ID and TCB version from the report.
	var attestation *sevpb.Attestation
	if len(report) > sevabi.ReportSize {
		attestation, err = sevabi.ReportCertsToProto(report)
		if err != nil {
			return fmt.Errorf("tee/snp: parse attestation: %w", err)
		}
	} else {
		attestation = &sevpb.Attestation{
			Report: protoReport,
		}
	}

	// Cryptographic verification: cert chain + signature.
	verifyOpts := sevverify.DefaultOptions()
	if err := sevverify.SnpAttestation(attestation, verifyOpts); err != nil {
		return fmt.Errorf("tee/snp: signature verification failed: %w", err)
	}

	// Policy validation: check reportData and enforce no-debug.
	if expectedUserData != nil {
		valOpts := &sevvalidate.Options{
			ReportData: padTo64(expectedUserData),
			GuestPolicy: sevabi.SnpPolicy{
				Debug: false, // must NOT be a debug VM
			},
		}
		if err := sevvalidate.SnpAttestation(attestation, valOpts); err != nil {
			return fmt.Errorf("tee/snp: policy validation failed: %w", err)
		}
	}

	return nil
}

// VerifyNitro verifies an AWS Nitro attestation document against the
// Nitro CA certificate chain (embedded in the nitrite library).
//
// Verification steps:
//  1. CBOR-decode the COSE_Sign1 structure
//  2. Validate the certificate chain to the AWS Nitro Root CA G1
//  3. Verify the ECDSA-P384-SHA384 COSE signature
//  4. Check user_data matches expectedUserData
//  5. Optionally check PCR0 against expected enclave image hash
func VerifyNitro(doc []byte, expectedUserData []byte) error {
	result, err := nitrite.Verify(doc, nitrite.VerifyOptions{
		CurrentTime: time.Now(),
	})
	if err != nil {
		return fmt.Errorf("tee/nitro: attestation verification failed: %w", err)
	}

	if !result.SignatureOK {
		return errors.New("tee/nitro: COSE signature verification failed")
	}

	// Validate user_data binding.
	if expectedUserData != nil {
		if !bytes.Equal(result.Document.UserData, expectedUserData) {
			return fmt.Errorf("tee/nitro: user_data mismatch: expected %x, got %x",
				expectedUserData, result.Document.UserData)
		}
	}

	return nil
}

// VerifyNitroWithPCR0 extends VerifyNitro with a PCR0 check against an
// expected enclave image hash (SHA-384 of the EIF file).
func VerifyNitroWithPCR0(doc []byte, expectedUserData []byte, expectedPCR0 []byte) error {
	result, err := nitrite.Verify(doc, nitrite.VerifyOptions{
		CurrentTime: time.Now(),
	})
	if err != nil {
		return fmt.Errorf("tee/nitro: attestation verification failed: %w", err)
	}

	if !result.SignatureOK {
		return errors.New("tee/nitro: COSE signature verification failed")
	}

	if expectedUserData != nil {
		if !bytes.Equal(result.Document.UserData, expectedUserData) {
			return errors.New("tee/nitro: user_data mismatch")
		}
	}

	if expectedPCR0 != nil {
		pcr0, ok := result.Document.PCRs[0]
		if !ok {
			return errors.New("tee/nitro: PCR0 not present in attestation")
		}

		if !bytes.Equal(pcr0, expectedPCR0) {
			return fmt.Errorf("tee/nitro: PCR0 mismatch: expected %x, got %x",
				expectedPCR0, pcr0)
		}
	}

	return nil
}

// ComputeModelHash returns the hex-encoded SHA-256 of the given model
// identifier string. This is a convenience for callers that don't have
// a pre-computed hash of the model weights.
func ComputeModelHash(modelID string) string {
	h := sha256.Sum256([]byte(modelID))
	return hex.EncodeToString(h[:])
}

// padTo64 returns a 64-byte slice with data left-padded (zero-filled on the right).
func padTo64(data []byte) []byte {
	out := make([]byte, 64)
	copy(out, data)

	return out
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
