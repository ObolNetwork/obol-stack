package buyer

import (
	"encoding/json"
	"sync"
	"testing"

	x402types "github.com/coinbase/x402/go/types"
)

func TestPreSignedSigner_CanSign(t *testing.T) {
	signer := NewPreSignedSigner(
		"base-sepolia",
		"0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		"0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		"1000",
		"USDC",
		6,
		[]*PreSignedAuth{makeAuth("0x1")},
		0,
		nil,
	)

	tests := []struct {
		name string
		req  *x402types.PaymentRequirements
		want bool
	}{
		{
			name: "matching requirement",
			req: &x402types.PaymentRequirements{
				Network: "eip155:84532",
				PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Amount:  "1000",
			},
			want: true,
		},
		{
			name: "case-insensitive match",
			req: &x402types.PaymentRequirements{
				Network: "eip155:84532",
				PayTo:   "0x70997970c51812dc3a010c7d01b50e0d17dc79c8",
				Asset:   "0x036cbd53842c5426634e7929541ec2318f3dcf7e",
				Amount:  "1000",
			},
			want: true,
		},
		{
			name: "wrong network",
			req: &x402types.PaymentRequirements{
				Network: "eip155:8453",
				PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
			},
			want: false,
		},
		{
			name: "wrong payTo",
			req: &x402types.PaymentRequirements{
				Network: "eip155:84532",
				PayTo:   "0xdeadbeef",
				Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
			},
			want: false,
		},
		{
			name: "wrong asset",
			req: &x402types.PaymentRequirements{
				Network: "eip155:84532",
				PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:   "0xdeadbeef",
			},
			want: false,
		},
		{
			name: "wrong amount",
			req: &x402types.PaymentRequirements{
				Network: "eip155:84532",
				PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Amount:  "999",
			},
			want: false,
		},
		{
			name: "nil requirement",
			req:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := signer.CanSign(tt.req)
			if got != tt.want {
				t.Errorf("CanSign() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPreSignedSigner_Sign(t *testing.T) {
	auths := []*PreSignedAuth{
		makeAuth("0xaaa"),
		makeAuth("0xbbb"),
	}
	signer := NewPreSignedSigner(
		"base-sepolia",
		"0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		"0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		"1000",
		"USDC",
		6,
		auths,
		0,
		nil,
	)

	req := &x402types.PaymentRequirements{
		Network: "eip155:84532",
		PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		Amount:  "1000",
	}

	// First sign — should pop "0xaaa".
	p1, err := signer.Sign(req)
	if err != nil {
		t.Fatalf("first Sign: %v", err)
	}

	payload1 := p1.Payload
	if sig, _ := payload1["signature"].(string); sig != "0xaaa" {
		t.Errorf("first signature = %q, want %q", sig, "0xaaa")
	}

	if p1.X402Version != 2 || p1.Accepted.Scheme != "exact" || p1.Accepted.Network != "eip155:84532" {
		t.Errorf("unexpected payload fields: version=%d scheme=%s network=%s",
			p1.X402Version, p1.Accepted.Scheme, p1.Accepted.Network)
	}

	if signer.Remaining() != 1 {
		t.Errorf("remaining = %d, want 1", signer.Remaining())
	}

	if signer.Spent() != 1 {
		t.Errorf("spent = %d, want 1", signer.Spent())
	}

	// Second sign — should pop "0xbbb".
	p2, err := signer.Sign(req)
	if err != nil {
		t.Fatalf("second Sign: %v", err)
	}

	payload2 := p2.Payload
	if sig, _ := payload2["signature"].(string); sig != "0xbbb" {
		t.Errorf("second signature = %q, want %q", sig, "0xbbb")
	}

	// Third sign — pool exhausted.
	_, err = signer.Sign(req)
	if err == nil {
		t.Fatal("expected error on exhausted pool")
	}

	// CanSign should return false now.
	if signer.CanSign(req) {
		t.Error("CanSign should return false when pool exhausted")
	}
}

func TestPreSignedSigner_ConcurrentSign(t *testing.T) {
	const N = 100

	auths := make([]*PreSignedAuth, N)
	for i := range auths {
		auths[i] = makeAuth("0xsig")
	}

	signer := NewPreSignedSigner(
		"base-sepolia",
		"0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		"0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		"1000",
		"USDC",
		6,
		auths,
		0,
		nil,
	)

	req := &x402types.PaymentRequirements{
		Network: "eip155:84532",
		PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		Amount:  "1000",
	}

	var wg sync.WaitGroup

	successes := make(chan struct{}, N)
	failures := make(chan struct{}, N)

	for range N {
		wg.Go(func() {
			_, err := signer.Sign(req)
			if err != nil {
				failures <- struct{}{}
			} else {
				successes <- struct{}{}
			}
		})
	}

	wg.Wait()
	close(successes)
	close(failures)

	s := len(successes)

	f := len(failures)
	if s != N {
		t.Errorf("successes = %d, want %d (failures=%d)", s, N, f)
	}

	if signer.Remaining() != 0 {
		t.Errorf("remaining = %d, want 0", signer.Remaining())
	}

	if signer.Spent() != N {
		t.Errorf("spent = %d, want %d", signer.Spent(), N)
	}
}

func TestPreSignedSigner_HoldConfirmRelease(t *testing.T) {
	var consumed int
	signer := NewPreSignedSigner(
		"base-sepolia",
		"0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		"0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		"1000",
		"USDC",
		6,
		[]*PreSignedAuth{makeAuth("0xhold")},
		0,
		func(*PreSignedAuth) error {
			consumed++
			return nil
		},
	)

	req := &x402types.PaymentRequirements{
		Network: "eip155:84532",
		PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		Amount:  "1000",
	}

	p, held, err := signer.HoldSign(req)
	if err != nil {
		t.Fatalf("HoldSign: %v", err)
	}
	if p == nil || held == nil {
		t.Fatal("expected payload and held auth")
	}
	if consumed != 0 {
		t.Fatalf("consume before confirm: %d", consumed)
	}
	if signer.Remaining() != 0 {
		t.Fatalf("remaining = %d, want 0", signer.Remaining())
	}

	signer.ReleaseSpend(held)
	if consumed != 0 {
		t.Fatalf("release should not consume: %d", consumed)
	}
	if signer.Remaining() != 1 {
		t.Fatalf("remaining after release = %d, want 1", signer.Remaining())
	}

	p2, held2, err := signer.HoldSign(req)
	if err != nil {
		t.Fatalf("second HoldSign: %v", err)
	}
	if err := signer.ConfirmSpend(held2); err != nil {
		t.Fatalf("ConfirmSpend: %v", err)
	}
	if consumed != 1 {
		t.Fatalf("consumed = %d, want 1", consumed)
	}
	_ = p2
}

func TestPreSignedSigner_Interface(t *testing.T) {
	signer := NewPreSignedSigner("base-sepolia", "0xpayto", "0xasset", "1000", "USDC", 6, nil, 0, nil)

	// Verify interface compliance.
	var _ Signer = signer

	if signer.Network() != "eip155:84532" {
		t.Errorf("Network() = %q", signer.Network())
	}

	if signer.Scheme() != "exact" {
		t.Errorf("Scheme() = %q", signer.Scheme())
	}

	if signer.GetPriority() != 0 {
		t.Errorf("GetPriority() = %d", signer.GetPriority())
	}

	if signer.GetMaxAmount() != nil {
		t.Errorf("GetMaxAmount() = %v, want nil", signer.GetMaxAmount())
	}

	tokens := signer.GetTokens()
	if len(tokens) != 1 || tokens[0].Address != "0xasset" {
		t.Errorf("GetTokens() = %+v", tokens)
	}
}

func TestPreSignedSigner_SignGenericPayment(t *testing.T) {
	var payment x402types.PaymentPayload
	if err := json.Unmarshal([]byte(`{
		"x402Version": 2,
		"accepted": {
			"scheme": "exact",
			"network": "eip155:84532",
			"amount": "1000",
			"asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
			"payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
			"maxTimeoutSeconds": 60,
			"extra": {"assetTransferMethod": "permit2", "name": "OBOL", "version": "1"}
		},
		"payload": {
			"signature": "0xabc",
			"permit2Authorization": {
				"from": "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266",
				"spender": "0x402085c248EeA27D92E8b30b2C58ed07f9E20001",
				"nonce": "42",
				"deadline": "99",
				"permitted": {
					"token": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
					"amount": "1000"
				},
				"witness": {
					"to": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
					"validAfter": "0"
				}
			}
		},
		"extensions": {
			"eip2612GasSponsoring": {"info": {"version": "1"}}
		}
	}`), &payment); err != nil {
		t.Fatalf("unmarshal payment: %v", err)
	}

	signer := NewPreSignedSigner(
		"base-sepolia",
		"0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		"0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		"1000",
		"OBOL",
		18,
		[]*PreSignedAuth{{ID: "42", Payment: &payment}},
		0,
		nil,
	)

	req := &x402types.PaymentRequirements{
		Network: "eip155:84532",
		PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		Amount:  "1000",
	}
	got, err := signer.Sign(req)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if got.Payload["permit2Authorization"] == nil {
		t.Fatalf("expected permit2Authorization payload, got %+v", got.Payload)
	}
	if got.Extensions["eip2612GasSponsoring"] == nil {
		t.Fatalf("expected eip2612 extension, got %+v", got.Extensions)
	}
}

func makeAuth(sig string) *PreSignedAuth {
	return &PreSignedAuth{
		Signature:   sig,
		From:        "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266",
		To:          "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		Value:       "1000",
		ValidAfter:  "0",
		ValidBefore: "4294967295",
		Nonce:       "0xdeadbeef" + sig,
	}
}
