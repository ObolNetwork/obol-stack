// Package source provides config sources for the x402 verifier.
// PaymentRouteSource watches PaymentRoute CRDs and builds the verifier's
// PricingConfig from them, replacing the old ConfigMap file watcher.
package source

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	x402 "github.com/ObolNetwork/obol-stack/internal/x402"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

var paymentRouteGVR = schema.GroupVersionResource{
	Group:    "obol.org",
	Version:  "v1alpha1",
	Resource: "paymentroutes",
}

// PaymentRouteSource watches PaymentRoute CRs and rebuilds the verifier
// config whenever routes change. It replaces the file-based WatchConfig.
type PaymentRouteSource struct {
	verifier  *x402.Verifier
	client    dynamic.Interface
	namespace string // watch namespace, "" for all namespaces

	mu     sync.RWMutex
	routes map[string]x402.RouteRule // key: CR name
}

// NewPaymentRouteSource creates a source that watches PaymentRoute CRs.
func NewPaymentRouteSource(client dynamic.Interface, verifier *x402.Verifier, namespace string) *PaymentRouteSource {
	return &PaymentRouteSource{
		verifier:  verifier,
		client:    client,
		namespace: namespace,
		routes:    make(map[string]x402.RouteRule),
	}
}

// Run starts the informer and blocks until ctx is cancelled.
func (s *PaymentRouteSource) Run(ctx context.Context) error {
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		s.client, 30*time.Second, s.namespace, nil,
	)

	informer := factory.ForResource(paymentRouteGVR).Informer()

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { s.handleEvent(ctx, obj) },
		UpdateFunc: func(_, obj interface{}) { s.handleEvent(ctx, obj) },
		DeleteFunc: func(obj interface{}) { s.handleDelete(ctx, obj) },
	})
	if err != nil {
		return fmt.Errorf("add event handler: %w", err)
	}

	log.Printf("paymentroute-source: watching PaymentRoute CRs in namespace %q", s.namespace)
	informer.Run(ctx.Done())
	return nil
}

func (s *PaymentRouteSource) handleEvent(ctx context.Context, obj interface{}) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}

	rule, err := paymentRouteToRule(u)
	if err != nil {
		log.Printf("paymentroute-source: convert %s: %v", u.GetName(), err)
		return
	}

	s.mu.Lock()
	s.routes[u.GetName()] = rule
	s.mu.Unlock()

	s.rebuildConfig()
	s.markAdmitted(ctx, u)
}

func (s *PaymentRouteSource) handleDelete(ctx context.Context, obj interface{}) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		// Handle DeletedFinalStateUnknown.
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			return
		}
		u, ok = tombstone.Obj.(*unstructured.Unstructured)
		if !ok {
			return
		}
	}

	s.mu.Lock()
	delete(s.routes, u.GetName())
	s.mu.Unlock()

	s.rebuildConfig()
}

func (s *PaymentRouteSource) rebuildConfig() {
	s.mu.RLock()
	routes := make([]x402.RouteRule, 0, len(s.routes))
	for _, r := range s.routes {
		routes = append(routes, r)
	}
	s.mu.RUnlock()

	// Rebuild config preserving global settings from the current config.
	current := s.verifier.Config()
	cfg := &x402.PricingConfig{
		Wallet:         current.Wallet,
		Chain:          current.Chain,
		FacilitatorURL: current.FacilitatorURL,
		VerifyOnly:     current.VerifyOnly,
		Routes:         routes,
	}

	if err := s.verifier.Reload(cfg); err != nil {
		log.Printf("paymentroute-source: reload failed: %v", err)
		return
	}

	log.Printf("paymentroute-source: loaded %d routes from PaymentRoute CRs", len(routes))
}

func (s *PaymentRouteSource) markAdmitted(ctx context.Context, u *unstructured.Unstructured) {
	patch := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "obol.org/v1alpha1",
			"kind":       "PaymentRoute",
			"metadata": map[string]interface{}{
				"name":      u.GetName(),
				"namespace": u.GetNamespace(),
			},
			"status": map[string]interface{}{
				"admitted":               true,
				"lastAdmittedGeneration": u.GetGeneration(),
			},
		},
	}

	_, err := s.client.Resource(paymentRouteGVR).Namespace(u.GetNamespace()).
		UpdateStatus(ctx, patch, metav1.UpdateOptions{})
	if err != nil {
		log.Printf("paymentroute-source: mark admitted %s: %v", u.GetName(), err)
	}
}

func paymentRouteToRule(u *unstructured.Unstructured) (x402.RouteRule, error) {
	spec, ok, _ := unstructured.NestedMap(u.Object, "spec")
	if !ok {
		return x402.RouteRule{}, fmt.Errorf("missing spec")
	}

	pattern, _, _ := unstructured.NestedString(spec, "pattern")
	price, _, _ := unstructured.NestedString(spec, "price")
	payTo, _, _ := unstructured.NestedString(spec, "payTo")
	network, _, _ := unstructured.NestedString(spec, "network")
	facilitatorURL, _, _ := unstructured.NestedString(spec, "facilitatorURL")
	description, _, _ := unstructured.NestedString(spec, "description")
	priceModel, _, _ := unstructured.NestedString(spec, "priceModel")
	perMTok, _, _ := unstructured.NestedString(spec, "perMTok")
	approxTokens, _, _ := unstructured.NestedInt64(spec, "approxTokensPerRequest")
	upstreamAuth, _, _ := unstructured.NestedString(spec, "upstreamAuth")

	if pattern == "" || price == "" {
		return x402.RouteRule{}, fmt.Errorf("pattern and price are required")
	}

	// Derive offer identity from ownerReferences.
	var offerName, offerNS string
	refs, _, _ := unstructured.NestedSlice(u.Object, "metadata", "ownerReferences")
	for _, ref := range refs {
		r, ok := ref.(map[string]interface{})
		if !ok {
			continue
		}
		if kind, _, _ := unstructured.NestedString(r, "kind"); kind == "ServiceOffer" {
			offerName, _, _ = unstructured.NestedString(r, "name")
			break
		}
	}
	offerNS = u.GetNamespace()

	_ = facilitatorURL // stored globally on verifier, not per-route

	return x402.RouteRule{
		Pattern:                pattern,
		Price:                  price,
		Description:            description,
		PayTo:                  payTo,
		Network:                network,
		UpstreamAuth:           upstreamAuth,
		PriceModel:             priceModel,
		PerMTok:                perMTok,
		ApproxTokensPerRequest: int(approxTokens),
		OfferNamespace:         offerNS,
		OfferName:              offerName,
	}, nil
}
