package dataset

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"strings"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// EthSigner signs version digests with a secp256k1 key — the same key kind
// used for the owner's on-chain (ERC-8004) registration. No new key custody:
// the caller supplies the already-loaded key.
type EthSigner struct {
	priv *ecdsa.PrivateKey
	addr string
}

// NewEthSigner wraps a secp256k1 private key as a Signer.
func NewEthSigner(priv *ecdsa.PrivateKey) *EthSigner {
	return &EthSigner{
		priv: priv,
		addr: strings.ToLower(ethcrypto.PubkeyToAddress(priv.PublicKey).Hex()),
	}
}

// SignerID returns the lowercased 0x EVM address of the signing key.
func (s *EthSigner) SignerID() string { return s.addr }

// SignDigest returns a 65-byte [R||S||V] secp256k1 signature, hex-encoded.
func (s *EthSigner) SignDigest(digest [32]byte) (string, error) {
	sig, err := ethcrypto.Sign(digest[:], s.priv)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sig), nil
}

// EthVerifier recovers the signer's EVM address from a secp256k1 signature.
// It is the zero value of an empty struct — stateless and reusable.
type EthVerifier struct{}

// RecoverSigner recovers the lowercased 0x EVM address that produced sigHex
// over digest.
func (EthVerifier) RecoverSigner(digest [32]byte, sigHex string) (string, error) {
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return "", fmt.Errorf("decode signature: %w", err)
	}
	if len(sig) != 65 {
		return "", fmt.Errorf("signature length %d, want 65", len(sig))
	}
	pub, err := ethcrypto.SigToPub(digest[:], sig)
	if err != nil {
		return "", fmt.Errorf("recover pubkey: %w", err)
	}
	return strings.ToLower(ethcrypto.PubkeyToAddress(*pub).Hex()), nil
}
