//go:build !tdx && !snp && !nitro

package tee

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/enclave"
)

// stubBackend generates a standard in-process P-256 key with no hardware
// TEE backing. attest() returns a JSON-encoded dummy quote that real
// verifiers will reject — it is for local development and CI only.
type stubBackend struct {
	privKey  *ecdsa.PrivateKey
	ecdhPriv *ecdh.PrivateKey // ecdh.PrivateKey for ECDH operations
}

func newStubBackend() (*stubBackend, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("tee/stub: key generation failed: %w", err)
	}

	// Convert *ecdsa.PrivateKey to *ecdh.PrivateKey for ECDH operations.
	ecdhPriv, err := priv.ECDH()
	if err != nil {
		return nil, fmt.Errorf("tee/stub: ECDH key conversion failed: %w", err)
	}

	return &stubBackend{
		privKey:  priv,
		ecdhPriv: ecdhPriv,
	}, nil
}

func (b *stubBackend) sign(digest []byte) ([]byte, error) {
	r, s, err := ecdsa.Sign(rand.Reader, b.privKey, digest)
	if err != nil {
		return nil, fmt.Errorf("tee/stub: sign failed: %w", err)
	}
	// DER-encode the signature (same format as SE).
	return marshalDER(r, s), nil
}

func (b *stubBackend) ecdh(peerPubKeyBytes []byte) ([]byte, error) {
	curve := ecdh.P256()
	peerKey, err := curve.NewPublicKey(peerPubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("tee/stub: invalid peer public key: %w", err)
	}
	shared, err := b.ecdhPriv.ECDH(peerKey)
	if err != nil {
		return nil, fmt.Errorf("tee/stub: ECDH failed: %w", err)
	}
	return shared, nil
}

func (b *stubBackend) attest(userData []byte) ([]byte, error) {
	doc := map[string]any{
		"type":      "stub",
		"user_data": hex.EncodeToString(userData),
		"timestamp": time.Now().Unix(),
	}
	return json.Marshal(doc)
}

func (b *stubBackend) delete() error {
	// No persistent state to clean up.
	return nil
}

// pubKeyBytes returns the 65-byte uncompressed SEC1 encoding.
func (b *stubBackend) pubKeyBytes() []byte {
	pub := b.privKey.PublicKey
	return elliptic.MarshalCompressed(pub.Curve, pub.X, pub.Y)
	// Actually we need uncompressed (65 bytes), not compressed.
}

// NewKey generates (or loads) a P-256 key inside the TEE (or stub) and
// returns a Key handle satisfying enclave.Key.
//
// tag namespaces the key (same semantics as the macOS enclave tag).
// modelHash is the hex-encoded SHA-256 of the model being served —
// bound into attestation user_data for verifier checks.
func NewKey(tag, modelHash string) (enclave.Key, error) {
	b, err := newStubBackend()
	if err != nil {
		return nil, err
	}

	// 65-byte uncompressed SEC1 public key.
	pub := b.privKey.PublicKey
	pubBytes := elliptic.Marshal(pub.Curve, pub.X, pub.Y)

	return &teeKey{
		tag:       tag,
		teeType:   TEETypeStub,
		pubBytes:  pubBytes,
		modelHash: modelHash,
		backend:   b,
	}, nil
}

// marshalDER encodes an ECDSA signature as DER.
func marshalDER(r, s *big.Int) []byte {
	rb := r.Bytes()
	sb := s.Bytes()
	// Pad with leading zero if high bit set (DER integer encoding).
	if len(rb) > 0 && rb[0]&0x80 != 0 {
		rb = append([]byte{0}, rb...)
	}
	if len(sb) > 0 && sb[0]&0x80 != 0 {
		sb = append([]byte{0}, sb...)
	}
	// SEQUENCE { INTEGER r, INTEGER s }
	inner := make([]byte, 0, 2+len(rb)+2+len(sb))
	inner = append(inner, 0x02, byte(len(rb)))
	inner = append(inner, rb...)
	inner = append(inner, 0x02, byte(len(sb)))
	inner = append(inner, sb...)

	out := make([]byte, 0, 2+len(inner))
	out = append(out, 0x30, byte(len(inner)))
	out = append(out, inner...)
	return out
}
