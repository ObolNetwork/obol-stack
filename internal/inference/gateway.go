package inference

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/mark3labs/x402-go"
	x402http "github.com/mark3labs/x402-go/http"
)

// GatewayConfig holds configuration for the x402 inference gateway.
type GatewayConfig struct {
	// ListenAddr is the address to listen on (e.g., ":8402").
	ListenAddr string

	// UpstreamURL is the upstream inference service URL (e.g., "http://localhost:11434").
	UpstreamURL string

	// WalletAddress is the USDC recipient address for payments.
	WalletAddress string

	// PricePerRequest is the USDC amount charged per inference request (e.g., "0.001").
	PricePerRequest string

	// Chain is the x402 chain configuration (e.g., x402.BaseMainnet).
	Chain x402.ChainConfig

	// FacilitatorURL is the x402 facilitator service URL.
	FacilitatorURL string
}

// Gateway is an x402-enabled reverse proxy for LLM inference.
type Gateway struct {
	config GatewayConfig
	server *http.Server
}

// NewGateway creates a new inference gateway with the given configuration.
func NewGateway(cfg GatewayConfig) (*Gateway, error) {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8402"
	}
	if cfg.FacilitatorURL == "" {
		cfg.FacilitatorURL = "https://facilitator.x402.rs"
	}
	if cfg.Chain.NetworkID == "" {
		cfg.Chain = x402.BaseSepolia
	}
	if cfg.PricePerRequest == "" {
		cfg.PricePerRequest = "0.001"
	}

	return &Gateway{config: cfg}, nil
}

// Start begins serving the gateway. Blocks until the server is shut down.
func (g *Gateway) Start() error {
	upstream, err := url.Parse(g.config.UpstreamURL)
	if err != nil {
		return fmt.Errorf("invalid upstream URL %q: %w", g.config.UpstreamURL, err)
	}

	// Build reverse proxy to upstream inference service
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("proxy error: %v", err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}

	// Create x402 payment requirement
	requirement, err := x402.NewUSDCPaymentRequirement(x402.USDCRequirementConfig{
		Chain:            g.config.Chain,
		Amount:           g.config.PricePerRequest,
		RecipientAddress: g.config.WalletAddress,
	})
	if err != nil {
		return fmt.Errorf("failed to create payment requirement: %w", err)
	}

	// Configure x402 middleware
	x402Config := &x402http.Config{
		FacilitatorURL:  g.config.FacilitatorURL,
		PaymentRequirements: []x402.PaymentRequirement{requirement},
	}
	paymentMiddleware := x402http.NewX402Middleware(x402Config)

	// Build HTTP mux
	mux := http.NewServeMux()

	// Health check (no payment required)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	// Protected inference endpoints (x402 payment required)
	mux.Handle("POST /v1/chat/completions", paymentMiddleware(proxy))
	mux.Handle("POST /v1/completions", paymentMiddleware(proxy))
	mux.Handle("POST /v1/embeddings", paymentMiddleware(proxy))
	mux.Handle("GET /v1/models", paymentMiddleware(proxy))

	// Unprotected OpenAI-compat metadata
	mux.Handle("/", proxy)

	g.server = &http.Server{
		Addr:              g.config.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	listener, err := net.Listen("tcp", g.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", g.config.ListenAddr, err)
	}

	log.Printf("x402 inference gateway listening on %s", g.config.ListenAddr)
	log.Printf("  upstream:  %s", g.config.UpstreamURL)
	log.Printf("  wallet:    %s", g.config.WalletAddress)
	log.Printf("  price:     %s USDC/request", g.config.PricePerRequest)
	log.Printf("  chain:     %s", g.config.Chain.NetworkID)
	log.Printf("  facilitator: %s", g.config.FacilitatorURL)

	return g.server.Serve(listener)
}

// Stop gracefully shuts down the gateway.
func (g *Gateway) Stop() error {
	if g.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return g.server.Shutdown(ctx)
}
