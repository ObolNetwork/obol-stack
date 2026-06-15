package x402

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	gethmath "github.com/ethereum/go-ethereum/common/math"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	x402types "github.com/x402-foundation/x402/go/types"
)

// SignExactPayment must produce a base64 X-PAYMENT whose EIP-712 signature
// recovers back to the signer's address — i.e. a real, verifiable payment a
// facilitator will accept — entirely host-side.
func TestSignExactPayment_RoundTrip(t *testing.T) {
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	from := ethcrypto.PubkeyToAddress(key.PublicKey).Hex()

	req := BuildV2Requirement(ChainBaseSepolia, "0.01", "0x1111111111111111111111111111111111111111", 0)

	hdr, err := SignExactPayment(key, req)
	if err != nil {
		t.Fatalf("SignExactPayment: %v", err)
	}

	raw, err := base64.StdEncoding.DecodeString(hdr)
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var p x402types.PaymentPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.X402Version != 2 || p.Accepted.PayTo != req.PayTo || p.Accepted.Amount != req.Amount {
		t.Fatalf("payload accepted mismatch: %+v", p.Accepted)
	}
	authMap, _ := p.Payload["authorization"].(map[string]any)
	sigHex, _ := p.Payload["signature"].(string)
	if authMap["from"] != from {
		t.Fatalf("authorization.from = %v, want signer %v", authMap["from"], from)
	}

	chainID, _ := chainIDFromNetwork(req.Network)
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
			Name:              req.Extra["name"].(string),
			Version:           req.Extra["version"].(string),
			ChainId:           gethmath.NewHexOrDecimal256(chainID),
			VerifyingContract: req.Asset,
		},
		Message: apitypes.TypedDataMessage{
			"from": authMap["from"], "to": authMap["to"], "value": authMap["value"],
			"validAfter": authMap["validAfter"], "validBefore": authMap["validBefore"], "nonce": authMap["nonce"],
		},
	}
	hash, _, err := apitypes.TypedDataAndHash(typedData)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	sig := common.FromHex(sigHex)
	if len(sig) != 65 {
		t.Fatalf("signature length = %d, want 65", len(sig))
	}
	sig[64] -= 27
	pub, err := ethcrypto.SigToPub(hash, sig)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	if got := ethcrypto.PubkeyToAddress(*pub).Hex(); got != from {
		t.Fatalf("recovered signer = %s, want %s — invalid EIP-712 signature", got, from)
	}
}

func TestSignExactPayment_RejectsIncompleteChallenge(t *testing.T) {
	key, _ := ethcrypto.GenerateKey()
	// A 402 with no asset EIP-712 name/version in extra cannot be signed.
	bad := x402types.PaymentRequirements{
		Scheme: "exact", Network: "eip155:84532",
		Asset: "0xabc", PayTo: "0xdef", Amount: "1000",
	}
	if _, err := SignExactPayment(key, bad); err == nil {
		t.Fatal("expected error for a 402 missing EIP-712 name/version")
	}
}
