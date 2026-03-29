package erc8004

// AgentRegistration is the JSON schema for the agent registration document
// served at agentURI (e.g., /.well-known/agent-registration.json).
// Conforms to ERC-8004 "Trustless Agents" registration format.
//
// REQUIRED fields per spec: type, name, description, image, services (>=1),
// x402Support, active, registrations (>=1).
// OPTIONAL: supportedTrust.
//
// Note: Description and Image use omitempty for parsing flexibility but MUST
// be populated when producing registration documents.
//
// Spec: https://eips.ethereum.org/EIPS/eip-8004
type AgentRegistration struct {
	Type           string       `json:"type"`                     // REQUIRED
	Name           string       `json:"name"`                     // REQUIRED
	Description    string       `json:"description,omitempty"`    // REQUIRED (omitempty for parsing)
	Image          string       `json:"image,omitempty"`          // REQUIRED (omitempty for parsing)
	Services       []ServiceDef `json:"services"`                 // REQUIRED (>=1)
	X402Support    bool         `json:"x402Support"`              // REQUIRED
	Active         bool         `json:"active"`                   // REQUIRED
	Registrations  []OnChainReg `json:"registrations,omitempty"`  // REQUIRED (>=1, omitempty for parsing)
	SupportedTrust []string     `json:"supportedTrust,omitempty"` // OPTIONAL
}

// RegistrationType is the canonical type URI for ERC-8004 registration v1.
const RegistrationType = "https://eips.ethereum.org/EIPS/eip-8004#registration-v1"

// ServiceDef describes an endpoint the agent exposes.
// For OASF entries (name="OASF"), Skills and Domains provide machine-readable
// taxonomy for agent discovery. See https://schema.oasf.outshift.com/
type ServiceDef struct {
	Name     string   `json:"name"`               // e.g., "web", "A2A", "MCP", "OASF"
	Endpoint string   `json:"endpoint,omitempty"` // full URL (omitempty for OASF entries)
	Version  string   `json:"version,omitempty"`  // protocol version (SHOULD per spec)
	Skills   []string `json:"skills,omitempty"`   // OASF skill taxonomy paths
	Domains  []string `json:"domains,omitempty"`  // OASF domain taxonomy paths
}

// OnChainReg links the registration to its on-chain record.
type OnChainReg struct {
	AgentID       int64  `json:"agentId"`       // ERC-721 tokenId
	AgentRegistry string `json:"agentRegistry"` // CAIP-10 format: "eip155:84532:0x8004A818..."
}
