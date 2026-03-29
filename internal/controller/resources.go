package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// applyMiddleware creates or updates the Traefik ForwardAuth Middleware.
func (r *Reconciler) applyMiddleware(ctx context.Context, so *unstructured.Unstructured, name, ns string) error {
	mw := &unstructured.Unstructured{}
	mw.SetGroupVersionKind(middlewareGVK)
	mw.SetName("x402-" + name)
	mw.SetNamespace(ns)

	_ = unstructured.SetNestedMap(mw.Object, map[string]interface{}{
		"forwardAuth": map[string]interface{}{
			"address": fmt.Sprintf("http://x402-verifier.%s.svc.cluster.local:8080/verify", verifierNamespace),
			"authResponseHeaders": []interface{}{
				"X-Payment-Status",
				"X-Payment-Tx",
				"Authorization",
			},
		},
	}, "spec")

	setOwnerRef(mw, so)
	return r.createOrUpdate(ctx, mw)
}

// applyPaymentRoute creates or updates the PaymentRoute CR.
// This replaces the old ConfigMap string mutation approach.
func (r *Reconciler) applyPaymentRoute(ctx context.Context, so *unstructured.Unstructured, spec *offerSpec, name, ns string) error {
	path := spec.Path
	if path == "" {
		path = "/services/" + name
	}

	pr := &unstructured.Unstructured{}
	pr.SetGroupVersionKind(paymentRouteGVK)
	pr.SetName(name + "-payment")
	pr.SetNamespace(verifierNamespace)

	prSpec := map[string]interface{}{
		"pattern":     path + "/*",
		"price":       spec.Payment.Price.effectiveRequestPrice(),
		"payTo":       spec.Payment.PayTo,
		"network":     spec.Payment.Network,
		"description": fmt.Sprintf("ServiceOffer %s", name),
		"priceModel":  spec.Payment.Price.priceModel(),
	}

	if spec.Payment.Price.PerMTok != "" {
		prSpec["perMTok"] = spec.Payment.Price.PerMTok
		prSpec["approxTokensPerRequest"] = int64(1000)
	}

	_ = unstructured.SetNestedMap(pr.Object, prSpec, "spec")
	setOwnerRef(pr, so)
	return r.createOrUpdate(ctx, pr)
}

// applyHTTPRoute creates or updates the Gateway API HTTPRoute.
func (r *Reconciler) applyHTTPRoute(ctx context.Context, so *unstructured.Unstructured, spec *offerSpec, name, ns, path string) error {
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(httpRouteGVK)
	route.SetName("so-" + name)
	route.SetNamespace(ns)

	_ = unstructured.SetNestedField(route.Object, map[string]interface{}{
		"parentRefs": []interface{}{
			map[string]interface{}{
				"name":        gatewayName,
				"namespace":   gatewayNamespace,
				"sectionName": gatewaySectionWeb,
			},
		},
		"rules": []interface{}{
			map[string]interface{}{
				"matches": []interface{}{
					map[string]interface{}{
						"path": map[string]interface{}{
							"type":  "PathPrefix",
							"value": path,
						},
					},
				},
				"filters": []interface{}{
					map[string]interface{}{
						"type": "ExtensionRef",
						"extensionRef": map[string]interface{}{
							"group": "traefik.io",
							"kind":  "Middleware",
							"name":  "x402-" + name,
						},
					},
					map[string]interface{}{
						"type": "URLRewrite",
						"urlRewrite": map[string]interface{}{
							"path": map[string]interface{}{
								"type":               "ReplacePrefixMatch",
								"replacePrefixMatch": "/",
							},
						},
					},
				},
				"backendRefs": []interface{}{
					map[string]interface{}{
						"name":      spec.Upstream.Service,
						"namespace": spec.Upstream.Namespace,
						"port":      spec.Upstream.Port,
					},
				},
			},
		},
	}, "spec")

	setOwnerRef(route, so)
	return r.createOrUpdate(ctx, route)
}

// createOrUpdate performs a get-then-create-or-update for an unstructured resource.
func (r *Reconciler) createOrUpdate(ctx context.Context, obj *unstructured.Unstructured) error {
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(obj.GroupVersionKind())

	err := r.Get(ctx, key, existing)
	if err != nil {
		// Not found — create.
		return r.Create(ctx, obj)
	}

	// Found — update (preserve resourceVersion).
	obj.SetResourceVersion(existing.GetResourceVersion())
	return r.Update(ctx, obj)
}

// setOwnerRef sets the ServiceOffer as the controller owner of a child resource.
func setOwnerRef(child, owner *unstructured.Unstructured) {
	refs := []interface{}{
		map[string]interface{}{
			"apiVersion":         "obol.org/v1alpha1",
			"kind":               "ServiceOffer",
			"name":               owner.GetName(),
			"uid":                string(owner.GetUID()),
			"blockOwnerDeletion": true,
			"controller":         true,
		},
	}
	_ = unstructured.SetNestedSlice(child.Object, refs, "metadata", "ownerReferences")
}
