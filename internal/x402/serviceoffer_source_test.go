package x402

import (
	"encoding/base64"
	"testing"

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
		mustOfferObject(t, monetizeapi.ServiceOffer{
			ObjectMeta: metav1.ObjectMeta{Name: "paused", Namespace: "alpha", Annotations: map[string]string{
				monetizeapi.PausedAnnotation: "true",
			}},
			Spec: monetizeapi.ServiceOfferSpec{
				Payment: monetizeapi.ServiceOfferPayment{
					Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "1"},
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

	if len(routes) != 2 {
		t.Fatalf("len(routes) = %d, want 2", len(routes))
	}
	if routes[0].OfferName != "a" || routes[1].OfferName != "b" {
		t.Fatalf("routes not sorted by offer identity: %+v", routes)
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
	if routes[1].UpstreamAuth != "" {
		t.Fatalf("routes[1].UpstreamAuth = %q, want empty", routes[1].UpstreamAuth)
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
