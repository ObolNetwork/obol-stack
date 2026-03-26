package erc8004

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// SponsoredRegisterRequest is the request body for the sponsored registration API.
type SponsoredRegisterRequest struct {
	AgentAddress    string                 `json:"agentAddress"`
	AgentURI        string                 `json:"agentURI"`
	Deadline        int64                  `json:"deadline"`
	IntentSignature string                 `json:"intentSignature"`
	Authorization   SponsorAuthorization   `json:"authorization"`
}

// SponsorAuthorization is the EIP-7702 authorization included in sponsored registration.
type SponsorAuthorization struct {
	Address string `json:"address"`
	ChainID int64  `json:"chainId"`
	Nonce   int64  `json:"nonce"`
	R       string `json:"r"`
	S       string `json:"s"`
	YParity int    `json:"yParity"`
}

// SponsoredRegisterResponse is the response from the sponsored registration API.
type SponsoredRegisterResponse struct {
	Success bool   `json:"success"`
	AgentID int64  `json:"agentId"`
	TxHash  string `json:"txHash"`
	Error   string `json:"error,omitempty"`
}

// SponsoredRegister performs a zero-gas registration via the sponsor API.
// It signs the required EIP-712 messages using the remote-signer, then
// submits them to the sponsor endpoint which broadcasts the transaction.
func SponsoredRegister(ctx context.Context, signer *RemoteSigner, agentURI string, net NetworkConfig) (*big.Int, string, error) {
	if !net.HasSponsor() {
		return nil, "", fmt.Errorf("network %q does not support sponsored registration", net.Name)
	}

	addr, err := signer.GetAddress(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("get signing address: %w", err)
	}

	deadline := time.Now().Add(time.Hour).Unix()

	// Sign the EIP-7702 authorization (delegates EOA to the registration contract).
	authSig, err := signAuthorization(ctx, signer, addr, net)
	if err != nil {
		return nil, "", fmt.Errorf("sign authorization: %w", err)
	}

	// Sign the registration intent.
	intentSig, err := signRegistrationIntent(ctx, signer, addr, agentURI, deadline, net)
	if err != nil {
		return nil, "", fmt.Errorf("sign registration intent: %w", err)
	}

	// Submit to sponsor.
	reqBody := SponsoredRegisterRequest{
		AgentAddress:    addr.Hex(),
		AgentURI:        agentURI,
		Deadline:        deadline,
		IntentSignature: intentSig,
		Authorization:   authSig,
	}

	result, err := postSponsor(ctx, signer.client, net.SponsorURL, reqBody)
	if err != nil {
		return nil, "", err
	}

	return big.NewInt(result.AgentID), result.TxHash, nil
}

// signAuthorization signs the EIP-7702 authorization to delegate the EOA to
// the registration delegate contract.
func signAuthorization(ctx context.Context, signer *RemoteSigner, addr common.Address, net NetworkConfig) (SponsorAuthorization, error) {
	typedData := EIP712TypedData{
		Types: map[string][]EIP712Field{
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
			},
			"Authorization": {
				{Name: "address", Type: "address"},
				{Name: "chainId", Type: "uint256"},
				{Name: "nonce", Type: "uint256"},
			},
		},
		PrimaryType: "Authorization",
		Domain: map[string]interface{}{
			"name":    "ERC8004Registry",
			"version": "1",
			"chainId": net.ChainID,
		},
		Message: map[string]interface{}{
			"address": net.DelegateAddress,
			"chainId": net.ChainID,
			"nonce":   0,
		},
	}

	sig, err := signer.SignTypedData(ctx, addr, typedData)
	if err != nil {
		return SponsorAuthorization{}, err
	}

	r, s, v, err := splitSignature(sig)
	if err != nil {
		return SponsorAuthorization{}, fmt.Errorf("split authorization signature: %w", err)
	}

	return SponsorAuthorization{
		Address: net.DelegateAddress,
		ChainID: net.ChainID,
		Nonce:   0,
		R:       r,
		S:       s,
		YParity: v,
	}, nil
}

// signRegistrationIntent signs the registration intent message.
func signRegistrationIntent(ctx context.Context, signer *RemoteSigner, addr common.Address, agentURI string, deadline int64, net NetworkConfig) (string, error) {
	typedData := EIP712TypedData{
		Types: map[string][]EIP712Field{
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			"Register": {
				{Name: "agentAddress", Type: "address"},
				{Name: "agentURI", Type: "string"},
				{Name: "deadline", Type: "uint256"},
			},
		},
		PrimaryType: "Register",
		Domain: map[string]interface{}{
			"name":              "ERC8004Registry",
			"version":           "1",
			"chainId":           net.ChainID,
			"verifyingContract": net.RegistryAddress,
		},
		Message: map[string]interface{}{
			"agentAddress": addr.Hex(),
			"agentURI":     agentURI,
			"deadline":     deadline,
		},
	}

	return signer.SignTypedData(ctx, addr, typedData)
}

// splitSignature splits a 65-byte hex signature into r, s, v components.
func splitSignature(sig string) (r, s string, v int, err error) {
	// Remove 0x prefix if present.
	hex := sig
	if len(hex) >= 2 && hex[:2] == "0x" {
		hex = hex[2:]
	}

	if len(hex) != 130 {
		return "", "", 0, fmt.Errorf("expected 130 hex chars (65 bytes), got %d", len(hex))
	}

	r = "0x" + hex[:64]
	s = "0x" + hex[64:128]

	// v is the last byte; convert to yParity (0 or 1).
	vByte := hex[128:130]
	switch vByte {
	case "00", "1b":
		v = 0
	case "01", "1c":
		v = 1
	default:
		// Parse as decimal fallback.
		var vInt int
		if _, err := fmt.Sscanf(vByte, "%02x", &vInt); err != nil {
			return "", "", 0, fmt.Errorf("unexpected v byte: %s", vByte)
		}
		if vInt >= 27 {
			vInt -= 27
		}
		v = vInt
	}

	return r, s, v, nil
}

// postSponsor submits the sponsored registration request to the sponsor API.
func postSponsor(ctx context.Context, client *http.Client, sponsorURL string, req SponsoredRegisterRequest) (*SponsoredRegisterResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal sponsor request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, sponsorURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build sponsor request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sponsor request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read sponsor response: %w", err)
	}

	var result SponsoredRegisterResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode sponsor response: %w (body: %s)", err, respBody)
	}

	if !result.Success {
		errMsg := result.Error
		if errMsg == "" {
			errMsg = string(respBody)
		}
		return nil, fmt.Errorf("sponsored registration failed: %s", errMsg)
	}

	return &result, nil
}
