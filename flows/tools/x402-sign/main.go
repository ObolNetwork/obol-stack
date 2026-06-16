// Command x402-sign is a host-side raw-X-PAYMENT signer for tests and smokes.
//
// It reads an x402 402 challenge JSON on stdin (the `{accepts:[...]}` body a
// seller returns, or a bare PaymentRequirements object) and a signer private
// key, and prints the base64 `X-PAYMENT` header value for accepts[0] to stdout.
//
// This is the host-side equivalent of what `obol buy dataset --join` does
// internally — it exposes the same x402.SignExactPayment primitive for the
// raw-X-PAYMENT seller paths (e.g. `obol sell inference`) that have no buyer
// CLI, so a smoke can drive a real paid request end to end.
//
//	curl -s seller/402 | x402-sign --key 0x<priv> > xpay.txt
//	curl seller -H "X-PAYMENT: $(cat xpay.txt)" ...
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/ethereum/go-ethereum/crypto"
	x402types "github.com/x402-foundation/x402/go/types"
)

func main() {
	keyHex := flag.String("key", os.Getenv("X402_SIGN_KEY"), "signer private key (hex, 0x-optional; or env X402_SIGN_KEY)")
	flag.Parse()

	if strings.TrimSpace(*keyHex) == "" {
		fatal("--key (or X402_SIGN_KEY) is required")
	}
	key, err := crypto.HexToECDSA(strings.TrimPrefix(strings.TrimSpace(*keyHex), "0x"))
	if err != nil {
		fatal("bad signer key: %v", err)
	}

	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		fatal("read stdin: %v", err)
	}

	req, err := firstRequirement(raw)
	if err != nil {
		fatal("%v", err)
	}

	xpay, err := x402.SignExactPayment(key, req)
	if err != nil {
		fatal("sign: %v", err)
	}
	fmt.Println(xpay)
}

// firstRequirement pulls accepts[0] from a 402 challenge body, or accepts a
// bare PaymentRequirements object.
func firstRequirement(raw []byte) (x402types.PaymentRequirements, error) {
	var challenge struct {
		Accepts []x402types.PaymentRequirements `json:"accepts"`
	}
	if err := json.Unmarshal(raw, &challenge); err == nil && len(challenge.Accepts) > 0 {
		return challenge.Accepts[0], nil
	}
	var pr x402types.PaymentRequirements
	if err := json.Unmarshal(raw, &pr); err == nil && pr.Scheme != "" {
		return pr, nil
	}
	return x402types.PaymentRequirements{}, fmt.Errorf("input is neither a 402 challenge with accepts[] nor a PaymentRequirements object")
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "x402-sign: "+format+"\n", a...)
	os.Exit(1)
}
