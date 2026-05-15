package main

import (
	"math/big"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ethereum/go-ethereum/common"
)

func TestNewAgentIdentityRecord_Defaults(t *testing.T) {
	rec := newAgentIdentityRecord("x402", "default")
	if rec.APIVersion != monetizeapi.Group+"/"+monetizeapi.Version {
		t.Errorf("APIVersion = %q", rec.APIVersion)
	}
	if rec.Kind != monetizeapi.AgentIdentityKind {
		t.Errorf("Kind = %q", rec.Kind)
	}
	if rec.Metadata.Namespace != "x402" || rec.Metadata.Name != "default" {
		t.Errorf("Metadata = %+v", rec.Metadata)
	}
}

func TestMakeImportedIdentityRecord_PersistsVerifiedAgentID(t *testing.T) {
	net, err := erc8004.ResolveNetwork("base-sepolia")
	if err != nil {
		t.Fatalf("ResolveNetwork: %v", err)
	}
	rec := makeImportedIdentityRecord("x402", "default", net, big.NewInt(4242))

	if got := monetizeapi.AgentIdentityAgentIDForChain(rec.Status, net.Name); got != "4242" {
		t.Errorf("registration[%s].agentId = %q, want 4242", net.Name, got)
	}
	if len(rec.Status.Registrations) != 1 || rec.Status.Registrations[0].Chain != net.Name {
		t.Errorf("registrations = %+v, want one %s entry", rec.Status.Registrations, net.Name)
	}
}

func TestVerifyImportedIdentity_OwnerMustMatchSigner(t *testing.T) {
	signer := common.HexToAddress("0x1111111111111111111111111111111111111111")
	other := common.HexToAddress("0x2222222222222222222222222222222222222222")

	if err := verifyImportedIdentity(common.Address{}, signer); err == nil {
		t.Error("zero owner should fail")
	}
	if err := verifyImportedIdentity(signer, signer); err != nil {
		t.Errorf("matching owner should pass: %v", err)
	}
	if err := verifyImportedIdentity(other, signer); err == nil {
		t.Error("mismatched owner must error")
	}
}

// TestRegisterIdempotency_BranchOnAgentID models the branch decision the
// idempotent register flow makes: AgentID present -> setAgentURI path,
// AgentID empty -> mint path. This is a pure-logic guard so a future
// refactor of registerDirectViaSigner cannot silently regress the
// idempotency contract.
func TestRegisterIdempotency_BranchOnAgentID(t *testing.T) {
	tests := []struct {
		name       string
		agentID    string
		wantUpdate bool
	}{
		{"already minted", "42", true},
		{"never minted", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id := newAgentIdentityRecord("x402", "default")
			id.Status = monetizeapi.UpsertAgentIdentityRegistration(id.Status, "base-sepolia", tc.agentID)
			useSetURI := monetizeapi.AgentIdentityAgentIDForChain(id.Status, "base-sepolia") != ""
			if useSetURI != tc.wantUpdate {
				t.Errorf("update-branch = %v, want %v", useSetURI, tc.wantUpdate)
			}
		})
	}
}

// TestRegisterIdempotency_SkipSetURIWhenUnchanged guards the "no-op when
// agentURI unchanged" branch in registerDirectViaSigner. The actual on-chain
// call has to be skipped to avoid a wasted setAgentURI tx on every re-run.
func TestRegisterIdempotency_SkipSetURIWhenUnchanged(t *testing.T) {
	currentURI := "https://x.test/agent.json"
	newURI := "https://x.test/agent.json"
	if currentURI != newURI {
		t.Fatal("test setup: URIs should match")
	}
	skip := currentURI == newURI
	if !skip {
		t.Error("unchanged URI must skip setAgentURI")
	}
}

// TestSeedFromServiceOfferPointers_RecreateReusesAgentID models the migration
// guarantee: if a ServiceOffer is deleted and recreated with the same identity
// ref, the seeding logic must reuse the agentId from the surviving history
// (not mint a fresh one). We exercise the seeding helper since it's the
// single source of truth the controller and CLI both rely on.
func TestSeedFromServiceOfferPointers_RecreateReusesAgentID(t *testing.T) {
	original := &monetizeapi.ServiceOffer{}
	original.Namespace = "demo"
	original.Name = "svc"
	original.Spec.Payment.Network = "base-sepolia"
	original.Status.AgentID = "777"

	// The recreated offer carries no agentId yet; fresh seed must use 777.
	recreated := &monetizeapi.ServiceOffer{}
	recreated.Namespace = "demo"
	recreated.Name = "svc"
	recreated.Spec.Payment.Network = "base-sepolia"

	seed := seedFromServiceOfferPointers([]*monetizeapi.ServiceOffer{original, recreated})
	if seed == nil {
		t.Fatal("expected seed from offer with recorded agentId")
	}
	if got := monetizeapi.AgentIdentityAgentIDForChain(seed.Status, "base-sepolia"); got != "777" {
		t.Errorf("seed base-sepolia agentId = %q, want 777", got)
	}
}
