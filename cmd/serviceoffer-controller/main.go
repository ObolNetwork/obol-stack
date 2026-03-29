// serviceoffer-controller is a Kubernetes controller that reconciles
// ServiceOffer CRDs into x402 payment-gated routes. It replaces the
// Python-based monetize.py reconciliation loop.
package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/ObolNetwork/obol-stack/internal/controller"
)

func main() {
	var metricsAddr string
	var probeAddr string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8383", "metrics endpoint bind address")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8384", "health probe bind address")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	logger := ctrl.Log.WithName("serviceoffer-controller")

	s := runtime.NewScheme()
	utilruntime.Must(scheme.AddToScheme(s))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 s,
		HealthProbeBindAddress: probeAddr,
	})
	if err != nil {
		logger.Error(err, "unable to create manager")
		os.Exit(1)
	}

	// Create a dynamic client for unstructured ServiceOffer access.
	cfg := ctrl.GetConfigOrDie()
	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		logger.Error(err, "unable to create dynamic client")
		os.Exit(1)
	}
	_ = dynClient // reserved for future use (SSA patches)

	reconciler := &controller.Reconciler{
		Client: mgr.GetClient(),
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to setup controller")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		logger.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		logger.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	logger.Info("starting controller")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error(err, "controller exited with error")
		os.Exit(1)
	}
}
