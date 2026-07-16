package erc8004

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// jsonrpcReq is a JSON-RPC 2.0 request.
type jsonrpcReq struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      json.RawMessage   `json:"id"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
}

// jsonrpcResp is a JSON-RPC 2.0 response.
type jsonrpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

// rpcMockDataError is the error type test handlers return when they want
// the mock to attach a `data` field on the JSON-RPC error envelope. Geth and
// Reth use the same envelope shape to carry revert payloads from
// eth_call / eth_estimateGas; go-ethereum's rpc client surfaces that as an
// rpc.DataError, which our decodeRevertReason picks up.
type rpcMockDataError struct {
	msg     string
	dataHex string // 0x-prefixed hex bytes (e.g. ABI-encoded Error(string))
}

func (e *rpcMockDataError) Error() string { return e.msg }

// mockRPC creates a test HTTP server that responds to JSON-RPC calls.
// The handler map keys are method names; values return the hex-encoded result.
func mockRPC(t *testing.T, handlers map[string]func(params []json.RawMessage) (json.RawMessage, error)) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonrpcReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Logf("mock rpc: decode error: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)

			return
		}

		handler, ok := handlers[req.Method]
		if !ok {
			t.Logf("mock rpc: unhandled method: %s", req.Method)
			resp := jsonrpcResp{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   json.RawMessage(`{"code":-32601,"message":"method not found"}`),
			}
			json.NewEncoder(w).Encode(resp)

			return
		}

		result, err := handler(req.Params)
		if err != nil {
			// Allow handlers to attach a `data` field on the JSON-RPC error
			// envelope (the spot Geth/Reth use to carry revert payloads). Plain
			// errors continue to work, so existing tests are unaffected.
			body := map[string]any{"code": -32000, "message": err.Error()}
			var de *rpcMockDataError
			if errors.As(err, &de) && de.dataHex != "" {
				body["data"] = de.dataHex
			}
			b, _ := json.Marshal(body)
			resp := jsonrpcResp{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   b,
			}
			json.NewEncoder(w).Encode(resp)

			return
		}

		resp := jsonrpcResp{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		}
		json.NewEncoder(w).Encode(resp)
	}))
}

func TestNewClient(t *testing.T) {
	handlers := map[string]func([]json.RawMessage) (json.RawMessage, error){
		"eth_chainId": func(_ []json.RawMessage) (json.RawMessage, error) {
			// Base Sepolia chain ID = 84532 = 0x14a34
			return json.RawMessage(`"0x14a34"`), nil
		},
	}

	srv := mockRPC(t, handlers)
	defer srv.Close()

	ctx := context.Background()

	client, err := newClient(ctx, srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	if client.chainID.Int64() != BaseSepoliaChainID {
		t.Errorf("chain ID = %d, want %d", client.chainID.Int64(), BaseSepoliaChainID)
	}

	if client.address != common.HexToAddress(IdentityRegistryBaseSepolia) {
		t.Errorf("address = %s, want %s", client.address.Hex(), IdentityRegistryBaseSepolia)
	}
}

func TestRegister(t *testing.T) {
	// Generate a test key.
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	// Build a fake Registered event log.
	// Registered(uint256 indexed agentId, string agentURI, address indexed owner)
	registeredABI, _ := json.Marshal([]any{})
	_ = registeredABI

	parsedABI, err := parseABI()
	if err != nil {
		t.Fatal(err)
	}

	registeredEvent := parsedABI.Events["Registered"]
	agentIDExpected := big.NewInt(42)
	ownerAddr := crypto.PubkeyToAddress(key.PublicKey)

	// ABI-encode the non-indexed param: agentURI (string).
	uriEncoded, err := parsedABI.Events["Registered"].Inputs.NonIndexed().Pack("https://example.com/.well-known/agent-registration.json")
	if err != nil {
		t.Fatalf("pack agentURI: %v", err)
	}

	fakeTxHash := common.HexToHash("0xaabbccdd")

	var nonceMu sync.Mutex

	nonce := uint64(0)

	handlers := map[string]func([]json.RawMessage) (json.RawMessage, error){
		"eth_chainId": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"0x14a34"`), nil
		},
		"eth_getCode": func(_ []json.RawMessage) (json.RawMessage, error) {
			// Return non-empty code so go-ethereum thinks the address is a contract.
			return json.RawMessage(`"0x6080"`), nil
		},
		"eth_gasPrice": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"0x3b9aca00"`), nil // 1 gwei
		},
		"eth_maxPriorityFeePerGas": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"0x3b9aca00"`), nil
		},
		"eth_getTransactionCount": func(_ []json.RawMessage) (json.RawMessage, error) {
			nonceMu.Lock()
			defer nonceMu.Unlock()

			result := fmt.Sprintf(`"0x%x"`, nonce)
			nonce++

			return json.RawMessage(result), nil
		},
		"eth_estimateGas": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"0x5208"`), nil // 21000
		},
		"eth_sendRawTransaction": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(fmt.Sprintf(`"%s"`, fakeTxHash.Hex())), nil
		},
		"eth_getTransactionReceipt": func(_ []json.RawMessage) (json.RawMessage, error) {
			// Build receipt with Registered event log.
			topic0 := registeredEvent.ID.Hex()
			topic1 := common.BigToHash(agentIDExpected).Hex()
			topic2 := common.HexToHash(ownerAddr.Hex()).Hex()

			receipt := fmt.Sprintf(`{
				"status": "0x1",
				"transactionHash": "%s",
				"blockNumber": "0x1",
				"blockHash": "0x0000000000000000000000000000000000000000000000000000000000000001",
				"transactionIndex": "0x0",
				"gasUsed": "0x5208",
				"cumulativeGasUsed": "0x5208",
				"contractAddress": null,
				"logs": [{
					"address": "%s",
					"topics": ["%s", "%s", "%s"],
					"data": "0x%s",
					"blockNumber": "0x1",
					"transactionHash": "%s",
					"transactionIndex": "0x0",
					"blockHash": "0x0000000000000000000000000000000000000000000000000000000000000001",
					"logIndex": "0x0",
					"removed": false
				}],
				"logsBloom": "0x`+strings.Repeat("0", 512)+`",
				"type": "0x2",
				"effectiveGasPrice": "0x3b9aca00"
			}`,
				fakeTxHash.Hex(),
				common.HexToAddress(IdentityRegistryBaseSepolia).Hex(),
				topic0, topic1, topic2,
				hex.EncodeToString(uriEncoded),
				fakeTxHash.Hex(),
			)

			return json.RawMessage(receipt), nil
		},
		"eth_blockNumber": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"0x1"`), nil
		},
		"eth_getBlockByNumber": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{
				"number": "0x1",
				"hash": "0x0000000000000000000000000000000000000000000000000000000000000001",
				"baseFeePerGas": "0x3b9aca00",
				"timestamp": "0x60000000",
				"gasLimit": "0x1c9c380",
				"gasUsed": "0x5208",
				"miner": "0x0000000000000000000000000000000000000000",
				"extraData": "0x",
				"parentHash": "0x0000000000000000000000000000000000000000000000000000000000000000",
				"sha3Uncles": "0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347",
				"logsBloom": "0x` + strings.Repeat("0", 512) + `",
				"transactionsRoot": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421",
				"stateRoot": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421",
				"receiptsRoot": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421",
				"mixHash": "0x0000000000000000000000000000000000000000000000000000000000000000",
				"nonce": "0x0000000000000000",
				"difficulty": "0x0",
				"totalDifficulty": "0x0",
				"size": "0x200",
				"uncles": [],
				"transactions": []
			}`), nil
		},
	}

	srv := mockRPC(t, handlers)
	defer srv.Close()

	ctx := context.Background()

	client, err := newClient(ctx, srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	opts, err := bind.NewKeyedTransactorWithChainID(key, client.chainID)
	if err != nil {
		t.Fatalf("transactor: %v", err)
	}
	opts.Context = ctx
	agentID, _, err := client.RegisterWithOptsDetailed(ctx, opts, "https://example.com/.well-known/agent-registration.json")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if agentID.Cmp(agentIDExpected) != 0 {
		t.Errorf("agentID = %s, want %s", agentID.String(), agentIDExpected.String())
	}
}

func TestGetMetadata(t *testing.T) {
	parsedABI, err := parseABI()
	if err != nil {
		t.Fatal(err)
	}

	metadataValue := []byte(`{"key":"value"}`)

	// ABI-encode the return value: bytes.
	encoded, err := parsedABI.Methods["getMetadata"].Outputs.Pack(metadataValue)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}

	handlers := map[string]func([]json.RawMessage) (json.RawMessage, error){
		"eth_chainId": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"0x14a34"`), nil
		},
		"eth_call": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(fmt.Sprintf(`"0x%s"`, hex.EncodeToString(encoded))), nil
		},
	}

	srv := mockRPC(t, handlers)
	defer srv.Close()

	ctx := context.Background()

	client, err := newClient(ctx, srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	result, err := client.GetMetadata(ctx, big.NewInt(1), "x402")
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}

	if string(result) != string(metadataValue) {
		t.Errorf("metadata = %q, want %q", string(result), string(metadataValue))
	}
}

func TestTokenURI(t *testing.T) {
	parsedABI, err := parseABI()
	if err != nil {
		t.Fatal(err)
	}

	expectedURI := "https://example.com/.well-known/agent-registration.json"

	encoded, err := parsedABI.Methods["tokenURI"].Outputs.Pack(expectedURI)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}

	handlers := map[string]func([]json.RawMessage) (json.RawMessage, error){
		"eth_chainId": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"0x14a34"`), nil
		},
		"eth_call": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(fmt.Sprintf(`"0x%s"`, hex.EncodeToString(encoded))), nil
		},
	}

	srv := mockRPC(t, handlers)
	defer srv.Close()

	ctx := context.Background()

	client, err := newClient(ctx, srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	uri, err := client.TokenURI(ctx, big.NewInt(1))
	if err != nil {
		t.Fatalf("TokenURI: %v", err)
	}

	if uri != expectedURI {
		t.Errorf("tokenURI = %q, want %q", uri, expectedURI)
	}
}

// txMockHandlers returns a handler map for write-transaction tests.
// It mocks the full tx lifecycle: chain ID, code check, gas, nonce,
// sendRawTransaction, receipt (status 0x1, empty logs), and block data.
func txMockHandlers(fakeTxHash common.Hash) map[string]func([]json.RawMessage) (json.RawMessage, error) {
	var nonceMu sync.Mutex

	nonce := uint64(0)

	return map[string]func([]json.RawMessage) (json.RawMessage, error){
		"eth_chainId": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"0x14a34"`), nil
		},
		"eth_getCode": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"0x6080"`), nil
		},
		"eth_gasPrice": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"0x3b9aca00"`), nil
		},
		"eth_maxPriorityFeePerGas": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"0x3b9aca00"`), nil
		},
		"eth_getTransactionCount": func(_ []json.RawMessage) (json.RawMessage, error) {
			nonceMu.Lock()
			defer nonceMu.Unlock()

			result := fmt.Sprintf(`"0x%x"`, nonce)
			nonce++

			return json.RawMessage(result), nil
		},
		"eth_estimateGas": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"0x5208"`), nil
		},
		"eth_sendRawTransaction": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(fmt.Sprintf(`"%s"`, fakeTxHash.Hex())), nil
		},
		"eth_getTransactionReceipt": func(_ []json.RawMessage) (json.RawMessage, error) {
			receipt := fmt.Sprintf(`{
				"status": "0x1",
				"transactionHash": "%s",
				"blockNumber": "0x1",
				"blockHash": "0x0000000000000000000000000000000000000000000000000000000000000001",
				"transactionIndex": "0x0",
				"gasUsed": "0x5208",
				"cumulativeGasUsed": "0x5208",
				"contractAddress": null,
				"logs": [],
				"logsBloom": "0x`+strings.Repeat("0", 512)+`",
				"type": "0x2",
				"effectiveGasPrice": "0x3b9aca00"
			}`, fakeTxHash.Hex())

			return json.RawMessage(receipt), nil
		},
		"eth_blockNumber": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"0x1"`), nil
		},
		"eth_getBlockByNumber": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{
				"number": "0x1",
				"hash": "0x0000000000000000000000000000000000000000000000000000000000000001",
				"baseFeePerGas": "0x3b9aca00",
				"timestamp": "0x60000000",
				"gasLimit": "0x1c9c380",
				"gasUsed": "0x5208",
				"miner": "0x0000000000000000000000000000000000000000",
				"extraData": "0x",
				"parentHash": "0x0000000000000000000000000000000000000000000000000000000000000000",
				"sha3Uncles": "0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347",
				"logsBloom": "0x` + strings.Repeat("0", 512) + `",
				"transactionsRoot": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421",
				"stateRoot": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421",
				"receiptsRoot": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421",
				"mixHash": "0x0000000000000000000000000000000000000000000000000000000000000000",
				"nonce": "0x0000000000000000",
				"difficulty": "0x0",
				"totalDifficulty": "0x0",
				"size": "0x200",
				"uncles": [],
				"transactions": []
			}`), nil
		},
	}
}

func TestSetAgentURI(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	fakeTxHash := common.HexToHash("0x1111")
	handlers := txMockHandlers(fakeTxHash)

	srv := mockRPC(t, handlers)
	defer srv.Close()

	ctx := context.Background()

	client, err := newClient(ctx, srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	opts, err := bind.NewKeyedTransactorWithChainID(key, client.chainID)
	if err != nil {
		t.Fatalf("transactor: %v", err)
	}
	opts.Context = ctx
	_, err = client.SetAgentURIWithOpts(ctx, opts, big.NewInt(42), "https://example.com/updated")
	if err != nil {
		t.Fatalf("SetAgentURI: %v", err)
	}
}

func TestSetMetadata(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	fakeTxHash := common.HexToHash("0x2222")
	handlers := txMockHandlers(fakeTxHash)

	srv := mockRPC(t, handlers)
	defer srv.Close()

	ctx := context.Background()

	client, err := newClient(ctx, srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	err = client.SetMetadata(ctx, key, big.NewInt(42), "x402", []byte(`{"payment":"info"}`))
	if err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}
}

func TestSetMetadata_TransactRevert(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	handlers := txMockHandlers(common.HexToHash("0x2222"))
	handlers["eth_estimateGas"] = func(_ []json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("execution reverted")
	}

	srv := mockRPC(t, handlers)
	defer srv.Close()

	ctx := context.Background()

	client, err := newClient(ctx, srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	err = client.SetMetadata(ctx, key, big.NewInt(42), "x402", []byte(`{"payment":"info"}`))
	if err == nil {
		t.Fatal("expected setMetadata revert error, got nil")
	}
	if !strings.Contains(err.Error(), "erc8004: setMetadata tx: execution reverted") {
		t.Fatalf("error = %q, want setMetadata tx execution reverted", err)
	}
}

// When the node carries a revert payload on the JSON-RPC error envelope —
// which Geth/Reth do for both eth_call and eth_estimateGas — the wrapped
// CLI error must surface the decoded Solidity Error(string) message so an
// operator can see *why* the contract is rejecting (the rc10 setMetadata
// revert was opaque before this).
func TestSetMetadata_RevertSurfacesErrorString(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	stringT, _ := abi.NewType("string", "", nil)
	args := abi.Arguments{{Type: stringT}}
	encoded, err := args.Pack("MetadataKeyAlreadySet")
	if err != nil {
		t.Fatal(err)
	}
	revertHex := "0x08c379a0" + hex.EncodeToString(encoded)

	handlers := txMockHandlers(common.HexToHash("0x2222"))
	handlers["eth_estimateGas"] = func(_ []json.RawMessage) (json.RawMessage, error) {
		return nil, &rpcMockDataError{msg: "execution reverted", dataHex: revertHex}
	}

	srv := mockRPC(t, handlers)
	defer srv.Close()

	ctx := context.Background()
	client, err := newClient(ctx, srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	err = client.SetMetadata(ctx, key, big.NewInt(42), "x402", []byte(`{"payment":"info"}`))
	if err == nil {
		t.Fatal("expected setMetadata revert error, got nil")
	}
	if !strings.Contains(err.Error(), "MetadataKeyAlreadySet") {
		t.Fatalf("error = %q, want decoded revert reason in message", err)
	}
	if !strings.Contains(err.Error(), "erc8004: setMetadata tx") {
		t.Fatalf("error = %q, lost the call-site prefix", err)
	}
}

// Custom errors don't have an ABI we know in this package, so we surface the
// 4-byte selector — enough for an operator to grep the contract source.
func TestSetMetadata_RevertSurfacesCustomErrorSelector(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	// 4-byte selector for a hypothetical Forbidden() — pick anything that
	// isn't 0x08c379a0 / 0x4e487b71 so we exercise the default branch.
	revertHex := "0xdeadbeef"

	handlers := txMockHandlers(common.HexToHash("0x2222"))
	handlers["eth_estimateGas"] = func(_ []json.RawMessage) (json.RawMessage, error) {
		return nil, &rpcMockDataError{msg: "execution reverted", dataHex: revertHex}
	}

	srv := mockRPC(t, handlers)
	defer srv.Close()

	ctx := context.Background()
	client, err := newClient(ctx, srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	err = client.SetMetadata(ctx, key, big.NewInt(42), "x402", []byte(`{"payment":"info"}`))
	if err == nil {
		t.Fatal("expected setMetadata revert error, got nil")
	}
	if !strings.Contains(err.Error(), "custom error 0xdeadbeef") {
		t.Fatalf("error = %q, want custom error selector in message", err)
	}
}

func TestNewClient_DialError(t *testing.T) {
	ctx := context.Background()
	// Use an unreachable address to trigger a dial/chain-id error.
	_, err := newClient(ctx, "http://127.0.0.1:1", IdentityRegistryBaseSepolia)
	if err == nil {
		t.Fatal("expected error from unreachable RPC URL, got nil")
	}
}

func TestRegister_NoRegisteredEvent(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	// Use txMockHandlers which returns a receipt with empty logs.
	fakeTxHash := common.HexToHash("0x3333")
	handlers := txMockHandlers(fakeTxHash)

	srv := mockRPC(t, handlers)
	defer srv.Close()

	ctx := context.Background()

	client, err := newClient(ctx, srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	opts, err := bind.NewKeyedTransactorWithChainID(key, client.chainID)
	if err != nil {
		t.Fatalf("transactor: %v", err)
	}
	opts.Context = ctx
	_, _, err = client.RegisterWithOptsDetailed(ctx, opts, "https://example.com/agent")
	if err == nil {
		t.Fatal("expected error when Registered event not found, got nil")
	}

	if !strings.Contains(err.Error(), "Registered event not found") {
		t.Errorf("error = %q, want it to contain 'Registered event not found'", err.Error())
	}
}

func TestRegister_TxError(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	fakeTxHash := common.HexToHash("0x4444")
	handlers := txMockHandlers(fakeTxHash)
	// Override sendRawTransaction to return an error.
	handlers["eth_sendRawTransaction"] = func(_ []json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("insufficient funds")
	}

	srv := mockRPC(t, handlers)
	defer srv.Close()

	ctx := context.Background()

	client, err := newClient(ctx, srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	opts, err := bind.NewKeyedTransactorWithChainID(key, client.chainID)
	if err != nil {
		t.Fatalf("transactor: %v", err)
	}
	opts.Context = ctx
	_, _, err = client.RegisterWithOptsDetailed(ctx, opts, "https://example.com/agent")
	if err == nil {
		t.Fatal("expected error from sendRawTransaction failure, got nil")
	}
}

func TestGetMetadata_EmptyResult(t *testing.T) {
	parsedABI, err := parseABI()
	if err != nil {
		t.Fatal(err)
	}

	// ABI-encode empty bytes ([]byte{}).
	encoded, err := parsedABI.Methods["getMetadata"].Outputs.Pack([]byte{})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}

	handlers := map[string]func([]json.RawMessage) (json.RawMessage, error){
		"eth_chainId": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"0x14a34"`), nil
		},
		"eth_call": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(fmt.Sprintf(`"0x%s"`, hex.EncodeToString(encoded))), nil
		},
	}

	srv := mockRPC(t, handlers)
	defer srv.Close()

	ctx := context.Background()

	client, err := newClient(ctx, srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	result, err := client.GetMetadata(ctx, big.NewInt(1), "x402")
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("expected empty bytes, got %q", result)
	}
}

// parseABI is a helper that parses the embedded ABI for use in tests.
func parseABI() (abi.ABI, error) {
	return abi.JSON(strings.NewReader(identityRegistryABI))
}

// TestWaitForAgent_RetriesUntilOwnerVisible verifies that WaitForAgent keeps
// polling ownerOf until the reader returns a successful result, simulating
// the read-side staleness window between Register's WaitMined (write upstream)
// and a subsequent setMetadata estimateGas (read upstream).
func TestWaitForAgent_RetriesUntilOwnerVisible(t *testing.T) {
	var attempts int
	owner := common.HexToAddress("0x2FbFe6cF08Ac224f97915ecF07eE29Be0b213f51")

	parsedABI, err := parseABI()
	if err != nil {
		t.Fatalf("parseABI: %v", err)
	}

	handlers := map[string]func([]json.RawMessage) (json.RawMessage, error){
		"eth_chainId": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"0x14a34"`), nil
		},
		"eth_call": func(_ []json.RawMessage) (json.RawMessage, error) {
			attempts++
			if attempts < 3 {
				// Simulate ERC721NonexistentToken on first two calls (read
				// upstream not yet caught up).
				return nil, fmt.Errorf("execution reverted")
			}
			// Third call: encode owner as ownerOf return.
			ownerBytes, encErr := parsedABI.Methods["getAgentWallet"].Outputs.Pack(owner)
			if encErr != nil {
				return nil, encErr
			}
			return json.RawMessage(fmt.Sprintf("%q", "0x"+common.Bytes2Hex(ownerBytes))), nil
		},
	}

	srv := mockRPC(t, handlers)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := newClient(ctx, srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	got, err := client.WaitForAgent(ctx, big.NewInt(5196), 20*time.Second)
	if err != nil {
		t.Fatalf("WaitForAgent: %v after %d attempts", err, attempts)
	}
	if got != owner {
		t.Errorf("expected owner %s, got %s", owner, got)
	}
	if attempts < 3 {
		t.Errorf("expected at least 3 attempts (2 reverts + 1 success), got %d", attempts)
	}
}

// TestWaitForAgent_TimeoutReturnsError verifies that persistent reverts
// surface as a timeout error.
func TestWaitForAgent_TimeoutReturnsError(t *testing.T) {
	handlers := map[string]func([]json.RawMessage) (json.RawMessage, error){
		"eth_chainId": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"0x14a34"`), nil
		},
		"eth_call": func(_ []json.RawMessage) (json.RawMessage, error) {
			return nil, fmt.Errorf("execution reverted")
		},
	}

	srv := mockRPC(t, handlers)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := newClient(ctx, srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	_, err = client.WaitForAgent(ctx, big.NewInt(5196), 3*time.Second)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected 'timed out' in error, got: %v", err)
	}
}

// decodeSentNonce extracts the nonce from a raw eth_sendRawTransaction
// param, mirroring the RLP/EIP-2718 decode RemoteSigner.RemoteTransactOpts
// does on the way back from the remote-signer.
func decodeSentNonce(t *testing.T, params []json.RawMessage) uint64 {
	t.Helper()

	var rawHex string
	if err := json.Unmarshal(params[0], &rawHex); err != nil {
		t.Fatalf("decode sendRawTransaction param: %v", err)
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(rawHex, "0x"))
	if err != nil {
		t.Fatalf("hex decode raw tx: %v", err)
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		t.Fatalf("unmarshal raw tx: %v", err)
	}
	return tx.Nonce()
}

// TestPinnedNonce_SequentialWritesDoNotCollide guards the fix for
// registerDirectViaSigner's "nonce too low" failure: pinning the nonce
// locally (PendingNonceAt once, bump after each successful send) must
// produce increasing on-chain nonces across sequential writes even when
// eth_getTransactionCount keeps reporting the same value — exactly what a
// lagging eRPC upstream does immediately after a just-broadcast tx.
func TestPinnedNonce_SequentialWritesDoNotCollide(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	handlers := txMockHandlers(common.HexToHash("0x4444"))
	handlers["eth_getTransactionCount"] = func(_ []json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`"0x5"`), nil // stale: never advances on its own
	}
	var sentNonces []uint64
	handlers["eth_sendRawTransaction"] = func(params []json.RawMessage) (json.RawMessage, error) {
		sentNonces = append(sentNonces, decodeSentNonce(t, params))
		return json.RawMessage(`"0x4444"`), nil
	}

	srv := mockRPC(t, handlers)
	defer srv.Close()

	ctx := context.Background()
	client, err := newClient(ctx, srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	opts, err := bind.NewKeyedTransactorWithChainID(key, client.chainID)
	if err != nil {
		t.Fatalf("transactor: %v", err)
	}
	opts.Context = ctx

	// Pin, exactly as registerDirectViaSigner does, instead of leaving
	// opts.Nonce nil (which re-queries eth_getTransactionCount every call).
	nonce, err := client.PendingNonceAt(ctx, crypto.PubkeyToAddress(key.PublicKey))
	if err != nil {
		t.Fatalf("PendingNonceAt: %v", err)
	}
	opts.Nonce = new(big.Int).SetUint64(nonce)

	if _, err := client.SetAgentURIWithOpts(ctx, opts, big.NewInt(1), "https://example.com/a"); err != nil {
		t.Fatalf("SetAgentURIWithOpts: %v", err)
	}
	opts.Nonce = new(big.Int).Add(opts.Nonce, big.NewInt(1))

	if err := client.SetMetadataWithOpts(ctx, opts, big.NewInt(1), "x402", []byte(`{"x402":true}`)); err != nil {
		t.Fatalf("SetMetadataWithOpts: %v", err)
	}

	if want := []uint64{5, 6}; len(sentNonces) != 2 || sentNonces[0] != want[0] || sentNonces[1] != want[1] {
		t.Errorf("nonces = %v, want %v — sequential writes must not collide even when the RPC reports a stale pending nonce", sentNonces, want)
	}
}

// TestUnpinnedNonce_StaleRPCCollides documents the bug the pinning fix
// closes: without it, two sequential Transact calls against a stale
// eth_getTransactionCount submit the same colliding nonce.
func TestUnpinnedNonce_StaleRPCCollides(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	handlers := txMockHandlers(common.HexToHash("0x5555"))
	handlers["eth_getTransactionCount"] = func(_ []json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`"0x5"`), nil
	}
	var sentNonces []uint64
	handlers["eth_sendRawTransaction"] = func(params []json.RawMessage) (json.RawMessage, error) {
		sentNonces = append(sentNonces, decodeSentNonce(t, params))
		return json.RawMessage(`"0x5555"`), nil
	}

	srv := mockRPC(t, handlers)
	defer srv.Close()

	ctx := context.Background()
	client, err := newClient(ctx, srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	opts, err := bind.NewKeyedTransactorWithChainID(key, client.chainID)
	if err != nil {
		t.Fatalf("transactor: %v", err)
	}
	opts.Context = ctx
	// opts.Nonce left nil on purpose: bind re-queries eth_getTransactionCount
	// on every call, which here always returns the same stale value.

	if _, err := client.SetAgentURIWithOpts(ctx, opts, big.NewInt(1), "https://example.com/a"); err != nil {
		t.Fatalf("SetAgentURIWithOpts: %v", err)
	}
	if err := client.SetMetadataWithOpts(ctx, opts, big.NewInt(1), "x402", []byte(`{"x402":true}`)); err != nil {
		t.Fatalf("SetMetadataWithOpts: %v", err)
	}

	if len(sentNonces) != 2 || sentNonces[0] != sentNonces[1] {
		t.Fatalf("nonces = %v, want both == the stale RPC nonce (5) to demonstrate the collision this fix avoids", sentNonces)
	}
}
