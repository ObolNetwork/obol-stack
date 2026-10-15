package erc8004

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// revertedReceiptHandler returns a mined receipt with status 0x0.
func revertedReceiptHandler(txHash common.Hash) func([]json.RawMessage) (json.RawMessage, error) {
	return func(_ []json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(fmt.Sprintf(`{
			"status": "0x0",
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
		}`, txHash.Hex())), nil
	}
}

func newRevertingTxClient(t *testing.T, txHash common.Hash) (*Client, *bind.TransactOpts) {
	t.Helper()

	handlers := txMockHandlers(txHash)
	handlers["eth_getTransactionReceipt"] = revertedReceiptHandler(txHash)

	srv := mockRPC(t, handlers)
	t.Cleanup(srv.Close)

	ctx := context.Background()
	client, err := newClient(ctx, srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(client.Close)

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	opts, err := bind.NewKeyedTransactorWithChainID(key, client.chainID)
	if err != nil {
		t.Fatalf("transactor: %v", err)
	}
	opts.Context = ctx
	return client, opts
}

var txHashInErr = regexp.MustCompile(`tx 0x[0-9a-f]{64} reverted`)

// assertRevertedErr checks the error reports a revert and carries a tx hash.
// For locally signed transactions the hash is the signed tx's own hash, so
// only its shape is checked; wantHash pins it when the caller knows it.
func assertRevertedErr(t *testing.T, err error, wantHash *common.Hash) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error for reverted receipt, got nil")
	}
	if !txHashInErr.MatchString(err.Error()) {
		t.Errorf("error %q does not report a reverted tx hash", err)
	}
	if wantHash != nil && !strings.Contains(err.Error(), wantHash.Hex()) {
		t.Errorf("error %q does not include tx hash %s", err, wantHash.Hex())
	}
}

func TestSetMetadata_RevertedReceiptIsError(t *testing.T) {
	txHash := common.HexToHash("0x3333")
	client, opts := newRevertingTxClient(t, txHash)

	err := client.SetMetadataWithOpts(context.Background(), opts, big.NewInt(42), "x402", []byte(`{"x402":true}`))
	assertRevertedErr(t, err, nil)
}

func TestSetAgentURI_RevertedReceiptIsError(t *testing.T) {
	txHash := common.HexToHash("0x4444")
	client, opts := newRevertingTxClient(t, txHash)

	got, err := client.SetAgentURIWithOpts(context.Background(), opts, big.NewInt(42), "https://example.com/updated")
	assertRevertedErr(t, err, nil)
	if got != "" {
		t.Errorf("tx hash = %q on reverted receipt, want empty", got)
	}
}

func TestRegister_RevertedReceiptIsError(t *testing.T) {
	txHash := common.HexToHash("0x5555")
	client, opts := newRevertingTxClient(t, txHash)

	_, _, err := client.RegisterWithOptsDetailed(context.Background(), opts, "https://example.com/.well-known/agent-registration.json")
	assertRevertedErr(t, err, nil)
}

func TestFindRegistrationByTxHash_RevertedReceiptIsError(t *testing.T) {
	txHash := common.HexToHash("0x6666")
	client, _ := newRevertingTxClient(t, txHash)

	_, _, found, err := client.FindRegistrationByTxHash(context.Background(), txHash.Hex())
	if found {
		t.Fatal("found = true for reverted registration tx")
	}
	assertRevertedErr(t, err, &txHash)
}

// recoveryLog describes one Registered event served by the recovery mock.
type recoveryLog struct {
	agentID int64
	uri     string
	txHash  common.Hash
}

// newRecoveryClient serves eth_getLogs with the given Registered events (all
// minted to minter, oldest first) and answers ownerOf(agentId) from owners.
func newRecoveryClient(t *testing.T, minter common.Address, logs []recoveryLog, owners map[int64]common.Address) *Client {
	t.Helper()

	parsed, err := parseABI()
	if err != nil {
		t.Fatal(err)
	}
	registered := parsed.Events["Registered"]
	ownerOf := parsed.Methods["ownerOf"]

	handlers := map[string]func([]json.RawMessage) (json.RawMessage, error){
		"eth_chainId": func(_ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"0x14a34"`), nil
		},
		"eth_getLogs": func(_ []json.RawMessage) (json.RawMessage, error) {
			out := make([]map[string]any, 0, len(logs))
			for i, l := range logs {
				data, err := registered.Inputs.NonIndexed().Pack(l.uri)
				if err != nil {
					return nil, err
				}
				out = append(out, map[string]any{
					"address": common.HexToAddress(IdentityRegistryBaseSepolia).Hex(),
					"topics": []string{
						registered.ID.Hex(),
						common.BigToHash(big.NewInt(l.agentID)).Hex(),
						common.BytesToHash(minter.Bytes()).Hex(),
					},
					"data":             "0x" + hex.EncodeToString(data),
					"blockNumber":      fmt.Sprintf("0x%x", i+1),
					"transactionHash":  l.txHash.Hex(),
					"transactionIndex": "0x0",
					"blockHash":        common.BigToHash(big.NewInt(int64(i + 1))).Hex(),
					"logIndex":         "0x0",
					"removed":          false,
				})
			}
			b, err := json.Marshal(out)
			return b, err
		},
		"eth_call": func(params []json.RawMessage) (json.RawMessage, error) {
			var call struct {
				Input string `json:"input"`
				Data  string `json:"data"`
			}
			if err := json.Unmarshal(params[0], &call); err != nil {
				return nil, err
			}
			input := call.Input
			if input == "" {
				input = call.Data
			}
			raw, err := hex.DecodeString(strings.TrimPrefix(input, "0x"))
			if err != nil {
				return nil, err
			}
			if len(raw) < 4 || !bytes.Equal(raw[:4], ownerOf.ID) {
				return nil, errors.New("unexpected eth_call")
			}
			id := new(big.Int).SetBytes(raw[4:]).Int64()
			owner, ok := owners[id]
			if !ok {
				return nil, &rpcMockDataError{msg: "execution reverted"}
			}
			return json.RawMessage(fmt.Sprintf(`"%s"`, common.BytesToHash(owner.Bytes()).Hex())), nil
		},
	}

	srv := mockRPC(t, handlers)
	t.Cleanup(srv.Close)

	client, err := newClient(context.Background(), srv.URL, IdentityRegistryBaseSepolia)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(client.Close)
	return client
}

const recoveryURI = "https://example.com/.well-known/agent-registration.json"

func TestFindRegistrationByOwnerAndURI_AdoptsWhenStillOwned(t *testing.T) {
	owner := common.HexToAddress("0x00000000000000000000000000000000000000a1")
	tx := common.HexToHash("0xa1")
	client := newRecoveryClient(t, owner,
		[]recoveryLog{{agentID: 7, uri: recoveryURI, txHash: tx}},
		map[int64]common.Address{7: owner},
	)

	id, txHash, found, err := client.FindRegistrationByOwnerAndURI(context.Background(), owner, recoveryURI, 0)
	if err != nil {
		t.Fatalf("FindRegistrationByOwnerAndURI: %v", err)
	}
	if !found || id.Int64() != 7 || txHash != tx.Hex() {
		t.Fatalf("got id=%v tx=%s found=%v, want 7 %s true", id, txHash, found, tx.Hex())
	}
}

func TestFindRegistrationByOwnerAndURI_RejectsTransferredToken(t *testing.T) {
	owner := common.HexToAddress("0x00000000000000000000000000000000000000a1")
	other := common.HexToAddress("0x00000000000000000000000000000000000000b2")
	client := newRecoveryClient(t, owner,
		[]recoveryLog{{agentID: 7, uri: recoveryURI, txHash: common.HexToHash("0xa1")}},
		map[int64]common.Address{7: other},
	)

	id, _, found, err := client.FindRegistrationByOwnerAndURI(context.Background(), owner, recoveryURI, 0)
	if found || id != nil {
		t.Fatalf("adopted transferred token: id=%v found=%v", id, found)
	}
	var mismatch *OwnerMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err = %v, want *OwnerMismatchError", err)
	}
	if len(mismatch.Candidates) != 1 || mismatch.Candidates[0].CurrentOwner != other {
		t.Fatalf("candidates = %+v, want one owned by %s", mismatch.Candidates, other.Hex())
	}
	if !strings.Contains(err.Error(), other.Hex()) {
		t.Errorf("error %q does not name current owner", err)
	}
}

func TestFindRegistrationByOwnerAndURI_FallsBackToOlderOwnedToken(t *testing.T) {
	owner := common.HexToAddress("0x00000000000000000000000000000000000000a1")
	other := common.HexToAddress("0x00000000000000000000000000000000000000b2")
	olderTx := common.HexToHash("0xa1")
	client := newRecoveryClient(t, owner,
		[]recoveryLog{
			{agentID: 7, uri: recoveryURI, txHash: olderTx},
			{agentID: 9, uri: recoveryURI, txHash: common.HexToHash("0xa2")},
		},
		map[int64]common.Address{7: owner, 9: other},
	)

	id, txHash, found, err := client.FindRegistrationByOwnerAndURI(context.Background(), owner, recoveryURI, 0)
	if err != nil {
		t.Fatalf("FindRegistrationByOwnerAndURI: %v", err)
	}
	if !found || id.Int64() != 7 || txHash != olderTx.Hex() {
		t.Fatalf("got id=%v tx=%s found=%v, want 7 %s true", id, txHash, found, olderTx.Hex())
	}
}

func TestFindRegistrationByOwnerAndURI_OwnerOfErrorIsNotAdopted(t *testing.T) {
	owner := common.HexToAddress("0x00000000000000000000000000000000000000a1")
	client := newRecoveryClient(t, owner,
		[]recoveryLog{{agentID: 7, uri: recoveryURI, txHash: common.HexToHash("0xa1")}},
		map[int64]common.Address{},
	)

	_, _, found, err := client.FindRegistrationByOwnerAndURI(context.Background(), owner, recoveryURI, 0)
	if found {
		t.Fatal("found = true when ownerOf failed")
	}
	if err == nil {
		t.Fatal("expected ownerOf error, got nil")
	}
}
