package serviceoffercontroller

import (
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUpstreamHealthStatusOK(t *testing.T) {
	// 2xx only — 404/3xx/5xx must not flip UpstreamHealthy=True.
	cases := map[int]bool{
		200: true,
		201: true,
		204: true,
		301: false,
		302: false,
		400: false,
		401: false,
		404: false,
		500: false,
		503: false,
		0:   false,
	}
	for code, want := range cases {
		if got := upstreamHealthStatusOK(code); got != want {
			t.Errorf("upstreamHealthStatusOK(%d) = %v, want %v", code, got, want)
		}
	}
}

func TestSelectRegistrationOwnerPrefersOldestEnabledOffer(t *testing.T) {
	now := time.Now().UTC()
	offers := []*monetizeapi.ServiceOffer{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "newer",
				Namespace:         "llm",
				CreationTimestamp: metav1.NewTime(now.Add(2 * time.Minute)),
			},
			Spec: monetizeapi.ServiceOfferSpec{
				Registration: monetizeapi.ServiceOfferRegistration{Enabled: true},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "older",
				Namespace:         "llm",
				CreationTimestamp: metav1.NewTime(now),
			},
			Spec: monetizeapi.ServiceOfferSpec{
				Registration: monetizeapi.ServiceOfferRegistration{Enabled: true},
			},
		},
	}

	owner := selectRegistrationOwner(offers)
	if owner == nil || owner.Name != "older" {
		t.Fatalf("owner = %v, want older", owner)
	}
}

func TestSelectRegistrationOwnerBreaksTiesByNamespaceAndName(t *testing.T) {
	now := metav1.NewTime(time.Now().UTC())
	offers := []*monetizeapi.ServiceOffer{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "zz", CreationTimestamp: now},
			Spec:       monetizeapi.ServiceOfferSpec{Registration: monetizeapi.ServiceOfferRegistration{Enabled: true}},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "aa", CreationTimestamp: now},
			Spec:       monetizeapi.ServiceOfferSpec{Registration: monetizeapi.ServiceOfferRegistration{Enabled: true}},
		},
	}

	owner := selectRegistrationOwner(offers)
	if owner == nil || owner.Namespace != "aa" || owner.Name != "a" {
		t.Fatalf("owner = %v, want aa/a", owner)
	}
}

func TestSelectRegistrationOwnerEmpty(t *testing.T) {
	if owner := selectRegistrationOwner(nil); owner != nil {
		t.Fatalf("owner = %v, want nil", owner)
	}
}

func TestRequestPhaseReady(t *testing.T) {
	if !requestPhaseReady(registrationPhaseRegistered) {
		t.Fatal("Registered should be ready")
	}
	if requestPhaseReady(registrationPhaseAwaitingExternal) {
		t.Fatal("AwaitingExternalRegistration should not be ready")
	}
	if requestPhaseReady(registrationPhaseOffChainOnly) {
		t.Fatal("OffChainOnly should not be ready")
	}
}

func TestPurchaseReadyRequiresRuntimePoolToMatchSpec(t *testing.T) {
	status := &monetizeapi.PurchaseRequestStatus{}
	pr := &monetizeapi.PurchaseRequest{
		Spec: monetizeapi.PurchaseRequestSpec{
			PreSignedAuths: []monetizeapi.PreSignedAuth{
				{Nonce: "a"},
				{Nonce: "b"},
				{Nonce: "c"},
			},
		},
	}

	status.Remaining = 1
	status.Spent = 2
	setPurchaseCondition(&status.Conditions, "Ready", "False", "RuntimeSyncing", "waiting")
	if purchaseConditionIsTrue(status.Conditions, "Ready") {
		t.Fatal("purchase should not be ready while runtime pool is still syncing")
	}

	status.Remaining = len(pr.Spec.PreSignedAuths)
	setPurchaseCondition(&status.Conditions, "Ready", "True", "Reconciled", "synced")
	if !purchaseConditionIsTrue(status.Conditions, "Ready") {
		t.Fatal("purchase should be ready once runtime pool matches spec")
	}
}

func TestApplySharedRegistrationStatus_NonOwnerUsesSharedAgent(t *testing.T) {
	status := &monetizeapi.ServiceOfferStatus{
		Conditions: []monetizeapi.Condition{{Type: "RoutePublished", Status: "True"}},
	}
	owner := &monetizeapi.ServiceOffer{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "demo"}}
	offer := &monetizeapi.ServiceOffer{ObjectMeta: metav1.ObjectMeta{Name: "beta", Namespace: "demo"}}
	offer.Spec.Payment.Network = "base"
	identity := &monetizeapi.AgentIdentity{}
	identity.Status = monetizeapi.UpsertAgentIdentityRegistration(identity.Status, "base", "42")
	request := &monetizeapi.RegistrationRequest{
		Status: monetizeapi.RegistrationRequestStatus{
			Phase:              registrationPhaseRegistered,
			AgentID:            "42",
			RegistrationTxHash: "0xtx",
		},
	}

	applySharedRegistrationStatus(status, offer, owner, identity, request)

	if status.AgentID != "42" || status.RegistrationTxHash != "0xtx" {
		t.Fatalf("shared registration identifiers not copied: %+v", status)
	}
	if !isConditionTrue(*status, "Registered") {
		t.Fatalf("registered condition not set true: %+v", status.Conditions)
	}
}

func TestApplySharedRegistrationStatus_WaitsForRoute(t *testing.T) {
	status := &monetizeapi.ServiceOfferStatus{}
	owner := &monetizeapi.ServiceOffer{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "demo"}}
	owner.Spec.Payment.Network = "base"
	identity := &monetizeapi.AgentIdentity{}
	identity.Status = monetizeapi.UpsertAgentIdentityRegistration(identity.Status, "base", "7")
	request := &monetizeapi.RegistrationRequest{
		Status: monetizeapi.RegistrationRequestStatus{
			Phase:   registrationPhaseRegistered,
			AgentID: "7",
		},
	}

	applySharedRegistrationStatus(status, owner, owner, identity, request)

	if isConditionTrue(*status, "Registered") {
		t.Fatalf("registered should remain false until route is published: %+v", status.Conditions)
	}
}

// CLI `obol sell register` may leave RegistrationRequest phase empty while
// AgentIdentity already has the agentId — non-owner offers must still flip
// Registered=True so status/checkmarks agree.
func TestApplySharedRegistrationStatus_AgentIDWithoutPhase(t *testing.T) {
	status := &monetizeapi.ServiceOfferStatus{
		Conditions: []monetizeapi.Condition{{Type: "RoutePublished", Status: "True"}},
	}
	owner := &monetizeapi.ServiceOffer{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "demo"}}
	offer := &monetizeapi.ServiceOffer{ObjectMeta: metav1.ObjectMeta{Name: "beta", Namespace: "other"}}
	offer.Spec.Payment.Network = "base"
	identity := &monetizeapi.AgentIdentity{}
	identity.Status = monetizeapi.UpsertAgentIdentityRegistration(identity.Status, "base", "8104")
	request := &monetizeapi.RegistrationRequest{
		Status: monetizeapi.RegistrationRequestStatus{
			AgentID: "8104",
			// Phase empty / Pending — common after CLI-only registration path
		},
	}

	applySharedRegistrationStatus(status, offer, owner, identity, request)

	if status.AgentID != "8104" {
		t.Fatalf("AgentID = %q, want 8104", status.AgentID)
	}
	if !isConditionTrue(*status, "Registered") {
		t.Fatalf("Registered should be True when agentId is known: %+v", status.Conditions)
	}
}

// Chain-switch regression: offer B borrows a shared registration from owner
// A, but B's own Spec.Payment.Network has no verified registration in the
// AgentIdentity (e.g. B just switched networks, or was never registered on
// this chain). Even though the owner's RegistrationRequest is Phase=Registered
// with a non-empty (different-chain) AgentID, B's status must not adopt it
// and Registered must not flip True on the strength of a foreign chain's id.
func TestApplySharedRegistrationStatus_ChainMismatchDoesNotAdoptForeignAgentID(t *testing.T) {
	status := &monetizeapi.ServiceOfferStatus{
		Conditions: []monetizeapi.Condition{{Type: "RoutePublished", Status: "True"}},
	}
	owner := &monetizeapi.ServiceOffer{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "demo"}}
	offer := &monetizeapi.ServiceOffer{ObjectMeta: metav1.ObjectMeta{Name: "beta", Namespace: "demo"}}
	offer.Spec.Payment.Network = "base" // switched away from base-sepolia
	identity := &monetizeapi.AgentIdentity{}
	identity.Status = monetizeapi.UpsertAgentIdentityRegistration(identity.Status, "base-sepolia", "42")
	request := &monetizeapi.RegistrationRequest{
		Status: monetizeapi.RegistrationRequestStatus{
			Phase:   registrationPhaseRegistered,
			AgentID: "42", // owner's base-sepolia id — must not leak onto offer's base status
		},
	}

	applySharedRegistrationStatus(status, offer, owner, identity, request)

	if status.AgentID != "" {
		t.Fatalf("AgentID = %q, want empty (no verified registration on offer's own chain)", status.AgentID)
	}
	if isConditionTrue(*status, "Registered") {
		t.Fatalf("Registered should not flip True from a different chain's agentId: %+v", status.Conditions)
	}
}
