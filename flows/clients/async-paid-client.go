//go:build ignore

// Generic paid-HTTP test client for flow-20-async-job. Wraps a plain
// *http.Client with the x402 Go SDK's own HTTP payment wrapper
// (x402http.WrapHTTPClientWithPayment, documented in the SDK's CLIENT.md
// "Basic HTTP Client" recipe) so a request retried after a 402 carries a
// real EIP-3009 signature from the given private key. This is the same
// x402-foundation SDK + exact-EVM-scheme signer flow-17-sell-mcp.sh uses
// (flows/clients/mcp-paid-client.go), just applied to a raw HTTP route
// instead of an in-band MCP payment — flow-08-buy.sh's `buy.py` /
// PurchaseRequest machinery is specific to the LiteLLM inference-purchase
// product and has no equivalent for an arbitrary paid HTTP endpoint, so it
// does not apply here.
//
// Run from the repo root (module context):
//
//	ASYNC_CLIENT_KEY=0x... go run flows/clients/async-paid-client.go \
//	    -url http://obol.stack:PORT/services/flow-async/api/version \
//	    -mode paid -method GET -resolve-ip 127.0.0.1
//
// Modes:
//
//	unpaid  plain request, no signer registered — expect the server's 402
//	paid    wrap with the signer from ASYNC_CLIENT_KEY — expect the
//	        server's real (post-payment) response, e.g. 202 for an async
//	        submit
//
// -resolve-ip, when set, forces every outbound connection to that IP
// (port from the dialed address is preserved) — the Go-side equivalent of
// the `curl --resolve obol.stack:<port>:127.0.0.1` trick flows/lib.sh uses,
// needed because "obol.stack" resolves via /etc/hosts + mDNS, which is
// slow/flaky on macOS.
//
// ASYNC_CLIENT_KEY carries the buyer's 0x-prefixed private key via env so
// it never appears on argv (mirrors MCP_CLIENT_KEY in mcp-paid-client.go).
// Output is ONE JSON object on stdout; the flow asserts on its fields.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	x402 "github.com/x402-foundation/x402/go/v2"
	x402http "github.com/x402-foundation/x402/go/v2/http"
	exactclient "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/client"
	evmsigner "github.com/x402-foundation/x402/go/v2/signers/evm"
)

func main() {
	url := flag.String("url", "", "target URL")
	mode := flag.String("mode", "paid", "unpaid|paid")
	method := flag.String("method", "GET", "HTTP method")
	network := flag.String("network", "eip155:84532", "CAIP-2 network for the signer scheme")
	resolveIP := flag.String("resolve-ip", "", "force every dial to this IP (port preserved) — Go equivalent of curl --resolve")
	authHeader := flag.String("auth", "", "optional Authorization header value (e.g. 'Bearer <jobToken>')")
	flag.Parse()

	if *url == "" {
		fmt.Fprintln(os.Stderr, "-url required")
		os.Exit(2)
	}

	out := map[string]any{"mode": *mode}
	if err := run(*url, *mode, *method, *network, *resolveIP, *authHeader, out); err != nil {
		out["error"] = err.Error()
		emit(out)
		os.Exit(1)
	}
	emit(out)
}

func emit(m map[string]any) {
	b, _ := json.Marshal(m)
	fmt.Println(string(b))
}

func run(url, mode, method, network, resolveIP, authHeader string, out map[string]any) error {
	transport := &http.Transport{}
	if resolveIP != "" {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		transport.DialContext = func(ctx context.Context, netw, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return dialer.DialContext(ctx, netw, addr)
			}
			return dialer.DialContext(ctx, netw, net.JoinHostPort(resolveIP, port))
		}
	}

	plain := &http.Client{Transport: transport, Timeout: 60 * time.Second}

	httpClient := plain
	if mode == "paid" {
		key := os.Getenv("ASYNC_CLIENT_KEY")
		if key == "" {
			return fmt.Errorf("ASYNC_CLIENT_KEY not set")
		}
		signer, err := evmsigner.NewClientSignerFromPrivateKey(key)
		if err != nil {
			return fmt.Errorf("signer: %w", err)
		}
		x402Client := x402.Newx402Client().
			Register(x402.Network(network), exactclient.NewExactEvmScheme(signer, nil))
		httpClient = x402http.WrapHTTPClientWithPayment(plain, x402http.Newx402HTTPClient(x402Client))
	}

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	out["status"] = resp.StatusCode
	out["body"] = string(body)
	out["location"] = resp.Header.Get("Location")
	out["wwwAuthenticate"] = resp.Header.Get("WWW-Authenticate")
	out["contentType"] = resp.Header.Get("Content-Type")
	return nil
}
