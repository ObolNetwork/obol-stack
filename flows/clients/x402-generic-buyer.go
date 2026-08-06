//go:build ignore

// Generic x402 HTTP buyer client for flow-22-external-buyer-compat.
//
// Built entirely from the public x402-foundation/x402/go/v2 SDK's
// documented client pattern (see CLIENT.md "Basic HTTP Client" in the SDK
// module): wrap a plain *http.Client with x402 payment handling and call it
// like any other HTTP client. No Obol CLI, no buy.py, no PurchaseRequest CR
// — this is what a third-party buyer tool's own wallet/agent does under the
// hood (AgentCash, Bankr, or any other x402-compliant client), so a
// successful "paid" run here is evidence the seller works with any
// standards-compliant buyer, not just Obol's own tooling.
//
// Run from the repo root (module context):
//
//	go run flows/clients/x402-generic-buyer.go \
//	  -mode <unpaid|paid> -url <resource-url> \
//	  [-method GET|POST] [-body '<json>']
//
// Modes:
//
//	unpaid  call the endpoint with no signer registered — expect a 402
//	paid    call the endpoint with a signer from X402_CLIENT_KEY — the
//	        wrapped client detects the 402, signs, retries, and returns
//	        whatever the seller sends back (expect 200 on success)
//
// -method/-body cover both the plain HTTP demo (GET, empty body) and the
// OpenAI-compatible chat-completions shape (POST + JSON body) used by
// inference/agent offers.
//
// X402_CLIENT_KEY carries the buyer's 0x-prefixed private key via env so it
// never appears on argv. Output is ONE JSON object on stdout; the flow
// asserts on its fields.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	x402 "github.com/x402-foundation/x402/go/v2"
	x402http "github.com/x402-foundation/x402/go/v2/http"
	exactclient "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/client"
	evmsigner "github.com/x402-foundation/x402/go/v2/signers/evm"
)

func main() {
	url := flag.String("url", "", "paid resource URL")
	mode := flag.String("mode", "paid", "unpaid|paid")
	network := flag.String("network", "eip155:84532", "CAIP-2 network to register the signer for")
	method := flag.String("method", "GET", "HTTP method")
	body := flag.String("body", "", "optional request body (e.g. chat-completions JSON)")
	timeout := flag.Duration("timeout", 120*time.Second, "per-request timeout")
	flag.Parse()

	out := map[string]any{"mode": *mode, "method": strings.ToUpper(*method)}
	if *url == "" {
		out["error"] = "-url is required"
		emit(out)
		os.Exit(1)
	}
	if err := run(*url, *mode, *network, strings.ToUpper(*method), *body, *timeout, out); err != nil {
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

func run(url, mode, network, method, body string, timeout time.Duration, out map[string]any) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// mode=unpaid deliberately skips the x402 wrapper entirely — a real
	// unpaid buyer is just a plain HTTP client with no x402 awareness, and
	// the wrapper's RoundTrip returns an error (not a 402 response) when no
	// scheme is registered for the seller's network, since it can't build a
	// payment payload. Bypassing it here is what lets this mode observe the
	// seller's raw 402 challenge, exactly like a naive caller would.
	httpClient := http.DefaultClient
	if mode == "paid" {
		key := os.Getenv("X402_CLIENT_KEY")
		if key == "" {
			return fmt.Errorf("X402_CLIENT_KEY is required for -mode paid")
		}
		signer, err := evmsigner.NewClientSignerFromPrivateKey(key)
		if err != nil {
			return fmt.Errorf("signer: %w", err)
		}
		out["buyerAddress"] = signer.Address()

		// Following CLIENT.md's quick-start verbatim: create the core
		// client, register the scheme, wrap a plain http.Client.
		client := x402.Newx402Client().
			Register(x402.Network(network), exactclient.NewExactEvmScheme(signer, nil))
		httpClient = x402http.WrapHTTPClientWithPayment(http.DefaultClient, x402http.Newx402HTTPClient(client))
	}

	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	out["status"] = resp.StatusCode
	out["body"] = string(respBody)
	if v := resp.Header.Get("X-PAYMENT-RESPONSE"); v != "" {
		out["paymentResponseHeader"] = v
	}
	if v := resp.Header.Get("PAYMENT-RESPONSE"); v != "" {
		out["paymentResponseHeaderV2"] = v
	}
	return nil
}
