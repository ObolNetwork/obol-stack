package serviceoffercontroller

import (
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

func TestApplySharedRegistrationStatus_NonOwnerUsesSharedAgent(t *testing.T) {
	status := &monetizeapi.ServiceOfferStatus{
		Conditions: []monetizeapi.Condition{{Type: "RoutePublished", Status: "True"}},
	}
	owner := &monetizeapi.ServiceOffer{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "demo"}}
	offer := &monetizeapi.ServiceOffer{ObjectMeta: metav1.ObjectMeta{Name: "beta", Namespace: "demo"}}
	request := &monetizeapi.RegistrationRequest{
		Status: monetizeapi.RegistrationRequestStatus{
			Phase:              registrationPhaseRegistered,
			AgentID:            "42",
			RegistrationTxHash: "0xtx",
		},
	}

	applySharedRegistrationStatus(status, offer, owner, request)

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
	request := &monetizeapi.RegistrationRequest{
		Status: monetizeapi.RegistrationRequestStatus{
			Phase:   registrationPhaseRegistered,
			AgentID: "7",
		},
	}

	applySharedRegistrationStatus(status, owner, owner, request)

	if isConditionTrue(*status, "Registered") {
		t.Fatalf("registered should remain false until route is published: %+v", status.Conditions)
	}
}
