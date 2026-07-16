package serviceoffercontroller

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

// fake.FakeDynamicClient's ObjectTracker doesn't implement server-side-apply
// merge semantics for pure unstructured objects, so applyObject's
// types.ApplyPatchType Patch fails against it. Reactor turns an apply patch
// into create-or-update, leaving any existing .status untouched — matching
// real SSA, which never touches a subresource it doesn't target. Goes
// straight to the Tracker (not back through the client) — routing through
// dynClient itself would re-enter the Fake's non-reentrant action lock and
// deadlock.
func withApplyPatchSupport(dynClient *fake.FakeDynamicClient) *fake.FakeDynamicClient {
	tracker := dynClient.Tracker()
	dynClient.PrependReactor("patch", "*", func(action ktesting.Action) (bool, runtime.Object, error) {
		patchAction := action.(ktesting.PatchAction)
		if patchAction.GetPatchType() != types.ApplyPatchType {
			return false, nil, nil
		}
		var patched map[string]any
		if err := json.Unmarshal(patchAction.GetPatch(), &patched); err != nil {
			return true, nil, err
		}
		gvr := patchAction.GetResource()
		ns := patchAction.GetNamespace()

		existing, err := tracker.Get(gvr, ns, patchAction.GetName())
		if apierrors.IsNotFound(err) {
			obj := &unstructured.Unstructured{Object: patched}
			if err := tracker.Create(gvr, obj, ns); err != nil {
				return true, nil, err
			}
			return true, obj, nil
		}
		if err != nil {
			return true, nil, err
		}
		merged := existing.(*unstructured.Unstructured).DeepCopy()
		for k, v := range patched {
			merged.Object[k] = v
		}
		if err := tracker.Update(gvr, merged, ns); err != nil {
			return true, nil, err
		}
		return true, merged, nil
	})
	return dynClient
}

func controllerForRouteTest(dynClient *fake.FakeDynamicClient) *Controller {
	dynClient = withApplyPatchSupport(dynClient)
	return &Controller{
		dynClient:       dynClient,
		client:          dynClient,
		middlewares:     dynClient.Resource(monetizeapi.MiddlewareGVR),
		httpRoutes:      dynClient.Resource(monetizeapi.HTTPRouteGVR),
		referenceGrants: dynClient.Resource(monetizeapi.ReferenceGrantGVR),
	}
}

func routeListKinds() map[schema.GroupVersionResource]string {
	return map[schema.GroupVersionResource]string{
		monetizeapi.MiddlewareGVR:     "MiddlewareList",
		monetizeapi.HTTPRouteGVR:      "HTTPRouteList",
		monetizeapi.ReferenceGrantGVR: "ReferenceGrantList",
	}
}

// acceptedHTTPRoute seeds an HTTPRoute that Traefik has already reported as
// programmed (status.parents Accepted=True, ResolvedRefs=True).
func acceptedHTTPRoute(namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"status": map[string]any{
			"parents": []any{
				map[string]any{
					"conditions": []any{
						map[string]any{"type": "Accepted", "status": "True"},
						map[string]any{"type": "ResolvedRefs", "status": "True"},
					},
				},
			},
		},
	}}
}

// staleGenerationHTTPRoute seeds an HTTPRoute at metadata.generation 2 whose
// status.parents conditions are Accepted=True / ResolvedRefs=True but still
// carry observedGeneration 1 — Traefik reported on the PREVIOUS spec and
// hasn't reconciled the update yet.
func staleGenerationHTTPRoute(namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata": map[string]any{
			"name":       name,
			"namespace":  namespace,
			"generation": int64(2),
		},
		"status": map[string]any{
			"parents": []any{
				map[string]any{
					"conditions": []any{
						map[string]any{"type": "Accepted", "status": "True", "observedGeneration": int64(1)},
						map[string]any{"type": "ResolvedRefs", "status": "True", "observedGeneration": int64(1)},
					},
				},
			},
		},
	}}
}

func TestReconcileRoute_WaitsWhenAcceptanceIsStale(t *testing.T) {
	offer := readyOffer("svc")
	offer.Namespace = "stalegen"
	dynClient := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		routeListKinds(),
		// applyObject's fake-SSA reactor merges the applied object over this
		// fixture without touching .status, so the route Get right after
		// apply still returns this stale generation/status pairing.
		staleGenerationHTTPRoute(offer.Namespace, childName(offer.Name)),
	)
	c := controllerForRouteTest(dynClient)
	status := &monetizeapi.ServiceOfferStatus{}

	if err := c.reconcileRoute(context.Background(), status, offer); err != nil {
		t.Fatalf("reconcileRoute: %v", err)
	}
	if isConditionTrue(*status, "RoutePublished") {
		t.Fatalf("RoutePublished should stay False when Traefik hasn't reconciled the current generation: %+v", status.Conditions)
	}
	if reason := conditionReason(status, "RoutePublished"); reason != "WaitingForTraefikAcceptance" {
		t.Fatalf("RoutePublished reason = %q, want WaitingForTraefikAcceptance", reason)
	}
}

func conditionReason(status *monetizeapi.ServiceOfferStatus, conditionType string) string {
	for _, c := range status.Conditions {
		if c.Type == conditionType {
			return c.Reason
		}
	}
	return ""
}

// --- RoutePublished gating on Traefik acceptance ----------------------------

func TestReconcileRoute_WaitsForTraefikAcceptance(t *testing.T) {
	offer := readyOffer("svc")
	offer.Namespace = "pending"
	dynClient := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), routeListKinds())
	c := controllerForRouteTest(dynClient)
	status := &monetizeapi.ServiceOfferStatus{}

	// applyObject creates the HTTPRoute with no status yet — Traefik hasn't
	// reconciled it, so RoutePublished must stay False, not flip True on
	// apply success alone.
	if err := c.reconcileRoute(context.Background(), status, offer); err != nil {
		t.Fatalf("reconcileRoute: %v", err)
	}
	if isConditionTrue(*status, "RoutePublished") {
		t.Fatalf("RoutePublished should stay False until Traefik accepts the route: %+v", status.Conditions)
	}
	if reason := conditionReason(status, "RoutePublished"); reason != "WaitingForTraefikAcceptance" {
		t.Fatalf("RoutePublished reason = %q, want WaitingForTraefikAcceptance", reason)
	}
}

func TestReconcileRoute_PublishesWhenAccepted(t *testing.T) {
	offer := readyOffer("svc")
	offer.Namespace = "accepted"
	dynClient := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		routeListKinds(),
		acceptedHTTPRoute(offer.Namespace, childName(offer.Name)),
	)
	c := controllerForRouteTest(dynClient)
	status := &monetizeapi.ServiceOfferStatus{}

	if err := c.reconcileRoute(context.Background(), status, offer); err != nil {
		t.Fatalf("reconcileRoute: %v", err)
	}
	if !isConditionTrue(*status, "RoutePublished") {
		t.Fatalf("RoutePublished should be True once Traefik has accepted the route: %+v", status.Conditions)
	}
}

func TestReconcileRoute_WaitsForHostRouteAcceptance(t *testing.T) {
	offer := readyOffer("svc")
	offer.Namespace = "hostpending"
	offer.Spec.Hostname = "svc.example.test"
	dynClient := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		routeListKinds(),
		// Main route is accepted, but the dedicated host route is not.
		acceptedHTTPRoute(offer.Namespace, childName(offer.Name)),
	)
	c := controllerForRouteTest(dynClient)
	status := &monetizeapi.ServiceOfferStatus{}

	if err := c.reconcileRoute(context.Background(), status, offer); err != nil {
		t.Fatalf("reconcileRoute: %v", err)
	}
	if isConditionTrue(*status, "RoutePublished") {
		t.Fatalf("RoutePublished should stay False until the host HTTPRoute is accepted too: %+v", status.Conditions)
	}
	if reason := conditionReason(status, "RoutePublished"); reason != "WaitingForTraefikAcceptance" {
		t.Fatalf("RoutePublished reason = %q, want WaitingForTraefikAcceptance", reason)
	}
}

// --- legacy ReferenceGrant sweep --------------------------------------------

func TestReconcileRoute_DeletesLegacyReferenceGrant(t *testing.T) {
	offer := readyOffer("svc")
	offer.Namespace = "legacygrant"
	legacyGrant := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1beta1",
		"kind":       "ReferenceGrant",
		"metadata": map[string]any{
			"name":      legacyBackendReferenceGrantName(offer.Name),
			"namespace": "x402",
		},
	}}
	dynClient := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		routeListKinds(),
		legacyGrant,
		acceptedHTTPRoute(offer.Namespace, childName(offer.Name)),
	)
	c := controllerForRouteTest(dynClient)
	status := &monetizeapi.ServiceOfferStatus{}

	if err := c.reconcileRoute(context.Background(), status, offer); err != nil {
		t.Fatalf("reconcileRoute: %v", err)
	}

	_, err := c.referenceGrants.Namespace("x402").Get(context.Background(), legacyBackendReferenceGrantName(offer.Name), metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("legacy ReferenceGrant should be deleted on reconcile, got err=%v", err)
	}
}
