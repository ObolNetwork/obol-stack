//go:build linux && cgo && nitro

package tee

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"fmt"
	"math/big"

	"github.com/ObolNetwork/obol-stack/internal/enclave"
	"github.com/hf/nsm"
	"github.com/hf/nsm/request"
)

// nitroBackend generates a P-256 key in-process inside the AWS Nitro
// Enclave and obtains signed attestation documents via /dev/nsm.
//
// The private key is protected by the Nitro hypervisor's isolation —
// even the parent EC2 instance cannot access enclave memory.
//
// The attestation document is a COSE_Sign1 structure (CBOR tag 18)
// signed with ECDSA-P384-SHA384 by the Nitro Security Module. It
// contains:
//   - user_data: our SHA256(pubkey || modelHash) binding (max 512 bytes)
//   - public_key: DER-encoded enclave ECDH public key (max 1024 bytes)
//   - nonce: optional anti-replay challenge (max 512 bytes)
//   - PCRs: platform configuration registers (PCR0 = enclave image hash)
//   - certificate + cabundle: cert chain to AWS Nitro Root CA G1
//
// Dependencies:
//   - Must be running inside an AWS Nitro Enclave
//   - /dev/nsm device must exist
//   - github.com/hf/nsm for enclave-side NSM communication
//   - github.com/hf/nitrite for client-side COSE/CBOR verification
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
	// Open a session to /dev/nsm.
	sess, err := nsm.OpenDefaultSession()
	if err != nil {
		return nil, fmt.Errorf("tee/nitro: open NSM session: %w", err)
	}
	defer sess.Close()

	// DER-encode the ECDH public key for the attestation document's
	// public_key field. Verifiers can use this to establish an encrypted
	// channel back to the enclave.
	pubKeyDER, err := x509.MarshalPKIXPublicKey(&b.privKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("tee/nitro: marshal public key: %w", err)
	}

	// Request attestation document. The NSM signs the document with
	// ECDSA-P384-SHA384 using the enclave's platform key. The result
	// is a CBOR-encoded COSE_Sign1 structure.
	res, err := sess.Send(&request.Attestation{
		UserData:  userData,   // our SHA256(pubkey || modelHash) binding
		PublicKey: pubKeyDER,  // enclave ECDH public key for key agreement
		Nonce:     nil,        // caller can add nonce via a wrapper if needed
	})
	if err != nil {
		return nil, fmt.Errorf("tee/nitro: NSM send: %w", err)
	}

	if res.Error != "" {
		return nil, fmt.Errorf("tee/nitro: NSM error: %s", res.Error)
	}

	if res.Attestation == nil || res.Attestation.Document == nil {
		return nil, errors.New("tee/nitro: NSM returned no attestation document")
	}

	return res.Attestation.Document, nil
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
