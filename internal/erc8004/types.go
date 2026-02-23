package erc8004

// AgentRegistration is the JSON schema for the agent registration document
// served at agentURI (e.g., /.well-known/agent-registration.json).
// Conforms to ERC-8004 "Trustless Agents" registration format.
//
// Spec: https://eips.ethereum.org/EIPS/eip-8004
type AgentRegistration struct {
	Type           string        `json:"type"`
	Name           string        `json:"name"`
	Description    string        `json:"description,omitempty"`
	Image          string        `json:"image,omitempty"`
	Services       []ServiceDef  `json:"services"`
	X402Support    bool          `json:"x402Support"`
	Active         bool          `json:"active"`
	Registrations  []OnChainReg  `json:"registrations,omitempty"`
	SupportedTrust []string      `json:"supportedTrust,omitempty"`
}

// RegistrationType is the canonical type URI for ERC-8004 registration v1.
const RegistrationType = "https://eips.ethereum.org/EIPS/eip-8004#registration-v1"

// ServiceDef describes an endpoint the agent exposes.
type ServiceDef struct {
	Name     string `json:"name"`               // e.g., "web", "A2A", "MCP"
	Endpoint string `json:"endpoint"`           // full URL
	Version  string `json:"version,omitempty"`  // protocol version (SHOULD per spec)
}

// OnChainReg links the registration to its on-chain record.
type OnChainReg struct {
	AgentID       int64  `json:"agentId"`       // ERC-721 tokenId
	AgentRegistry string `json:"agentRegistry"` // CAIP-10 format: "eip155:84532:0x8004A818..."
}
