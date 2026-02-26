package testutil

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

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
		"--port", fmt.Sprintf("%d", port),
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
