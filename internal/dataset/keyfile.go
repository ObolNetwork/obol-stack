package dataset

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// LoadOrCreateKey loads a hex-encoded secp256k1 private key from path, or
// generates one and persists it (0600) on first use. This is the owner's
// dataset-signing key: it signs the version log and its address is the owner
// identity buyers pin via Verify. Operators who already control an on-chain
// registration key can point path at that key's hex to unify the identity.
func LoadOrCreateKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		key, perr := ethcrypto.HexToECDSA(strings.TrimSpace(string(data)))
		if perr != nil {
			return nil, fmt.Errorf("dataset: parse signing key %s: %w", path, perr)
		}
		return key, nil
	case !os.IsNotExist(err):
		return nil, fmt.Errorf("dataset: read signing key %s: %w", path, err)
	}

	key, err := ethcrypto.GenerateKey()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(ethcrypto.FromECDSA(key))), 0o600); err != nil {
		return nil, fmt.Errorf("dataset: persist signing key: %w", err)
	}
	return key, nil
}
