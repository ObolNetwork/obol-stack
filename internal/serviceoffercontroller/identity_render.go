package serviceoffercontroller

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// IdentityRegistrationView is the identity-driven inputs needed to render an
// ERC-8004 registration document. Decouples the renderer from owner-offer
// coupling so the registration document survives deletion of every
// ServiceOffer that ever referenced the identity.
type IdentityRegistrationView struct {
	// Identity is the durable on-chain agent. Required.
	Identity *monetizeapi.AgentIdentity
	// Offers are ServiceOffers that currently reference Identity. Only the
	// subset that pass offerPublishedForRegistration is included in the active
	// document services list. May be empty when the identity is tombstoned.
	Offers []*monetizeapi.ServiceOffer
	// BaseURL is the public origin (tunnel URL) that should prefix all
	// service endpoint paths and the agent image fallback.
	BaseURL string
}

// BuildIdentityRegistrationDocument is the single render entry point shared by
// the active and tombstone paths. It produces an active document when at
// least one referencing offer is published-for-registration; otherwise it
// produces a tombstone (active:false, x402Support:false) that still carries
// the recorded agentId so external observers see the historical record.
func BuildIdentityRegistrationDocument(view IdentityRegistrationView) erc8004.AgentRegistration {
	if view.Identity == nil {
		return erc8004.AgentRegistration{Type: erc8004.RegistrationType}
	}
	baseURL := strings.TrimRight(view.BaseURL, "/")

	publishable := filterPublishedOffers(view.Offers)
	active := len(publishable) > 0

	name, description, image := identityDocumentMetadata(view.Identity, publishable, baseURL)

	doc := erc8004.AgentRegistration{
		Type:        erc8004.RegistrationType,
		Name:        name,
		Description: description,
		Image:       image,
		Active:      active,
		X402Support: active,
		Services:    buildIdentityRegistrationServices(publishable, baseURL),
	}

	doc.Registrations = buildIdentityOnChainRegistrations(view.Identity)

	if active {
		owner := selectRegistrationOwner(publishable)
		doc.SupportedTrust = owner.Spec.Registration.SupportedTrust
		if metadata := nonEmptyStringMap(owner.Spec.Registration.Metadata); len(metadata) > 0 {
			doc.Metadata = metadata
		}
		if provenance := nonEmptyStringMap(owner.Spec.Provenance); len(provenance) > 0 {
			doc.Provenance = provenance
		}
	} else {
		doc.Description = fmt.Sprintf("Agent %s (deactivated)", name)
		doc.Services = []erc8004.ServiceDef{}
	}

	return doc
}

func identityDocumentMetadata(identity *monetizeapi.AgentIdentity, offers []*monetizeapi.ServiceOffer, baseURL string) (string, string, string) {
	if len(offers) == 0 {
		name := identity.Name
		if strings.TrimSpace(name) == "" {
			name = monetizeapi.AgentIdentityDefaultName
		}
		return name, fmt.Sprintf("ERC-8004 agent identity %s/%s", identity.Namespace, name), baseURL + "/agent-icon.png"
	}
	owner := selectRegistrationOwner(offers)
	name := defaultString(owner.Spec.Registration.Name, owner.Name)
	// Operator-supplied description wins. Only fall back to a controller-
	// generated default when the offer left Spec.Registration.Description
	// empty. The inference-typed default is more specific (names the model),
	// so it preempts the generic default — but neither overrides an explicit
	// operator value. Mirrors the same precedence in render.go's
	// buildActiveRegistrationDocument.
	description := owner.Spec.Registration.Description
	if description == "" {
		if owner.IsInference() && owner.Spec.Model.Name != "" {
			description = fmt.Sprintf("%s inference via x402 micropayments", owner.Spec.Model.Name)
		} else {
			description = fmt.Sprintf("x402 payment-gated %s service: %s", fallbackOfferType(owner), owner.Name)
		}
	}
	image := owner.Spec.Registration.Image
	if image == "" {
		image = baseURL + "/agent-icon.png"
	}
	return name, description, image
}

func buildIdentityOnChainRegistrations(identity *monetizeapi.AgentIdentity) []erc8004.OnChainReg {
	if identity == nil {
		return nil
	}
	registrations := make([]monetizeapi.AgentIdentityRegistration, 0, len(identity.Status.Registrations))
	for _, registration := range identity.Status.Registrations {
		if strings.TrimSpace(registration.Chain) == "" || strings.TrimSpace(registration.AgentID) == "" {
			continue
		}
		registrations = append(registrations, registration)
	}
	sort.Slice(registrations, func(i, j int) bool {
		return registrations[i].Chain < registrations[j].Chain
	})

	out := make([]erc8004.OnChainReg, 0, len(registrations))
	for _, registration := range registrations {
		network, err := erc8004.ResolveNetwork(registration.Chain)
		if err != nil {
			continue
		}
		out = append(out, erc8004.OnChainReg{
			AgentID:       parseInt64(registration.AgentID),
			AgentRegistry: network.CAIP10Registry(),
		})
	}
	return out
}

func filterPublishedOffers(offers []*monetizeapi.ServiceOffer) []*monetizeapi.ServiceOffer {
	out := make([]*monetizeapi.ServiceOffer, 0, len(offers))
	for _, o := range offers {
		if offerPublishedForRegistration(o) {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace == out[j].Namespace {
			return out[i].Name < out[j].Name
		}
		return out[i].Namespace < out[j].Namespace
	})
	return out
}

func buildIdentityRegistrationServices(offers []*monetizeapi.ServiceOffer, baseURL string) []erc8004.ServiceDef {
	baseURL = strings.TrimRight(baseURL, "/")
	services := make([]erc8004.ServiceDef, 0, len(offers)*2)
	for _, offer := range offers {
		services = append(services, serviceDefWithDrain(offer, erc8004.ServiceDef{
			Name:     "web",
			Endpoint: baseURL + offer.EffectivePath(),
		}))
		if len(offer.Spec.Registration.Skills) > 0 || len(offer.Spec.Registration.Domains) > 0 {
			services = append(services, erc8004.ServiceDef{
				Name:    "OASF",
				Version: "0.8",
				Skills:  offer.Spec.Registration.Skills,
				Domains: offer.Spec.Registration.Domains,
			})
		}
		for _, svc := range offer.Spec.Registration.Services {
			services = append(services, serviceDefWithDrain(offer, erc8004.ServiceDef{
				Name:     svc.Name,
				Endpoint: svc.Endpoint,
				Version:  svc.Version,
			}))
		}
	}
	return services
}

// SeedIdentityFromOffers returns per-chain AgentIdentity registrations from
// existing ServiceOffer status. Caller is responsible for the actual CR write.
// Returns nil when no offer carries a recorded agentId.
func SeedIdentityFromOffers(offers []*monetizeapi.ServiceOffer) *monetizeapi.AgentIdentity {
	sorted := make([]*monetizeapi.ServiceOffer, 0, len(offers))
	status := monetizeapi.AgentIdentityStatus{}
	for _, offer := range offers {
		if offer != nil {
			sorted = append(sorted, offer)
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		ti := sorted[i].CreationTimestamp.Time
		tj := sorted[j].CreationTimestamp.Time
		if ti.Equal(tj) {
			if sorted[i].Namespace == sorted[j].Namespace {
				return sorted[i].Name < sorted[j].Name
			}
			return sorted[i].Namespace < sorted[j].Namespace
		}
		return ti.Before(tj)
	})
	for _, o := range sorted {
		if strings.TrimSpace(o.Status.AgentID) == "" {
			continue
		}
		status = monetizeapi.UpsertAgentIdentityRegistration(status, o.Spec.Payment.Network, o.Status.AgentID)
	}
	if !monetizeapi.HasAgentIdentityRegistrations(status) {
		return nil
	}
	return &monetizeapi.AgentIdentity{Status: status}
}
