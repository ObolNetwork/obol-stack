package monetizeapi

import "testing"

func TestUpsertAgentIdentityRegistration_PerChain(t *testing.T) {
	status := AgentIdentityStatus{}
	status = UpsertAgentIdentityRegistration(status, "base-sepolia", "99")
	status = UpsertAgentIdentityRegistration(status, "base", "42")

	if got := AgentIdentityAgentIDForChain(status, "base"); got != "42" {
		t.Errorf("base agentId = %q, want 42", got)
	}
	if got := AgentIdentityAgentIDForChain(status, "base-sepolia"); got != "99" {
		t.Errorf("base-sepolia agentId = %q, want 99", got)
	}
}

func TestUpsertAgentIdentityRegistration_DedupesChain(t *testing.T) {
	status := AgentIdentityStatus{
		Registrations: []AgentIdentityRegistration{
			{Chain: "base", AgentID: "1"},
			{Chain: "base-sepolia", AgentID: "2"},
			{Chain: "BASE", AgentID: "3"},
		},
	}

	status = UpsertAgentIdentityRegistration(status, "base", "4")

	if got := AgentIdentityAgentIDForChain(status, "base"); got != "4" {
		t.Errorf("base agentId = %q, want 4", got)
	}
	if len(status.Registrations) != 2 {
		t.Fatalf("registrations = %+v, want deduped base + base-sepolia", status.Registrations)
	}
}

func TestRemoveAgentIdentityRegistration(t *testing.T) {
	status := AgentIdentityStatus{}
	status = UpsertAgentIdentityRegistration(status, "base-sepolia", "99")
	status = UpsertAgentIdentityRegistration(status, "base", "42")

	status = RemoveAgentIdentityRegistration(status, "base")

	if got := AgentIdentityAgentIDForChain(status, "base"); got != "" {
		t.Errorf("base agentId = %q, want empty after remove", got)
	}
	if got := AgentIdentityAgentIDForChain(status, "base-sepolia"); got != "99" {
		t.Errorf("base-sepolia agentId = %q, want 99 (untouched)", got)
	}
	if len(status.Registrations) != 1 {
		t.Fatalf("registrations = %+v, want only base-sepolia left", status.Registrations)
	}
}

func TestRemoveAgentIdentityRegistration_UnknownChainAndEmptyChainAreNoops(t *testing.T) {
	status := AgentIdentityStatus{}
	status = UpsertAgentIdentityRegistration(status, "base", "42")

	status = RemoveAgentIdentityRegistration(status, "polygon")
	if got := AgentIdentityAgentIDForChain(status, "base"); got != "42" {
		t.Errorf("base agentId = %q, want 42 unchanged by removing an unrelated chain", got)
	}

	status = RemoveAgentIdentityRegistration(status, "")
	if got := AgentIdentityAgentIDForChain(status, "base"); got != "42" {
		t.Errorf("base agentId = %q, want 42 unchanged by an empty-chain no-op", got)
	}
}

// TestRemoveAgentIdentityRegistration_DoesNotMutateSharedBackingArray guards
// against RemoveAgentIdentityRegistration filtering status.Registrations in
// place: a caller (e.g. an informer-cached object) may hold another
// reference to the same backing array, which an in-place compaction would
// silently corrupt.
func TestRemoveAgentIdentityRegistration_DoesNotMutateSharedBackingArray(t *testing.T) {
	backing := []AgentIdentityRegistration{
		{Chain: "base", AgentID: "1"},
		{Chain: "base-sepolia", AgentID: "2"},
	}
	shared := AgentIdentityStatus{Registrations: backing}

	_ = RemoveAgentIdentityRegistration(shared, "base")

	if backing[0].Chain != "base" || backing[0].AgentID != "1" {
		t.Errorf("shared backing array was mutated: backing[0] = %+v, want unchanged {base 1}", backing[0])
	}
}
