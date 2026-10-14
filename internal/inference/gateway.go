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

	x402pkg "github.com/ObolNetwork/obol-stack/internal/x402"
	x402types "github.com/x402-foundation/x402/go/v2/types"
)

// GatewayConfig holds configuration for the x402 inference gateway.
type GatewayConfig struct {
	// ListenAddr is the address to listen on (e.g., ":8402").
	ListenAddr string

	// UpstreamURL is the upstream inference service URL (e.g., "http://localhost:11434").
	UpstreamURL string

	// WalletAddress is the USDC recipient address for payments.
	WalletAddress string

	// PricePerRequest is the amount charged per inference request, denominated
	// in AssetSymbol units (e.g., "0.001" with AssetSymbol="USDC", or "0.023"
	// with AssetSymbol="OBOL"). Atomic-unit conversion happens at the x402
	// middleware boundary using the token's decimals.
	PricePerRequest string

	// AssetSymbol is the token symbol charged per request (default "USDC").
	// Used for human-readable log output and price summaries.
	AssetSymbol string

	// Chain is the x402 chain configuration (e.g., x402pkg.ChainBaseMainnet).
	Chain x402pkg.ChainInfo

	// FacilitatorURL is the x402 facilitator service URL.
	FacilitatorURL string

	// VerifyOnly skips blockchain settlement after successful verification.
	// Useful for testing and staging environments where no real funds are involved.
	VerifyOnly bool
}

// PaidRoutes are the "METHOD /path" patterns the gateway proxies to the
// upstream, each behind the x402 payment gate. Nothing else is forwarded.
var PaidRoutes = []string{
	"POST /v1/chat/completions",
	"POST /v1/completions",
	"POST /v1/embeddings",
	"GET /v1/models",
}

// Gateway is an x402-enabled reverse proxy for LLM inference.
type Gateway struct {
	config GatewayConfig
	server *http.Server
}

// formatPriceLogLine renders the human-readable per-request price string used
// in the gateway's startup log. An empty symbol defaults to "USDC" so legacy
// callers that don't set AssetSymbol keep their previous output.
func formatPriceLogLine(price, symbol string) string {
	if symbol == "" {
		symbol = "USDC"
	}
	return fmt.Sprintf("%s %s/request", price, symbol)
}

// NewGateway creates a new inference gateway with the given configuration.
func NewGateway(cfg GatewayConfig) (*Gateway, error) {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8402"
	}

	if cfg.FacilitatorURL == "" {
		cfg.FacilitatorURL = x402pkg.DefaultFacilitatorURL
	}

	if err := x402pkg.ValidateFacilitatorURL(cfg.FacilitatorURL); err != nil {
		return nil, err
	}

	if cfg.Chain.NetworkID == "" {
		cfg.Chain = x402pkg.ChainBaseMainnet
	}

	if cfg.PricePerRequest == "" {
		cfg.PricePerRequest = "0.001"
	}

	return &Gateway{config: cfg}, nil
}

// buildHandler constructs the HTTP mux and middleware stack for the gateway.
// It is separated from Start() to allow tests to inject the handler into an
// httptest.Server without requiring a real network listener.

func (g *Gateway) buildHandler(upstreamURL string) (http.Handler, error) {
	upstream, err := url.Parse(upstreamURL)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream URL %q: %w", upstreamURL, err)
	}

	// Build reverse proxy to upstream inference service.
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("proxy error: %v", err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}

	// Create x402 payment requirement. The standalone gateway has no
	// operator surface for MaxTimeoutSeconds yet, so pass 0 to fall back
	// to DefaultMaxTimeoutSeconds.
	requirement, err := x402pkg.BuildV2Requirement(g.config.Chain, g.config.PricePerRequest, g.config.WalletAddress, 0)
	if err != nil {
		return nil, fmt.Errorf("invalid price %q: %w", g.config.PricePerRequest, err)
	}

	// Configure x402 ForwardAuth middleware. The bazaar discovery extension
	// advertises the chat-completions invocation shape on every 402 so
	// facilitators/indexers can catalog the standalone gateway too.
	paymentMiddleware := x402pkg.NewForwardAuthMiddleware(x402pkg.ForwardAuthConfig{
		FacilitatorURL: g.config.FacilitatorURL,
		VerifyOnly:     g.config.VerifyOnly,
		Extensions:     x402pkg.WithBazaar(nil, "inference", ""),
		// The standalone inference gateway is in-process and settlement-aware,
		// so a configured VerifyOnly=false is correct by design — suppress the
		// misleading per-request warning on this path.
		SettlesInProcess: true,
		// Settle only when the upstream delivered a completion (issue
		// #821) — an inference server can answer 200 with an empty body
		// or an empty completion. Non-chat JSON (embeddings, model lists)
		// fails open: any non-empty body still settles.
		SettleOnDelivery: true,
		// One signed authorization per request: a concurrent duplicate is
		// refused with 409 before it reaches the inference server. The
		// registry is in-process; the gateway is a single process.
		InUse: x402pkg.NewPaymentInUseRegistry(),
	}, []x402types.PaymentRequirements{requirement})

	// Build HTTP mux. The gateway serves an explicit allowlist: the paid
	// OpenAI-compatible inference routes plus an unpaid health check. Every
	// other path or method (including an upstream's native API, e.g.
	// Ollama's /api/*) returns 404 without reaching the upstream.
	mux := http.NewServeMux()

	// Health check — no payment required. Answered by the gateway itself.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	// Paid inference endpoints (x402 payment).
	for _, route := range PaidRoutes {
		mux.Handle(route, paymentMiddleware(proxy))
	}

	// Anything outside the allowlist is not served.
	mux.Handle("/", http.NotFoundHandler())

	return mux, nil
}

// Start begins serving the gateway. Blocks until the server is shut down.
func (g *Gateway) Start() error {
	upstreamURL := g.config.UpstreamURL

	handler, err := g.buildHandler(upstreamURL)
	if err != nil {
		return err
	}

	g.server = &http.Server{
		Addr:              g.config.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	listener, err := net.Listen("tcp", g.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", g.config.ListenAddr, err)
	}

	log.Printf("x402 inference gateway listening on %s", g.config.ListenAddr)
	log.Printf("  upstream:    %s", upstreamURL)
	log.Printf("  wallet:      %s", g.config.WalletAddress)
	log.Printf("  price:       %s", formatPriceLogLine(g.config.PricePerRequest, g.config.AssetSymbol))
	log.Printf("  chain:       %s", g.config.Chain.NetworkID)
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
