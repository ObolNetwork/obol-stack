package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/ObolNetwork/obol-stack/internal/x402/source"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func main() {
	configPath := flag.String("config", "/config/pricing.yaml", "Path to pricing config YAML (global settings)")
	listen := flag.String("listen", ":8080", "Listen address")
	routeSource := flag.String("route-source", "paymentroute", "Route source: paymentroute (CRD watch) or configmap (legacy file watcher)")
	routeNamespace := flag.String("route-namespace", "x402", "Namespace to watch for PaymentRoute CRs")
	flag.Parse()

	// Load base config for global settings (wallet, chain, facilitator).
	cfg, err := x402verifier.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if cfg.Wallet != "" {
		if err := x402verifier.ValidateWallet(cfg.Wallet); err != nil {
			log.Fatalf("config: %v", err)
		}
	}

	v, err := x402verifier.NewVerifier(cfg)
	if err != nil {
		log.Fatalf("create verifier: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/verify", v.HandleVerify)
	mux.HandleFunc("/healthz", v.HandleHealthz)
	mux.HandleFunc("/readyz", v.HandleReadyz)
	mux.Handle("GET /metrics", v.MetricsHandler())

	server := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start route source.
	switch *routeSource {
	case "paymentroute":
		restCfg, err := rest.InClusterConfig()
		if err != nil {
			log.Fatalf("in-cluster config: %v (use --route-source=configmap outside cluster)", err)
		}

		dynClient, err := dynamic.NewForConfig(restCfg)
		if err != nil {
			log.Fatalf("dynamic client: %v", err)
		}

		src := source.NewPaymentRouteSource(dynClient, v, *routeNamespace)
		go func() {
			if err := src.Run(ctx); err != nil {
				log.Fatalf("paymentroute source: %v", err)
			}
		}()
		log.Printf("route source: PaymentRoute CRs (namespace: %s)", *routeNamespace)

	case "configmap":
		go x402verifier.WatchConfig(ctx, *configPath, v, 5*time.Second)
		log.Printf("route source: ConfigMap file watcher (%s)", *configPath)

	default:
		log.Fatalf("unknown route source: %s", *routeSource)
	}

	// Graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down...")
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	log.Printf("x402 verifier listening on %s", *listen)
	log.Printf("  wallet:      %s", cfg.Wallet)
	log.Printf("  chain:       %s", cfg.Chain)
	log.Printf("  facilitator: %s", cfg.FacilitatorURL)
	log.Printf("  verifyOnly:  %v", cfg.VerifyOnly)

	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
