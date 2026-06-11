package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	// ForkObolProjectDir is the local Foundry project used to deploy a fork-only
	// OBOL-compatible ERC20Permit token for x402 testing.
	ForkObolProjectDir = "contracts/fork-obol"
)

func jsonDecode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

// AnvilFork represents a running Anvil instance forking a live chain.
type AnvilFork struct {
	Port     int
	RPCURL   string
	Accounts []AnvilAccount

	cmd    *exec.Cmd
	cancel context.CancelFunc
}

// AnvilAccount is one of the 10 deterministic Anvil accounts.
type AnvilAccount struct {
	Address    string
	PrivateKey string
}

type AnvilTransactionReceipt struct {
	TransactionHash   string `json:"transactionHash"`
	BlockNumber       string `json:"blockNumber"`
	From              string `json:"from"`
	To                string `json:"to"`
	Status            string `json:"status"`
	GasUsed           string `json:"gasUsed"`
	EffectiveGasPrice string `json:"effectiveGasPrice"`
}

// defaultAnvilAccounts returns the 10 deterministic accounts that Anvil
// always creates with 10000 ETH each.
func defaultAnvilAccounts() []AnvilAccount {
	return []AnvilAccount{
		{Address: "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266", PrivateKey: "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"},
		{Address: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8", PrivateKey: "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"},
		{Address: "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC", PrivateKey: "0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"},
		{Address: "0x90F79bf6EB2c4f870365E785982E1f101E93b906", PrivateKey: "0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6"},
		{Address: "0x15d34AAf54267DB7D7c367839AAf71A00a2C6A65", PrivateKey: "0x47e179ec197488593b187f80a00eb0da91f1b9d0b13f8733639f19c30a34926a"},
		{Address: "0x9965507D1a55bcC2695C58ba16FB37d819B0A4dc", PrivateKey: "0x8b3a350cf5c34c9194ca85829a2df0ec3153be0318b5e2d3348e872092edffba"},
		{Address: "0x976EA74026E726554dB657fA54763abd0C3a0aa9", PrivateKey: "0x92db14e403b83dfe3df233f83dfa3a0d7096f21ca9b0d6d6b8d88b2b4ec1564e"},
		{Address: "0x14dC79964da2C08dfd0cC27B2a01620c928fF1c0", PrivateKey: "0x4bbbf85ce3377467afe5d46f804f221813b2bb87f24d81f60f1fcdbf7cbf4356"},
		{Address: "0x23618e81E3f5cdF7f54C3d65f7FBc0aBf5B21E8f", PrivateKey: "0xdbda1821b80551c9d65939329250298aa3472ba22feea921c0cf5d620ea67b97"},
		{Address: "0xa0Ee7A142d267C1f36714E4a8F75612F20a79720", PrivateKey: "0x2a871d0798f97d79848a013d4936a73bf4cc922c825d33c1cf7073dff6d409c6"},
	}
}

// StartAnvilFork starts anvil forking Base Sepolia on a free port.
// Skips the test if anvil is not installed.
// Registers t.Cleanup to kill the process.
func StartAnvilFork(t *testing.T) *AnvilFork {
	t.Helper()
	return StartAnvilForkWithURL(t, "")
}

// StartAnvilForkWithURL forks from a custom RPC URL.
// Uses BASE_SEPOLIA_RPC_URL env var or falls back to https://sepolia.base.org.
func StartAnvilForkWithURL(t *testing.T, forkURL string) *AnvilFork {
	t.Helper()

	if _, err := exec.LookPath("anvil"); err != nil {
		t.Skip("anvil not installed — install Foundry: https://getfoundry.sh")
	}

	if forkURL == "" {
		forkURL = "https://sepolia.base.org"
	}

	// Find a free port.  Bind on 0.0.0.0 so the k3d cluster can reach
	// Anvil via the docker0 bridge IP on Linux.
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}

	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, "anvil",
		"--fork-url", forkURL,
		"--host", "0.0.0.0",
		"--port", strconv.Itoa(port),
		"--silent",
	)

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start anvil: %v", err)
	}

	fork := &AnvilFork{
		Port:     port,
		RPCURL:   fmt.Sprintf("http://127.0.0.1:%d", port),
		Accounts: defaultAnvilAccounts(),
		cmd:      cmd,
		cancel:   cancel,
	}

	t.Cleanup(func() {
		cancel()

		_ = cmd.Wait()
	})

	// Wait for RPC readiness with timeout.
	if err := fork.waitReady(10 * time.Second); err != nil {
		t.Fatalf("anvil failed to become ready: %v\nstderr: %s", err, stderr.String())
	}

	return fork
}

// waitReady polls the Anvil RPC endpoint until eth_blockNumber succeeds.
func (f *AnvilFork) waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	body := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`

	for time.Now().Before(deadline) {
		resp, err := http.Post(f.RPCURL, "application/json", strings.NewReader(body))
		if err == nil {
			resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		time.Sleep(200 * time.Millisecond)
	}

	return fmt.Errorf("anvil not ready after %v on port %d", timeout, f.Port)
}

// MintUSDC sets the USDC balance for the given address on the Anvil fork
// using anvil_setStorageAt. This writes directly to the ERC-20 balanceOf
// mapping in the USDC proxy contract.
//
// USDC on Base Sepolia: 0x036CbD53842c5426634e7929541eC2318f3dCF7e
// Balance mapping slot: 9 (FiatTokenV2 uses slot 9 for balances)
func (f *AnvilFork) MintUSDC(t *testing.T, to string, amount *big.Int) {
	t.Helper()

	// Compute storage slot: keccak256(abi.encode(address, uint256(9)))
	// This is the standard Solidity mapping slot for mapping(address => uint256) at slot 9.
	addr := common.HexToAddress(to)
	slot := big.NewInt(9)

	// abi.encode(address, uint256(9)) — both padded to 32 bytes.
	key := common.LeftPadBytes(addr.Bytes(), 32)
	slotBytes := common.LeftPadBytes(slot.Bytes(), 32)
	packed := append(append([]byte{}, key...), slotBytes...)
	storageSlot := crypto.Keccak256Hash(packed)

	// Pad amount to 32 bytes.
	valueHex := fmt.Sprintf("0x%064x", amount)

	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"anvil_setStorageAt","params":["%s","%s","%s"],"id":1}`,
		USDCBaseSepolia, storageSlot.Hex(), valueHex,
	)

	resp, err := http.Post(f.RPCURL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("anvil_setStorageAt failed: %v", err)
	}

	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anvil_setStorageAt returned %d", resp.StatusCode)
	}

	t.Logf("minted %s USDC to %s (slot %s)", amount, to, storageSlot.Hex())
}

// FundETH sets the ETH balance for an address on the Anvil fork using anvil_setBalance.
func (f *AnvilFork) FundETH(t *testing.T, addr string, amount *big.Int) {
	t.Helper()

	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"anvil_setBalance","params":["%s","0x%x"],"id":1}`,
		addr, amount,
	)

	resp, err := http.Post(f.RPCURL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("anvil_setBalance failed: %v", err)
	}

	resp.Body.Close()
	t.Logf("funded %s with %s wei", addr, amount)
}

// ApprovePermit2ViaImpersonation performs the one-time approve(Permit2, max)
// from owner on token via anvil_impersonateAccount — the fork-test stand-in
// for the on-chain approval a real wallet owner does once per token. Without
// it buy.py's Permit2 allowance preflight (correctly) refuses to pre-sign.
func (f *AnvilFork) ApprovePermit2ViaImpersonation(t *testing.T, token, owner string) {
	t.Helper()

	const permit2 = "0x000000000022D473030F116dDEE9F6B43aC78BA3"
	// approve(address,uint256) selector + permit2 + max uint256.
	data := "0x095ea7b3" +
		"000000000000000000000000" + strings.ToLower(strings.TrimPrefix(permit2, "0x")) +
		strings.Repeat("f", 64)

	for _, call := range []string{
		fmt.Sprintf(`{"jsonrpc":"2.0","method":"anvil_impersonateAccount","params":["%s"],"id":1}`, owner),
		fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_sendTransaction","params":[{"from":"%s","to":"%s","data":"%s"}],"id":1}`, owner, token, data),
		fmt.Sprintf(`{"jsonrpc":"2.0","method":"anvil_stopImpersonatingAccount","params":["%s"],"id":1}`, owner),
	} {
		resp, err := http.Post(f.RPCURL, "application/json", strings.NewReader(call))
		if err != nil {
			t.Fatalf("approve Permit2 via impersonation: %v", err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(raw), `"error"`) {
			t.Fatalf("approve Permit2 via impersonation: %s", raw)
		}
	}
	t.Logf("approved Permit2 for %s on token %s (impersonated)", owner, token)
}

// ClearCode removes contract code from an address on Anvil.
// Required for deterministic Anvil accounts that have proxy contracts on Base Sepolia —
// USDC's SignatureChecker sees code → tries EIP-1271 instead of ecrecover.
func (f *AnvilFork) ClearCode(t *testing.T, addr string) {
	t.Helper()

	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"anvil_setCode","params":["%s","0x"],"id":1}`,
		addr,
	)

	resp, err := http.Post(f.RPCURL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("anvil_setCode failed: %v", err)
	}

	resp.Body.Close()
}

// GetUSDCBalance returns the USDC balance for an address via eth_call.
func (f *AnvilFork) GetUSDCBalance(t *testing.T, addr string) *big.Int {
	t.Helper()
	// balanceOf(address) selector = 0x70a08231
	paddedAddr := fmt.Sprintf("%064s", common.HexToAddress(addr).Hex()[2:])
	calldata := "0x70a08231" + paddedAddr

	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"eth_call","params":[{"to":"%s","data":"%s"},"latest"],"id":1}`,
		USDCBaseSepolia, calldata,
	)

	resp, err := http.Post(f.RPCURL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("eth_call balanceOf failed: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Result string `json:"result"`
	}
	if err := jsonDecode(resp.Body, &result); err != nil {
		t.Fatalf("parse balanceOf response: %v", err)
	}

	balance := new(big.Int)
	balance.SetString(strings.TrimPrefix(result.Result, "0x"), 16)

	return balance
}

// GetERC20Balance returns the ERC-20 balance for an address via eth_call.
func (f *AnvilFork) GetERC20Balance(t *testing.T, tokenAddr, addr string) *big.Int {
	t.Helper()

	paddedAddr := fmt.Sprintf("%064s", common.HexToAddress(addr).Hex()[2:])
	calldata := "0x70a08231" + paddedAddr

	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"eth_call","params":[{"to":"%s","data":"%s"},"latest"],"id":1}`,
		tokenAddr, calldata,
	)

	resp, err := http.Post(f.RPCURL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("eth_call balanceOf failed: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Result string `json:"result"`
	}
	if err := jsonDecode(resp.Body, &result); err != nil {
		t.Fatalf("parse balanceOf response: %v", err)
	}

	balance := new(big.Int)
	balance.SetString(strings.TrimPrefix(result.Result, "0x"), 16)

	return balance
}

func (f *AnvilFork) BlockNumber(t *testing.T) uint64 {
	t.Helper()

	body := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`
	resp, err := http.Post(f.RPCURL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("eth_blockNumber failed: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Result string `json:"result"`
	}
	if err := jsonDecode(resp.Body, &result); err != nil {
		t.Fatalf("parse eth_blockNumber response: %v", err)
	}

	block, err := strconv.ParseUint(strings.TrimPrefix(result.Result, "0x"), 16, 64)
	if err != nil {
		t.Fatalf("parse eth_blockNumber %q: %v", result.Result, err)
	}

	return block
}

func (f *AnvilFork) FindERC20TransferReceipt(t *testing.T, tokenAddr, from, to string, fromBlock uint64) *AnvilTransactionReceipt {
	t.Helper()

	transferTopic := crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)")).Hex()
	fromTopic := common.LeftPadBytes(common.HexToAddress(from).Bytes(), 32)
	toTopic := common.LeftPadBytes(common.HexToAddress(to).Bytes(), 32)

	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "eth_getLogs",
		"params": []any{
			map[string]any{
				"address":   tokenAddr,
				"fromBlock": fmt.Sprintf("0x%x", fromBlock),
				"toBlock":   "latest",
				"topics": []string{
					transferTopic,
					common.BytesToHash(fromTopic).Hex(),
					common.BytesToHash(toTopic).Hex(),
				},
			},
		},
		"id": 1,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal eth_getLogs payload: %v", err)
	}

	resp, err := http.Post(f.RPCURL, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("eth_getLogs failed: %v", err)
	}
	defer resp.Body.Close()

	var logsResp struct {
		Result []struct {
			TransactionHash string `json:"transactionHash"`
		} `json:"result"`
	}
	if err := jsonDecode(resp.Body, &logsResp); err != nil {
		t.Fatalf("parse eth_getLogs response: %v", err)
	}
	if len(logsResp.Result) == 0 {
		t.Fatalf("no ERC20 Transfer logs found for token=%s from=%s to=%s fromBlock=%d", tokenAddr, from, to, fromBlock)
	}

	txHash := logsResp.Result[len(logsResp.Result)-1].TransactionHash
	receiptPayload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "eth_getTransactionReceipt",
		"params":  []string{txHash},
		"id":      1,
	}
	data, err = json.Marshal(receiptPayload)
	if err != nil {
		t.Fatalf("marshal eth_getTransactionReceipt payload: %v", err)
	}

	receiptResp, err := http.Post(f.RPCURL, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("eth_getTransactionReceipt failed: %v", err)
	}
	defer receiptResp.Body.Close()

	var receipt struct {
		Result *AnvilTransactionReceipt `json:"result"`
	}
	if err := jsonDecode(receiptResp.Body, &receipt); err != nil {
		t.Fatalf("parse eth_getTransactionReceipt response: %v", err)
	}
	if receipt.Result == nil {
		t.Fatalf("no transaction receipt found for hash %s", txHash)
	}

	return receipt.Result
}

func (f *AnvilFork) FindERC20TransferReceipts(t *testing.T, tokenAddr, from, to string, fromBlock uint64) []*AnvilTransactionReceipt {
	t.Helper()

	transferTopic := crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)")).Hex()
	fromTopic := common.LeftPadBytes(common.HexToAddress(from).Bytes(), 32)
	toTopic := common.LeftPadBytes(common.HexToAddress(to).Bytes(), 32)

	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "eth_getLogs",
		"params": []any{
			map[string]any{
				"address":   tokenAddr,
				"fromBlock": fmt.Sprintf("0x%x", fromBlock),
				"toBlock":   "latest",
				"topics": []string{
					transferTopic,
					common.BytesToHash(fromTopic).Hex(),
					common.BytesToHash(toTopic).Hex(),
				},
			},
		},
		"id": 1,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal eth_getLogs payload: %v", err)
	}

	resp, err := http.Post(f.RPCURL, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("eth_getLogs failed: %v", err)
	}
	defer resp.Body.Close()

	var logsResp struct {
		Result []struct {
			TransactionHash string `json:"transactionHash"`
		} `json:"result"`
	}
	if err := jsonDecode(resp.Body, &logsResp); err != nil {
		t.Fatalf("parse eth_getLogs response: %v", err)
	}
	if len(logsResp.Result) == 0 {
		t.Fatalf("no ERC20 Transfer logs found for token=%s from=%s to=%s fromBlock=%d", tokenAddr, from, to, fromBlock)
	}

	seen := make(map[string]struct{}, len(logsResp.Result))
	receipts := make([]*AnvilTransactionReceipt, 0, len(logsResp.Result))
	for _, logEntry := range logsResp.Result {
		txHash := logEntry.TransactionHash
		if _, ok := seen[txHash]; ok {
			continue
		}
		seen[txHash] = struct{}{}

		receiptPayload := map[string]any{
			"jsonrpc": "2.0",
			"method":  "eth_getTransactionReceipt",
			"params":  []string{txHash},
			"id":      1,
		}
		data, err = json.Marshal(receiptPayload)
		if err != nil {
			t.Fatalf("marshal eth_getTransactionReceipt payload: %v", err)
		}

		receiptResp, err := http.Post(f.RPCURL, "application/json", bytes.NewReader(data))
		if err != nil {
			t.Fatalf("eth_getTransactionReceipt failed: %v", err)
		}

		var receipt struct {
			Result *AnvilTransactionReceipt `json:"result"`
		}
		if err := jsonDecode(receiptResp.Body, &receipt); err != nil {
			receiptResp.Body.Close()
			t.Fatalf("parse eth_getTransactionReceipt response: %v", err)
		}
		receiptResp.Body.Close()
		if receipt.Result == nil {
			t.Fatalf("no transaction receipt found for hash %s", txHash)
		}
		receipts = append(receipts, receipt.Result)
	}

	return receipts
}

func ParseHexBigInt(t *testing.T, hexValue string) *big.Int {
	t.Helper()

	value := new(big.Int)
	if _, ok := value.SetString(strings.TrimPrefix(hexValue, "0x"), 16); !ok {
		t.Fatalf("parse hex big.Int %q", hexValue)
	}

	return value
}

// DeployForkObolToken deploys a fork-local OBOL-compatible ERC20Permit token
// via Foundry and returns the deployed contract address.
func (f *AnvilFork) DeployForkObolToken(t *testing.T, deployerKey, initialHolder string, initialSupply *big.Int) string {
	t.Helper()

	if _, err := exec.LookPath("forge"); err != nil {
		t.Skip("forge not installed — required for fork-local OBOL deployment")
	}

	// Build the contract once in the local Foundry project.
	build := exec.Command("forge", "build")
	build.Dir = filepathJoinRepoRoot(t, ForkObolProjectDir)
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("forge build fork-obol failed: %v", err)
	}

	args := []string{
		"create",
		"--root", filepathJoinRepoRoot(t, ForkObolProjectDir),
		"src/ForkObolToken.sol:ForkObolToken",
		"--rpc-url", f.RPCURL,
		"--private-key", deployerKey,
		"--broadcast",
		"--json",
		"--constructor-args", initialHolder, initialSupply.String(),
	}
	cmd := exec.Command("forge", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("forge create ForkObolToken failed: %v", err)
	}

	var result struct {
		DeployedTo string `json:"deployedTo"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("parse forge create output: %v\n%s", err, out.String())
	}
	if result.DeployedTo == "" {
		t.Fatalf("forge create did not return deployedTo: %s", out.String())
	}
	t.Logf("deployed fork OBOL token at %s", result.DeployedTo)
	return result.DeployedTo
}

// MintMintableERC20 mints tokens on a permissive test ERC-20 with a public
// mint(address,uint256) function.
func (f *AnvilFork) MintMintableERC20(t *testing.T, tokenAddr, callerKey, to string, amount *big.Int) {
	t.Helper()

	cmd := exec.Command(
		"cast", "send",
		tokenAddr,
		"mint(address,uint256)",
		to,
		amount.String(),
		"--rpc-url", f.RPCURL,
		"--private-key", callerKey,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mint test ERC20 failed: %v\n%s", err, string(out))
	}
	t.Logf("minted %s tokens at %s to %s", amount, tokenAddr, to)
}

func filepathJoinRepoRoot(t *testing.T, rel string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	return filepath.Join(root, rel)
}
