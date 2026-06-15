package x402

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	gethmath "github.com/ethereum/go-ethereum/common/math"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	x402types "github.com/x402-foundation/x402/go/types"
)

// pastValidityBuffer backdates an EIP-3009 authorization's validAfter so it is
// not rejected as "not yet valid" when the verifying chain's block.timestamp
// lags wall-clock (the classic anvil-fork skew). Matches obol's buy.py.
const pastValidityBuffer = 300 * time.Second

// SignExactPayment signs an x402 "exact" (EIP-3009 TransferWithAuthorization)
// payment for req with key and returns the base64 X-PAYMENT header value. It is
// fully host-side — no cluster, sidecar, or remote signer — so a standalone
// buyer can pay a standalone seller directly, peer-to-peer.
//
// Everything needed is taken from the seller's 402 challenge: the EIP-712 token
// domain (name/version) from req.Extra, the verifying contract from req.Asset,
// the recipient from req.PayTo, the amount from req.Amount, and the chain id
// from req.Network (CAIP-2 "eip155:<id>").
func SignExactPayment(key *ecdsa.PrivateKey, req x402types.PaymentRequirements) (string, error) {
	if key == nil {
		return "", fmt.Errorf("x402: nil signing key")
	}
	chainID, err := chainIDFromNetwork(req.Network)
	if err != nil {
		return "", err
	}
	name, _ := req.Extra["name"].(string)
	version, _ := req.Extra["version"].(string)
	if name == "" || version == "" {
		return "", fmt.Errorf("x402: 402 challenge missing asset EIP-712 name/version in accepts[].extra")
	}
	if req.Asset == "" || req.PayTo == "" || req.Amount == "" {
		return "", fmt.Errorf("x402: 402 challenge missing asset/payTo/amount")
	}

	from := ethcrypto.PubkeyToAddress(key.PublicKey).Hex()

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	nonceHex := fmt.Sprintf("0x%x", nonce)

	now := time.Now()
	validAfter := now.Add(-pastValidityBuffer).Unix()
	if validAfter < 0 {
		validAfter = 0
	}
	window := time.Duration(req.MaxTimeoutSeconds) * time.Second
	if window <= 0 {
		window = time.Hour
	}
	validBefore := now.Add(window).Unix()

	auth := map[string]any{
		"from":        from,
		"to":          req.PayTo,
		"value":       req.Amount,
		"validAfter":  strconv.FormatInt(validAfter, 10),
		"validBefore": strconv.FormatInt(validBefore, 10),
		"nonce":       nonceHex,
	}

	typedData := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			"TransferWithAuthorization": {
				{Name: "from", Type: "address"},
				{Name: "to", Type: "address"},
				{Name: "value", Type: "uint256"},
				{Name: "validAfter", Type: "uint256"},
				{Name: "validBefore", Type: "uint256"},
				{Name: "nonce", Type: "bytes32"},
			},
		},
		PrimaryType: "TransferWithAuthorization",
		Domain: apitypes.TypedDataDomain{
			Name:              name,
			Version:           version,
			ChainId:           gethmath.NewHexOrDecimal256(chainID),
			VerifyingContract: req.Asset,
		},
		Message: apitypes.TypedDataMessage{
			"from":        from,
			"to":          req.PayTo,
			"value":       req.Amount,
			"validAfter":  auth["validAfter"],
			"validBefore": auth["validBefore"],
			"nonce":       nonceHex,
		},
	}

	hash, _, err := apitypes.TypedDataAndHash(typedData)
	if err != nil {
		return "", fmt.Errorf("x402: hash typed data: %w", err)
	}
	sig, err := ethcrypto.Sign(hash, key)
	if err != nil {
		return "", fmt.Errorf("x402: sign: %w", err)
	}
	sig[64] += 27 // Ethereum v convention (27/28)

	payload := x402types.PaymentPayload{
		X402Version: 2,
		Accepted:    req,
		Payload: map[string]any{
			"signature":     fmt.Sprintf("0x%x", sig),
			"authorization": auth,
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// chainIDFromNetwork parses a CAIP-2 network ("eip155:84532") or a bare decimal
// into the numeric chain id.
func chainIDFromNetwork(network string) (int64, error) {
	s := strings.TrimSpace(network)
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("x402: cannot derive chain id from network %q", network)
	}
	return n, nil
}
