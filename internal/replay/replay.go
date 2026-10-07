// Package replay re-applies the host-side records in $OBOL_CONFIG_DIR to the
// cluster. Every piece of operator state that only lives in etcd has a
// record-on-write file next to the stack config; ReplayRecorded is the ONE
// ordered list that brings a fresh (or reset) cluster back in line with
// them. `obol stack up` and `obol sell resume` both call it, so a host
// reboot and a full cluster recreation converge on the same state.
//
// The order is defined, and justified step by step, on [Order].
//
// Every step is best-effort: a failure (returned error or any warning the
// step printed) is reported per step and in the closing summary, and never
// aborts the remaining steps or `stack up`.
package replay

import (
	"context"
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/agentcrd"
	"github.com/ObolNetwork/obol-stack/internal/agentidentity"
	"github.com/ObolNetwork/obol-stack/internal/agentsync"
	"github.com/ObolNetwork/obol-stack/internal/app"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/model"
	"github.com/ObolNetwork/obol-stack/internal/network"
	"github.com/ObolNetwork/obol-stack/internal/storefront"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
)

// Step names, in replay order (see the package doc for why).
const (
	StepModels         = "models"
	StepNetworks       = "networks"
	StepRPCUpstreams   = "rpc-upstreams"
	StepERPCOverlay    = "erpc-overlay"
	StepX402Pricing    = "x402-pricing"
	StepAgentIdentity  = "agent-identity"
	StepAgentInstances = "agent-instances"
	StepAgentCRs       = "agent-crs"
	StepStorefront     = "storefront"
	StepApps           = "apps"
	StepSellOffers     = "sell-offers"
)

// Order is the single replay list shared by `obol stack up` and
// `obol sell resume`. Each step depends only on steps above it.
var Order = []string{
	// LiteLLM model_list + provider keys. Agents render their default model
	// from the model_list head, so this precedes every agent step. stack.Up
	// also runs it earlier (between LLM auto-config and default-Hermes
	// setup); the repeat here is a no-op diff unless something drifted, and
	// it is what restores models on `sell resume`.
	StepModels,
	// Local node deployments whose helm release is missing or failed. Their
	// sync registers a local-<net>-<id> eRPC upstream, so before eRPC steps.
	StepNetworks,
	// Recorded remote RPC upstreams merged into the eRPC ConfigMap.
	StepRPCUpstreams,
	// Operator baskets/scoring/rate-limits: merges onto base + local +
	// recorded upstreams, so AFTER both (#763).
	StepERPCOverlay,
	// payTo wallet / chain / facilitator. Before offers so the verifier
	// prices the first replayed offer with the operator's wallet.
	StepX402Pricing,
	// ERC-8004 AgentIdentity incl. status.registrations. Before offers: the
	// controller consults it when registering and would otherwise mint a
	// NEW on-chain agent.
	StepAgentIdentity,
	// Hermes/OpenClaw helmfile instances whose release is missing or failed
	// (the default instance was just deployed by stack.Up, so it is skipped).
	StepAgentInstances,
	// Recorded Agent CRs. Before offers: agent-backed ServiceOffers resolve
	// agent.ref and would dangle without their Agent.
	StepAgentCRs,
	// Storefront branding ConfigMap. Before offers so the controller's first
	// catalog rebuild carries the operator's branding.
	StepStorefront,
	// Installed obol-app deployments. Before offers: http offers can gate an
	// app's Service as their upstream.
	StepApps,
	// Persisted ServiceOffers + sell-inference gateway relaunch. Last: it
	// depends on everything above.
	StepSellOffers,
}

// Options carries the steps implemented outside internal/ (package main).
type Options struct {
	// ResumeSellOffers replays persisted ServiceOffers and relaunches
	// sell-inference host gateways (cmd/obol resumeSellOffers). nil skips
	// the step.
	ResumeSellOffers func(ctx context.Context, cfg *config.Config, u *ui.UI) error
}

// StepFunc runs one replay step. A returned error marks the step warned.
type StepFunc func(ctx context.Context, cfg *config.Config, u *ui.UI) error

// Step is one named entry of the replay list.
type Step struct {
	Name string
	Run  StepFunc
}

// Summary reports the outcome of a replay.
type Summary struct {
	OK     []string
	Warned []string
	// Errors holds the error returned by each failed step (steps that only
	// printed warnings are in Warned without an entry here).
	Errors map[string]error
}

// Steps returns the replay list in Order.
func Steps(opts Options) []Step {
	fns := map[string]StepFunc{
		StepModels: func(_ context.Context, cfg *config.Config, u *ui.UI) error {
			model.ReconcileRecorded(cfg, u)
			return nil
		},
		StepNetworks: func(_ context.Context, cfg *config.Config, u *ui.UI) error {
			return network.ResumeInstalled(cfg, u)
		},
		StepRPCUpstreams: func(_ context.Context, cfg *config.Config, u *ui.UI) error {
			network.ReconcileRecordedRPCs(cfg, u)
			return nil
		},
		StepERPCOverlay: func(_ context.Context, cfg *config.Config, u *ui.UI) error {
			network.ReconcileERPCOverlay(cfg, u)
			return nil
		},
		StepX402Pricing:   replayPricing,
		StepAgentIdentity: replayIdentities,
		StepAgentInstances: func(_ context.Context, cfg *config.Config, u *ui.UI) error {
			return agentsync.SyncInstances(cfg, u, agentsync.Options{OnlyMissing: true})
		},
		StepAgentCRs: func(_ context.Context, cfg *config.Config, u *ui.UI) error {
			agentcrd.ResumeAll(cfg, u)
			return nil
		},
		StepStorefront: func(_ context.Context, cfg *config.Config, u *ui.UI) error {
			storefront.ReconcileRecorded(cfg, u)
			return nil
		},
		StepApps: func(_ context.Context, cfg *config.Config, u *ui.UI) error {
			app.ResumeAll(cfg, u)
			return nil
		},
		StepSellOffers: func(ctx context.Context, cfg *config.Config, u *ui.UI) error {
			if opts.ResumeSellOffers == nil {
				return nil
			}
			return opts.ResumeSellOffers(ctx, cfg, u)
		},
	}
	out := make([]Step, 0, len(Order))
	for _, name := range Order {
		out = append(out, Step{Name: name, Run: fns[name]})
	}
	return out
}

// ReplayRecorded runs every replay step in Order against cfg's cluster.
func ReplayRecorded(ctx context.Context, cfg *config.Config, u *ui.UI, opts Options) Summary {
	return run(ctx, cfg, u, Steps(opts))
}

func run(ctx context.Context, cfg *config.Config, u *ui.UI, steps []Step) Summary {
	sum := Summary{Errors: map[string]error{}}
	for _, s := range steps {
		if err := ctx.Err(); err != nil {
			sum.Warned = append(sum.Warned, s.Name)
			sum.Errors[s.Name] = err
			continue
		}
		before := u.WarnCount()
		err := s.Run(ctx, cfg, u)
		if err != nil {
			u.Warnf("Replay step %s: %v", s.Name, err)
			sum.Errors[s.Name] = err
		}
		if err != nil || u.WarnCount() > before {
			sum.Warned = append(sum.Warned, s.Name)
			continue
		}
		sum.OK = append(sum.OK, s.Name)
	}
	if len(sum.Warned) == 0 {
		u.Dim(fmt.Sprintf("Recorded state replay: %d/%d steps ok", len(sum.OK), len(steps)))
	} else {
		u.Warnf("Recorded state replay: %d ok, %d warned (%s) — see warnings above",
			len(sum.OK), len(sum.Warned), strings.Join(sum.Warned, ", "))
	}
	return sum
}

func replayPricing(_ context.Context, cfg *config.Config, u *ui.UI) error {
	changed, err := x402verifier.ReconcileRecordedPricing(cfg)
	if err != nil {
		return fmt.Errorf("x402 pricing (re-run 'obol sell pricing'): %w", err)
	}
	if changed {
		u.Detail("Restored", "x402 seller pricing (payTo wallet, chain, facilitator)")
	}
	return nil
}

func replayIdentities(_ context.Context, cfg *config.Config, u *ui.UI) error {
	recs, listErr := agentidentity.List(cfg)
	if len(recs) == 0 {
		return listErr
	}
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return err
	}
	k := agentidentity.NewKube(cfg)
	var errs []string
	if listErr != nil {
		errs = append(errs, listErr.Error())
	}
	for _, rec := range recs {
		outcome, err := agentidentity.ReconcileOne(cfg, k, rec)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		switch outcome {
		case agentidentity.OutcomeCreated, agentidentity.OutcomeSeeded:
			u.Detail("Restored", fmt.Sprintf("AgentIdentity %s/%s (%s)", rec.Metadata.Namespace, rec.Metadata.Name, outcome))
		case agentidentity.OutcomeRefreshed:
			u.Dim(fmt.Sprintf("  Refreshed AgentIdentity record %s/%s from cluster", rec.Metadata.Namespace, rec.Metadata.Name))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}
