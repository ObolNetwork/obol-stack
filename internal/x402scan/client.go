package x402scan

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// DefaultBaseURL is the public x402scan registry.
const DefaultBaseURL = "https://x402scan.com"

const registerOriginPath = "/api/x402/registry/register-origin"

// siwxHeader carries the base64-encoded signed SIWX payload on the retry.
const siwxHeader = "SIGN-IN-WITH-X"

// Sentinel errors for the registry's two well-known rejection modes so the
// CLI can print targeted fix-it guidance.
var (
	// ErrNoDiscovery: the registry could not find a discovery document
	// (openapi.json / .well-known/x402) at the origin. HTTP 404.
	ErrNoDiscovery = errors.New("x402scan found no discovery document at the origin")
	// ErrNoValidResources: a discovery document was found but no advertised
	// endpoint passed the live x402 402 probe. HTTP 422.
	ErrNoValidResources = errors.New("x402scan found no valid paid resources at the origin")
)

// MessageSigner signs a plain-text message EIP-191 style with the key held
// for addr. *erc8004.RemoteSigner satisfies this.
type MessageSigner interface {
	SignMessage(ctx context.Context, addr common.Address, message string) (string, error)
}

// Client talks to an x402scan-compatible registry.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient returns a registry client. An empty baseURL selects the public
// x402scan.com instance.
func NewClient(baseURL string) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		// Registration crawls the origin and live-probes every advertised
		// endpoint before answering, so allow well over the registry's own
		// 10s-per-fetch discovery budget.
		http: &http.Client{Timeout: 120 * time.Second},
	}
}

// RegisterResult is the registry's summary of a registration run.
type RegisterResult struct {
	Success      bool             `json:"success"`
	Registered   int              `json:"registered"`
	SIWX         int              `json:"siwx"`
	Failed       int              `json:"failed"`
	Skipped      int              `json:"skipped"`
	Deprecated   int              `json:"deprecated"`
	Total        int              `json:"total"`
	Source       string           `json:"source"`
	FailedList   []FailedResource `json:"failedDetails,omitempty"`
	SIWXList     []SIWXResource   `json:"siwxDetails,omitempty"`
	ContactEmail string           `json:"contactEmail,omitempty"`
	Warning      string           `json:"warning,omitempty"`
}

// FailedResource describes one advertised endpoint that failed the
// registry's live probe.
type FailedResource struct {
	URL    string `json:"url"`
	Error  string `json:"error"`
	Status int    `json:"status,omitempty"`
}

// SIWXResource is an identity-only (wallet-gated, unpaid) endpoint.
type SIWXResource struct {
	URL string `json:"url"`
}

// registryError is the {"success":false,"error":{...}} failure envelope.
// FailedDetails mirrors RegisterResult.FailedList: the registry may carry
// the same per-endpoint probe diagnostics on a 422 rejection as it does on
// a 200-with-partial-failures response, so a genuinely-no-402 origin can be
// told apart from a per-endpoint reason (wrong network, timeout, ...).
type registryError struct {
	Success bool `json:"success"`
	Error   struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
	FailedDetails []FailedResource `json:"failedDetails,omitempty"`
}

// challengeBody is the subset of the 402 response we need: the SIWX
// challenge under extensions["sign-in-with-x"].
type challengeBody struct {
	Extensions struct {
		SignInWithX struct {
			Info            SIWXInfo `json:"info"`
			SupportedChains []struct {
				ChainID string `json:"chainId"`
				Type    string `json:"type"`
			} `json:"supportedChains"`
		} `json:"sign-in-with-x"`
	} `json:"extensions"`
}

// siwxPayload is the signed proof sent back in the SIGN-IN-WITH-X header:
// the challenge fields verbatim plus the signer address and signature.
type siwxPayload struct {
	SIWXInfo
	Address   string `json:"address"`
	Signature string `json:"signature"`
}

// RegisterOrigin registers origin in the discovery index, authenticating as
// addr via signer. It performs the full SIWX handshake: unauthenticated POST
// -> 402 challenge -> EIP-191 sign -> authenticated retry.
func (c *Client) RegisterOrigin(ctx context.Context, origin string, addr common.Address, signer MessageSigner) (*RegisterResult, error) {
	// First POST without auth to obtain the SIWX challenge (nonce, domain,
	// uri, timestamps) minted by the registry.
	status, body, err := c.postRegister(ctx, origin, "")
	if err != nil {
		return nil, err
	}
	if status != http.StatusPaymentRequired {
		// Auth requirements may be relaxed in future; accept a direct answer.
		return parseRegisterResponse(status, body)
	}

	var challenge challengeBody
	if err := json.Unmarshal(body, &challenge); err != nil {
		return nil, fmt.Errorf("x402scan: parse SIWX challenge: %w (body: %.300s)", err, body)
	}
	info := challenge.Extensions.SignInWithX.Info
	if info.Nonce == "" || info.Domain == "" {
		return nil, fmt.Errorf("x402scan: 402 response carried no sign-in-with-x challenge (body: %.300s)", body)
	}
	if info.Type != "" && info.Type != "eip191" {
		return nil, fmt.Errorf("x402scan: registry requires unsupported signature scheme %q (only eip191 is supported)", info.Type)
	}

	message := FormatSIWEMessage(info, addr)
	signature, err := signer.SignMessage(ctx, addr, message)
	if err != nil {
		return nil, fmt.Errorf("sign SIWX challenge: %w", err)
	}

	payload := siwxPayload{SIWXInfo: info, Address: addr.Hex(), Signature: signature}
	if payload.Type == "" {
		payload.Type = "eip191"
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("x402scan: marshal SIWX payload: %w", err)
	}

	status, body, err = c.postRegister(ctx, origin, base64.StdEncoding.EncodeToString(encoded))
	if err != nil {
		return nil, err
	}
	if status == http.StatusPaymentRequired {
		// Auth was rejected on the retry — surface the registry's reason
		// (siwx_expired, siwx_invalid_signature, ...) rather than a bare 402.
		var authErr struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &authErr)
		if authErr.Error != "" {
			return nil, fmt.Errorf("x402scan rejected SIWX authentication: %s: %s", authErr.Error, authErr.Message)
		}
		return nil, fmt.Errorf("x402scan rejected SIWX authentication (body: %.300s)", body)
	}
	return parseRegisterResponse(status, body)
}

func (c *Client) postRegister(ctx context.Context, origin, siwx string) (int, []byte, error) {
	payload, err := json.Marshal(map[string]string{"origin": origin})
	if err != nil {
		return 0, nil, fmt.Errorf("x402scan: marshal body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+registerOriginPath, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, fmt.Errorf("x402scan: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if siwx != "" {
		req.Header.Set(siwxHeader, siwx)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("x402scan: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("x402scan: read response: %w", err)
	}
	return resp.StatusCode, body, nil
}

func parseRegisterResponse(status int, body []byte) (*RegisterResult, error) {
	switch status {
	case http.StatusOK:
		var result RegisterResult
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("x402scan: parse registration result: %w (body: %.300s)", err, body)
		}
		return &result, nil
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrNoDiscovery, registryErrorMessage(body))
	case http.StatusUnprocessableEntity:
		return nil, fmt.Errorf("%w: %s", ErrNoValidResources, registryErrorMessage(body))
	default:
		return nil, fmt.Errorf("x402scan: registration failed: HTTP %d: %.300s", status, body)
	}
}

func registryErrorMessage(body []byte) string {
	var e registryError
	if err := json.Unmarshal(body, &e); err == nil && e.Error.Message != "" {
		if len(e.FailedDetails) == 0 {
			return e.Error.Message
		}
		var b strings.Builder
		b.WriteString(e.Error.Message)
		for _, f := range e.FailedDetails {
			fmt.Fprintf(&b, "\n  - %s — %s", f.URL, f.Error)
			if f.Status != 0 {
				fmt.Fprintf(&b, " (status %d)", f.Status)
			}
		}
		return b.String()
	}
	return strings.TrimSpace(fmt.Sprintf("%.300s", body))
}
