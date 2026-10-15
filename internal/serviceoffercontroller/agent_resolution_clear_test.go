package serviceoffercontroller

import (
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

func TestClearNonAgentResolution(t *testing.T) {
	stale := &monetizeapi.ServiceOfferAgentResolution{Model: "old", Runtime: "hermes"}

	for _, typ := range []string{"http", "inference", ""} {
		offer := &monetizeapi.ServiceOffer{Spec: monetizeapi.ServiceOfferSpec{Type: typ}}
		status := monetizeapi.ServiceOfferStatus{AgentResolution: stale}
		clearNonAgentResolution(offer, &status)
		if status.AgentResolution != nil {
			t.Errorf("type=%q: AgentResolution not cleared: %+v", typ, status.AgentResolution)
		}
	}

	agentOffer := &monetizeapi.ServiceOffer{Spec: monetizeapi.ServiceOfferSpec{Type: "agent"}}
	status := monetizeapi.ServiceOfferStatus{AgentResolution: stale}
	clearNonAgentResolution(agentOffer, &status)
	if status.AgentResolution != stale {
		t.Error("type=agent: AgentResolution must be left for resolveAgentOffer to manage")
	}
}
