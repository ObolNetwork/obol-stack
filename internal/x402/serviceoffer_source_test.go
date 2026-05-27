package x402

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestRoutesFromStore(t *testing.T) {
	items := []any{
		mustOfferObject(t, monetizeapi.ServiceOffer{
			ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "beta"},
			Spec: monetizeapi.ServiceOfferSpec{
				Upstream: monetizeapi.ServiceOfferUpstream{Service: "httpbin"},
				Payment: monetizeapi.ServiceOfferPayment{
					Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.2"},
				},
			},
			Status: monetizeapi.ServiceOfferStatus{
				Conditions: []monetizeapi.Condition{{Type: "RoutePublished", Status: "True"}},
			},
		}),
		mustOfferObject(t, monetizeapi.ServiceOffer{
			ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "alpha"},
			Spec: monetizeapi.ServiceOfferSpec{
				Upstream: monetizeapi.ServiceOfferUpstream{Service: "litellm"},
				Payment: monetizeapi.ServiceOfferPayment{
					PayTo: "0x1111111111111111111111111111111111111111",
					Price: monetizeapi.ServiceOfferPriceTable{PerMTok: "2.5"},
				},
			},
			Status: monetizeapi.ServiceOfferStatus{
				Conditions: []monetizeapi.Condition{{Type: "RoutePublished", Status: "True"}},
			},
		}),
		// Drain-expired offer: drainAt + zero grace period in the past
		// → route should already be torn down, and the verifier rule
		// should be filtered out even though RoutePublished is still
		// True in the cached status snapshot.
		mustOfferObject(t, monetizeapi.ServiceOffer{
			ObjectMeta: metav1.ObjectMeta{Name: "drained", Namespace: "alpha"},
			Spec: monetizeapi.ServiceOfferSpec{
				Upstream:         monetizeapi.ServiceOfferUpstream{Service: "httpbin"},
				DrainAt:          &metav1.Time{Time: time.Now().Add(-2 * time.Hour)},
				DrainGracePeriod: &metav1.Duration{Duration: time.Hour},
				Payment: monetizeapi.ServiceOfferPayment{
					Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "1"},
				},
			},
			Status: monetizeapi.ServiceOfferStatus{
				Conditions: []monetizeapi.Condition{{Type: "RoutePublished", Status: "True"}},
			},
		}),
		// Mid-drain offer: drainAt = now, grace = 1h → still within the
		// drain window, route stays up so in-flight buyers can settle.
		// Should appear in the verifier rules.
		mustOfferObject(t, monetizeapi.ServiceOffer{
			ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "alpha"},
			Spec: monetizeapi.ServiceOfferSpec{
				Upstream:         monetizeapi.ServiceOfferUpstream{Service: "httpbin"},
				DrainAt:          &metav1.Time{Time: time.Now()},
				DrainGracePeriod: &metav1.Duration{Duration: time.Hour},
				Payment: monetizeapi.ServiceOfferPayment{
					Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.1"},
				},
			},
			Status: monetizeapi.ServiceOfferStatus{
				Conditions: []monetizeapi.Condition{{Type: "RoutePublished", Status: "True"}},
			},
		}),
	}
	secrets := []any{
		mustSecretObject(t, "alpha", "litellm-secrets", map[string]string{
			"LITELLM_MASTER_KEY": base64.StdEncoding.EncodeToString([]byte("sk-test")),
		}),
	}

	routes, err := routesFromStore(items, secrets)
	if err != nil {
		t.Fatalf("routesFromStore: %v", err)
	}

	if len(routes) != 3 {
		t.Fatalf("len(routes) = %d, want 3", len(routes))
	}
	// Expected sort order: alpha/a, alpha/c, beta/b.
	// "drained" must be filtered out because its drain window expired.
	if routes[0].OfferName != "a" || routes[1].OfferName != "c" || routes[2].OfferName != "b" {
		t.Fatalf("routes not sorted by offer identity (drained leaked?): %+v", routes)
	}
	if routes[0].OfferNamespace != "alpha" || routes[1].OfferNamespace != "alpha" || routes[2].OfferNamespace != "beta" {
		t.Fatalf("unexpected route namespaces: %+v", routes)
	}
	if routes[0].Pattern != "/services/a/*" {
		t.Fatalf("routes[0].Pattern = %q, want /services/a/*", routes[0].Pattern)
	}
	if routes[0].Price != "0.0025" {
		t.Fatalf("routes[0].Price = %q, want 0.0025", routes[0].Price)
	}
	if routes[0].UpstreamAuth != "Bearer sk-test" {
		t.Fatalf("routes[0].UpstreamAuth = %q, want Bearer sk-test", routes[0].UpstreamAuth)
	}
	if routes[0].UpstreamURL != "http://litellm.alpha.svc.cluster.local:11434" {
		t.Fatalf("routes[0].UpstreamURL = %q, want litellm upstream URL", routes[0].UpstreamURL)
	}
	if routes[0].StripPrefix != "/services/a" {
		t.Fatalf("routes[0].StripPrefix = %q, want /services/a", routes[0].StripPrefix)
	}
	if routes[2].UpstreamAuth != "" {
		t.Fatalf("routes[2].UpstreamAuth = %q, want empty", routes[2].UpstreamAuth)
	}
	if routes[2].UpstreamURL != "http://httpbin.beta.svc.cluster.local:11434" {
		t.Fatalf("routes[2].UpstreamURL = %q, want httpbin upstream URL", routes[2].UpstreamURL)
	}
	// Mid-drain offer "c" stays in the rules but tracks its own
	// upstream — verifies the drain window keeps the route alive.
	if routes[1].UpstreamURL != "http://httpbin.alpha.svc.cluster.local:11434" {
		t.Fatalf("routes[1] (mid-drain) UpstreamURL = %q, want httpbin upstream URL", routes[1].UpstreamURL)
	}
}

func TestRoutesFromStore_IgnoresUnpublishedOffers(t *testing.T) {
	items := []any{
		mustOfferObject(t, monetizeapi.ServiceOffer{
			ObjectMeta: metav1.ObjectMeta{Name: "draft", Namespace: "llm"},
			Spec: monetizeapi.ServiceOfferSpec{
				Payment: monetizeapi.ServiceOfferPayment{
					Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.1"},
				},
			},
			Status: monetizeapi.ServiceOfferStatus{
				Conditions: []monetizeapi.Condition{{Type: "PaymentGateReady", Status: "True"}},
			},
		}),
	}

	routes, err := routesFromStore(items, nil)
	if err != nil {
		t.Fatalf("routesFromStore: %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("len(routes) = %d, want 0", len(routes))
	}
}

func TestRouteRuleFromOffer_AgentResolutionAdvertisesRuntimeModelSkills(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-quant", Namespace: "agent-demo-quant"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "agent",
			Agent: monetizeapi.ServiceOfferAgent{
				Ref: monetizeapi.ServiceOfferAgentRef{Name: "demo-quant", Namespace: "agent-demo-quant"},
			},
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "ethereum",
				PayTo:   "0x1111111111111111111111111111111111111111",
				Asset: monetizeapi.ServiceOfferAsset{
					Symbol:   "OBOL",
					Decimals: 18,
				},
				Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "10"},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{
			AgentResolution: &monetizeapi.ServiceOfferAgentResolution{
				Model:    "qwen3.5:9b",
				Runtime:  "hermes",
				Skills:   []string{"ethereum-networks", "addresses"},
				Endpoint: "http://hermes.agent-demo-quant.svc.cluster.local:8642",
			},
		},
	}

	route, err := routeRuleFromOffer(offer, "")
	if err != nil {
		t.Fatalf("routeRuleFromOffer: %v", err)
	}
	if route.Price != "10" {
		t.Errorf("Price = %q, want 10", route.Price)
	}
	if route.AgentModel != "qwen3.5:9b" {
		t.Errorf("AgentModel = %q, want qwen3.5:9b", route.AgentModel)
	}
	if route.AgentRuntime != "hermes" {
		t.Errorf("AgentRuntime = %q, want hermes", route.AgentRuntime)
	}
	if len(route.AgentSkills) != 2 || route.AgentSkills[0] != "ethereum-networks" {
		t.Errorf("AgentSkills = %v", route.AgentSkills)
	}
	if route.UpstreamURL != "http://hermes.agent-demo-quant.svc.cluster.local:8642" {
		t.Errorf("UpstreamURL = %q", route.UpstreamURL)
	}
	if route.Pattern != "/services/demo-quant/*" {
		t.Errorf("Pattern = %q, want /services/demo-quant/*", route.Pattern)
	}
}

func TestRoutesFromStore_AgentOfferInjectsHermesAPIKey(t *testing.T) {
	items := []any{
		mustOfferObject(t, monetizeapi.ServiceOffer{
			ObjectMeta: metav1.ObjectMeta{Name: "demo-quant", Namespace: "seller"},
			Spec: monetizeapi.ServiceOfferSpec{
				Type: "agent",
				Agent: monetizeapi.ServiceOfferAgent{
					Ref: monetizeapi.ServiceOfferAgentRef{Name: "demo-quant", Namespace: "agent-demo-quant"},
				},
				Payment: monetizeapi.ServiceOfferPayment{
					PayTo: "0x1111111111111111111111111111111111111111",
					Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "10"},
				},
			},
			Status: monetizeapi.ServiceOfferStatus{
				Conditions: []monetizeapi.Condition{{Type: "RoutePublished", Status: "True"}},
				AgentResolution: &monetizeapi.ServiceOfferAgentResolution{
					Model:    "qwen3.5:9b",
					Runtime:  "hermes",
					Endpoint: "http://hermes.agent-demo-quant.svc.cluster.local:8642",
				},
			},
		}),
	}
	secrets := []any{
		mustSecretObject(t, "agent-demo-quant", "hermes-api-server", map[string]string{
			"API_SERVER_KEY": base64.StdEncoding.EncodeToString([]byte("agent-api-key")),
		}),
		mustSecretObject(t, "seller", "litellm-secrets", map[string]string{
			"LITELLM_MASTER_KEY": base64.StdEncoding.EncodeToString([]byte("wrong-secret")),
		}),
	}

	routes, err := routesFromStore(items, secrets)
	if err != nil {
		t.Fatalf("routesFromStore: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("len(routes) = %d, want 1", len(routes))
	}
	if routes[0].UpstreamAuth != "Bearer agent-api-key" {
		t.Fatalf("agent UpstreamAuth = %q, want Bearer agent-api-key", routes[0].UpstreamAuth)
	}
}

func TestRoutesFromStore_AgentAuthUsesReferencedAgentNamespace(t *testing.T) {
	items := []any{
		mustOfferObject(t, monetizeapi.ServiceOffer{
			ObjectMeta: metav1.ObjectMeta{Name: "cross-ns-agent", Namespace: "seller-ns"},
			Spec: monetizeapi.ServiceOfferSpec{
				Type: "agent",
				Agent: monetizeapi.ServiceOfferAgent{
					Ref: monetizeapi.ServiceOfferAgentRef{Name: "quant", Namespace: "agent-quant"},
				},
				Payment: monetizeapi.ServiceOfferPayment{
					PayTo: "0x1111111111111111111111111111111111111111",
					Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "1"},
				},
			},
			Status: monetizeapi.ServiceOfferStatus{
				Conditions: []monetizeapi.Condition{{Type: "RoutePublished", Status: "True"}},
				AgentResolution: &monetizeapi.ServiceOfferAgentResolution{
					Model:    "qwen3.5:9b",
					Runtime:  "hermes",
					Endpoint: "http://hermes.agent-quant.svc.cluster.local:8642",
				},
			},
		}),
	}
	secrets := []any{
		mustSecretObject(t, "seller-ns", "hermes-api-server", map[string]string{
			"API_SERVER_KEY": base64.StdEncoding.EncodeToString([]byte("seller-ns-key")),
		}),
		mustSecretObject(t, "agent-quant", "hermes-api-server", map[string]string{
			"API_SERVER_KEY": base64.StdEncoding.EncodeToString([]byte("agent-ns-key")),
		}),
	}

	routes, err := routesFromStore(items, secrets)
	if err != nil {
		t.Fatalf("routesFromStore: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("len(routes) = %d, want 1", len(routes))
	}
	if routes[0].UpstreamAuth != "Bearer agent-ns-key" {
		t.Fatalf("agent UpstreamAuth = %q, want referenced agent namespace key", routes[0].UpstreamAuth)
	}
}

func mustOfferObject(t *testing.T, offer monetizeapi.ServiceOffer) *unstructured.Unstructured {
	t.Helper()
	offer.TypeMeta = metav1.TypeMeta{
		APIVersion: monetizeapi.Group + "/" + monetizeapi.Version,
		Kind:       monetizeapi.ServiceOfferKind,
	}
	data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&offer)
	if err != nil {
		t.Fatalf("ToUnstructured: %v", err)
	}
	return &unstructured.Unstructured{Object: data}
}

func mustSecretObject(t *testing.T, namespace, name string, data map[string]string) *unstructured.Unstructured {
	t.Helper()
	values := make(map[string]any, len(data))
	for key, value := range data {
		values[key] = value
	}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"data": values,
	}}
	return obj
}
