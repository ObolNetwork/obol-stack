// Package x402mcp serves a local x402-paid MCP (Model Context Protocol) server.
//
// It wraps MCP tools with x402 payment using x402-foundation/x402/go/mcp and the
// official modelcontextprotocol/go-sdk, gated by an x402 facilitator. Buyers
// (e.g. hermes-agent's pay_mcp plugin) settle in-band via the MCP request
// _meta["x402/payment"] field, per specs/transports-v2/mcp.md. Verify ->
// execute -> settle happens inside the tool call, so a caller is never charged
// for a failed tool. This is the application-layer (in-band) counterpart to the
// HTTP ForwardAuth gate in internal/x402.
//
// The paid tool proxies the buyer's JSON arguments to a backend HTTP service and
// returns the response, so any real service — e.g. a paid weather/data API — can
// be resold to agents per call. This mirrors the canonical x402 paid-MCP example
// (a paid get_weather tool), with the upstream generalised from a stub to a real
// backend. The backend's own auth (if any) is configured server-side and is
// never exposed to buyers. (Inference is just one possible upstream, not the point.)
package x402mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	x402 "github.com/x402-foundation/x402/go"
	x402http "github.com/x402-foundation/x402/go/http"
	mcp402 "github.com/x402-foundation/x402/go/mcp"
	evmserver "github.com/x402-foundation/x402/go/mechanisms/evm/exact/server"
)

// caip2 maps the CLI chain name to its CAIP-2 network id. Mirrors the EVM
// 'exact'-settleable chains in internal/x402.ChainInfo.CAIP2Network.
var caip2 = map[string]string{
	"base":         "eip155:8453",
	"base-sepolia": "eip155:84532",
	"ethereum":     "eip155:1",
	"polygon":      "eip155:137",
}

// Options configures an x402-paid MCP server.
type Options struct {
	Name            string            // server display name
	ToolName        string            // paid tool name (default "call")
	Description     string            // human-readable tool description (document the arg shape here)
	Port            int               // HTTP port (default 4022)
	PayTo           string            // payment recipient address (required)
	Price           string            // per-call price, USD-denominated (e.g. "0.001"); a "$" is added if absent
	Chain           string            // base | base-sepolia | ethereum | polygon
	FacilitatorURL  string            // x402 facilitator (verify/settle); caller supplies a default
	Upstream        string            // backend HTTP service the paid tool POSTs the buyer's JSON args to (e.g. a weather/data API)
	UpstreamHeaders map[string]string // optional auth headers for the backend (e.g. "X-Api-Key": "<key>"); set server-side, never exposed to buyers

	// BountyReportsDir, when set, registers the free bounty_report tool
	// serving ServiceBounty A2UI reports from
	// <dir>/<namespace>/<name>/<variant surface file>.
	BountyReportsDir string
}

// Serve builds and runs the x402-paid MCP server in the foreground over
// streamable HTTP (endpoint /mcp). It blocks until ctx is cancelled or the
// server stops.
func Serve(ctx context.Context, opts Options) error {
	if strings.TrimSpace(opts.PayTo) == "" {
		return errors.New("pay-to address is required")
	}
	network, ok := caip2[strings.ToLower(strings.TrimSpace(opts.Chain))]
	if !ok {
		return fmt.Errorf("unsupported chain %q (want base, base-sepolia, ethereum, polygon)", opts.Chain)
	}
	if opts.ToolName == "" {
		opts.ToolName = "call"
	}
	if opts.Port == 0 {
		opts.Port = 4022
	}
	price := opts.Price
	if price != "" && !strings.HasPrefix(price, "$") {
		price = "$" + price
	}

	// x402 resource server + facilitator client (verify/settle over HTTP).
	facilitator := x402http.NewHTTPFacilitatorClient(&x402http.FacilitatorConfig{URL: opts.FacilitatorURL})
	resource := x402.Newx402ResourceServer(x402.WithFacilitatorClient(facilitator))
	resource.Register(x402.Network(network), evmserver.NewExactEvmScheme())
	if err := resource.Initialize(ctx); err != nil {
		return fmt.Errorf("initialize x402 resource server: %w", err)
	}

	accepts, err := resource.BuildPaymentRequirementsFromConfig(ctx, x402.ResourceConfig{
		Scheme:  "exact",
		Network: x402.Network(network),
		PayTo:   opts.PayTo,
		Price:   price,
	})
	if err != nil {
		return fmt.Errorf("build payment requirements: %w", err)
	}

	wrapper := mcp402.NewPaymentWrapper(resource, mcp402.PaymentWrapperConfig{
		Accepts: accepts,
		Resource: &mcp402.ResourceInfo{
			URL:         "mcp://tool/" + opts.ToolName,
			Description: nonEmpty(opts.Description, "Paid MCP tool"),
			MimeType:    "application/json",
		},
	})

	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    nonEmpty(opts.Name, "obol x402 MCP server"),
		Version: "0.1.0",
	}, nil)

	// Free health-check tool (unwrapped — no payment).
	server.AddTool(&mcpsdk.Tool{
		Name:        "ping",
		Description: "Free health check.",
		InputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return textResult("pong"), nil
	})

	// Free bounty-report tool (unwrapped — reports are gate:local in v1; the
	// mcp-x402 gate wraps this same handler with the payment wrapper later).
	if strings.TrimSpace(opts.BountyReportsDir) != "" {
		AddBountyReportTool(server, opts.BountyReportsDir)
	}

	// Paid tool: forward the buyer's JSON arguments to the backend service and
	// return the response. The arg shape is the backend's own request body —
	// documented by the operator in opts.Description (e.g. a get_weather tool:
	// {city}).
	server.AddTool(&mcpsdk.Tool{
		Name:        opts.ToolName,
		Description: nonEmpty(opts.Description, fmt.Sprintf("Paid proxy to a backend service (%s per call). Arguments are forwarded as the service request body.", price)),
		InputSchema: map[string]any{
			"type":                 "object",
			"description":          "Forwarded verbatim as the upstream service request body.",
			"additionalProperties": true,
		},
	}, wrapper.Wrap(func(reqCtx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		body := []byte(req.Params.Arguments)
		if len(body) == 0 {
			body = []byte("{}")
		}
		out, err := proxyTool(reqCtx, opts.Upstream, opts.UpstreamHeaders, body)
		if err != nil {
			return errResult(fmt.Sprintf("upstream error: %v", err)), nil
		}
		return textResult(out), nil
	}))

	mux := http.NewServeMux()
	streamable := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return server }, nil)
	mux.Handle("/mcp", streamable)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "tool": opts.ToolName})
	})

	addr := fmt.Sprintf(":%d", opts.Port)
	log.Printf("obol sell mcp: serving tool %q (paid %s -> %s on %s) at http://localhost%s/mcp",
		opts.ToolName, price, opts.PayTo, opts.Chain, addr)
	log.Printf("  facilitator: %s | upstream: %s", opts.FacilitatorURL, opts.Upstream)

	httpServer := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		// Derive from ctx (not Background) but drop its cancellation so the
		// graceful-shutdown deadline is independent of the already-cancelled parent.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// proxyTool POSTs the buyer's tool arguments (raw JSON) to the seller's backend
// HTTP service, injecting the seller's headers (e.g. an API key the buyer never
// sees), and returns the response body verbatim. This makes any credentialed
// HTTP service sellable as a single paid MCP tool.
func proxyTool(ctx context.Context, upstream string, headers map[string]string, body []byte) (string, error) {
	if strings.TrimSpace(upstream) == "" {
		return "", errors.New("no --upstream configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return string(raw), nil
}

func textResult(s string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: s}}}
}

func errResult(s string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: s}},
		IsError: true,
	}
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
