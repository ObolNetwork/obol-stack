package testutil

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

const (
	// USDCBaseSepolia is the USDC contract address on Base Sepolia.
	USDCBaseSepolia = "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
)

// SignRealPaymentHeader constructs a real EIP-712 TransferWithAuthorization
// (ERC-3009) payment header and returns it as a base64-encoded string
// compatible with the x402 V1 wire format.
//
// The signerKey is the buyer's private key (signs the authorization).
// payTo is the seller's address (from ServiceOffer payment.payTo).
// amount is the USDC amount in micro-units (e.g. "1000000" = 1 USDC).
// chainID is the EVM chain ID (84532 for Base Sepolia).
//
// Critical x402-rs wire format requirements:
//   - validAfter/validBefore must be STRINGS (x402-rs UnixTimestamp deserializes from string)
//   - value must be a STRING (x402-rs U256 uses decimal_u256 serde)
//   - nonce must be a hex-encoded 32-byte value with 0x prefix
func SignRealPaymentHeader(t *testing.T, signerKeyHex string, payTo string, amount string, chainID int64) string {
	t.Helper()

	// Parse private key.
	key, err := crypto.HexToECDSA(stripHexPrefix(signerKeyHex))
	if err != nil {
		t.Fatalf("parse signer key: %v", err)
	}

	fromAddr := crypto.PubkeyToAddress(key.PublicKey)

	// Generate random nonce (32 bytes).
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("generate nonce: %v", err)
	}

	nonceHex := fmt.Sprintf("0x%x", nonce)

	// Build EIP-712 typed data for TransferWithAuthorization (ERC-3009).
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
			Name:              "USDC",
			Version:           "2",
			ChainId:           math.NewHexOrDecimal256(chainID),
			VerifyingContract: USDCBaseSepolia,
		},
		Message: apitypes.TypedDataMessage{
			"from":        fromAddr.Hex(),
			"to":          payTo,
			"value":       amount,
			"validAfter":  "0",
			"validBefore": "4294967295",
			"nonce":       nonceHex,
		},
	}

	// Compute EIP-712 hash and sign.
	hash, _, err := apitypes.TypedDataAndHash(typedData)
	if err != nil {
		t.Fatalf("TypedDataAndHash: %v", err)
	}

	sig, err := crypto.Sign(hash, key)
	if err != nil {
		t.Fatalf("sign EIP-712 hash: %v", err)
	}

	// Ethereum convention: v = sig[64] + 27.
	sig[64] += 27
	sigHex := fmt.Sprintf("0x%x", sig)

	// Build the x402 V1 payment envelope.
	// All numeric values that x402-rs expects as strings must be strings here.
	envelope := map[string]any{
		"x402Version": 1,
		"scheme":      "exact",
		"network":     chainName(chainID),
		"payload": map[string]any{
			"signature": sigHex,
			"authorization": map[string]any{
				"from":        fromAddr.Hex(),
				"to":          payTo,
				"value":       amount,       // string — x402-rs decimal_u256
				"validAfter":  "0",          // string — x402-rs UnixTimestamp
				"validBefore": "4294967295", // string — x402-rs UnixTimestamp
				"nonce":       nonceHex,
			},
		},
		"resource": map[string]any{
			"payTo":             payTo,
			"maxAmountRequired": amount,
			"asset":             USDCBaseSepolia,
			"network":           chainName(chainID),
		},
	}

	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal payment envelope: %v", err)
	}

	encoded := base64.StdEncoding.EncodeToString(data)

	t.Logf("signed real payment: from=%s, to=%s, amount=%s, chain=%d", fromAddr.Hex(), payTo, amount, chainID)

	return encoded
}

// ParseAnvilKey converts a hex-encoded private key string to an ecdsa.PrivateKey.
func ParseAnvilKey(t *testing.T, hexKey string) *ecdsa.PrivateKey {
	t.Helper()

	key, err := crypto.HexToECDSA(stripHexPrefix(hexKey))
	if err != nil {
		t.Fatalf("parse anvil key: %v", err)
	}

	return key
}

// AnvilKeyAddress returns the Ethereum address for an Anvil private key.
func AnvilKeyAddress(t *testing.T, hexKey string) common.Address {
	t.Helper()
	key := ParseAnvilKey(t, hexKey)

	return crypto.PubkeyToAddress(key.PublicKey)
}

// USDCMicroUnits converts a USDC amount (e.g. 1.0) to micro-units (1000000).
func USDCMicroUnits(usdc float64) *big.Int {
	// USDC has 6 decimals.
	micro := new(big.Float).Mul(big.NewFloat(usdc), big.NewFloat(1e6))
	result, _ := micro.Int(nil)

	return result
}

func stripHexPrefix(s string) string {
	if len(s) > 2 && (s[:2] == "0x" || s[:2] == "0X") {
		return s[2:]
	}

	return s
}

// SignPaymentHeaderDirect is a non-test version of SignRealPaymentHeader.
// It panics on error instead of calling t.Fatal.
func SignPaymentHeaderDirect(signerKeyHex, payTo, amount string, chainID int64) string {
	key, err := crypto.HexToECDSA(stripHexPrefix(signerKeyHex))
	if err != nil {
		panic(fmt.Sprintf("parse signer key: %v", err))
	}

	fromAddr := crypto.PubkeyToAddress(key.PublicKey)

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		panic(fmt.Sprintf("generate nonce: %v", err))
	}

	nonceHex := fmt.Sprintf("0x%x", nonce)

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
			Name:              "USDC",
			Version:           "2",
			ChainId:           math.NewHexOrDecimal256(chainID),
			VerifyingContract: USDCBaseSepolia,
		},
		Message: apitypes.TypedDataMessage{
			"from":        fromAddr.Hex(),
			"to":          payTo,
			"value":       amount,
			"validAfter":  "0",
			"validBefore": "4294967295",
			"nonce":       nonceHex,
		},
	}

	hash, _, err := apitypes.TypedDataAndHash(typedData)
	if err != nil {
		panic(fmt.Sprintf("TypedDataAndHash: %v", err))
	}

	sig, err := crypto.Sign(hash, key)
	if err != nil {
		panic(fmt.Sprintf("sign: %v", err))
	}

	sig[64] += 27

	envelope := map[string]any{
		"x402Version": 1,
		"scheme":      "exact",
		"network":     chainName(chainID),
		"payload": map[string]any{
			"signature": fmt.Sprintf("0x%x", sig),
			"authorization": map[string]any{
				"from":        fromAddr.Hex(),
				"to":          payTo,
				"value":       amount,
				"validAfter":  "0",
				"validBefore": "4294967295",
				"nonce":       nonceHex,
			},
		},
		"resource": map[string]any{
			"payTo":             payTo,
			"maxAmountRequired": amount,
			"asset":             USDCBaseSepolia,
			"network":           chainName(chainID),
		},
	}

	data, err := json.Marshal(envelope)
	if err != nil {
		panic(fmt.Sprintf("marshal: %v", err))
	}

	return base64.StdEncoding.EncodeToString(data)
}

func chainName(chainID int64) string {
	switch chainID {
	case 84532:
		return "base-sepolia"
	case 8453:
		return "base"
	case 1:
		return "ethereum"
	default:
		return fmt.Sprintf("eip155:%d", chainID)
	}
}
