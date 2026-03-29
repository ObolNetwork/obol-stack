// Package controller implements a Kubernetes controller for ServiceOffer CRDs.
// It replaces the Python-based monetize.py reconciliation loop with an
// event-driven controller-runtime reconciler.
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

	condUpstreamHealthy  = "UpstreamHealthy"
	condPaymentGateReady = "PaymentGateReady"
	condRoutePublished   = "RoutePublished"
	condReady            = "Ready"

	verifierNamespace = "x402"
	verifierConfigMap = "x402-pricing"
	gatewayName       = "traefik-gateway"
	gatewayNamespace  = "traefik"
	gatewaySectionWeb = "web"
)

var (
	serviceOfferGVR = schema.GroupVersionResource{Group: "obol.org", Version: "v1alpha1", Resource: "serviceoffers"}
	serviceOfferGVK = schema.GroupVersionKind{Group: "obol.org", Version: "v1alpha1", Kind: "ServiceOffer"}
)

// Reconciler reconciles ServiceOffer CRDs into child Kubernetes resources:
// Middleware (traefik.io), HTTPRoute (gateway API), and x402 pricing ConfigMap entries.
type Reconciler struct {
	client.Client
	HTTPClient *http.Client
}

// SetupWithManager registers the reconciler with the controller manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&unstructured.Unstructured{}, withGVK(serviceOfferGVK)).
		Complete(r)
}

// Reconcile handles a single ServiceOffer event.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the ServiceOffer.
	so := &unstructured.Unstructured{}
	so.SetGroupVersionKind(serviceOfferGVK)
	if err := r.Get(ctx, req.NamespacedName, so); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion.
	if !so.GetDeletionTimestamp().IsZero() {
		return r.handleDeletion(ctx, so)
	}

	// Ensure finalizer.
	if !controllerutil.ContainsFinalizer(so, finalizerName) {
		controllerutil.AddFinalizer(so, finalizerName)
		if err := r.Update(ctx, so); err != nil {
			return ctrl.Result{}, err
		}
	}

	spec, err := getSpec(so)
	if err != nil {
		logger.Error(err, "invalid ServiceOffer spec")
		return ctrl.Result{}, nil // don't requeue on bad spec
	}

	name := so.GetName()
	ns := so.GetNamespace()

	// --- Stage 1: Upstream health check ---
	healthy, msg := r.checkUpstreamHealth(ctx, spec)
	setCondition(so, condUpstreamHealthy, healthy, msg)
	if !healthy {
		if err := r.updateStatus(ctx, so); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("upstream not healthy, requeueing", "name", name, "msg", msg)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// --- Stage 2: Payment gate (Middleware + ConfigMap pricing route) ---
	if err := r.ensureMiddleware(ctx, so, name, ns); err != nil {
		setCondition(so, condPaymentGateReady, false, fmt.Sprintf("middleware error: %v", err))
		_ = r.updateStatus(ctx, so)
		return ctrl.Result{}, err
	}
	if err := r.ensurePricingRoute(ctx, so, spec, name, ns); err != nil {
		setCondition(so, condPaymentGateReady, false, fmt.Sprintf("pricing error: %v", err))
		_ = r.updateStatus(ctx, so)
		return ctrl.Result{}, err
	}
	setCondition(so, condPaymentGateReady, true, "Middleware and pricing route configured")

	// --- Stage 3: HTTPRoute ---
	if err := r.ensureHTTPRoute(ctx, so, spec, name, ns); err != nil {
		setCondition(so, condRoutePublished, false, fmt.Sprintf("httproute error: %v", err))
		_ = r.updateStatus(ctx, so)
		return ctrl.Result{}, err
	}
	path := spec.Path
	if path == "" {
		path = "/services/" + name
	}
	setCondition(so, condRoutePublished, true, "HTTPRoute published at "+path)

	// Set endpoint in status.
	setNestedField(so, path, "status", "endpoint")

	// --- All conditions met ---
	setCondition(so, condReady, true, "All conditions satisfied")
	setNestedField(so, so.GetGeneration(), "status", "observedGeneration")

	if err := r.updateStatus(ctx, so); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("ServiceOffer reconciled", "name", name, "endpoint", path)
	return ctrl.Result{}, nil
}

// handleDeletion removes the pricing route from the ConfigMap (ownerRefs handle the rest).
func (r *Reconciler) handleDeletion(ctx context.Context, so *unstructured.Unstructured) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(so, finalizerName) {
		spec, err := getSpec(so)
		if err == nil {
			path := spec.Path
			if path == "" {
				path = "/services/" + so.GetName()
			}
			if rmErr := r.removePricingRoute(ctx, path, so.GetName()); rmErr != nil {
				logger.Error(rmErr, "failed to remove pricing route, continuing cleanup")
			}
		}

		controllerutil.RemoveFinalizer(so, finalizerName)
		if err := r.Update(ctx, so); err != nil {
			return ctrl.Result{}, err
		}
	}

	logger.Info("ServiceOffer deleted", "name", so.GetName())
	return ctrl.Result{}, nil
}

// checkUpstreamHealth probes the upstream service health endpoint.
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

	// Any response = reachable (matches monetize.py behavior).
	return true, fmt.Sprintf("GET %s returned %d", healthPath, resp.StatusCode)
}

// updateStatus patches the status subresource.
func (r *Reconciler) updateStatus(ctx context.Context, so *unstructured.Unstructured) error {
	return r.Status().Update(ctx, so)
}
