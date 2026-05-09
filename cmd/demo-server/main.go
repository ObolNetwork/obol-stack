// Command demo-server runs a lightweight HTTP server for obol sell demo.
//
// The demo type is selected by the DEMO_TYPE environment variable:
//   - hello:  proof-of-payment echo (no external dependencies)
//   - blocks: basic chain data from eRPC
//
// The quant demo is no longer served from this binary — it is an
// agent-backed offer (Agent CRD + ServiceOffer of type=agent), wired by
// `obol sell demo quant` directly.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/demo"
)

func main() {
	demoType := envOr("DEMO_TYPE", "hello")
	port := envOr("PORT", "8080")
	erpcURL := envOr("ERPC_URL", "http://erpc.erpc.svc.cluster.local/rpc/base")

	var handler http.HandlerFunc
	switch demoType {
	case "hello":
		handler = demo.HelloHandler()
	case "blocks":
		handler = demo.BlocksHandler(erpcURL)
	default:
		log.Fatalf("unknown DEMO_TYPE: %q (expected hello or blocks; quant moved to agent-backed)", demoType)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handler)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	addr := net.JoinHostPort("", port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("demo-server type=%s listening on %s", demoType, addr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
