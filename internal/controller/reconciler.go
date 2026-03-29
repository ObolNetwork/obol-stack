// Package controller implements a Kubernetes controller for ServiceOffer CRDs.
// It replaces the Python-based monetize.py reconciliation loop with an
// event-driven controller-runtime reconciler. The controller is generation-driven:
// it derives desired child resources from spec and observes convergence.
package controller

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	finalizerName = "obol.org/serviceoffer-cleanup"
	fieldOwner    = "serviceoffer-controller"

	condUpstreamHealthy  = "UpstreamHealthy"
	condPaymentGateReady = "PaymentGateReady"
	condRoutePublished   = "RoutePublished"
	condReady            = "Ready"

	verifierNamespace = "x402"
	gatewayName       = "traefik-gateway"
	gatewayNamespace  = "traefik"
	gatewaySectionWeb = "web"
)

var (
	serviceOfferGVK = schema.GroupVersionKind{Group: "obol.org", Version: "v1alpha1", Kind: "ServiceOffer"}

	paymentRouteGVK = schema.GroupVersionKind{Group: "obol.org", Version: "v1alpha1", Kind: "PaymentRoute"}
	middlewareGVK   = schema.GroupVersionKind{Group: "traefik.io", Version: "v1alpha1", Kind: "Middleware"}
	httpRouteGVK    = schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRoute"}
)

// Reconciler reconciles ServiceOffer CRDs into child Kubernetes resources:
// PaymentRoute (obol.org), Middleware (traefik.io), and HTTPRoute (gateway API).
type Reconciler struct {
	client.Client
	HTTPClient *http.Client // injectable for testing
}

// SetupWithManager registers the reconciler with the controller manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&unstructured.Unstructured{}, withGVK(serviceOfferGVK)).
		Complete(r)
}

// Reconcile handles a single ServiceOffer event. It is generation-driven:
// always derive desired child resources from spec, apply them, then observe status.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	so := &unstructured.Unstructured{}
	so.SetGroupVersionKind(serviceOfferGVK)
	if err := r.Get(ctx, req.NamespacedName, so); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion via finalizer.
	if !so.GetDeletionTimestamp().IsZero() {
		return r.handleDeletion(ctx, so)
	}

	// Ensure finalizer is present.
	if !controllerutil.ContainsFinalizer(so, finalizerName) {
		controllerutil.AddFinalizer(so, finalizerName)
		if err := r.Update(ctx, so); err != nil {
			return ctrl.Result{}, err
		}
	}

	spec, err := getSpec(so)
	if err != nil {
		logger.Error(err, "invalid ServiceOffer spec")
		return ctrl.Result{}, nil // don't requeue bad spec
	}

	name := so.GetName()
	ns := so.GetNamespace()

	// --- Derive and apply desired child resources ---

	// 1. Check upstream health (non-resource precondition).
	healthy, healthMsg := r.checkUpstreamHealth(ctx, spec)
	setCondition(so, condUpstreamHealthy, healthy, healthMsg)
	if !healthy {
		_ = r.updateStatus(ctx, so)
		logger.Info("upstream not healthy, requeueing", "name", name, "msg", healthMsg)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// 2. Apply Middleware.
	if err := r.applyMiddleware(ctx, so, name, ns); err != nil {
		setCondition(so, condPaymentGateReady, false, fmt.Sprintf("middleware: %v", err))
		_ = r.updateStatus(ctx, so)
		return ctrl.Result{}, err
	}

	// 3. Apply PaymentRoute CR (replaces ConfigMap mutation).
	if err := r.applyPaymentRoute(ctx, so, spec, name, ns); err != nil {
		setCondition(so, condPaymentGateReady, false, fmt.Sprintf("payment route: %v", err))
		_ = r.updateStatus(ctx, so)
		return ctrl.Result{}, err
	}
	setCondition(so, condPaymentGateReady, true, "Middleware and PaymentRoute applied")

	// 4. Apply HTTPRoute.
	path := spec.Path
	if path == "" {
		path = "/services/" + name
	}
	if err := r.applyHTTPRoute(ctx, so, spec, name, ns, path); err != nil {
		setCondition(so, condRoutePublished, false, fmt.Sprintf("httproute: %v", err))
		_ = r.updateStatus(ctx, so)
		return ctrl.Result{}, err
	}
	setCondition(so, condRoutePublished, true, "HTTPRoute published at "+path)

	// --- Observe and finalize ---
	setNestedField(so, path, "status", "endpoint")
	setCondition(so, condReady, true, "All conditions satisfied")
	setNestedField(so, so.GetGeneration(), "status", "observedGeneration")

	if err := r.updateStatus(ctx, so); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("reconciled", "name", name, "endpoint", path)
	return ctrl.Result{}, nil
}

// handleDeletion runs finalizer logic. OwnerReferences handle child resource
// GC for PaymentRoute, Middleware, and HTTPRoute. The finalizer is for any
// external side effects that can't be expressed via ownerRefs.
func (r *Reconciler) handleDeletion(ctx context.Context, so *unstructured.Unstructured) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(so, finalizerName) {
		// Currently no external side effects beyond owned resources.
		// ERC-8004 deactivation will be added here in a future phase.
		controllerutil.RemoveFinalizer(so, finalizerName)
		if err := r.Update(ctx, so); err != nil {
			return ctrl.Result{}, err
		}
	}
	log.FromContext(ctx).Info("deleted", "name", so.GetName())
	return ctrl.Result{}, nil
}

// checkUpstreamHealth probes the upstream service health endpoint.
// Any HTTP response (even 4xx/5xx) counts as reachable, matching the
// existing monetize.py behavior.
func (r *Reconciler) checkUpstreamHealth(ctx context.Context, spec *offerSpec) (bool, string) {
	healthPath := spec.Upstream.HealthPath
	if healthPath == "" {
		healthPath = "/health"
	}

	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d%s",
		spec.Upstream.Service, spec.Upstream.Namespace, spec.Upstream.Port, healthPath)

	httpClient := r.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Sprintf("bad health URL: %v", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return false, fmt.Sprintf("health check failed: %v", err)
	}
	defer resp.Body.Close()

	return true, fmt.Sprintf("GET %s returned %d", healthPath, resp.StatusCode)
}

// updateStatus patches the status subresource.
func (r *Reconciler) updateStatus(ctx context.Context, so *unstructured.Unstructured) error {
	return r.Status().Update(ctx, so)
}
