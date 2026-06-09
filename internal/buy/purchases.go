package buy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
)

// PurchaseSummary is the host-side view of one paid-inference subscription
// the agent is funding. The fields mirror buy.py's `list --json` payload —
// rename in lockstep with internal/embed/skills/buy-x402/scripts/buy.py
// (cmd_list `as_json=True`) when changing either side.
type PurchaseSummary struct {
	Name          string `json:"name"`
	Alias         string `json:"alias"`
	Model         string `json:"model"`
	Remaining     int    `json:"remaining"`
	Spent         int    `json:"spent"`
	TotalSigned   int    `json:"totalSigned"`
	Expired       int    `json:"expired"`
	Price         string `json:"price"` // per-request atomic amount
	Chain         string `json:"chain"`
	Endpoint      string `json:"endpoint"`
	AutoRefill    bool   `json:"autoRefill"`
	AssetSymbol   string `json:"assetSymbol"`
	AssetDecimals int    `json:"assetDecimals"`
}

// ListPurchases shells `buy.py list --json` inside the named agent pod
// and returns the parsed payload. Used by `obol model status` to surface
// pre-authorized budgets alongside the provider table.
//
// Returns an empty slice + nil error when the agent has no purchases, so
// callers can render a "no paid models" message without distinguishing
// from the "buy.py failed" case.
func ListPurchases(cfg *config.Config, runtime agentruntime.Runtime, id string) ([]PurchaseSummary, error) {
	kubectlBin := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfig := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	argv := BuyPyCommand(runtime, "list", "--json")
	kubectlArgs := agentruntime.BuildExecArgs(runtime, id, argv, false)

	cmd := exec.Command(kubectlBin, kubectlArgs...)
	// Inherit os.Environ so kubectl finds $HOME for its discovery cache
	// (otherwise the cache leaks into $PWD/.kube — see balance.go).
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("kubectl exec buy.py list: %s", msg)
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" || out == "null" {
		return nil, nil
	}
	var purchases []PurchaseSummary
	if err := json.Unmarshal([]byte(out), &purchases); err != nil {
		return nil, fmt.Errorf("parse buy.py list output: %w (raw: %s)", err, truncate(out, 200))
	}
	return purchases, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ErrNoAgent is returned when no agent target is available to query the
// purchase list (e.g. `obol stack up` not yet run, or no agents created).
var ErrNoAgent = errors.New("no agent instance available to query purchases")
