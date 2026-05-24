package stack

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// LiveServiceOffer is the subset of a ServiceOffer relevant to the safety
// check: enough to render the prompt without pulling in monetizeapi.
type LiveServiceOffer struct {
	Namespace string
	Name      string
	Type      string
	Model     string
	Path      string
	Price     string
}

// LiveHostGateway is a sell-inference offer with a still-running host
// gateway process (PID file present and alive). These survive `obol stack
// down` of the cluster and need a separate signal.
type LiveHostGateway struct {
	Name string
	PID  int
}

// LiveServices captures the snapshot used by ConfirmRunningServicesLoss.
type LiveServices struct {
	Offers   []LiveServiceOffer
	Gateways []LiveHostGateway
}

// Empty returns true when no live cluster services and no live host
// gateways were found. The caller treats this as "safe to proceed without
// prompting".
func (s LiveServices) Empty() bool { return len(s.Offers) == 0 && len(s.Gateways) == 0 }

// ConfirmRunningServicesLoss warns the operator when a destructive action
// (`obol stack down` / `obol stack purge`) is about to tear down a cluster
// that is currently serving traffic, and asks whether to proceed.
//
// Returns true when the caller should continue. The rules:
//
//   - No live offers and no live gateways -> pass through (no prompt).
//   - skipConfirm (operator passed --yes) -> pass through.
//   - Interactive TTY -> render the list and ask `ui.Confirm` (default N).
//   - Non-interactive without --yes -> return a non-nil error so the
//     caller fails closed. This is the safety bar that prevents a stray
//     `ssh host '<obol stack down>'` from a non-prod worktree wiping the
//     production stack without an explicit operator decision.
func ConfirmRunningServicesLoss(cfg *config.Config, u *ui.UI, action string, skipConfirm bool) (bool, error) {
	services := DiscoverLiveServices(cfg)
	if services.Empty() {
		return true, nil
	}
	if skipConfirm {
		u.Warnf("%s: %d live offer(s), %d host gateway(s) — proceeding because --yes was passed",
			action, len(services.Offers), len(services.Gateways))
		return true, nil
	}

	if !u.IsTTY() {
		return false, fmt.Errorf("refusing to run %q non-interactively while %d live offer(s) and %d host gateway(s) are serving traffic: pass --yes to confirm",
			action, len(services.Offers), len(services.Gateways))
	}

	u.Blank()
	u.Warnf("The following services are currently live on this stack:")
	u.Blank()
	for _, o := range services.Offers {
		ns := o.Namespace
		if ns == "" {
			ns = "default"
		}
		summary := o.Type
		if o.Model != "" {
			summary = fmt.Sprintf("%s, %s", summary, o.Model)
		}
		if o.Price != "" {
			summary = fmt.Sprintf("%s, %s", summary, o.Price)
		}
		u.Printf("  • %s/%s", ns, o.Name)
		if o.Path != "" {
			u.Dim("      endpoint: " + o.Path)
		}
		u.Dim("      " + summary)
	}
	for _, g := range services.Gateways {
		u.Printf("  • sell-inference gateway %s (pid %d) will be orphaned", g.Name, g.PID)
	}
	u.Blank()
	u.Dim(fmt.Sprintf("  After `%s`, external buyers will see 402/530 errors until `obol stack up`.", action))
	u.Blank()

	return u.Confirm(fmt.Sprintf("Proceed with %s?", action), false), nil
}

// DiscoverLiveServices snapshots cluster-side ServiceOffers in a
// payment-gate-ready state and host-side sell-inference gateways with
// alive PID files. Errors are intentionally swallowed: a discovery failure
// must not block `obol stack down`. A best-effort empty snapshot is
// returned so the caller falls through to "no prompt needed" only when we
// have positive evidence of nothing live.
func DiscoverLiveServices(cfg *config.Config) LiveServices {
	return LiveServices{
		Offers:   discoverLiveOffers(cfg),
		Gateways: discoverLiveHostGateways(cfg),
	}
}

// discoverLiveOffers returns ServiceOffers whose PaymentGateReady AND
// RoutePublished conditions are True. We deliberately do not require
// `Ready=True` — the `Registered` condition stays False for unregistered
// offers indefinitely, and excluding those would let `obol stack down`
// silently nuke real production traffic (which is exactly the regression
// this gate exists to prevent).
func discoverLiveOffers(cfg *config.Config) []LiveServiceOffer {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		// Cluster already unreachable: down/purge is a no-op for the
		// in-cluster side. Caller will still see host gateways.
		return nil
	}
	bin, kc := kubectl.Paths(cfg)
	raw, err := kubectl.Output(bin, kc, "get", "serviceoffers.obol.org", "-A", "-o", "json")
	if err != nil {
		return nil
	}
	var list struct {
		Items []rawOffer `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil
	}

	out := make([]LiveServiceOffer, 0, len(list.Items))
	for _, item := range list.Items {
		if !item.gateReady() {
			continue
		}
		out = append(out, LiveServiceOffer{
			Namespace: item.Metadata.Namespace,
			Name:      item.Metadata.Name,
			Type:      item.Spec.Type,
			Model:     item.Spec.Model.Name,
			Path:      item.Spec.Path,
			Price:     item.priceSummary(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace == out[j].Namespace {
			return out[i].Name < out[j].Name
		}
		return out[i].Namespace < out[j].Namespace
	})
	return out
}

// rawOffer mirrors the subset of monetizeapi.ServiceOffer used here. We
// avoid importing monetizeapi to keep internal/stack free of CRD typing.
type rawOffer struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		Type  string `json:"type"`
		Path  string `json:"path"`
		Model struct {
			Name string `json:"name"`
		} `json:"model"`
		Payment struct {
			Price struct {
				PerRequest string `json:"perRequest"`
				PerMTok    string `json:"perMTok"`
				PerHour    string `json:"perHour"`
			} `json:"price"`
			Asset struct {
				Symbol string `json:"symbol"`
			} `json:"asset"`
		} `json:"payment"`
	} `json:"spec"`
	Status struct {
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
	} `json:"status"`
}

func (o rawOffer) gateReady() bool {
	var paymentGate, routePublished bool
	for _, c := range o.Status.Conditions {
		if c.Status != "True" {
			continue
		}
		switch c.Type {
		case "PaymentGateReady":
			paymentGate = true
		case "RoutePublished":
			routePublished = true
		}
	}
	return paymentGate && routePublished
}

func (o rawOffer) priceSummary() string {
	asset := o.Spec.Payment.Asset.Symbol
	if asset == "" {
		asset = "USDC"
	}
	switch {
	case o.Spec.Payment.Price.PerRequest != "":
		return fmt.Sprintf("%s %s/request", o.Spec.Payment.Price.PerRequest, asset)
	case o.Spec.Payment.Price.PerMTok != "":
		return fmt.Sprintf("%s %s/MTok", o.Spec.Payment.Price.PerMTok, asset)
	case o.Spec.Payment.Price.PerHour != "":
		return fmt.Sprintf("%s %s/hour", o.Spec.Payment.Price.PerHour, asset)
	}
	return ""
}

// discoverLiveHostGateways walks <StateDir>/sell-inference/<name>/gateway.pid
// for each persisted sell-inference deployment and reports those with a
// PID file pointing at an alive process. Mirrors the readGatewayPID +
// processAlive logic in cmd/obol/sell.go to avoid an import-from-cmd
// cycle.
func discoverLiveHostGateways(cfg *config.Config) []LiveHostGateway {
	root := filepath.Join(cfg.StateDir, "sell-inference")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := make([]LiveHostGateway, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pidPath := filepath.Join(root, e.Name(), "gateway.pid")
		data, err := os.ReadFile(pidPath)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 0 {
			continue
		}
		if !pidAlive(pid) {
			continue
		}
		out = append(out, LiveHostGateway{Name: e.Name(), PID: pid})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 doesn't deliver — just probes existence/permission on Unix.
	return p.Signal(syscall.Signal(0)) == nil
}

// errSafetyAborted lets cmd/obol/stack.go distinguish "user said no" from
// other errors. The CLI maps this to exit code 0 with a brief message
// instead of a noisy error trace.
var errSafetyAborted = errors.New("aborted by operator at safety prompt")

// ErrSafetyAborted is exported for callers that want to detect the abort
// path with errors.Is.
func ErrSafetyAborted() error { return errSafetyAborted }
