//go:build integration

package openclaw

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// ---------------------------------------------------------------------------
// Helpers — wallet-specific
// ---------------------------------------------------------------------------

// scaffoldWalletInstance creates a deployment with wallet + remote-signer.
// Uses Anthropic via llmspy for the LLM provider.
func scaffoldWalletInstance(t *testing.T, cfg *config.Config, id string, apiKey string) *WalletInfo {
	t.Helper()

	deploymentDir := deploymentPath(cfg, id)
	if err := os.MkdirAll(deploymentDir, 0755); err != nil {
		t.Fatalf("failed to create deployment dir: %v", err)
	}

	// Generate wallet (key + keystore + provision to PVC path).
	wallet, err := GenerateWallet(cfg, id)
	if err != nil {
		t.Fatalf("GenerateWallet: %v", err)
	}
	t.Logf("generated wallet: %s (keystore: %s)", wallet.Address, wallet.KeystoreUUID)

	// Write remote-signer values.
	rsValues := generateRemoteSignerValues(wallet)
	if err := os.WriteFile(filepath.Join(deploymentDir, "values-remote-signer.yaml"), []byte(rsValues), 0600); err != nil {
		t.Fatalf("write remote-signer values: %v", err)
	}

	// Write wallet metadata.
	if err := writeWalletMetadata(deploymentDir, wallet); err != nil {
		t.Fatalf("write wallet metadata: %v", err)
	}

	// Configure llmspy with Anthropic key.
	cloud := &CloudProviderInfo{
		Name:    "anthropic",
		APIKey:  apiKey,
		ModelID: "claude-sonnet-4-5-20250929",
		Display: "Claude Sonnet 4.5",
	}
	imported := buildLLMSpyRoutedOverlay(cloud)

	// Write overlay values (includes REMOTE_SIGNER_URL).
	hostname := fmt.Sprintf("openclaw-%s.%s", id, defaultDomain)
	namespace := fmt.Sprintf("%s-%s", appName, id)
	secretData := collectSensitiveData(imported)
	if err := writeUserSecretsFile(deploymentDir, secretData); err != nil {
		t.Fatalf("write secrets: %v", err)
	}
	overlay := generateOverlayValues(hostname, imported, len(secretData) > 0, nil)
	if err := os.WriteFile(filepath.Join(deploymentDir, "values-obol.yaml"), []byte(overlay), 0644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	// Write helmfile (openclaw + remote-signer).
	helmfileContent := generateHelmfile(id, namespace)
	if err := os.WriteFile(filepath.Join(deploymentDir, "helmfile.yaml"), []byte(helmfileContent), 0644); err != nil {
		t.Fatalf("write helmfile: %v", err)
	}

	// Stage skills.
	stageDefaultSkills(deploymentDir)

	return wallet
}

// waitForRemoteSignerReady waits for the remote-signer pod to be ready.
func waitForRemoteSignerReady(t *testing.T, cfg *config.Config, namespace string) {
	t.Helper()
	obolRun(t, cfg, "kubectl",
		"wait", "--for=condition=ready", "pod",
		"-l", "app.kubernetes.io/instance=remote-signer",
		"-n", namespace,
		"--timeout=180s",
	)
}

// kubectlExec runs a command inside the openclaw pod and returns stdout.
func kubectlExec(t *testing.T, cfg *config.Config, namespace string, args ...string) string {
	t.Helper()
	execArgs := append([]string{
		"kubectl", "-n", namespace,
		"exec", "deploy/openclaw", "-c", "openclaw", "--",
	}, args...)
	return obolRun(t, cfg, execArgs...)
}

// kubectlExecErr runs a command inside the openclaw pod, returning output + error.
func kubectlExecErr(cfg *config.Config, namespace string, args ...string) (string, error) {
	execArgs := append([]string{
		"kubectl", "-n", namespace,
		"exec", "deploy/openclaw", "-c", "openclaw", "--",
	}, args...)
	return obolRunErr(cfg, execArgs...)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestIntegration_WalletE2E deploys an OpenClaw instance with a wallet,
// funds it on Hoodi, sends a transaction, and verifies it succeeds.
// It also exercises the ethereum-networks skill for read-only queries.
//
// Required environment:
//
//	ANTHROPIC_API_KEY        — for LLM provider (routed through llmspy)
//	HOODI_FUNDER_PRIVATE_KEY — hex private key of a pre-funded Hoodi wallet
func TestIntegration_WalletE2E(t *testing.T) {
	cfg := requireCluster(t)
	apiKey := requireEnvKey(t, "ANTHROPIC_API_KEY")
	funderKey := requireEnvKey(t, "HOODI_FUNDER_PRIVATE_KEY")

	const id = "test-wallet-e2e"
	t.Cleanup(func() { cleanupInstance(t, cfg, id) })

	// 1. Scaffold and deploy instance with wallet + remote-signer.
	t.Log("scaffolding OpenClaw instance with wallet...")
	wallet := scaffoldWalletInstance(t, cfg, id, apiKey)

	// Configure llmspy gateway.
	obolRun(t, cfg, "model", "setup", "--provider", "anthropic", "--api-key", apiKey)

	t.Log("deploying via: obol openclaw sync " + id)
	obolRun(t, cfg, "openclaw", "sync", id)

	namespace := fmt.Sprintf("%s-%s", appName, id)

	// 2. Wait for both pods to be ready.
	t.Log("waiting for openclaw pod...")
	waitForPodReady(t, cfg, namespace)
	t.Log("waiting for remote-signer pod...")
	waitForRemoteSignerReady(t, cfg, namespace)

	// 3. Verify remote-signer health from inside the pod.
	t.Run("remote-signer/health", func(t *testing.T) {
		out := kubectlExec(t, cfg, namespace,
			"python3", "/data/.openclaw/skills/ethereum-local-wallet/scripts/signer.py", "health")
		t.Logf("remote-signer health: %s", strings.TrimSpace(out))
		if !strings.Contains(out, "ok") {
			t.Fatalf("remote-signer unhealthy: %s", out)
		}
	})

	// 4. Verify wallet address is listed.
	t.Run("remote-signer/accounts", func(t *testing.T) {
		out := kubectlExec(t, cfg, namespace,
			"python3", "/data/.openclaw/skills/ethereum-local-wallet/scripts/signer.py", "accounts")
		t.Logf("accounts output: %s", strings.TrimSpace(out))
		// The address from wallet generation should appear (lowercase compare).
		addrLower := strings.ToLower(wallet.Address)
		if !strings.Contains(strings.ToLower(out), addrLower[2:]) {
			t.Fatalf("expected wallet address %s in accounts output", wallet.Address)
		}
	})

	// 5. Fund the wallet on Hoodi using cast inside the pod.
	t.Run("hoodi/fund-wallet", func(t *testing.T) {
		// Send 0.01 ETH from funder to agent wallet on Hoodi.
		rpcURL := "http://erpc.erpc.svc.cluster.local:4000/rpc/hoodi"
		t.Logf("funding %s with 0.01 ETH on Hoodi...", wallet.Address)

		out := kubectlExec(t, cfg, namespace,
			"cast", "send",
			"--private-key", funderKey,
			"--rpc-url", rpcURL,
			"--value", "10000000000000000", // 0.01 ETH
			wallet.Address,
		)
		t.Logf("funding tx: %s", strings.TrimSpace(out))

		// Verify the agent wallet received funds.
		balOut := kubectlExec(t, cfg, namespace,
			"cast", "balance", "--ether", "--rpc-url", rpcURL, wallet.Address,
		)
		t.Logf("agent balance: %s ETH", strings.TrimSpace(balOut))
	})

	// 6. Sign and send a transaction from the agent wallet on Hoodi.
	t.Run("hoodi/send-tx", func(t *testing.T) {
		// Send a tiny amount back to the funder (or a burn address).
		// We use signer.py send-tx which auto-fills nonce/gas from eRPC.
		burnAddr := "0x000000000000000000000000000000000000dEaD"
		out := kubectlExec(t, cfg, namespace,
			"python3", "/data/.openclaw/skills/ethereum-local-wallet/scripts/signer.py",
			"send-tx",
			"--from", wallet.Address,
			"--to", burnAddr,
			"--value", "1000000000000", // 0.000001 ETH
			"--network", "hoodi",
		)
		t.Logf("send-tx output:\n%s", out)

		// Verify transaction hash is in output.
		if !strings.Contains(out, "0x") {
			t.Fatalf("no transaction hash in output")
		}
		// Verify success status.
		if strings.Contains(out, "reverted") {
			t.Fatal("transaction reverted")
		}
	})

	// 7. Sign a message (no network needed).
	t.Run("sign-message", func(t *testing.T) {
		out := kubectlExec(t, cfg, namespace,
			"python3", "/data/.openclaw/skills/ethereum-local-wallet/scripts/signer.py",
			"sign-msg", wallet.Address, "Hello from Obol Stack integration test",
		)
		sig := strings.TrimSpace(out)
		t.Logf("message signature: %s", sig)
		// EIP-191 signature = 65 bytes = 132 hex chars + 0x prefix = 132 chars.
		if !strings.HasPrefix(sig, "0x") || len(sig) != 132 {
			t.Fatalf("invalid signature length: got %d chars, want 132", len(sig))
		}
	})

	// 8. Wallet metadata ConfigMap exists.
	t.Run("wallet-metadata-configmap", func(t *testing.T) {
		out := obolRun(t, cfg, "kubectl", "-n", namespace,
			"get", "configmap", "wallet-metadata", "-o", "jsonpath={.data.addresses\\.json}")
		t.Logf("wallet-metadata: %s", out)

		var metadata struct {
			Addresses []struct {
				Address string `json:"address"`
			} `json:"addresses"`
			Count int `json:"count"`
		}
		if err := json.Unmarshal([]byte(out), &metadata); err != nil {
			t.Fatalf("parse wallet-metadata: %v", err)
		}
		if metadata.Count < 1 {
			t.Fatal("expected at least 1 address in wallet-metadata")
		}
		if !strings.EqualFold(metadata.Addresses[0].Address, wallet.Address) {
			t.Errorf("address mismatch: got %s, want %s", metadata.Addresses[0].Address, wallet.Address)
		}
	})

	// 9. Ethereum-networks skill — read-only queries via rpc.sh (cast).
	t.Run("ethereum-networks/hoodi-chain-id", func(t *testing.T) {
		out := kubectlExec(t, cfg, namespace,
			"sh", "/data/.openclaw/skills/ethereum-networks/scripts/rpc.sh",
			"--network", "hoodi", "chain-id",
		)
		chainID := strings.TrimSpace(out)
		t.Logf("Hoodi chain ID: %s", chainID)
		if chainID != "560048" {
			t.Errorf("expected chain ID 560048, got %s", chainID)
		}
	})

	t.Run("ethereum-networks/hoodi-block", func(t *testing.T) {
		out := kubectlExec(t, cfg, namespace,
			"sh", "/data/.openclaw/skills/ethereum-networks/scripts/rpc.sh",
			"--network", "hoodi", "block", "latest",
		)
		t.Logf("latest block output (first 200 chars): %.200s", out)
		if !strings.Contains(out, "number") {
			t.Error("block output missing 'number' field")
		}
	})

	t.Run("ethereum-networks/hoodi-balance", func(t *testing.T) {
		out := kubectlExec(t, cfg, namespace,
			"sh", "/data/.openclaw/skills/ethereum-networks/scripts/rpc.sh",
			"--network", "hoodi", "balance", wallet.Address,
		)
		t.Logf("agent Hoodi balance: %s", strings.TrimSpace(out))
	})

	t.Run("ethereum-networks/mainnet-gas-price", func(t *testing.T) {
		out := kubectlExec(t, cfg, namespace,
			"sh", "/data/.openclaw/skills/ethereum-networks/scripts/rpc.sh",
			"gas-price",
		)
		t.Logf("mainnet gas price: %s", strings.TrimSpace(out))
		if strings.TrimSpace(out) == "" || strings.TrimSpace(out) == "0" {
			t.Error("gas price should be > 0")
		}
	})
}
