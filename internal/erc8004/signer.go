package erc8004

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
)

// RemoteSigner wraps the remote-signer REST API for signing operations.
// It communicates with the signer over HTTP (typically via a port-forward).
type RemoteSigner struct {
	baseURL string
	client  *http.Client
}

// NewRemoteSigner creates a client for the remote-signer API at the given base URL.
func NewRemoteSigner(baseURL string) *RemoteSigner {
	return &RemoteSigner{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// keysResponse is the response from GET /api/v1/keys.
type keysResponse struct {
	Keys []string `json:"keys"`
}

// GetAddress returns the first loaded signing address from the remote-signer.
func (s *RemoteSigner) GetAddress(ctx context.Context) (common.Address, error) {
	url := s.baseURL + "/api/v1/keys"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return common.Address{}, fmt.Errorf("remote-signer: build request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return common.Address{}, fmt.Errorf("remote-signer: get keys: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return common.Address{}, fmt.Errorf("remote-signer: get keys: HTTP %d: %s", resp.StatusCode, body)
	}

	var kr keysResponse
	if err := json.NewDecoder(resp.Body).Decode(&kr); err != nil {
		return common.Address{}, fmt.Errorf("remote-signer: decode keys: %w", err)
	}
	if len(kr.Keys) == 0 {
		return common.Address{}, fmt.Errorf("remote-signer: no signing keys loaded")
	}

	return common.HexToAddress(kr.Keys[0]), nil
}

// SignTxRequest contains the fields for signing an EIP-1559 transaction.
// All numeric fields are sent as JSON integers (u64) to match the Rust
// remote-signer's expected types — sending any of them as strings causes HTTP 422.
type SignTxRequest struct {
	ChainID              int64  `json:"chain_id"`
	To                   string `json:"to"`
	Nonce                uint64 `json:"nonce"`
	GasLimit             uint64 `json:"gas_limit"`
	MaxFeePerGas         uint64 `json:"max_fee_per_gas"`
	MaxPriorityFeePerGas uint64 `json:"max_priority_fee_per_gas"`
	Value                uint64 `json:"value"`
	Data                 string `json:"data"`
}

// signResponse is the response from signing endpoints.
type signResponse struct {
	SignedTransaction string `json:"signed_transaction,omitempty"`
	Signature         string `json:"signature,omitempty"`
	Error             string `json:"error,omitempty"`
}

// SignTransaction signs an EIP-1559 transaction via the remote-signer.
// Returns the RLP-encoded signed transaction bytes (hex-encoded with 0x prefix).
func (s *RemoteSigner) SignTransaction(ctx context.Context, addr common.Address, tx SignTxRequest) (string, error) {
	url := fmt.Sprintf("%s/api/v1/sign/%s/transaction", s.baseURL, addr.Hex())

	body, err := json.Marshal(tx)
	if err != nil {
		return "", fmt.Errorf("remote-signer: marshal tx: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("remote-signer: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("remote-signer: sign transaction: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("remote-signer: sign transaction: HTTP %d: %s", resp.StatusCode, body)
	}

	var sr signResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return "", fmt.Errorf("remote-signer: decode response: %w", err)
	}
	if sr.Error != "" {
		return "", fmt.Errorf("remote-signer: %s", sr.Error)
	}

	return sr.SignedTransaction, nil
}

// EIP712TypedData represents a full EIP-712 typed data structure for signing.
type EIP712TypedData struct {
	Types       map[string][]EIP712Field `json:"types"`
	PrimaryType string                   `json:"primaryType"`
	Domain      map[string]interface{}   `json:"domain"`
	Message     map[string]interface{}   `json:"message"`
}

// EIP712Field describes a single field in an EIP-712 type.
type EIP712Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// SignTypedData signs EIP-712 typed data via the remote-signer.
// Returns the 65-byte signature as a hex string with 0x prefix.
func (s *RemoteSigner) SignTypedData(ctx context.Context, addr common.Address, data EIP712TypedData) (string, error) {
	url := fmt.Sprintf("%s/api/v1/sign/%s/typed-data", s.baseURL, addr.Hex())

	body, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("remote-signer: marshal typed data: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("remote-signer: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("remote-signer: sign typed data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("remote-signer: sign typed data: HTTP %d: %s", resp.StatusCode, body)
	}

	var sr signResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return "", fmt.Errorf("remote-signer: decode response: %w", err)
	}
	if sr.Error != "" {
		return "", fmt.Errorf("remote-signer: %s", sr.Error)
	}

	return sr.Signature, nil
}

// RemoteTransactOpts creates a bind.TransactOpts that delegates signing to the
// remote-signer. The returned opts can be used with Client.RegisterWithOpts and
// Client.SetMetadataWithOpts.
func (s *RemoteSigner) RemoteTransactOpts(ctx context.Context, addr common.Address, chainID *big.Int) *bind.TransactOpts {
	return &bind.TransactOpts{
		From:    addr,
		Context: ctx,
		Signer: func(fromAddr common.Address, tx *types.Transaction) (*types.Transaction, error) {
			// Convert the unsigned transaction to a SignTxRequest.
			var toAddr string
			if tx.To() != nil {
				toAddr = tx.To().Hex()
			}
			req := SignTxRequest{
				ChainID:  chainID.Int64(),
				To:       toAddr,
				Nonce:    tx.Nonce(),
				GasLimit: tx.Gas(),
				Value:    tx.Value().Uint64(),
				Data:     "0x" + hex.EncodeToString(tx.Data()),
			}
			// Use EIP-1559 fields if available, otherwise legacy gas price.
			if tx.GasFeeCap() != nil && tx.GasFeeCap().Sign() > 0 {
				req.MaxFeePerGas = tx.GasFeeCap().Uint64()
				req.MaxPriorityFeePerGas = tx.GasTipCap().Uint64()
			} else if tx.GasPrice() != nil {
				req.MaxFeePerGas = tx.GasPrice().Uint64()
				req.MaxPriorityFeePerGas = tx.GasPrice().Uint64()
			}

			signedHex, err := s.SignTransaction(ctx, fromAddr, req)
			if err != nil {
				return nil, fmt.Errorf("remote sign: %w", err)
			}

			// Decode the signed transaction RLP returned by the signer.
			rawBytes, err := hexToBytes(signedHex)
			if err != nil {
				return nil, fmt.Errorf("decode signed tx: %w", err)
			}

			var signedTx types.Transaction
			if err := rlp.DecodeBytes(rawBytes, &signedTx); err != nil {
				// Try as typed tx envelope (EIP-2718).
				if decErr := signedTx.UnmarshalBinary(rawBytes); decErr != nil {
					return nil, fmt.Errorf("decode signed tx (rlp: %v, binary: %v)", err, decErr)
				}
			}
			return &signedTx, nil
		},
	}
}

// hexToBytes decodes a hex string (with optional 0x prefix) to bytes.
func hexToBytes(s string) ([]byte, error) {
	if len(s) >= 2 && s[:2] == "0x" {
		s = s[2:]
	}
	return hex.DecodeString(s)
}
