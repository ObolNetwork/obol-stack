package stack

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

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

// LiveServices captures the snapshot used by ConfirmRunningServicesLoss.
type LiveServices struct {
	Offers []LiveServiceOffer
}

// Empty returns true when no live cluster services were found. The caller
// treats this as "safe to proceed without prompting".
func (s LiveServices) Empty() bool { return len(s.Offers) == 0 }

// ConfirmRunningServicesLoss warns the operator when `obol stack down` is
// about to stop a cluster that is currently serving traffic, and asks
// whether to proceed. Stopping preserves config and data, so a failed
// discovery (cluster already unreachable) is not a reason to prompt here;
// commands that DESTROY a cluster use ConfirmClusterDestroy instead.
//
// Returns true when the caller should continue. The rules:
//
//   - No live offers -> pass through (no prompt).
//   - skipConfirm (operator passed --yes) -> pass through.
//   - Interactive TTY -> render the list and ask `ui.Confirm` (default N).
//   - Non-interactive without --yes -> return a non-nil error so the
//     caller fails closed. This is the safety bar that prevents a stray
//     `ssh host '<obol stack down>'` from a non-prod worktree wiping the
//     production stack without an explicit operator decision.
func ConfirmRunningServicesLoss(cfg *config.Config, u *ui.UI, action string, skipConfirm bool) (bool, error) {
	services, _ := discoverLiveServicesFn(cfg)
	if services.Empty() {
		return true, nil
	}
	return confirmLoss(u, action, skipConfirm, services, nil)
}

// ConfirmClusterDestroy gates an action that deletes the cluster (and with
// it every in-cluster resource, including sub-agent wallet Secrets).
//
// Live services are always listed when found, and a discovery failure
// (cluster unreachable, listing error) is reported as "could not check what
// is running" and treated like live services: it needs an explicit yes.
// With always=true confirmation is required even when the cluster was
// positively checked and found idle. In every gated case: --yes proceeds
// with a warning, a TTY gets a default-No prompt, and a non-interactive run
// fails closed with an error naming --yes.
func ConfirmClusterDestroy(cfg *config.Config, u *ui.UI, action string, skipConfirm, always bool) (bool, error) {
	services, discoverErr := discoverLiveServicesFn(cfg)
	if services.Empty() && discoverErr == nil && !always {
		return true, nil
	}
	return confirmLoss(u, action, skipConfirm, services, discoverErr)
}

func confirmLoss(u *ui.UI, action string, skipConfirm bool, services LiveServices, discoverErr error) (bool, error) {
	if skipConfirm {
		switch {
		case !services.Empty():
			u.Warnf("%s: %d live offer(s) — proceeding because --yes was passed",
				action, len(services.Offers))
		case discoverErr != nil:
			u.Warnf("%s: could not check what is running on the cluster (%v) — proceeding because --yes was passed", action, discoverErr)
		default:
			u.Warnf("%s: destroying the cluster — proceeding because --yes was passed", action)
		}
		return true, nil
	}

	if !u.CanPrompt() {
		switch {
		case !services.Empty():
			return false, fmt.Errorf("refusing to run %q non-interactively while %d live offer(s) are serving traffic: pass --yes to confirm",
				action, len(services.Offers))
		case discoverErr != nil:
			return false, fmt.Errorf("refusing to run %q non-interactively: could not check what is running on the cluster (%v): pass --yes to confirm", action, discoverErr)
		default:
			return false, fmt.Errorf("refusing to run %q non-interactively: it destroys the existing cluster: pass --yes to confirm", action)
		}
	}

	u.Blank()
	if !services.Empty() {
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
		u.Blank()
		u.Dim(fmt.Sprintf("  After `%s`, external buyers will see 402/530 errors until `obol stack up`.", action))
	}
	if discoverErr != nil {
		u.Warnf("Could not check what is running on the cluster: %v", discoverErr)
		u.Dim("  Live offers and in-cluster wallets (sub-agent Secrets) may be lost.")
	} else if services.Empty() {
		u.Warnf("%s destroys the existing cluster and everything in it (including sub-agent wallet Secrets).", action)
	}
	u.Blank()

	return u.Confirm(fmt.Sprintf("Proceed with %s?", action), false), nil
}

// discoverLiveServicesFn is swappable in tests.
var discoverLiveServicesFn = discoverLiveServicesChecked

// DiscoverLiveServices snapshots cluster-side ServiceOffers in a
// payment-gate-ready state. `obol sell inference` gateways are standalone
// processes that don't depend on the cluster, so they aren't tracked. Errors
// are swallowed (best-effort snapshot); callers that
// must distinguish "nothing live" from "could not check" use
// discoverLiveServicesChecked.
func DiscoverLiveServices(cfg *config.Config) LiveServices {
	s, _ := discoverLiveServicesChecked(cfg)
	return s
}

// discoverLiveServicesChecked is DiscoverLiveServices plus the reason the
// cluster half could not be checked (unreachable cluster, listing or
// decoding failure).
func discoverLiveServicesChecked(cfg *config.Config) (LiveServices, error) {
	offers, err := discoverLiveOffers(cfg)
	return LiveServices{Offers: offers}, err
}

// discoverLiveOffers returns ServiceOffers whose PaymentGateReady AND
// RoutePublished conditions are True. We deliberately do not require
// `Ready=True` — the `Registered` condition stays False for unregistered
// offers indefinitely, and excluding those would let `obol stack down`
// silently nuke real production traffic (which is exactly the regression
// this gate exists to prevent). A cluster without the ServiceOffer CRD has
// no offers (not an error).
func discoverLiveOffers(cfg *config.Config) ([]LiveServiceOffer, error) {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return nil, err
	}
	bin, kc := kubectl.Paths(cfg)
	raw, err := kubectl.Output(bin, kc, "get", "serviceoffers.obol.org", "-A", "-o", "json")
	if err != nil {
		if strings.Contains(err.Error(), "doesn't have a resource type") {
			return nil, nil
		}
		return nil, fmt.Errorf("list ServiceOffers: %w", err)
	}
	var list struct {
		Items []rawOffer `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, fmt.Errorf("decode ServiceOffers: %w", err)
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
	return out, nil
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

// errSafetyAborted is a sentinel for "user declined a ConfirmClusterDestroy
// prompt". destroyOldBackendIfSwitching returns it so Init stops the whole
// command before switching backends (Down/Purge instead return nil directly
// from their own top-level check); Init maps it to a clean exit-0.
var errSafetyAborted = errors.New("aborted by operator at safety prompt")
