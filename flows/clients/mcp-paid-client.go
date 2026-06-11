//go:build ignore

// Paid-MCP test client for flow-17-sell-mcp. Connects to an `obol sell
// mcp` server over streamable HTTP and exercises the x402 in-band
// (_meta["x402/payment"]) loop with the same SDK the server is built on.
//
// Run from the repo root (module context):
//
//	go run flows/clients/mcp-paid-client.go -mode <free|requirements|unpaid|paid> \
//	    -url http://localhost:4022/mcp -tool get_weather -args '{"city":"Paris"}'
//
// Modes:
//
//	free          call the free `ping` tool — expect success, no payment
//	requirements  surface the paid tool's payment requirements without paying
//	unpaid        call the paid tool with NO signer — expect a payment error
//	paid          call the paid tool with auto-payment (EIP-3009 typed-data
//	              signature from MCP_CLIENT_KEY; offline, no RPC needed)
//
// MCP_CLIENT_KEY carries the buyer's 0x-prefixed private key via env so it
// never appears on argv. Output is ONE JSON object on stdout; the flow
// asserts on its fields.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	x402 "github.com/x402-foundation/x402/go"
	x402mcp "github.com/x402-foundation/x402/go/mcp"
	exactclient "github.com/x402-foundation/x402/go/mechanisms/evm/exact/client"
	evmsigner "github.com/x402-foundation/x402/go/signers/evm"
)

func main() {
	url := flag.String("url", "http://localhost:4022/mcp", "MCP endpoint URL")
	mode := flag.String("mode", "paid", "free|requirements|unpaid|paid")
	tool := flag.String("tool", "get_weather", "paid tool name")
	argsJSON := flag.String("args", `{"city":"Paris"}`, "tool args as JSON")
	network := flag.String("network", "eip155:84532", "CAIP-2 network for the signer scheme")
	flag.Parse()

	out := map[string]any{"mode": *mode}
	if err := run(*url, *mode, *tool, *argsJSON, *network, out); err != nil {
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

func run(url, mode, tool, argsJSON, network string, out map[string]any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Errorf("parse -args: %w", err)
	}

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "flow17-paid-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: url}, nil)
	if err != nil {
		return fmt.Errorf("mcp connect: %w", err)
	}
	defer session.Close()

	// Signer is optional: free/requirements/unpaid modes run without one,
	// which also proves the server rejects unpaid calls cleanly.
	var regs []x402mcp.SchemeRegistration
	if key := os.Getenv("MCP_CLIENT_KEY"); key != "" {
		signer, err := evmsigner.NewClientSignerFromPrivateKey(key)
		if err != nil {
			return fmt.Errorf("signer: %w", err)
		}
		regs = append(regs, x402mcp.SchemeRegistration{
			Network: x402.Network(network),
			Client:  exactclient.NewExactEvmScheme(signer, nil),
		})
	}

	pc := x402mcp.NewX402MCPClientFromConfig(session, regs, x402mcp.Options{})

	switch mode {
	case "free":
		res, err := pc.CallTool(ctx, "ping", map[string]any{})
		if err != nil {
			return fmt.Errorf("ping: %w", err)
		}
		fillResult(out, res)
	case "requirements":
		req, err := pc.GetToolPaymentRequirements(ctx, tool, args)
		if err != nil {
			return fmt.Errorf("requirements: %w", err)
		}
		b, err := json.Marshal(req)
		if err != nil {
			return fmt.Errorf("marshal requirements: %w", err)
		}
		out["requirements"] = json.RawMessage(b)
	case "unpaid", "paid":
		res, err := pc.CallTool(ctx, tool, args)
		if err != nil {
			return fmt.Errorf("call %s: %w", tool, err)
		}
		fillResult(out, res)
	default:
		return fmt.Errorf("unknown -mode %q", mode)
	}
	return nil
}

func fillResult(out map[string]any, res *x402mcp.MCPToolCallResult) {
	out["isError"] = res.IsError
	out["paymentMade"] = res.PaymentMade
	out["content"] = firstText(res)
	if res.PaymentResponse != nil {
		out["settleSuccess"] = res.PaymentResponse.Success
		out["settleTx"] = res.PaymentResponse.Transaction
		out["settleNetwork"] = string(res.PaymentResponse.Network)
		out["settlePayer"] = res.PaymentResponse.Payer
	}
}

func firstText(res *x402mcp.MCPToolCallResult) string {
	for _, c := range res.Content {
		if c.Text != "" {
			return c.Text
		}
	}
	return ""
}
