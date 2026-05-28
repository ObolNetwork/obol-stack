package serviceoffercontroller

import (
	"context"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
)

func TestResolveAgentOffer_PopulatesFromReadyAgent(t *testing.T) {
	agent := &monetizeapi.Agent{
		TypeMeta:   metav1.TypeMeta{APIVersion: "obol.org/v1alpha1", Kind: "Agent"},
		ObjectMeta: metav1.ObjectMeta{Name: "quant", Namespace: "agent-quant"},
		Spec: monetizeapi.AgentSpec{
			Runtime: "hermes",
			Skills:  []string{"addresses", "gas"},
		},
		Status: monetizeapi.AgentStatus{
			Phase:       monetizeapi.AgentPhaseReady,
			PinnedModel: "qwen3.5:9b",
			Endpoint:    "http://hermes.agent-quant.svc.cluster.local:8642",
		},
	}

	c := newResolverTestController(t, agent)

	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Namespace: "agent-quant"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "agent",
			Agent: monetizeapi.ServiceOfferAgent{
				Ref: monetizeapi.ServiceOfferAgentRef{Name: "quant", Namespace: "agent-quant"},
			},
		},
	}
	var status monetizeapi.ServiceOfferStatus

	ok, err := c.resolveAgentOffer(context.Background(), offer, &status)
	if err != nil {
		t.Fatalf("resolveAgentOffer: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for a ready agent")
	}

	res := status.AgentResolution
	if res == nil {
		t.Fatal("AgentResolution not populated")
	}
	if res.Model != "qwen3.5:9b" {
		t.Errorf("model = %q, want qwen3.5:9b", res.Model)
	}
	if res.Endpoint != agent.Status.Endpoint {
		t.Errorf("endpoint = %q, want %q", res.Endpoint, agent.Status.Endpoint)
	}
	if res.Runtime != "hermes" {
		t.Errorf("runtime = %q, want hermes", res.Runtime)
	}
	if !equalSlice(res.Skills, []string{"addresses", "gas"}) {
		t.Errorf("skills = %v, want [addresses gas]", res.Skills)
	}

	// Spec must be synthesised in-memory so the rest of the reconcile
	// pipeline runs unchanged.
	if offer.Spec.Upstream.Service != "hermes" || offer.Spec.Upstream.Namespace != "agent-quant" {
		t.Errorf("upstream not synthesised: %+v", offer.Spec.Upstream)
	}
	if offer.Spec.Model.Name != "qwen3.5:9b" {
		t.Errorf("model name not synthesised: %q", offer.Spec.Model.Name)
	}
}

func TestResolveAgentOffer_NotReadyAgentClearsResolution(t *testing.T) {
	agent := &monetizeapi.Agent{
		TypeMeta:   metav1.TypeMeta{APIVersion: "obol.org/v1alpha1", Kind: "Agent"},
		ObjectMeta: metav1.ObjectMeta{Name: "quant", Namespace: "agent-quant"},
		Status:     monetizeapi.AgentStatus{Phase: monetizeapi.AgentPhaseProvisioning},
	}
	c := newResolverTestController(t, agent)

	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Namespace: "agent-quant"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "agent",
			Agent: monetizeapi.ServiceOfferAgent{
				Ref: monetizeapi.ServiceOfferAgentRef{Name: "quant", Namespace: "agent-quant"},
			},
		},
	}
	// Pretend a previous reconcile populated AgentResolution; the
	// transition to not-ready must clear it so RouteRules don't keep
	// surfacing stale model/skill metadata.
	status := monetizeapi.ServiceOfferStatus{
		AgentResolution: &monetizeapi.ServiceOfferAgentResolution{Model: "old"},
	}

	ok, err := c.resolveAgentOffer(context.Background(), offer, &status)
	if err != nil {
		t.Fatalf("resolveAgentOffer: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for a non-ready agent")
	}
	if status.AgentResolution != nil {
		t.Errorf("stale AgentResolution not cleared: %+v", status.AgentResolution)
	}
}

func TestResolveAgentOffer_MissingAgentReturnsNotReady(t *testing.T) {
	c := newResolverTestController(t)

	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Namespace: "agent-missing"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "agent",
			Agent: monetizeapi.ServiceOfferAgent{
				Ref: monetizeapi.ServiceOfferAgentRef{Name: "missing", Namespace: "agent-missing"},
			},
		},
	}
	var status monetizeapi.ServiceOfferStatus

	ok, err := c.resolveAgentOffer(context.Background(), offer, &status)
	if err != nil {
		t.Fatalf("resolveAgentOffer: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false when agent does not exist")
	}
	if status.AgentResolution != nil {
		t.Errorf("AgentResolution unexpectedly populated: %+v", status.AgentResolution)
	}
}

func TestResolveAgentOffer_RejectsMissingRef(t *testing.T) {
	c := newResolverTestController(t)

	offer := &monetizeapi.ServiceOffer{
		Spec: monetizeapi.ServiceOfferSpec{Type: "agent"},
	}
	var status monetizeapi.ServiceOfferStatus

	if _, err := c.resolveAgentOffer(context.Background(), offer, &status); err == nil {
		t.Fatal("expected error for missing spec.agent.ref")
	}
}

// TestResolveAgentOffer_RejectsCrossNamespaceRef guards the confused-deputy
// invariant: an offer in namespace A must not be allowed to reference an agent
// in namespace B, because the verifier route source injects ref.Namespace's
// hermes-api-server API_SERVER_KEY as the upstream Authorization. Allowing a
// cross-namespace ref would let any principal with serviceoffers write expose
// another tenant's Hermes /api as an x402-gated route under attacker-controlled
// path + payTo.
func TestResolveAgentOffer_RejectsCrossNamespaceRef(t *testing.T) {
	agent := &monetizeapi.Agent{
		TypeMeta:   metav1.TypeMeta{APIVersion: "obol.org/v1alpha1", Kind: "Agent"},
		ObjectMeta: metav1.ObjectMeta{Name: "victim", Namespace: "agent-victim"},
		Status: monetizeapi.AgentStatus{
			Phase:    monetizeapi.AgentPhaseReady,
			Endpoint: "http://hermes.agent-victim.svc.cluster.local:8642",
		},
	}
	c := newResolverTestController(t, agent)

	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "spoof", Namespace: "attacker-ns"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "agent",
			Agent: monetizeapi.ServiceOfferAgent{
				Ref: monetizeapi.ServiceOfferAgentRef{Name: "victim", Namespace: "agent-victim"},
			},
		},
	}
	status := monetizeapi.ServiceOfferStatus{
		AgentResolution: &monetizeapi.ServiceOfferAgentResolution{Model: "stale"},
	}

	ok, err := c.resolveAgentOffer(context.Background(), offer, &status)
	if err == nil {
		t.Fatal("expected error for cross-namespace spec.agent.ref")
	}
	if ok {
		t.Fatal("expected ok=false for cross-namespace ref")
	}
	if status.AgentResolution == nil || status.AgentResolution.Model != "stale" {
		// Guard fires before touching status: the caller is responsible for
		// the failure-mode condition update, and we should not silently wipe
		// a prior AgentResolution.
		t.Errorf("guard must reject without mutating status.AgentResolution; got %+v", status.AgentResolution)
	}
}

func newResolverTestController(t *testing.T, agents ...*monetizeapi.Agent) *Controller {
	t.Helper()
	objs := make([]runtime.Object, 0, len(agents))
	for _, a := range agents {
		objs = append(objs, mustAgentObject(t, a))
	}
	dynClient := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{monetizeapi.AgentGVR: "AgentList"},
		objs...,
	)
	return &Controller{
		dynClient: dynClient,
		client:    dynClient,
		agents:    dynClient.Resource(monetizeapi.AgentGVR),
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// suppress unused-import warning if other helpers shrink down.
var _ = unstructured.Unstructured{}
