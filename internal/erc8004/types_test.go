package erc8004

import (
	"encoding/json"
	"testing"
)

func TestAgentRegistration_MarshalJSON(t *testing.T) {
	reg := AgentRegistration{
		Type:        RegistrationType,
		Name:        "test-agent",
		Description: "A test agent",
		Image:       "https://example.com/icon.png",
		Services: []ServiceDef{
			{Name: "web", Endpoint: "https://example.com", Version: "1.0"},
		},
		X402Support: true,
		Active:      true,
		Registrations: []OnChainReg{
			{AgentID: 42, AgentRegistry: "eip155:84532:0x8004A818BFB912233c491871b3d84c89A494BD9e"},
		},
		SupportedTrust: []string{"x402"},
	}

	data, err := json.Marshal(reg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Verify key fields are present in the JSON.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	requiredKeys := []string{"type", "name", "description", "image", "services", "x402Support", "active", "registrations", "supportedTrust"}
	for _, k := range requiredKeys {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q in marshalled JSON", k)
		}
	}
}

func TestAgentRegistration_UnmarshalJSON(t *testing.T) {
	// Canonical spec-compliant JSON.
	input := `{
		"type": "https://eips.ethereum.org/EIPS/eip-8004#registration-v1",
		"name": "my-agent",
		"description": "An AI agent",
		"services": [
			{"name": "A2A", "endpoint": "https://a2a.example.com", "version": "0.2.1"}
		],
		"x402Support": true,
		"active": true,
		"registrations": [
			{"agentId": 7, "agentRegistry": "eip155:84532:0x8004A818BFB912233c491871b3d84c89A494BD9e"}
		],
		"supportedTrust": ["x402", "tee"]
	}`

	var reg AgentRegistration
	if err := json.Unmarshal([]byte(input), &reg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if reg.Type != RegistrationType {
		t.Errorf("Type = %q, want %q", reg.Type, RegistrationType)
	}
	if reg.Name != "my-agent" {
		t.Errorf("Name = %q, want %q", reg.Name, "my-agent")
	}
	if reg.Description != "An AI agent" {
		t.Errorf("Description = %q, want %q", reg.Description, "An AI agent")
	}
	if !reg.X402Support {
		t.Error("X402Support = false, want true")
	}
	if !reg.Active {
		t.Error("Active = false, want true")
	}
	if len(reg.Services) != 1 {
		t.Fatalf("len(Services) = %d, want 1", len(reg.Services))
	}
	if reg.Services[0].Name != "A2A" {
		t.Errorf("Services[0].Name = %q, want %q", reg.Services[0].Name, "A2A")
	}
	if reg.Services[0].Version != "0.2.1" {
		t.Errorf("Services[0].Version = %q, want %q", reg.Services[0].Version, "0.2.1")
	}
	if len(reg.Registrations) != 1 {
		t.Fatalf("len(Registrations) = %d, want 1", len(reg.Registrations))
	}
	if reg.Registrations[0].AgentID != 7 {
		t.Errorf("Registrations[0].AgentID = %d, want 7", reg.Registrations[0].AgentID)
	}
	if len(reg.SupportedTrust) != 2 {
		t.Errorf("len(SupportedTrust) = %d, want 2", len(reg.SupportedTrust))
	}
}

func TestAgentRegistration_OmitEmptyFields(t *testing.T) {
	// Only required fields set; optional fields left as zero values.
	reg := AgentRegistration{
		Type:     RegistrationType,
		Name:     "minimal",
		Services: []ServiceDef{{Name: "web", Endpoint: "https://example.com"}},
	}

	data, err := json.Marshal(reg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	// Fields with omitempty should be absent when zero.
	omitKeys := []string{"description", "image", "registrations", "supportedTrust"}
	for _, k := range omitKeys {
		if _, ok := m[k]; ok {
			t.Errorf("key %q should be omitted when empty, but was present", k)
		}
	}

	// Required fields should still be present.
	presentKeys := []string{"type", "name", "services"}
	for _, k := range presentKeys {
		if _, ok := m[k]; !ok {
			t.Errorf("required key %q should be present", k)
		}
	}
}

func TestServiceDef_VersionOptional(t *testing.T) {
	// Version has omitempty — when empty it should not appear.
	svc := ServiceDef{Name: "MCP", Endpoint: "https://mcp.example.com"}

	data, err := json.Marshal(svc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	if _, ok := m["version"]; ok {
		t.Error("version should be omitted when empty")
	}

	// With version set, it should appear.
	svc.Version = "2.0"
	data, err = json.Marshal(svc)
	if err != nil {
		t.Fatalf("Marshal with version: %v", err)
	}

	m = nil
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["version"]; !ok {
		t.Error("version should be present when set")
	}
}

func TestOnChainReg_AgentIDNumeric(t *testing.T) {
	reg := OnChainReg{
		AgentID:       42,
		AgentRegistry: "eip155:84532:0x8004A818BFB912233c491871b3d84c89A494BD9e",
	}

	data, err := json.Marshal(reg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Verify agentId serializes as a JSON number, not a string.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	raw, ok := m["agentId"]
	if !ok {
		t.Fatal("missing agentId key")
	}

	// A JSON number does not start with '"'.
	if len(raw) > 0 && raw[0] == '"' {
		t.Errorf("agentId serialized as string %s, want JSON number", string(raw))
	}

	// Verify it round-trips to the correct value.
	var back OnChainReg
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("round-trip Unmarshal: %v", err)
	}
	if back.AgentID != 42 {
		t.Errorf("round-trip AgentID = %d, want 42", back.AgentID)
	}
}

func TestRegistrationType_Constant(t *testing.T) {
	want := "https://eips.ethereum.org/EIPS/eip-8004#registration-v1"
	if RegistrationType != want {
		t.Errorf("RegistrationType = %q, want %q", RegistrationType, want)
	}
}
