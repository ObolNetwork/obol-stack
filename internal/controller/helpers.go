package controller

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// offerSpec is a lightweight parsed view of a ServiceOffer spec.
// We use unstructured access to avoid generating CRD types — KISS.
type offerSpec struct {
	Path     string
	Upstream upstreamSpec
	Payment  paymentSpec
}

type upstreamSpec struct {
	Service    string
	Namespace  string
	Port       int64
	HealthPath string
}

type paymentSpec struct {
	Network string
	PayTo   string
	Price   priceTable
}

type priceTable struct {
	PerRequest string
	PerMTok    string
	PerHour    string
}

// effectiveRequestPrice returns the per-request price for x402 gating.
func (p priceTable) effectiveRequestPrice() string {
	if p.PerRequest != "" {
		return p.PerRequest
	}
	if p.PerMTok != "" {
		return p.PerMTok // simplified; real conversion in schemas package
	}
	if p.PerHour != "" {
		return p.PerHour
	}
	return "0"
}

// priceModel returns the pricing model string for the PaymentRoute CR.
func (p priceTable) priceModel() string {
	if p.PerRequest != "" {
		return "per-request"
	}
	if p.PerMTok != "" {
		return "per-mtok"
	}
	if p.PerHour != "" {
		return "per-hour"
	}
	return "per-request"
}

// getSpec extracts a typed spec from an unstructured ServiceOffer.
func getSpec(so *unstructured.Unstructured) (*offerSpec, error) {
	spec, ok, _ := unstructured.NestedMap(so.Object, "spec")
	if !ok {
		return nil, fmt.Errorf("missing spec")
	}

	upstream, _, _ := unstructured.NestedMap(spec, "upstream")
	payment, _, _ := unstructured.NestedMap(spec, "payment")
	price, _, _ := unstructured.NestedMap(payment, "price")

	path, _, _ := unstructured.NestedString(spec, "path")
	svc, _, _ := unstructured.NestedString(upstream, "service")
	ns, _, _ := unstructured.NestedString(upstream, "namespace")
	port, _, _ := unstructured.NestedInt64(upstream, "port")
	healthPath, _, _ := unstructured.NestedString(upstream, "healthPath")
	network, _, _ := unstructured.NestedString(payment, "network")
	payTo, _, _ := unstructured.NestedString(payment, "payTo")
	perRequest, _, _ := unstructured.NestedString(price, "perRequest")
	perMTok, _, _ := unstructured.NestedString(price, "perMTok")
	perHour, _, _ := unstructured.NestedString(price, "perHour")

	if svc == "" || ns == "" || port == 0 {
		return nil, fmt.Errorf("upstream requires service, namespace, and port")
	}

	return &offerSpec{
		Path: path,
		Upstream: upstreamSpec{
			Service:    svc,
			Namespace:  ns,
			Port:       port,
			HealthPath: healthPath,
		},
		Payment: paymentSpec{
			Network: network,
			PayTo:   payTo,
			Price: priceTable{
				PerRequest: perRequest,
				PerMTok:    perMTok,
				PerHour:    perHour,
			},
		},
	}, nil
}

// setCondition upserts a condition on the ServiceOffer status.
func setCondition(so *unstructured.Unstructured, condType string, ok bool, message string) {
	status := "False"
	reason := "NotReady"
	if ok {
		status = "True"
		reason = "Ready"
	}

	conditions, _, _ := unstructured.NestedSlice(so.Object, "status", "conditions")

	now := time.Now().UTC().Format(time.RFC3339)
	newCond := map[string]interface{}{
		"type":               condType,
		"status":             status,
		"reason":             reason,
		"message":            message,
		"lastTransitionTime": now,
	}

	// Update existing or append.
	found := false
	for i, c := range conditions {
		cond, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if cond["type"] == condType {
			// Only update lastTransitionTime if status changed.
			if cond["status"] != status {
				conditions[i] = newCond
			} else {
				cond["reason"] = reason
				cond["message"] = message
				conditions[i] = cond
			}
			found = true
			break
		}
	}
	if !found {
		conditions = append(conditions, newCond)
	}

	_ = unstructured.SetNestedSlice(so.Object, conditions, "status", "conditions")
}

// setNestedField is a convenience wrapper.
func setNestedField(so *unstructured.Unstructured, value interface{}, fields ...string) {
	_ = unstructured.SetNestedField(so.Object, value, fields...)
}

// computePhase returns the overall phase based on conditions.
func computePhase(so *unstructured.Unstructured) string {
	conditions, _, _ := unstructured.NestedSlice(so.Object, "status", "conditions")
	for _, c := range conditions {
		cond, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if cond["type"] == condReady && cond["status"] == "True" {
			return "Ready"
		}
	}
	return "Reconciling"
}

// withGVK returns a builder option that sets the GVK for unstructured watches.
func withGVK(gvk schema.GroupVersionKind) builder.ForOption {
	return builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			return false
		}
		return u.GroupVersionKind() == gvk
	}))
}
