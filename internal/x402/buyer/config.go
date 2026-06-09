// Package buyer implements an x402 buyer sidecar that handles payments using
// pre-signed ERC-3009 TransferWithAuthorization vouchers. The sidecar acts as
// an OpenAI-compatible reverse proxy — it intercepts 402 responses from upstream
// sellers, attaches pre-signed payment headers, and retries automatically.
//
// The agent pre-signs a bounded batch of authorizations and stores them in a
// ConfigMap. The sidecar reads from this pool and has zero signer access.
// Spending is bounded by design: max loss = N * price.
package buyer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	x402types "github.com/x402-foundation/x402/go/types"
)

// Config is the top-level sidecar configuration, loaded from a JSON file
// mounted from the x402-buyer-config ConfigMap.
type Config struct {
	Upstreams map[string]UpstreamConfig `json:"upstreams"`
}

// UpstreamConfig describes a single x402-gated upstream endpoint.
type UpstreamConfig struct {
	// URL is the upstream base URL (e.g. "https://seller.example.com/services/qwen").
	URL string `json:"url"`

	// RemoteModel is the concrete upstream model served by this purchased route.
	// The LiteLLM paid/* namespace resolves to this model before the sidecar
	// forwards the request to the seller.
	RemoteModel string `json:"remoteModel,omitempty"`

	// Network is the blockchain network identifier (e.g. "base-sepolia").
	Network string `json:"network"`

	// PayTo is the seller's receiving address.
	PayTo string `json:"payTo"`

	// Asset is the token contract address (e.g. USDC on Base Sepolia).
	Asset string `json:"asset"`

	// AssetSymbol is the human-friendly token symbol (e.g. USDC, OBOL).
	AssetSymbol string `json:"assetSymbol,omitempty"`

	// AssetDecimals is the token precision in atomic units.
	AssetDecimals int `json:"assetDecimals,omitempty"`

	// AssetTransferMethod is the x402 transfer method (eip3009 or permit2).
	AssetTransferMethod string `json:"assetTransferMethod,omitempty"`

	// EIP712Name is the EIP-712 domain name for the token or permit flow.
	EIP712Name string `json:"eip712Name,omitempty"`

	// EIP712Version is the EIP-712 domain version for the token or permit flow.
	EIP712Version string `json:"eip712Version,omitempty"`

	// Price is the amount in atomic units per request (e.g. "1000" for 0.001 USDC).
	Price string `json:"price"`
}

// PreSignedAuth is a queued signed x402 payment. Legacy ERC-3009 auth fields are
// still supported for backward compatibility, but new entries should prefer the
// fully formed Payment payload.
type PreSignedAuth struct {
	ID          string                    `json:"id,omitempty"`
	Payment     *x402types.PaymentPayload `json:"payment,omitempty"`
	Signature   string                    `json:"signature"`
	From        string                    `json:"from"`
	To          string                    `json:"to"`
	Value       string                    `json:"value"`
	ValidAfter  string                    `json:"validAfter"`
	ValidBefore string                    `json:"validBefore"`
	Nonce       string                    `json:"nonce"`
}

// AuthsFile is the top-level structure for the pre-signed authorizations file,
// loaded from the x402-buyer-auths ConfigMap.
// Keys are upstream names matching Config.Upstreams.
type AuthsFile map[string][]*PreSignedAuth

func (a *PreSignedAuth) ConsumeKey() string {
	if a == nil {
		return ""
	}
	if a.ID != "" {
		return a.ID
	}
	if a.Nonce != "" {
		return a.Nonce
	}
	if a.Payment != nil {
		if nonce := paymentNonce(a.Payment); nonce != "" {
			return nonce
		}
	}
	if a.Signature != "" {
		return a.Signature
	}
	return ""
}

func paymentNonce(payment *x402types.PaymentPayload) string {
	if payment == nil {
		return ""
	}
	if authz, ok := payment.Payload["authorization"].(map[string]interface{}); ok {
		if nonce, ok := authz["nonce"].(string); ok {
			return nonce
		}
	}
	if authz, ok := payment.Payload["permit2Authorization"].(map[string]interface{}); ok {
		if nonce, ok := authz["nonce"].(string); ok {
			return nonce
		}
	}
	return ""
}

// LoadConfig reads and parses the sidecar config from a JSON file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if cfg.Upstreams == nil {
		cfg.Upstreams = make(map[string]UpstreamConfig)
	}

	return &cfg, nil
}

// LoadAuths reads and parses the pre-signed authorizations from a JSON file.
func LoadAuths(path string) (AuthsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read auths %s: %w", path, err)
	}

	var auths AuthsFile
	if err := json.Unmarshal(data, &auths); err != nil {
		return nil, fmt.Errorf("parse auths %s: %w", path, err)
	}

	if auths == nil {
		auths = make(AuthsFile)
	}

	return auths, nil
}

// LoadConfigDir reads per-upstream config files from a directory. Each *.json
// file is one upstream, keyed by the filename stem (e.g. "42.json" → key "42").
// This is the SSA-compatible format where the controller applies one key per
// PurchaseRequest via Server-Side Apply.
func LoadConfigDir(dir string) (*Config, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read config dir %s: %w", dir, err)
	}

	cfg := &Config{Upstreams: make(map[string]UpstreamConfig)}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		name := strings.TrimSuffix(e.Name(), ".json")
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}

		var upstream UpstreamConfig
		if err := json.Unmarshal(data, &upstream); err != nil {
			continue
		}

		cfg.Upstreams[name] = upstream
	}

	return cfg, nil
}

// LoadAuthsDir reads per-upstream auth files from a directory. Each *.json
// file contains an array of PreSignedAuth for one upstream.
func LoadAuthsDir(dir string) (AuthsFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read auths dir %s: %w", dir, err)
	}

	auths := make(AuthsFile)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		name := strings.TrimSuffix(e.Name(), ".json")
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}

		var pool []*PreSignedAuth
		if err := json.Unmarshal(data, &pool); err != nil {
			continue
		}

		auths[name] = pool
	}

	return auths, nil
}
