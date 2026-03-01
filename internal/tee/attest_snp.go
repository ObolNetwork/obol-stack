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
	sevclient "github.com/google/go-sev-guest/client"
)

// snpBackend generates a P-256 key in-process (inside the SEV-SNP guest VM)
// and obtains attestation reports via /dev/sev-guest. The private key is
// protected by SEV-SNP memory encryption — even the host hypervisor cannot
// read the guest's RAM.
//
// The 64-byte REPORT_DATA field at offset 0x050 in the 1184-byte report
// carries SHA256(pubkey || modelHash) in its first 32 bytes.
//
// Dependencies:
//   - Must be running inside an SEV-SNP guest VM
//   - /dev/sev-guest device must exist
//   - AMD PSP firmware must support SNP_GET_REPORT / SNP_GET_EXT_REPORT
//   - VCEK certificate chain fetched from AMD KDS by the verifier
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
	// Get a QuoteProvider — auto-selects configfs-tsm (Linux >= 6.7) or
	// legacy /dev/sev-guest ioctl.
	qp, err := sevclient.GetQuoteProvider()
	if err != nil {
		return nil, fmt.Errorf("tee/snp: get quote provider: %w", err)
	}

	// Build the 64-byte REPORT_DATA. Our user_data (32-byte SHA-256 binding)
	// occupies the first 32 bytes; the rest is zero-padded.
	var reportData [64]byte
	copy(reportData[:], userData)

	// GetRawQuote returns: 1184-byte report + certificate table (VCEK, ASK, ARK).
	// The certificate table allows the verifier to validate without fetching
	// certs from AMD KDS.
	rawQuote, err := qp.GetRawQuote(reportData)
	if err != nil {
		return nil, fmt.Errorf("tee/snp: get attestation report: %w", err)
	}

	return rawQuote, nil
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
