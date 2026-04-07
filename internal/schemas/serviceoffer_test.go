package schemas

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWorkloadType_Constants(t *testing.T) {
	if WorkloadInference != "inference" {
		t.Errorf("WorkloadInference = %q, want %q", WorkloadInference, "inference")
	}

	if WorkloadFineTuning != "fine-tuning" {
		t.Errorf("WorkloadFineTuning = %q, want %q", WorkloadFineTuning, "fine-tuning")
	}
}

func TestServiceOfferSpec_JSONRoundTrip(t *testing.T) {
	original := ServiceOfferSpec{
		Type: WorkloadInference,
		Model: &ModelSpec{
			Name:    "qwen3.5:35b",
			Runtime: "ollama",
		},
		Upstream: UpstreamSpec{
			Service:    "ollama",
			Namespace:  "llm",
			Port:       11434,
			HealthPath: "/api/tags",
		},
		Payment: PaymentTerms{
			Network:           "base-sepolia",
			PayTo:             "0xABC123",
			Scheme:            "exact",
			MaxTimeoutSeconds: 300,
			Price: PriceTable{
				PerRequest: "0.001",
			},
		},
		Path: "/services/my-inference",
		Registration: &RegistrationSpec{
			Enabled:     true,
			Name:        "my-agent",
			Description: "An inference agent",
			Services: []ServiceDef{
				{Name: "web", Endpoint: "https://example.com", Version: "1.0.0"},
			},
			SupportedTrust: []string{"reputation"},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded ServiceOfferSpec
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	// Verify top-level fields
	if decoded.Type != original.Type {
		t.Errorf("Type = %q, want %q", decoded.Type, original.Type)
	}

	if decoded.Path != original.Path {
		t.Errorf("Path = %q, want %q", decoded.Path, original.Path)
	}

	// Verify Model
	if decoded.Model == nil {
		t.Fatal("Model is nil after round-trip")
	}

	if decoded.Model.Name != original.Model.Name {
		t.Errorf("Model.Name = %q, want %q", decoded.Model.Name, original.Model.Name)
	}

	if decoded.Model.Runtime != original.Model.Runtime {
		t.Errorf("Model.Runtime = %q, want %q", decoded.Model.Runtime, original.Model.Runtime)
	}

	// Verify Upstream
	if decoded.Upstream.Service != original.Upstream.Service {
		t.Errorf("Upstream.Service = %q, want %q", decoded.Upstream.Service, original.Upstream.Service)
	}

	if decoded.Upstream.Namespace != original.Upstream.Namespace {
		t.Errorf("Upstream.Namespace = %q, want %q", decoded.Upstream.Namespace, original.Upstream.Namespace)
	}

	if decoded.Upstream.Port != original.Upstream.Port {
		t.Errorf("Upstream.Port = %d, want %d", decoded.Upstream.Port, original.Upstream.Port)
	}

	if decoded.Upstream.HealthPath != original.Upstream.HealthPath {
		t.Errorf("Upstream.HealthPath = %q, want %q", decoded.Upstream.HealthPath, original.Upstream.HealthPath)
	}

	// Verify Payment
	if decoded.Payment.Network != original.Payment.Network {
		t.Errorf("Payment.Network = %q, want %q", decoded.Payment.Network, original.Payment.Network)
	}

	if decoded.Payment.PayTo != original.Payment.PayTo {
		t.Errorf("Payment.PayTo = %q, want %q", decoded.Payment.PayTo, original.Payment.PayTo)
	}

	if decoded.Payment.Price.PerRequest != original.Payment.Price.PerRequest {
		t.Errorf("Payment.Price.PerRequest = %q, want %q", decoded.Payment.Price.PerRequest, original.Payment.Price.PerRequest)
	}

	// Verify Registration
	if decoded.Registration == nil {
		t.Fatal("Registration is nil after round-trip")
	}

	if decoded.Registration.Enabled != original.Registration.Enabled {
		t.Errorf("Registration.Enabled = %v, want %v", decoded.Registration.Enabled, original.Registration.Enabled)
	}

	if decoded.Registration.Name != original.Registration.Name {
		t.Errorf("Registration.Name = %q, want %q", decoded.Registration.Name, original.Registration.Name)
	}

	if len(decoded.Registration.Services) != len(original.Registration.Services) {
		t.Fatalf("Registration.Services length = %d, want %d", len(decoded.Registration.Services), len(original.Registration.Services))
	}

	if decoded.Registration.Services[0].Name != original.Registration.Services[0].Name {
		t.Errorf("Registration.Services[0].Name = %q, want %q", decoded.Registration.Services[0].Name, original.Registration.Services[0].Name)
	}
}

func TestServiceOfferSpec_OptionalModel(t *testing.T) {
	spec := ServiceOfferSpec{
		Type:  WorkloadInference,
		Model: nil,
		Upstream: UpstreamSpec{
			Service:   "ollama",
			Namespace: "llm",
			Port:      11434,
		},
		Payment: PaymentTerms{
			Network: "base-sepolia",
			PayTo:   "0xABC",
			Price:   PriceTable{PerRequest: "0.001"},
		},
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal to map failed: %v", err)
	}

	if _, ok := raw["model"]; ok {
		t.Error("expected 'model' key to be omitted when Model is nil")
	}
}

func TestServiceOfferSpec_OptionalRegistration(t *testing.T) {
	spec := ServiceOfferSpec{
		Type:         WorkloadInference,
		Registration: nil,
		Upstream: UpstreamSpec{
			Service:   "ollama",
			Namespace: "llm",
			Port:      11434,
		},
		Payment: PaymentTerms{
			Network: "base-sepolia",
			PayTo:   "0xABC",
			Price:   PriceTable{PerRequest: "0.001"},
		},
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal to map failed: %v", err)
	}

	if _, ok := raw["registration"]; ok {
		t.Error("expected 'registration' key to be omitted when Registration is nil")
	}
}

func TestServiceOfferStatus_Conditions(t *testing.T) {
	original := ServiceOfferStatus{
		Conditions: []Condition{
			{
				Type:               "Ready",
				Status:             "True",
				Reason:             "AllChecksPass",
				Message:            "Service is ready",
				LastTransitionTime: "2026-02-26T12:00:00Z",
			},
			{
				Type:   "PaymentConfigured",
				Status: "True",
				Reason: "VerifierReachable",
			},
		},
		Endpoint:           "https://tunnel.example.com/services/my-inference",
		AgentID:            "agent-123",
		RegistrationTxHash: "0xdeadbeef",
		ObservedGeneration: 3,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded ServiceOfferStatus
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if len(decoded.Conditions) != len(original.Conditions) {
		t.Fatalf("Conditions length = %d, want %d", len(decoded.Conditions), len(original.Conditions))
	}

	for i, c := range decoded.Conditions {
		orig := original.Conditions[i]
		if c.Type != orig.Type {
			t.Errorf("Conditions[%d].Type = %q, want %q", i, c.Type, orig.Type)
		}

		if c.Status != orig.Status {
			t.Errorf("Conditions[%d].Status = %q, want %q", i, c.Status, orig.Status)
		}

		if c.Reason != orig.Reason {
			t.Errorf("Conditions[%d].Reason = %q, want %q", i, c.Reason, orig.Reason)
		}

		if c.Message != orig.Message {
			t.Errorf("Conditions[%d].Message = %q, want %q", i, c.Message, orig.Message)
		}

		if c.LastTransitionTime != orig.LastTransitionTime {
			t.Errorf("Conditions[%d].LastTransitionTime = %q, want %q", i, c.LastTransitionTime, orig.LastTransitionTime)
		}
	}

	if decoded.Endpoint != original.Endpoint {
		t.Errorf("Endpoint = %q, want %q", decoded.Endpoint, original.Endpoint)
	}

	if decoded.AgentID != original.AgentID {
		t.Errorf("AgentID = %q, want %q", decoded.AgentID, original.AgentID)
	}

	if decoded.RegistrationTxHash != original.RegistrationTxHash {
		t.Errorf("RegistrationTxHash = %q, want %q", decoded.RegistrationTxHash, original.RegistrationTxHash)
	}

	if decoded.ObservedGeneration != original.ObservedGeneration {
		t.Errorf("ObservedGeneration = %d, want %d", decoded.ObservedGeneration, original.ObservedGeneration)
	}
}

func TestRegistrationSpec_SupportedTrust(t *testing.T) {
	original := RegistrationSpec{
		Enabled:        true,
		SupportedTrust: []string{"reputation", "tee-attestation"},
	}

	data, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}

	var decoded RegistrationSpec
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}

	if len(decoded.SupportedTrust) != len(original.SupportedTrust) {
		t.Fatalf("SupportedTrust length = %d, want %d", len(decoded.SupportedTrust), len(original.SupportedTrust))
	}

	for i, v := range decoded.SupportedTrust {
		if v != original.SupportedTrust[i] {
			t.Errorf("SupportedTrust[%d] = %q, want %q", i, v, original.SupportedTrust[i])
		}
	}
}

func TestRegistrationSpec_Services(t *testing.T) {
	original := RegistrationSpec{
		Enabled: true,
		Name:    "test-agent",
		Services: []ServiceDef{
			{Name: "web", Endpoint: "https://example.com/web", Version: "1.0.0"},
			{Name: "A2A", Endpoint: "https://example.com/a2a", Version: "2.0.0"},
			{Name: "MCP", Endpoint: "https://example.com/mcp"},
		},
	}

	// JSON round-trip
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded RegistrationSpec
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if len(decoded.Services) != len(original.Services) {
		t.Fatalf("Services length = %d, want %d", len(decoded.Services), len(original.Services))
	}

	for i, svc := range decoded.Services {
		orig := original.Services[i]
		if svc.Name != orig.Name {
			t.Errorf("Services[%d].Name = %q, want %q", i, svc.Name, orig.Name)
		}

		if svc.Endpoint != orig.Endpoint {
			t.Errorf("Services[%d].Endpoint = %q, want %q", i, svc.Endpoint, orig.Endpoint)
		}

		if svc.Version != orig.Version {
			t.Errorf("Services[%d].Version = %q, want %q", i, svc.Version, orig.Version)
		}
	}
}
