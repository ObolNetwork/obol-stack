// Package x402scan registers this stack's public origin in the x402scan.com
// discovery index (https://x402scan.com/discovery/spec). Registration is a
// SIWX-authenticated POST: the registry answers an unauthenticated request
// with a 402 carrying a Sign-In-With-X (EIP-4361 / SIWE) challenge, the
// client signs it EIP-191 with the agent wallet, and retries with the signed
// payload base64-encoded in the SIGN-IN-WITH-X header. x402scan then crawls
// the origin's /openapi.json, probes each advertised endpoint for a real
// x402 402 challenge, and indexes the ones that pass.
package x402scan

import (
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// SIWXInfo is the challenge issued by the registry under
// extensions["sign-in-with-x"].info in the 402 body. The signed payload
// echoes these fields verbatim (plus address and signature) — the server
// re-renders the SIWE message from them to verify the signature, so they
// must round-trip unmodified.
type SIWXInfo struct {
	Domain         string   `json:"domain"`
	URI            string   `json:"uri"`
	Version        string   `json:"version"`
	ChainID        string   `json:"chainId"` // CAIP-2, e.g. "eip155:8453"
	Type           string   `json:"type"`    // signature scheme, "eip191"
	Nonce          string   `json:"nonce"`
	IssuedAt       string   `json:"issuedAt"`
	ExpirationTime string   `json:"expirationTime,omitempty"`
	NotBefore      string   `json:"notBefore,omitempty"`
	RequestID      string   `json:"requestId,omitempty"`
	Statement      string   `json:"statement,omitempty"`
	Resources      []string `json:"resources,omitempty"`
}

// FormatSIWEMessage renders the EIP-4361 message for a challenge and signer
// address. Layout follows the spec ABNF exactly — the server re-renders the
// same message from the payload fields, so any deviation (a missing blank
// line, a lowercase address) makes signature verification fail.
func FormatSIWEMessage(info SIWXInfo, addr common.Address) string {
	var b strings.Builder
	// addr.Hex() is EIP-55 checksummed, which SIWE requires.
	fmt.Fprintf(&b, "%s wants you to sign in with your Ethereum account:\n%s\n\n", info.Domain, addr.Hex())
	if info.Statement != "" {
		b.WriteString(info.Statement + "\n")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "URI: %s\n", info.URI)
	fmt.Fprintf(&b, "Version: %s\n", info.Version)
	fmt.Fprintf(&b, "Chain ID: %s\n", numericChainID(info.ChainID))
	fmt.Fprintf(&b, "Nonce: %s\n", info.Nonce)
	fmt.Fprintf(&b, "Issued At: %s", info.IssuedAt)
	if info.ExpirationTime != "" {
		fmt.Fprintf(&b, "\nExpiration Time: %s", info.ExpirationTime)
	}
	if info.NotBefore != "" {
		fmt.Fprintf(&b, "\nNot Before: %s", info.NotBefore)
	}
	if info.RequestID != "" {
		fmt.Fprintf(&b, "\nRequest ID: %s", info.RequestID)
	}
	if len(info.Resources) > 0 {
		b.WriteString("\nResources:")
		for _, r := range info.Resources {
			fmt.Fprintf(&b, "\n- %s", r)
		}
	}
	return b.String()
}

// numericChainID strips the CAIP-2 namespace ("eip155:8453" -> "8453") for
// the SIWE "Chain ID" line, which is numeric per EIP-4361.
func numericChainID(caip2 string) string {
	if _, id, ok := strings.Cut(caip2, ":"); ok {
		return id
	}
	return caip2
}
