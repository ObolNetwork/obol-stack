package inference

// client.go — cross-platform client SDK for the Obol SE inference gateway.
//
// Provides per-request encryption using the gateway's Secure Enclave public key:
//
//  1. Fetch the gateway's SE public key once (cached).
//  2. Encrypt each request body with ECIES (enclave.Encrypt).
//  3. Optionally request an encrypted response by supplying a reply key.
//
// Usage:
//
//	c, err := inference.NewClient("http://localhost:8402")
//	resp, err := c.Do(req)   // transparently encrypts request
//
// The Client satisfies http.RoundTripper, so it can be plugged into any
// OpenAI-compatible SDK:
//
//	oai := openai.NewClient(
//	    option.WithBaseURL("http://localhost:8402/v1"),
//	    option.WithHTTPClient(&http.Client{Transport: c}),
//	)

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/ObolNetwork/obol-stack/internal/enclave"
)

// Client is an http.RoundTripper that transparently encrypts request bodies
// to an Obol SE inference gateway and optionally decrypts encrypted responses.
//
// The SE public key is fetched lazily on first use and cached for the lifetime
// of the Client.
type Client struct {
	// GatewayURL is the base URL of the inference gateway (e.g. "http://localhost:8402").
	GatewayURL string

	// HTTP is the underlying transport.  Defaults to http.DefaultTransport.
	HTTP http.RoundTripper

	mu     sync.RWMutex
	pubkey []byte // cached 65-byte SE public key; nil until first fetch

	// replyKey is an optional ephemeral key used to request encrypted responses.
	// Set via EnableEncryptedReplies.
	replyKey enclave.Key
}

// NewClient creates a Client targeting the given gateway URL and eagerly
// fetches the SE public key so the first request does not block on the fetch.
func NewClient(ctx context.Context, gatewayURL string) (*Client, error) {
	c := &Client{
		GatewayURL: gatewayURL,
		HTTP:       http.DefaultTransport,
	}
	if _, err := c.fetchPubkey(ctx); err != nil {
		return nil, err
	}

	return c, nil
}

// EnableEncryptedReplies generates an ephemeral local key.  When set, the
// client attaches X-Obol-Reply-Pubkey to every encrypted request so the
// gateway encrypts the response back to this key, and Do() decrypts it
// transparently before returning.
//
// On non-darwin builds this returns enclave.ErrNotSupported because the
// decryption half requires the SE; encryption (for the request) is always
// available.
func (c *Client) EnableEncryptedReplies(tag string) error {
	k, err := enclave.NewKey(tag)
	if err != nil {
		return fmt.Errorf("inference client: generate reply key: %w", err)
	}

	c.mu.Lock()
	c.replyKey = k
	c.mu.Unlock()

	return nil
}

// Pubkey returns the cached SE public key bytes (65-byte uncompressed P-256).
// Returns nil if the key has not been fetched yet.
func (c *Client) Pubkey() []byte {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.pubkey
}

// RoundTrip implements http.RoundTripper.  If the request has a non-nil body,
// it is encrypted to the gateway's SE public key and the Content-Type is set
// to application/x-obol-encrypted.  Non-body requests (GET, HEAD, etc.) are
// forwarded as-is.
func (c *Client) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return c.transport().RoundTrip(req)
	}

	// Read and encrypt the body.
	plain, err := io.ReadAll(req.Body)
	req.Body.Close()

	if err != nil {
		return nil, fmt.Errorf("inference client: read body: %w", err)
	}

	pubkey, err := c.fetchPubkey(req.Context())
	if err != nil {
		return nil, err
	}

	ct, err := enclave.Encrypt(pubkey, plain)
	if err != nil {
		return nil, fmt.Errorf("inference client: encrypt: %w", err)
	}

	// Clone request so we don't mutate the caller's original.
	out := req.Clone(req.Context())
	out.Body = io.NopCloser(bytes.NewReader(ct))
	out.ContentLength = int64(len(ct))
	out.Header.Set("Content-Type", contentTypeEncrypted)

	// If a reply key is configured, ask the gateway to encrypt the response.
	c.mu.RLock()
	rk := c.replyKey
	c.mu.RUnlock()

	if rk != nil {
		out.Header.Set(headerReplyPubkey, hex.EncodeToString(rk.PublicKeyBytes()))
	}

	resp, err := c.transport().RoundTrip(out)
	if err != nil {
		return nil, err
	}

	// Decrypt an encrypted response.
	if rk != nil && resp.Header.Get("Content-Type") == contentTypeEncrypted {
		defer resp.Body.Close()

		enc, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("inference client: read encrypted response: %w", err)
		}

		plainResp, err := rk.Decrypt(enc)
		if err != nil {
			return nil, fmt.Errorf("inference client: decrypt response: %w", err)
		}

		resp = &http.Response{
			Status:     resp.Status,
			StatusCode: resp.StatusCode,
			Header:     resp.Header.Clone(),
			Body:       io.NopCloser(bytes.NewReader(plainResp)),
		}
		resp.Header.Set("Content-Type", "application/json")
	}

	return resp, nil
}

// Do sends req using the client's transport (with SE encryption applied).
// It is a convenience wrapper around RoundTrip that matches http.Client.Do's
// signature.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	return c.RoundTrip(req)
}

// fetchPubkey returns the cached SE public key, fetching it from the gateway
// if not yet available.
func (c *Client) fetchPubkey(ctx context.Context) ([]byte, error) {
	c.mu.RLock()

	if c.pubkey != nil {
		pk := c.pubkey
		c.mu.RUnlock()

		return pk, nil
	}

	c.mu.RUnlock()

	// Fetch under write lock to avoid thundering herd.
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.pubkey != nil { // double-check after acquiring write lock
		return c.pubkey, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.GatewayURL+"/v1/enclave/pubkey", nil) //nolint:gosec // G704: URL from user-configured GatewayURL, not tainted input
	if err != nil {
		return nil, fmt.Errorf("inference client: build pubkey request: %w", err)
	}

	resp, err := c.transport().RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("inference client: fetch pubkey: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("inference client: pubkey endpoint returned %s", resp.Status)
	}

	var body pubkeyJSON
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("inference client: decode pubkey response: %w", err)
	}

	pk, err := hex.DecodeString(body.Pubkey)
	if err != nil {
		return nil, fmt.Errorf("inference client: decode pubkey hex: %w", err)
	}

	c.pubkey = pk

	return pk, nil
}

func (c *Client) transport() http.RoundTripper {
	if c.HTTP != nil {
		return c.HTTP
	}

	return http.DefaultTransport
}
