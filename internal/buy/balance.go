package buy

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
)

// WalletInfo is the host-side view of an agent's remote-signer wallet on a
// given chain, including the atomic balance for one token.
type WalletInfo struct {
	Address     string   // 0x-prefixed signer address from buy.py.
	Token       string   // Upper-case symbol the caller asked for (USDC, OBOL).
	Chain       string   // Canonical chain name the balance was read on.
	AtomicUnits *big.Int // Raw on-chain balance, scaled by the token's decimals.
	Decimals    int      // Decimals for the token, for display formatting.
}

// HumanBalance returns the balance with the minimum fractional digits
// needed (whole numbers render as "12", dust as "0.001"). Matches the
// host-side summary formatter (cmd/obol/buy.go::formatTokenAmount) so
// balance lines and budget lines look consistent.
func (w WalletInfo) HumanBalance() string {
	if w.AtomicUnits == nil {
		return "0"
	}
	dec := w.Decimals
	if dec <= 0 {
		return w.AtomicUnits.String()
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(dec)), nil)
	r := new(big.Rat).SetFrac(w.AtomicUnits, scale)
	s := r.FloatString(dec)
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	return s
}

// FetchWalletInfo execs `buy.py balance --chain <chain>` inside the named
// agent pod and parses the output to recover the signer address and the
// balance for `token` (e.g. "USDC" or "OBOL").
//
// We shell out instead of re-implementing the signer + eRPC RPC walk in Go
// because the agent pod already has SA credentials, the signer URL, and the
// eRPC alias resolution wired up via the buy-x402 skill. Re-deriving all
// three from the host would be a meaningful surface area expansion for what
// is a single confirmation-line preflight.
func FetchWalletInfo(cfg *config.Config, runtime agentruntime.Runtime, id, token, chain string) (*WalletInfo, error) {
	token = strings.ToUpper(strings.TrimSpace(token))
	if token == "" {
		return nil, errors.New("token is empty")
	}
	chain = strings.TrimSpace(chain)
	if chain == "" {
		return nil, errors.New("chain is empty")
	}

	kubectlBin := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfig := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	argv := BuyPyCommand(runtime, "balance", "--chain", chain)
	kubectlArgs := agentruntime.BuildExecArgs(runtime, id, argv, false)

	cmd := exec.Command(kubectlBin, kubectlArgs...)
	// Inherit the parent environment so kubectl can find $HOME for its
	// discovery + HTTP cache (default $HOME/.kube/cache). Setting cmd.Env
	// without os.Environ() leaves HOME unset and kubectl drops a .kube/
	// folder in the current working directory on every invocation.
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("kubectl exec buy.py balance: %s", msg)
	}

	return parseBalanceOutput(stdout.String(), token, chain)
}

var balanceLineRe = regexp.MustCompile(`^([A-Z0-9]+):\s+\S+\s+\((\d+)\s+(micro-units|base-units)\)`)

func parseBalanceOutput(out, token, chain string) (*WalletInfo, error) {
	info := &WalletInfo{Token: token, Chain: chain}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Wallet:") {
			info.Address = strings.TrimSpace(strings.TrimPrefix(line, "Wallet:"))
			continue
		}
		matches := balanceLineRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		if !strings.EqualFold(matches[1], token) {
			continue
		}
		atomic, ok := new(big.Int).SetString(matches[2], 10)
		if !ok {
			return nil, fmt.Errorf("parse %s balance %q: not a base-10 integer", token, matches[2])
		}
		info.AtomicUnits = atomic
		// micro-units → 6 decimals, base-units → 18 decimals (per buy.py
		// labeling). Token registry has the authoritative decimals, but we
		// stay self-contained here so callers don't need to re-resolve.
		if matches[3] == "micro-units" {
			info.Decimals = 6
		} else {
			info.Decimals = 18
		}
		break
	}
	if info.Address == "" {
		return nil, errors.New("buy.py balance produced no Wallet: line")
	}
	if info.AtomicUnits == nil {
		return nil, fmt.Errorf("buy.py balance produced no %s row on chain %s", token, chain)
	}
	return info, nil
}
