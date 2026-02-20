//go:build linux && cgo && snp

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

// snpBackend uses /dev/sev-guest ioctl SNP_GET_REPORT to produce an AMD
// SEV-SNP attestation report. The 64-byte user_data field carries
// SHA256(pubkey||modelHash).
type snpBackend struct {
	privKey  *ecdsa.PrivateKey
	ecdhPriv *ecdh.PrivateKey
}

func newSNPBackend() (*snpBackend, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("tee/snp: key generation failed: %w", err)
	}
	ecdhPriv, err := priv.ECDH()
	if err != nil {
		return nil, fmt.Errorf("tee/snp: ECDH key conversion failed: %w", err)
	}
	return &snpBackend{privKey: priv, ecdhPriv: ecdhPriv}, nil
}

func (b *snpBackend) sign(digest []byte) ([]byte, error) {
	r, s, err := ecdsa.Sign(rand.Reader, b.privKey, digest)
	if err != nil {
		return nil, fmt.Errorf("tee/snp: sign failed: %w", err)
	}
	return marshalDER(r, s), nil
}

func (b *snpBackend) ecdh(peerPubKeyBytes []byte) ([]byte, error) {
	curve := ecdh.P256()
	peerKey, err := curve.NewPublicKey(peerPubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("tee/snp: invalid peer public key: %w", err)
	}
	shared, err := b.ecdhPriv.ECDH(peerKey)
	if err != nil {
		return nil, fmt.Errorf("tee/snp: ECDH failed: %w", err)
	}
	return shared, nil
}

func (b *snpBackend) attest(userData []byte) ([]byte, error) {
	// TODO(phase-2b): Implement SNP attestation via /dev/sev-guest.
	//
	// Steps:
	// 1. Open /dev/sev-guest
	// 2. ioctl(fd, SNP_GET_REPORT, &req) where req.UserData = userData[:64]
	// 3. Return raw SNP AttestationReport (1184 bytes)
	//
	// Dependencies:
	// - /dev/sev-guest must exist (running inside SEV-SNP VM)
	// - github.com/virtee/sev-snp-guest Go bindings
	return nil, fmt.Errorf("tee/snp: attestation not yet implemented")
}

func (b *snpBackend) delete() error {
	return nil
}

// NewKey for SNP builds generates a key inside the SEV-SNP VM.
func NewKey(tag, modelHash string) (enclave.Key, error) {
	b, err := newSNPBackend()
	if err != nil {
		return nil, err
	}

	pub := b.privKey.PublicKey
	pubBytes := elliptic.Marshal(pub.Curve, pub.X, pub.Y)

	return &teeKey{
		tag:       tag,
		teeType:   TEETypeSNP,
		pubBytes:  pubBytes,
		modelHash: modelHash,
		backend:   b,
	}, nil
}

// marshalDER for SNP builds.
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
