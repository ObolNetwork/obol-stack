package controller

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ensureMiddleware creates or updates the Traefik ForwardAuth Middleware.
func (r *Reconciler) ensureMiddleware(ctx context.Context, so *unstructured.Unstructured, name, ns string) error {
	mw := &unstructured.Unstructured{}
	mw.SetGroupVersionKind(middlewareGVK)
	mw.SetName("x402-" + name)
	mw.SetNamespace(ns)

	_ = unstructured.SetNestedMap(mw.Object, map[string]interface{}{
		"forwardAuth": map[string]interface{}{
			"address": "http://x402-verifier.x402.svc.cluster.local:8080/verify",
			"authResponseHeaders": []interface{}{
				"X-Payment-Status",
				"X-Payment-Tx",
				"Authorization",
			},
		},
	}, "spec")

	setOwnerRef(mw, so)

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(middlewareGVK)
	err := r.Get(ctx, types.NamespacedName{Name: mw.GetName(), Namespace: ns}, existing)
	if err != nil {
		return r.Create(ctx, mw)
	}
	mw.SetResourceVersion(existing.GetResourceVersion())
	return r.Update(ctx, mw)
}

// ensureHTTPRoute creates or updates the Gateway API HTTPRoute.
func (r *Reconciler) ensureHTTPRoute(ctx context.Context, so *unstructured.Unstructured, spec *offerSpec, name, ns string) error {
	path := spec.Path
	if path == "" {
		path = "/services/" + name
	}

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

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(httpRouteGVK)
	err := r.Get(ctx, types.NamespacedName{Name: route.GetName(), Namespace: ns}, existing)
	if err != nil {
		return r.Create(ctx, route)
	}
	route.SetResourceVersion(existing.GetResourceVersion())
	return r.Update(ctx, route)
}

// ensurePricingRoute adds a route entry to the x402-pricing ConfigMap.
func (r *Reconciler) ensurePricingRoute(ctx context.Context, so *unstructured.Unstructured, spec *offerSpec, name, ns string) error {
	path := spec.Path
	if path == "" {
		path = "/services/" + name
	}
	pattern := path + "/*"

	cm := &unstructured.Unstructured{}
	cm.SetGroupVersionKind(configMapGVK)
	if err := r.Get(ctx, types.NamespacedName{Name: verifierConfigMap, Namespace: verifierNamespace}, cm); err != nil {
		return fmt.Errorf("get x402-pricing ConfigMap: %w", err)
	}

	data, _, _ := unstructured.NestedStringMap(cm.Object, "data")
	if data == nil {
		data = map[string]string{}
	}

	pricingYAML := data["pricing.yaml"]

	// Check if route already exists.
	if strings.Contains(pricingYAML, fmt.Sprintf("pattern: %q", pattern)) {
		return nil // already present
	}

	// Build route entry.
	price := spec.Payment.Price.effectiveRequestPrice()
	entry := buildRouteEntry(pattern, price, name, ns, spec)

	// Append to routes section.
	if strings.Contains(pricingYAML, "routes: []") {
		pricingYAML = strings.Replace(pricingYAML, "routes: []", "routes:\n"+entry, 1)
	} else if strings.Contains(pricingYAML, "routes:") {
		pricingYAML += entry
	} else {
		pricingYAML += "\nroutes:\n" + entry
	}

	data["pricing.yaml"] = pricingYAML
	_ = unstructured.SetNestedStringMap(cm.Object, data, "data")

	return r.Update(ctx, cm)
}

// removePricingRoute removes a route entry from the x402-pricing ConfigMap.
func (r *Reconciler) removePricingRoute(ctx context.Context, path, name string) error {
	logger := log.FromContext(ctx)

	cm := &unstructured.Unstructured{}
	cm.SetGroupVersionKind(configMapGVK)
	if err := r.Get(ctx, types.NamespacedName{Name: verifierConfigMap, Namespace: verifierNamespace}, cm); err != nil {
		logger.Error(err, "failed to get x402-pricing ConfigMap for cleanup")
		return err
	}

	data, _, _ := unstructured.NestedStringMap(cm.Object, "data")
	if data == nil {
		return nil
	}

	pricingYAML := data["pricing.yaml"]
	pattern := path + "/*"

	// Remove the route block starting with this pattern.
	lines := strings.Split(pricingYAML, "\n")
	var filtered []string
	skip := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, fmt.Sprintf("pattern: %q", pattern)) {
			skip = true
			continue
		}
		if skip {
			// Skip continuation lines (indented fields of the same route).
			if strings.HasPrefix(trimmed, "- pattern:") || trimmed == "" || !strings.HasPrefix(line, "    ") {
				skip = false
			} else {
				continue
			}
		}
		if !skip {
			filtered = append(filtered, line)
		}
	}

	result := strings.Join(filtered, "\n")
	// If no routes remain, set empty list.
	if !strings.Contains(result, "- pattern:") && strings.Contains(result, "routes:") {
		result = strings.Replace(result, "routes:", "routes: []", 1)
	}

	data["pricing.yaml"] = result
	_ = unstructured.SetNestedStringMap(cm.Object, data, "data")

	return r.Update(ctx, cm)
}

// buildRouteEntry builds a YAML route entry for the pricing ConfigMap.
func buildRouteEntry(pattern, price, name, ns string, spec *offerSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  - pattern: %q\n", pattern)
	fmt.Fprintf(&b, "    price: %q\n", price)
	fmt.Fprintf(&b, "    description: \"ServiceOffer %s\"\n", name)
	if spec.Payment.PayTo != "" {
		fmt.Fprintf(&b, "    payTo: %q\n", spec.Payment.PayTo)
	}
	if spec.Payment.Network != "" {
		fmt.Fprintf(&b, "    network: %q\n", spec.Payment.Network)
	}
	fmt.Fprintf(&b, "    priceModel: %q\n", spec.Payment.Price.priceModel())
	if spec.Payment.Price.PerMTok != "" {
		fmt.Fprintf(&b, "    perMTok: %q\n", spec.Payment.Price.PerMTok)
		fmt.Fprintf(&b, "    approxTokensPerRequest: 1000\n")
	}
	fmt.Fprintf(&b, "    offerNamespace: %q\n", ns)
	fmt.Fprintf(&b, "    offerName: %q\n", name)
	return b.String()
}

// setOwnerRef sets the ServiceOffer as the controller owner of a child resource.
func setOwnerRef(child, owner *unstructured.Unstructured) {
	_ = controllerutil.SetControllerReference(owner, child, nil)
	// controllerutil needs a scheme; since we're unstructured, set manually.
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

var (
	middlewareGVK = schema.GroupVersionKind{
		Group:   "traefik.io",
		Version: "v1alpha1",
		Kind:    "Middleware",
	}
	httpRouteGVK = schema.GroupVersionKind{
		Group:   "gateway.networking.k8s.io",
		Version: "v1",
		Kind:    "HTTPRoute",
	}
	configMapGVK = schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "ConfigMap",
	}
)
