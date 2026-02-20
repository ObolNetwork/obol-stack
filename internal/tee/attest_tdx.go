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
	tdxclient "github.com/google/go-tdx-guest/client"
)

// tdxBackend generates a P-256 key in-process inside the TDX Trust Domain
// (TD) and obtains DCAP v4 quotes via /dev/tdx-guest or configfs-tsm.
//
// The private key lives in TD-protected memory — the host VMM cannot read it
// even with root access, thanks to TDX memory encryption and integrity.
//
// The 64-byte reportData in the TD Quote Body carries our user_data binding
// (SHA256(pubkey || modelHash)) in its first 32 bytes.
//
// Dependencies:
//   - Must be running inside a TDX Trust Domain (TD)
//   - /dev/tdx-guest or /sys/kernel/config/tsm/report/ (Linux >= 6.7)
//   - Quote Generation Service (QGS) reachable for DCAP quote signing
//   - PCK certificate chain fetched from Intel PCS by the verifier
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
	// Get a QuoteProvider — prefers configfs-tsm (Linux >= 6.7), falls back
	// to legacy /dev/tdx-guest ioctl.
	qp, err := tdxclient.GetQuoteProvider()
	if err != nil {
		return nil, fmt.Errorf("tee/tdx: get quote provider: %w", err)
	}

	// Build the 64-byte reportData. Our user_data (32-byte SHA-256 binding)
	// occupies the first 32 bytes; the rest is zero-padded.
	var reportData [64]byte
	copy(reportData[:], userData)

	// GetRawQuote triggers:
	//   1. TDCALL[TDG.MR.REPORT] → TD Report with reportData
	//   2. QGS signs TD Report → DCAP v4 quote (header + TDQuoteBody + sig + certs)
	rawQuote, err := tdxclient.GetRawQuote(qp, reportData)
	if err != nil {
		return nil, fmt.Errorf("tee/tdx: get DCAP quote: %w", err)
	}

	return rawQuote, nil
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
