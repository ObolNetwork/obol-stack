package serviceoffercontroller

import (
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// readyOffer returns a registration-enabled ServiceOffer with the four
// conditions BuildIdentityRegistrationDocument's published-filter requires.
func readyOffer(name string) *monetizeapi.ServiceOffer {
	o := &monetizeapi.ServiceOffer{}
	o.Namespace = "demo"
	o.Name = name
	o.Spec.Type = "http"
	o.Spec.Path = "/services/" + name
	o.Spec.Registration.Enabled = true
	o.Spec.Registration.Name = name
	o.Spec.Registration.Skills = []string{"chat/general"}
	for _, t := range []string{"ModelReady", "UpstreamHealthy", "PaymentGateReady", "RoutePublished"} {
		o.Status.Conditions = append(o.Status.Conditions, monetizeapi.Condition{Type: t, Status: "True"})
	}
	return o
}

func defaultIdentity(agentID string) *monetizeapi.AgentIdentity {
	id := &monetizeapi.AgentIdentity{}
	id.Namespace = monetizeapi.AgentIdentityDefaultNamespace
	id.Name = monetizeapi.AgentIdentityDefaultName
	id.Status = monetizeapi.UpsertAgentIdentityRegistration(id.Status, "base-sepolia", agentID)
	return id
}

func TestBuildIdentityRegistrationDocument_Active(t *testing.T) {
	id := defaultIdentity("42")
	offer := readyOffer("svc-a")

	doc := BuildIdentityRegistrationDocument(IdentityRegistrationView{
		Identity: id,
		Offers:   []*monetizeapi.ServiceOffer{offer},
		BaseURL:  "https://example.tunnel.test/",
	})

	if !doc.Active || !doc.X402Support {
		t.Fatalf("active document must have Active && X402Support, got Active=%v X402Support=%v", doc.Active, doc.X402Support)
	}
	if len(doc.Services) == 0 {
		t.Fatal("active document must have services")
	}
	if doc.Services[0].Endpoint != "https://example.tunnel.test/services/svc-a" {
		t.Errorf("services[0].Endpoint = %q", doc.Services[0].Endpoint)
	}
	if len(doc.Registrations) != 1 || doc.Registrations[0].AgentID != 42 {
		t.Errorf("Registrations = %+v, want one entry with agentId=42", doc.Registrations)
	}
}

func TestBuildIdentityRegistrationDocument_TombstoneWhenNoOffers(t *testing.T) {
	id := defaultIdentity("99")
	doc := BuildIdentityRegistrationDocument(IdentityRegistrationView{
		Identity: id,
		Offers:   nil,
		BaseURL:  "https://example.tunnel.test",
	})
	if doc.Active {
		t.Errorf("tombstone Active = true, want false")
	}
	if doc.X402Support {
		t.Errorf("tombstone X402Support = true, want false")
	}
	if len(doc.Registrations) != 1 || doc.Registrations[0].AgentID != 99 {
		t.Errorf("tombstone preserved agentId = %+v, want 99", doc.Registrations)
	}
	if len(doc.Services) != 0 {
		t.Errorf("tombstone services = %+v, want empty", doc.Services)
	}
}

func TestBuildIdentityRegistrationDocument_UsesIdentityChain(t *testing.T) {
	id := defaultIdentity("42")
	id.Status = monetizeapi.AgentIdentityStatus{}
	id.Status = monetizeapi.UpsertAgentIdentityRegistration(id.Status, "base", "42")
	offer := readyOffer("svc-a")
	offer.Spec.Payment.Network = "base-sepolia"

	doc := BuildIdentityRegistrationDocument(IdentityRegistrationView{
		Identity: id,
		Offers:   []*monetizeapi.ServiceOffer{offer},
		BaseURL:  "https://example.tunnel.test",
	})

	if len(doc.Registrations) != 1 {
		t.Fatalf("Registrations = %+v, want one entry", doc.Registrations)
	}
	if got, want := doc.Registrations[0].AgentRegistry, erc8004.Base.CAIP10Registry(); got != want {
		t.Errorf("agentRegistry = %q, want %q", got, want)
	}
}

func TestBuildIdentityRegistrationDocument_RendersPerChainRegistrations(t *testing.T) {
	id := defaultIdentity("99")
	id.Status = monetizeapi.UpsertAgentIdentityRegistration(id.Status, "base", "42")

	doc := BuildIdentityRegistrationDocument(IdentityRegistrationView{
		Identity: id,
		Offers:   []*monetizeapi.ServiceOffer{readyOffer("svc-a")},
		BaseURL:  "https://example.tunnel.test",
	})

	if len(doc.Registrations) != 2 {
		t.Fatalf("Registrations = %+v, want base + base-sepolia", doc.Registrations)
	}
	if got, want := doc.Registrations[0].AgentRegistry, erc8004.Base.CAIP10Registry(); got != want {
		t.Errorf("registrations[0].agentRegistry = %q, want %q", got, want)
	}
	if got := doc.Registrations[0].AgentID; got != 42 {
		t.Errorf("registrations[0].agentId = %d, want 42", got)
	}
	if got, want := doc.Registrations[1].AgentRegistry, erc8004.BaseSepolia.CAIP10Registry(); got != want {
		t.Errorf("registrations[1].agentRegistry = %q, want %q", got, want)
	}
	if got := doc.Registrations[1].AgentID; got != 99 {
		t.Errorf("registrations[1].agentId = %d, want 99", got)
	}
}

func TestBuildIdentityRegistrationDocument_TombstoneWhenAllOffersStale(t *testing.T) {
	id := defaultIdentity("7")
	stale := &monetizeapi.ServiceOffer{}
	stale.Namespace = "demo"
	stale.Name = "stale"
	stale.Spec.Registration.Enabled = true
	// No Ready conditions set, so the offer is not publishable.

	doc := BuildIdentityRegistrationDocument(IdentityRegistrationView{
		Identity: id,
		Offers:   []*monetizeapi.ServiceOffer{stale},
		BaseURL:  "https://x.test",
	})
	if doc.Active {
		t.Fatal("all offers stale -> tombstone (active=false)")
	}
	if doc.X402Support {
		t.Fatal("all offers stale -> tombstone (x402Support=false)")
	}
}

func TestBuildIdentityRegistrationDocument_LastOfferDeletedStillRenders(t *testing.T) {
	doc := BuildIdentityRegistrationDocument(IdentityRegistrationView{
		Identity: defaultIdentity("123"),
		BaseURL:  "https://x.test",
	})
	if doc.Active {
		t.Error("tombstone doc must have active=false")
	}
	if len(doc.Registrations) != 1 || doc.Registrations[0].AgentID != 123 {
		t.Errorf("tombstone Registrations = %+v, want agentId=123", doc.Registrations)
	}
}

func TestSeedIdentityFromOffers_PicksOldestWithAgentID(t *testing.T) {
	earlier := readyOffer("alpha")
	earlier.CreationTimestamp = metav1.NewTime(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	earlier.Status.AgentID = "11"
	earlier.Spec.Payment.Network = "base"

	later := readyOffer("beta")
	later.CreationTimestamp = metav1.NewTime(time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))
	later.Status.AgentID = "22"
	later.Spec.Payment.Network = "base-sepolia"

	seed := SeedIdentityFromOffers([]*monetizeapi.ServiceOffer{later, earlier})
	if seed == nil {
		t.Fatal("expected seed, got nil")
	}
	if got := monetizeapi.AgentIdentityAgentIDForChain(seed.Status, "base"); got != "11" {
		t.Errorf("seed base agentId = %q, want 11", got)
	}
	if got := monetizeapi.AgentIdentityAgentIDForChain(seed.Status, "base-sepolia"); got != "22" {
		t.Errorf("seed base-sepolia agentId = %q, want 22", got)
	}
}

func TestSeedIdentityFromOffers_NoAgentIDReturnsNil(t *testing.T) {
	o := readyOffer("plain")
	seed := SeedIdentityFromOffers([]*monetizeapi.ServiceOffer{o})
	if seed != nil {
		t.Errorf("seed = %+v, want nil when no offer has agentId", seed)
	}
}

// TestBuildIdentityRegistrationDocument_DescriptionPrecedence pins the
// operator > inference-default > generic-default ordering. Regression test
// for the case where an explicit spec.registration.description on an
// inference ServiceOffer was being overwritten by the auto-generated
// "<model> inference via x402 micropayments" string.
func TestBuildIdentityRegistrationDocument_DescriptionPrecedence(t *testing.T) {
	id := defaultIdentity("1")

	cases := []struct {
		name    string
		mutate  func(*monetizeapi.ServiceOffer)
		wantDoc string
	}{
		{
			name: "operator description wins on inference offer",
			mutate: func(o *monetizeapi.ServiceOffer) {
				o.Spec.Type = "inference"
				o.Spec.Model.Name = "aeon-ultimate"
				o.Spec.Registration.Description = "Uncensored Qwen3.6-27B abliteration on NVIDIA GB10"
			},
			wantDoc: "Uncensored Qwen3.6-27B abliteration on NVIDIA GB10",
		},
		{
			name: "inference fallback fires when description empty",
			mutate: func(o *monetizeapi.ServiceOffer) {
				o.Spec.Type = "inference"
				o.Spec.Model.Name = "aeon-ultimate"
				o.Spec.Registration.Description = ""
			},
			wantDoc: "aeon-ultimate inference via x402 micropayments",
		},
		{
			name: "generic fallback when no description and not inference",
			mutate: func(o *monetizeapi.ServiceOffer) {
				o.Spec.Type = "http"
				o.Spec.Registration.Description = ""
			},
			wantDoc: "x402 payment-gated http service: svc-a",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			offer := readyOffer("svc-a")
			tc.mutate(offer)
			doc := BuildIdentityRegistrationDocument(IdentityRegistrationView{
				Identity: id,
				Offers:   []*monetizeapi.ServiceOffer{offer},
				BaseURL:  "https://example.tunnel.test",
			})
			if doc.Description != tc.wantDoc {
				t.Errorf("Description = %q, want %q", doc.Description, tc.wantDoc)
			}
		})
	}
}
