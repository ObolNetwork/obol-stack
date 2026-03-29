package schemas

// RegistrationSpec defines ERC-8004 registration metadata for a ServiceOffer.
// Field names align with the AgentRegistration document schema defined in
// the ERC-8004 specification.
//
// Spec: https://eips.ethereum.org/EIPS/eip-8004
type RegistrationSpec struct {
	// Enabled controls whether the reconciler registers on-chain.
	// Replaces the bare "register: boolean" field from v1alpha1.
	Enabled bool `json:"enabled" yaml:"enabled"`

	// Name is the agent name. Maps to AgentRegistration.name.
	Name string `json:"name,omitempty" yaml:"name,omitempty"`

	// Description is a human-readable description.
	// Maps to AgentRegistration.description.
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Image is a URL to the agent image/icon.
	// Maps to AgentRegistration.image.
	Image string `json:"image,omitempty" yaml:"image,omitempty"`

	// Services lists endpoints the agent exposes.
	// Maps to AgentRegistration.services[].
	Services []ServiceDef `json:"services,omitempty" yaml:"services,omitempty"`

	// SupportedTrust lists trust verification methods.
	// Maps to AgentRegistration.supportedTrust[].
	// Valid values: "reputation", "crypto-economic", "tee-attestation".
	SupportedTrust []string `json:"supportedTrust,omitempty" yaml:"supportedTrust,omitempty"`

	// Skills lists OASF skills for discovery.
	Skills []string `json:"skills,omitempty" yaml:"skills,omitempty"`

	// Domains lists OASF domains for discovery.
	Domains []string `json:"domains,omitempty" yaml:"domains,omitempty"`
}

// ServiceDef describes an endpoint the agent exposes.
// Mirrors erc8004.ServiceDef and the ERC-8004 service definition schema.
type ServiceDef struct {
	// Name identifies the service type (e.g., "web", "A2A", "MCP").
	Name string `json:"name" yaml:"name"`

	// Endpoint is the service URL. Auto-filled from tunnel URL if empty.
	Endpoint string `json:"endpoint" yaml:"endpoint"`

	// Version is the protocol version (SHOULD per ERC-8004 spec).
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
}
