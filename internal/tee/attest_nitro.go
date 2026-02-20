//go:build linux && cgo && nitro

package tee

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"math/big"

	"github.com/ObolNetwork/obol-stack/internal/enclave"
)

// nitroBackend uses the AWS Nitro Security Module (NSM) at /dev/nsm to
// produce an attestation document. UserData is SHA256(pubkey||modelHash)
// encoded as CBOR inside the NSM request.
type nitroBackend struct {
	privKey  *ecdsa.PrivateKey
	ecdhPriv *ecdh.PrivateKey
}

func newNitroBackend() (*nitroBackend, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("tee/nitro: key generation failed: %w", err)
	}
	ecdhPriv, err := priv.ECDH()
	if err != nil {
		return nil, fmt.Errorf("tee/nitro: ECDH key conversion failed: %w", err)
	}
	return &nitroBackend{privKey: priv, ecdhPriv: ecdhPriv}, nil
}

func (b *nitroBackend) sign(digest []byte) ([]byte, error) {
	r, s, err := ecdsa.Sign(rand.Reader, b.privKey, digest)
	if err != nil {
		return nil, fmt.Errorf("tee/nitro: sign failed: %w", err)
	}
	return marshalDER(r, s), nil
}

func (b *nitroBackend) ecdh(peerPubKeyBytes []byte) ([]byte, error) {
	curve := ecdh.P256()
	peerKey, err := curve.NewPublicKey(peerPubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("tee/nitro: invalid peer public key: %w", err)
	}
	shared, err := b.ecdhPriv.ECDH(peerKey)
	if err != nil {
		return nil, fmt.Errorf("tee/nitro: ECDH failed: %w", err)
	}
	return shared, nil
}

func (b *nitroBackend) attest(userData []byte) ([]byte, error) {
	// TODO(phase-2b): Implement Nitro attestation via NSM device.
	//
	// Steps:
	// 1. Open /dev/nsm
	// 2. nsm.GetAttestationDocument(nonce=nil, userData=userData, publicKey=pubKeyDER)
	// 3. Return signed CBOR attestation document
	//
	// Dependencies:
	// - Running inside AWS Nitro Enclave
	// - github.com/hf/nsm Go bindings
	return nil, fmt.Errorf("tee/nitro: attestation not yet implemented")
}

func (b *nitroBackend) delete() error {
	return nil
}

// NewKey for Nitro builds generates a key inside the Nitro enclave.
func NewKey(tag, modelHash string) (enclave.Key, error) {
	b, err := newNitroBackend()
	if err != nil {
		return nil, err
	}

	pub := b.privKey.PublicKey
	pubBytes := elliptic.Marshal(pub.Curve, pub.X, pub.Y)

	return &teeKey{
		tag:       tag,
		teeType:   TEETypeNitro,
		pubBytes:  pubBytes,
		modelHash: modelHash,
		backend:   b,
	}, nil
}

// marshalDER for Nitro builds.
func marshalDER(r, s *big.Int) []byte {
	rb := r.Bytes()
	sb := s.Bytes()
	if len(rb) > 0 && rb[0]&0x80 != 0 {
		rb = append([]byte{0}, rb...)
	}
	if len(sb) > 0 && sb[0]&0x80 != 0 {
		sb = append([]byte{0}, sb...)
	}
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
