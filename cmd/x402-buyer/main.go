// x402-buyer is a lean sidecar that handles x402 payments using pre-signed
// ERC-3009 TransferWithAuthorization vouchers. It runs as an OpenAI-compatible
// reverse proxy in the llm namespace, alongside LiteLLM.
//
// LiteLLM sees the sidecar as a plain OpenAI provider. The sidecar intercepts
// 402 responses from upstream sellers, attaches pre-signed payment headers,
// and retries. It has zero signer access — spending is bounded by the number
// of pre-signed authorizations in the pool.
//
// Usage:
//
//	x402-buyer --config /config/config.json --auths /config/auths.json --listen :8402
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/x402/buyer"
)

func main() {
	var (
		configPath     = flag.String("config", "/config/config.json", "path to upstream config JSON")
		authsPath      = flag.String("auths", "/config/auths.json", "path to pre-signed auths JSON")
		statePath      = flag.String("state", "/state/consumed.json", "path to persisted consumed-auth state")
		listen         = flag.String("listen", ":8402", "listen address")
		reloadInterval = flag.Duration("reload-interval", 5*time.Second, "config/auth reload interval")
	)
	flag.Parse()

	state, err := buyer.LoadStateStore(*statePath)
	if err != nil {
		log.Fatalf("load state: %v", err)
	}

	cfg, err := buyer.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	auths, err := buyer.LoadAuths(*authsPath)
	if err != nil {
		log.Fatalf("load auths: %v", err)
	}

	proxy, err := buyer.NewProxy(cfg, auths, state)
	if err != nil {
		log.Fatalf("create proxy: %v", err)
	}

	// Log startup summary.
	for name, upstream := range cfg.Upstreams {
		pool := auths[name]
		log.Printf("upstream %q → %s (%d auths, price=%s, network=%s)",
			name, upstream.URL, len(pool), upstream.Price, upstream.Network)
	}

	srv := &http.Server{
		Addr:         *listen,
		Handler:      proxy,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("x402-buyer listening on %s", *listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	if *reloadInterval > 0 {
		ticker := time.NewTicker(*reloadInterval)
		defer ticker.Stop()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					cfg, err := buyer.LoadConfig(*configPath)
					if err != nil {
						log.Printf("reload config: %v", err)
						continue
					}
					auths, err := buyer.LoadAuths(*authsPath)
					if err != nil {
						log.Printf("reload auths: %v", err)
						continue
					}
					if err := proxy.Reload(cfg, auths); err != nil {
						log.Printf("reload proxy: %v", err)
					}
				}
			}
		}()
	}

	<-ctx.Done()
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintf(os.Stderr, "shutdown: %v\n", err)
	}
}
