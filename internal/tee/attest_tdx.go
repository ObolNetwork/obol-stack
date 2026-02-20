//go:build linux && cgo && tdx

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

// tdxBackend uses the Intel TDX TDCALL instruction to produce a TD Report,
// then requests a full quote from the Quote Generation Service (QGS) over
// the host-side /dev/tdx_guest device.
//
// The private key is generated in-process inside the TVM guest memory.
// Even the host hypervisor cannot read it — TDX memory isolation guarantees
// this.
type tdxBackend struct {
	privKey  *ecdsa.PrivateKey
	ecdhPriv *ecdh.PrivateKey
}

func newTDXBackend() (*tdxBackend, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("tee/tdx: key generation failed: %w", err)
	}
	ecdhPriv, err := priv.ECDH()
	if err != nil {
		return nil, fmt.Errorf("tee/tdx: ECDH key conversion failed: %w", err)
	}
	return &tdxBackend{privKey: priv, ecdhPriv: ecdhPriv}, nil
}

func (b *tdxBackend) sign(digest []byte) ([]byte, error) {
	r, s, err := ecdsa.Sign(rand.Reader, b.privKey, digest)
	if err != nil {
		return nil, fmt.Errorf("tee/tdx: sign failed: %w", err)
	}
	return marshalDER(r, s), nil
}

func (b *tdxBackend) ecdh(peerPubKeyBytes []byte) ([]byte, error) {
	curve := ecdh.P256()
	peerKey, err := curve.NewPublicKey(peerPubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("tee/tdx: invalid peer public key: %w", err)
	}
	shared, err := b.ecdhPriv.ECDH(peerKey)
	if err != nil {
		return nil, fmt.Errorf("tee/tdx: ECDH failed: %w", err)
	}
	return shared, nil
}

func (b *tdxBackend) attest(userData []byte) ([]byte, error) {
	// TODO(phase-2b): Implement TDX attestation via TDCALL + QGS.
	//
	// Steps:
	// 1. Build TDREPORT via TDCALL leaf 4 (TDG.MR.REPORT)
	//    - reportData[0:32] = userData (SHA256 of pubkey||modelHash)
	// 2. Send TDREPORT to QGS via /dev/tdx_guest ioctl (TDX_CMD_GET_QUOTE)
	// 3. Return raw DCAP quote bytes
	//
	// Dependencies:
	// - /dev/tdx_guest device must exist (running inside TDX TVM)
	// - QGS must be reachable at /run/tdx-qgs/qgs.socket or localhost:4050
	return nil, fmt.Errorf("tee/tdx: attestation not yet implemented")
}

func (b *tdxBackend) delete() error {
	return nil
}

// NewKey for TDX builds generates a key inside the TVM.
func NewKey(tag, modelHash string) (enclave.Key, error) {
	b, err := newTDXBackend()
	if err != nil {
		return nil, err
	}

	pub := b.privKey.PublicKey
	pubBytes := elliptic.Marshal(pub.Curve, pub.X, pub.Y)

	return &teeKey{
		tag:       tag,
		teeType:   TEETypeTDX,
		pubBytes:  pubBytes,
		modelHash: modelHash,
		backend:   b,
	}, nil
}

// marshalDER for TDX builds (same logic as stub).
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
