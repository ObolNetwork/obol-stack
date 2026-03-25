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
)

func main() {
	configPath := flag.String("config", "/config/pricing.yaml", "Path to pricing config YAML")
	listen := flag.String("listen", ":8080", "Listen address")
	watch := flag.Bool("watch", true, "Watch config file for changes")

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

	v, err := x402verifier.NewVerifier(cfg)
	if err != nil {
		log.Fatalf("create verifier: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/verify", v.HandleVerify)
	mux.HandleFunc("/healthz", v.HandleHealthz)
	mux.HandleFunc("/readyz", v.HandleReadyz)
	mux.HandleFunc("GET /.well-known/agent-registration.json", v.HandleWellKnown)
	mux.Handle("GET /metrics", v.MetricsHandler())

	server := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start config watcher in background.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if *watch {
		go x402verifier.WatchConfig(ctx, *configPath, v, 5*time.Second)
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

	log.Printf("x402 verifier listening on %s", *listen)
	log.Printf("  config:      %s", *configPath)
	log.Printf("  wallet:      %s", cfg.Wallet)
	log.Printf("  chain:       %s", cfg.Chain)
	log.Printf("  facilitator: %s", cfg.FacilitatorURL)
	log.Printf("  routes:      %d", len(cfg.Routes))
	log.Printf("  verifyOnly:  %v", cfg.VerifyOnly)

	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
