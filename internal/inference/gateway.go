package inference

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/enclave"
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

	// VerifyOnly skips blockchain settlement after successful verification.
	// Useful for testing and staging environments where no real funds are involved.
	VerifyOnly bool

	// EnclaveTag is the macOS Secure Enclave keychain application tag used for
	// request decryption.  When non-empty the gateway enables two additional
	// behaviours:
	//
	//   1. GET /v1/enclave/pubkey — returns the SE public key as JSON so that
	//      clients can encrypt their request bodies.
	//
	//   2. Inference endpoints accept Content-Type: application/x-obol-encrypted
	//      bodies.  The gateway decrypts them via the SE private key before
	//      forwarding to the upstream service.  If the request also contains a
	//      X-Obol-Reply-Pubkey header, the response is re-encrypted to the
	//      client's ephemeral key (end-to-end confidentiality).
	//
	// When empty, all enclave functionality is disabled and the gateway
	// operates in plain x402-only mode.
	EnclaveTag string

	// VMMode enables running the upstream inference engine inside an Apple
	// Containerization Linux micro-VM via the apple/container CLI.
	// When true, the gateway starts the container on Start() and stops it on
	// Stop(), overriding UpstreamURL with the container's mapped local port.
	VMMode bool

	// VMImage is the OCI image to run (default "ollama/ollama:latest").
	VMImage string

	// VMCPUs is the number of vCPUs to allocate (default 4).
	VMCPUs int

	// VMMemoryMB is the RAM to allocate in MiB (default 8192).
	VMMemoryMB int

	// VMHostPort is the host-local port mapped from the container's Ollama
	// port 11434 (default 11435).
	VMHostPort int

	// VMBinary is the path to the container CLI binary.
	// Defaults to "container" (PATH lookup).
	VMBinary string
}

// Gateway is an x402-enabled reverse proxy for LLM inference with optional
// Secure Enclave request encryption and optional container-isolated upstream.
type Gateway struct {
	config    GatewayConfig
	server    *http.Server
	container *ContainerManager // non-nil when VMMode is active
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

// buildHandler constructs the HTTP mux and middleware stack for the gateway.
// It is separated from Start() to allow tests to inject the handler into an
// httptest.Server without requiring a real network listener.
//
// upstreamURL must be pre-resolved (i.e. VM container URL override already applied).
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

	// Create x402 payment requirement.
	requirement, err := x402.NewUSDCPaymentRequirement(x402.USDCRequirementConfig{
		Chain:            g.config.Chain,
		Amount:           g.config.PricePerRequest,
		RecipientAddress: g.config.WalletAddress,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create payment requirement: %w", err)
	}

	// Configure x402 middleware.
	x402Config := &x402http.Config{
		FacilitatorURL:      g.config.FacilitatorURL,
		PaymentRequirements: []x402.PaymentRequirement{requirement},
		VerifyOnly:          g.config.VerifyOnly,
	}
	paymentMiddleware := x402http.NewX402Middleware(x402Config)

	// Optionally initialise SE enclave middleware.
	var em *enclaveMiddleware
	if g.config.EnclaveTag != "" {
		if err := enclave.CheckSIP(); err != nil {
			return nil, fmt.Errorf("enclave SIP check failed: %w", err)
		}
		em, err = newEnclaveMiddleware(g.config.EnclaveTag)
		if err != nil {
			return nil, fmt.Errorf("enclave middleware: %w", err)
		}
		log.Printf("  enclave:   tag=%q persistent=%v pubkey=%x...",
			em.key.Tag(), em.key.Persistent(), em.key.PublicKeyBytes()[:8])
	}

	// protect wraps a handler with the payment gate and (when enabled) the SE
	// encryption/decryption layer.
	//
	// Layer order (innermost → outermost):
	//   upstream proxy → enclave middleware → x402 payment gate → client
	//
	// The enclave middleware decrypts the request body via the SE private key
	// before forwarding plaintext to the upstream.  Note: the decrypted body
	// is present in this process's memory — this provides transit encryption
	// and hardware key custody, not operator-blind inference.  Phase 2a (VM
	// mode) reduces the exfiltration surface by running the upstream inside
	// an isolated container with no network egress.
	protect := func(h http.Handler) http.Handler {
		if em != nil {
			h = em.wrap(h)
		}
		return paymentMiddleware(h)
	}

	// Build HTTP mux.
	mux := http.NewServeMux()

	// Health check — no payment or encryption required.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	// Enclave public key endpoint — unauthenticated, no payment required.
	// Only registered when enclave mode is active.
	if em != nil {
		mux.HandleFunc("GET /v1/enclave/pubkey", em.handlePubkey)
	}

	// Protected inference endpoints (x402 payment + optional SE decryption).
	mux.Handle("POST /v1/chat/completions", protect(proxy))
	mux.Handle("POST /v1/completions", protect(proxy))
	mux.Handle("POST /v1/embeddings", protect(proxy))
	mux.Handle("GET /v1/models", protect(proxy))

	// Unprotected OpenAI-compat metadata passthrough.
	mux.Handle("/", proxy)

	return mux, nil
}

// Start begins serving the gateway. Blocks until the server is shut down.
func (g *Gateway) Start() error {
	upstreamURL := g.config.UpstreamURL

	// If VM mode is enabled, start the Ollama container and override upstream.
	if g.config.VMMode {
		cm := newContainerManager(g.config.VMBinary, "", g.config.VMHostPort)
		// Use deployment name from enclave tag suffix if available.
		if g.config.EnclaveTag != "" {
			const prefix = "com.obol.inference."
			if strings.HasPrefix(g.config.EnclaveTag, prefix) {
				cm = newContainerManager(g.config.VMBinary,
					strings.TrimPrefix(g.config.EnclaveTag, prefix),
					g.config.VMHostPort)
			}
		}
		ctx := context.Background()
		if err := cm.Start(ctx, g.config.VMImage, g.config.VMCPUs, g.config.VMMemoryMB); err != nil {
			return fmt.Errorf("container start: %w", err)
		}
		g.container = cm
		upstreamURL = cm.UpstreamURL()
		log.Printf("  container:   %s → %s", cm.name, cm.UpstreamURL())
	}

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
	log.Printf("  price:       %s USDC/request", g.config.PricePerRequest)
	log.Printf("  chain:       %s", g.config.Chain.NetworkID)
	log.Printf("  facilitator: %s", g.config.FacilitatorURL)
	if g.config.EnclaveTag == "" {
		log.Printf("  enclave:     disabled")
	}

	return g.server.Serve(listener)
}

// Stop gracefully shuts down the gateway and any managed container.
func (g *Gateway) Stop() error {
	var serverErr error
	if g.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		serverErr = g.server.Shutdown(ctx)
	}

	if g.container != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := g.container.Stop(ctx); err != nil {
			log.Printf("container stop: %v", err)
		}
	}

	return serverErr
}
