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
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	configPath := flag.String("config", "/config/pricing.yaml", "Path to pricing config YAML")
	listen := flag.String("listen", ":8080", "Listen address")
	watch := flag.Bool("watch", true, "Watch config file for changes")
	routeSource := flag.String("route-source", "file", "Route source: file or kube")
	kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig for out-of-cluster kube route source")
	flag.Parse()

	cfg, err := x402verifier.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if cfg.Wallet != "" {
		if err := x402verifier.ValidateWallet(cfg.Wallet); err != nil {
			log.Fatalf("config: %v", err)
		}
	}

	initialCfg := *cfg

	v, err := x402verifier.NewVerifier(&initialCfg)
	if err != nil {
		log.Fatalf("create verifier: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/verify", v.HandleVerify)
	mux.HandleFunc("/healthz", v.HandleHealthz)
	mux.HandleFunc("/readyz", v.HandleReadyz)
	mux.Handle("GET /metrics", v.MetricsHandler())
	mux.Handle("/", http.HandlerFunc(v.HandleProxy))

	server := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start config watcher in background.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// File-sourced routes are populated synchronously by LoadConfig above,
	// so they are "loaded" as soon as NewVerifier returns. The kube branch
	// below flips this flag only after the first informer apply succeeds.
	if *routeSource == "file" {
		v.MarkRoutesLoaded()
	}

	if *watch {
		switch *routeSource {
		case "file":
			go x402verifier.WatchConfig(ctx, *configPath, v, 5*time.Second)
		case "kube":
			accumulator := x402verifier.NewConfigAccumulator(&initialCfg, v)
			go x402verifier.WatchConfigWithHandler(ctx, *configPath, 5*time.Second, func(next *x402verifier.PricingConfig) error {
				updated := *next
				return accumulator.SetBase(&updated)
			})

			kubeCfg, err := loadKubeConfig(*kubeconfig)
			if err != nil {
				log.Fatalf("load kube route source config: %v", err)
			}
			go func() {
				if err := x402verifier.WatchServiceOffers(ctx, kubeCfg, accumulator.SetRoutes, v.MarkRoutesLoaded); err != nil {
					log.Printf("x402-serviceoffer-source: stopped: %v", err)
				}
			}()
			// Storefront branding (seller name/logo/theme on 402, sign-in,
			// and error pages). Best-effort: failure keeps stack defaults.
			go func() {
				if err := x402verifier.WatchStorefrontProfile(ctx, kubeCfg); err != nil {
					log.Printf("x402-profile-source: stopped: %v (pages keep default branding)", err)
				}
			}()
		default:
			log.Fatalf("unsupported --route-source=%q (use file or kube)", *routeSource)
		}
	}

	// Handle graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down...")
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
	}()

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	log.Printf("x402 gateway listening on %s", *listen)
	log.Printf("  config:      %s", *configPath)
	log.Printf("  wallet:      %s", cfg.Wallet)
	log.Printf("  chain:       %s", cfg.Chain)
	log.Printf("  facilitator: %s", cfg.FacilitatorURL)
	log.Printf("  routes:      %d", len(cfg.Routes))
	log.Printf("  verifyOnly:  %v", cfg.VerifyOnly)
	log.Printf("  routeSource: %s", *routeSource)

	// verifyOnly only affects the legacy /verify endpoint. The shared seller
	// gateway path served on all other routes always settles after upstream
	// success, which is the correct x402 resource-server semantic.
	if !cfg.VerifyOnly {
		log.Printf("x402 gateway: WARNING verifyOnly=false loaded from %s. "+
			"This only impacts the legacy /verify endpoint and is unsafe for Traefik "+
			"ForwardAuth. Keep verifyOnly=true in x402-pricing.yaml if /verify is used.", *configPath)
	}

	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func loadKubeConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if env := os.Getenv("KUBECONFIG"); env != "" {
		return clientcmd.BuildConfigFromFlags("", env)
	}
	return rest.InClusterConfig()
}
