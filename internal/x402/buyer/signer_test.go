package buyer

import (
	"sync"
	"testing"

	x402 "github.com/mark3labs/x402-go"
)

func TestPreSignedSigner_CanSign(t *testing.T) {
	signer := NewPreSignedSigner(
		"base-sepolia",
		"0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		"0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		"1000",
		[]*PreSignedAuth{makeAuth("0x1")},
		0,
		nil,
	)

	tests := []struct {
		name string
		req  *x402.PaymentRequirement
		want bool
	}{
		{
			name: "matching requirement",
			req: &x402.PaymentRequirement{
				Network:           "base-sepolia",
				PayTo:             "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:             "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				MaxAmountRequired: "1000",
			},
			want: true,
		},
		{
			name: "case-insensitive match",
			req: &x402.PaymentRequirement{
				Network:           "Base-Sepolia",
				PayTo:             "0x70997970c51812dc3a010c7d01b50e0d17dc79c8",
				Asset:             "0x036cbd53842c5426634e7929541ec2318f3dcf7e",
				MaxAmountRequired: "1000",
			},
			want: true,
		},
		{
			name: "wrong network",
			req: &x402.PaymentRequirement{
				Network: "base",
				PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
			},
			want: false,
		},
		{
			name: "wrong payTo",
			req: &x402.PaymentRequirement{
				Network: "base-sepolia",
				PayTo:   "0xdeadbeef",
				Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
			},
			want: false,
		},
		{
			name: "wrong asset",
			req: &x402.PaymentRequirement{
				Network: "base-sepolia",
				PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:   "0xdeadbeef",
			},
			want: false,
		},
		{
			name: "wrong amount",
			req: &x402.PaymentRequirement{
				Network:           "base-sepolia",
				PayTo:             "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:             "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				MaxAmountRequired: "999",
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
		auths,
		0,
		nil,
	)

	req := &x402.PaymentRequirement{
		Network:           "base-sepolia",
		PayTo:             "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		Asset:             "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		MaxAmountRequired: "1000",
	}

	// First sign — should pop "0xaaa".
	p1, err := signer.Sign(req)
	if err != nil {
		t.Fatalf("first Sign: %v", err)
	}
	payload1 := p1.Payload.(x402.EVMPayload)
	if payload1.Signature != "0xaaa" {
		t.Errorf("first signature = %q, want %q", payload1.Signature, "0xaaa")
	}
	if p1.X402Version != 1 || p1.Scheme != "exact" || p1.Network != "base-sepolia" {
		t.Errorf("unexpected payload fields: version=%d scheme=%s network=%s",
			p1.X402Version, p1.Scheme, p1.Network)
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
	payload2 := p2.Payload.(x402.EVMPayload)
	if payload2.Signature != "0xbbb" {
		t.Errorf("second signature = %q, want %q", payload2.Signature, "0xbbb")
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
		auths,
		0,
		nil,
	)

	req := &x402.PaymentRequirement{
		Network:           "base-sepolia",
		PayTo:             "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		Asset:             "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		MaxAmountRequired: "1000",
	}

	var wg sync.WaitGroup
	successes := make(chan struct{}, N)
	failures := make(chan struct{}, N)

	for range N {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := signer.Sign(req)
			if err != nil {
				failures <- struct{}{}
			} else {
				successes <- struct{}{}
			}
		}()
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

func TestPreSignedSigner_Interface(t *testing.T) {
	signer := NewPreSignedSigner("base-sepolia", "0xpayto", "0xasset", "1000", nil, 0, nil)

	// Verify interface compliance.
	var _ x402.Signer = signer

	if signer.Network() != "base-sepolia" {
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
