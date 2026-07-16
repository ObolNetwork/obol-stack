package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/stack"
	"github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli/v3"
)

func writeTempFile(pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func removeTempFile(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

// agentIdentityRecord is the JSON-shaped view of AgentIdentity used by the
// CLI to read / write the CR via kubectl. Mirrors monetizeapi.AgentIdentity
// but only carries the fields the CLI cares about.
type agentIdentityRecord struct {
	APIVersion string                          `json:"apiVersion"`
	Kind       string                          `json:"kind"`
	Metadata   agentIdentityMetadata           `json:"metadata"`
	Spec       monetizeapi.AgentIdentitySpec   `json:"spec"`
	Status     monetizeapi.AgentIdentityStatus `json:"status,omitempty"`
}

type agentIdentityMetadata struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

func newAgentIdentityRecord(ns, name string) *agentIdentityRecord {
	return &agentIdentityRecord{
		APIVersion: monetizeapi.Group + "/" + monetizeapi.Version,
		Kind:       monetizeapi.AgentIdentityKind,
		Metadata: agentIdentityMetadata{
			Namespace: ns,
			Name:      name,
		},
	}
}

// loadAgentIdentity reads the AgentIdentity CR at ns/name. Returns (nil,
// nil) when the resource does not exist so callers can branch on "first
// run vs migration". Other errors are returned verbatim.
func loadAgentIdentity(cfg *config.Config, ns, name string) (*agentIdentityRecord, error) {
	raw, err := kubectlOutput(cfg, "get", "agentidentities.obol.org", name, "-n", ns, "-o", "json")
	if err != nil {
		// kubectl returns a non-zero exit for NotFound; the wrapped error
		// message carries "NotFound". Treat that as missing.
		if strings.Contains(err.Error(), "NotFound") || strings.Contains(err.Error(), "not found") {
			return nil, nil
		}
		return nil, err
	}
	var rec agentIdentityRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, fmt.Errorf("decode AgentIdentity %s/%s: %w", ns, name, err)
	}
	return &rec, nil
}

// applyAgentIdentity creates or updates the AgentIdentity CR, then patches
// the status subresource. The CRD enables status as a subresource, so a
// plain kubectl apply must not be relied on to persist status.registrations.
func applyAgentIdentity(cfg *config.Config, rec *agentIdentityRecord) error {
	if rec == nil || rec.Metadata.Name == "" || rec.Metadata.Namespace == "" {
		return errors.New("applyAgentIdentity: namespace and name required")
	}
	specRecord := struct {
		APIVersion string                        `json:"apiVersion"`
		Kind       string                        `json:"kind"`
		Metadata   agentIdentityMetadata         `json:"metadata"`
		Spec       monetizeapi.AgentIdentitySpec `json:"spec"`
	}{
		APIVersion: rec.APIVersion,
		Kind:       rec.Kind,
		Metadata:   rec.Metadata,
		Spec:       rec.Spec,
	}
	data, err := json.Marshal(specRecord)
	if err != nil {
		return err
	}
	tmp, err := writeTempFile("agentidentity-*.json", data)
	if err != nil {
		return err
	}
	defer removeTempFile(tmp)
	if err := kubectlRun(cfg, "apply", "-f", tmp); err != nil {
		return err
	}
	if !hasAgentIdentityStatus(rec.Status) {
		return nil
	}
	return patchAgentIdentityStatus(cfg, rec)
}

func patchAgentIdentityStatus(cfg *config.Config, rec *agentIdentityRecord) error {
	patch, err := json.Marshal(map[string]any{"status": rec.Status})
	if err != nil {
		return err
	}
	return kubectlRun(
		cfg,
		"patch", "agentidentities.obol.org", rec.Metadata.Name,
		"-n", rec.Metadata.Namespace,
		"--subresource=status",
		"--type=merge",
		"-p", string(patch),
	)
}

func hasAgentIdentityStatus(status monetizeapi.AgentIdentityStatus) bool {
	return monetizeapi.HasAgentIdentityRegistrations(status)
}

// ensureAgentIdentity loads the canonical AgentIdentity at ns/name, seeding
// per-chain registrations from existing ServiceOffer.status.agentId or
// RegistrationRequest.status.agentId entries when missing. Returns the
// loaded-or-seeded record.
func ensureAgentIdentity(cfg *config.Config, ns, name string, defaults monetizeapi.AgentIdentitySpec) (*agentIdentityRecord, error) {
	rec, err := loadAgentIdentity(cfg, ns, name)
	if err != nil {
		return nil, err
	}
	if rec != nil {
		return rec, nil
	}

	rec = newAgentIdentityRecord(ns, name)
	rec.Spec = defaults

	if seed := seedAgentIdentityFromCluster(cfg); seed != nil {
		rec.Status = seed.Status
	}

	if err := applyAgentIdentity(cfg, rec); err != nil {
		return nil, fmt.Errorf("apply AgentIdentity %s/%s: %w", ns, name, err)
	}
	return rec, nil
}

// seedAgentIdentityFromCluster scans existing ServiceOffers and
// RegistrationRequests for a recorded agentId. Returns the oldest entry by
// creation timestamp so that an early-mainnet agent is preferred over a
// later base-sepolia experiment. Best-effort: errors are swallowed and
// nil is returned so the caller falls back to "create empty identity".
func seedAgentIdentityFromCluster(cfg *config.Config) *monetizeapi.AgentIdentity {
	if seed := seedFromServiceOffers(cfg); seed != nil {
		return seed
	}
	if seed := seedFromRegistrationRequests(cfg); seed != nil {
		return seed
	}
	return nil
}

func seedFromServiceOffers(cfg *config.Config) *monetizeapi.AgentIdentity {
	raw, err := kubectlOutput(cfg, "get", "serviceoffers.obol.org", "-A", "-o", "json")
	if err != nil {
		return nil
	}
	var list struct {
		Items []monetizeapi.ServiceOffer `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil
	}
	pointers := make([]*monetizeapi.ServiceOffer, 0, len(list.Items))
	for i := range list.Items {
		pointers = append(pointers, &list.Items[i])
	}
	// Reuse the controller's seeding helper for the oldest-with-agentId rule.
	return seedFromServiceOfferPointers(pointers)
}

// seedFromServiceOfferPointers is a thin local copy of the controller-side
// helper so cmd/obol does not need to depend on internal/serviceoffercontroller
// (which would create an import cycle on test packages).
func seedFromServiceOfferPointers(offers []*monetizeapi.ServiceOffer) *monetizeapi.AgentIdentity {
	type tsEntry struct {
		offer *monetizeapi.ServiceOffer
		ts    time.Time
	}
	entries := make([]tsEntry, 0, len(offers))
	for _, o := range offers {
		if o == nil || strings.TrimSpace(o.Status.AgentID) == "" {
			continue
		}
		entries = append(entries, tsEntry{offer: o, ts: o.CreationTimestamp.Time})
	}
	if len(entries) == 0 {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ts.Equal(entries[j].ts) {
			left := entries[i].offer.Namespace + "/" + entries[i].offer.Name
			right := entries[j].offer.Namespace + "/" + entries[j].offer.Name
			return left < right
		}
		return entries[i].ts.Before(entries[j].ts)
	})
	status := monetizeapi.AgentIdentityStatus{}
	for _, entry := range entries {
		o := entry.offer
		if monetizeapi.AgentIdentityAgentIDForChain(status, o.Spec.Payment.Network) == "" {
			status = monetizeapi.UpsertAgentIdentityRegistration(status, o.Spec.Payment.Network, o.Status.AgentID)
		}
	}
	return &monetizeapi.AgentIdentity{Status: status}
}

func seedFromRegistrationRequests(cfg *config.Config) *monetizeapi.AgentIdentity {
	raw, err := kubectlOutput(cfg, "get", "registrationrequests.obol.org", "-A", "-o", "json")
	if err != nil {
		return nil
	}
	var list struct {
		Items []monetizeapi.RegistrationRequest `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil
	}
	type tsEntry struct {
		request *monetizeapi.RegistrationRequest
		ts      time.Time
	}
	entries := make([]tsEntry, 0, len(list.Items))
	for i := range list.Items {
		r := &list.Items[i]
		if strings.TrimSpace(r.Spec.Chain) == "" || strings.TrimSpace(r.Status.AgentID) == "" {
			continue
		}
		entries = append(entries, tsEntry{request: r, ts: r.CreationTimestamp.Time})
	}
	if len(entries) == 0 {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ts.Equal(entries[j].ts) {
			left := entries[i].request.Namespace + "/" + entries[i].request.Name
			right := entries[j].request.Namespace + "/" + entries[j].request.Name
			return left < right
		}
		return entries[i].ts.Before(entries[j].ts)
	})
	status := monetizeapi.AgentIdentityStatus{}
	for _, entry := range entries {
		r := entry.request
		if monetizeapi.AgentIdentityAgentIDForChain(status, r.Spec.Chain) == "" {
			status = monetizeapi.UpsertAgentIdentityRegistration(status, r.Spec.Chain, r.Status.AgentID)
		}
	}
	if !monetizeapi.HasAgentIdentityRegistrations(status) {
		return nil
	}
	return &monetizeapi.AgentIdentity{Status: status}
}

// sellIdentityCommand groups identity-level subcommands.
func sellIdentityCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "identity",
		Usage: "Manage the durable ERC-8004 AgentIdentity record",
		Commands: []*cli.Command{
			sellIdentityImportCommand(cfg),
			sellIdentityForgetCommand(cfg),
		},
	}
}

func sellIdentityForgetCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "forget",
		Usage:     "Remove a chain's registration from the AgentIdentity record",
		ArgsUsage: "<chain>",
		Description: `Removes the recorded agentId for <chain> from the AgentIdentity CR.

Use this to correct a registration that was recorded under the wrong
chain (e.g. a wrong-chain agentId written by a bug, or an offer that
switched networks before an on-chain id was verified). This does not
touch the on-chain NFT itself; it only clears the local record so the
controller re-derives the id from scratch on the next reconcile.`,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 {
				return fmt.Errorf("chain required: obol sell identity forget <chain>")
			}
			chain := strings.TrimSpace(cmd.Args().First())

			ns := monetizeapi.AgentIdentityDefaultNamespace
			name := monetizeapi.AgentIdentityDefaultName
			rec, err := loadAgentIdentity(cfg, ns, name)
			if err != nil {
				return err
			}
			if rec == nil {
				return fmt.Errorf("AgentIdentity %s/%s not found", ns, name)
			}
			existing := monetizeapi.AgentIdentityAgentIDForChain(rec.Status, chain)
			if existing == "" {
				getUI(cmd).Printf("AgentIdentity %s/%s has no registration on %s; nothing to do.", ns, name, chain)
				return nil
			}
			rec.Status = monetizeapi.RemoveAgentIdentityRegistration(rec.Status, chain)
			// Patch registrations explicitly rather than via
			// patchAgentIdentityStatus: that helper marshals with
			// `omitempty`, so dropping the last entry (the common case —
			// the poisoned record is usually the only one) would omit
			// the field from the JSON merge patch and leave the stale
			// array untouched server-side instead of clearing it.
			registrations := rec.Status.Registrations
			if registrations == nil {
				registrations = []monetizeapi.AgentIdentityRegistration{}
			}
			patch, err := json.Marshal(map[string]any{"status": map[string]any{"registrations": registrations}})
			if err != nil {
				return err
			}
			if err := kubectlRun(
				cfg,
				"patch", "agentidentities.obol.org", name,
				"-n", ns,
				"--subresource=status",
				"--type=merge",
				"-p", string(patch),
			); err != nil {
				return err
			}
			getUI(cmd).Successf("Removed agent %s on %s from AgentIdentity %s/%s.", existing, chain, ns, name)
			return nil
		},
	}
}

func sellIdentityImportCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "import",
		Usage: "Import an existing on-chain ERC-8004 agent into an AgentIdentity record",
		Description: `Verifies that the agent exists at --agent-id on --chain, that the
remote-signer wallet controls it, reads tokenURI(), and writes the
result to the AgentIdentity CR. Use this when migrating an agent that
was registered before AgentIdentity existed.`,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "chain", Usage: "Registration chain alias", Value: "base"},
			&cli.StringFlag{Name: "agent-id", Usage: "On-chain ERC-721 tokenId", Required: true},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			network, err := erc8004.ResolveNetwork(cmd.String("chain"))
			if err != nil {
				return err
			}
			agentIDStr := strings.TrimSpace(cmd.String("agent-id"))
			agentID, ok := new(big.Int).SetString(agentIDStr, 10)
			if !ok || agentID.Sign() <= 0 {
				return fmt.Errorf("--agent-id must be a positive decimal integer, got %q", agentIDStr)
			}

			signerNS, err := hermes.ResolveInstanceNamespace(cfg)
			if err != nil {
				return fmt.Errorf("resolve Hermes instance namespace: %w", err)
			}
			pf, err := startSignerPortForward(cfg, signerNS)
			if err != nil {
				return fmt.Errorf("port-forward to remote-signer: %w", err)
			}
			defer pf.Stop()

			signer := erc8004.NewRemoteSigner(fmt.Sprintf("http://localhost:%d", pf.localPort))
			signerAddr, err := signer.GetAddress(ctx)
			if err != nil {
				return err
			}
			u.Printf("    Signer:   %s", signerAddr.Hex())

			rpcBase := stack.LocalIngressURL(cfg) + "/rpc"
			client, err := erc8004.NewClientForNetwork(ctx, rpcBase, network)
			if err != nil {
				return fmt.Errorf("connect to %s via eRPC: %w", network.Name, err)
			}
			defer client.Close()

			owner, err := client.AgentWallet(ctx, agentID)
			if err != nil {
				return fmt.Errorf("agent %s not found on %s: %w", agentID, network.Name, err)
			}
			if owner == (common.Address{}) {
				return fmt.Errorf("agent %s on %s has zero owner", agentID, network.Name)
			}
			if owner != signerAddr {
				return fmt.Errorf("signer %s does not control agent %s (owner: %s)", signerAddr.Hex(), agentID, owner.Hex())
			}
			uri, err := client.TokenURI(ctx, agentID)
			if err != nil {
				return fmt.Errorf("read tokenURI(%s): %w", agentID, err)
			}

			ns := monetizeapi.AgentIdentityDefaultNamespace
			name := monetizeapi.AgentIdentityDefaultName
			rec, err := loadAgentIdentity(cfg, ns, name)
			if err != nil {
				return err
			}
			if rec == nil {
				rec = newAgentIdentityRecord(ns, name)
			}
			existing := monetizeapi.AgentIdentityAgentIDForChain(rec.Status, network.Name)
			if existing != "" && existing != agentID.String() {
				return fmt.Errorf("AgentIdentity %s/%s already has agent %s on %s; refusing to overwrite with %s", ns, name, existing, network.Name, agentID)
			}
			rec.Status = monetizeapi.UpsertAgentIdentityRegistration(rec.Status, network.Name, agentID.String())

			if err := applyAgentIdentity(cfg, rec); err != nil {
				return err
			}

			u.Successf("Imported agent %s into AgentIdentity %s/%s on %s.", agentID, ns, name, network.Name)
			u.Printf("  tokenURI: %s", uri)
			return nil
		},
	}
}

// Pure helpers exposed for testing the import command without a live
// kubectl/RPC; cmd/obol/sell_identity_test.go covers the persist path.

// verifyImportedIdentity checks the chain ownership invariant the import
// command relies on. Extracted so tests can exercise it without a live RPC.
func verifyImportedIdentity(owner, signer common.Address) error {
	if owner == (common.Address{}) {
		return fmt.Errorf("agent owner is zero")
	}
	if owner != signer {
		return fmt.Errorf("signer %s does not control agent (owner: %s)", signer.Hex(), owner.Hex())
	}
	return nil
}

// makeImportedIdentityRecord builds the record the import command would
// persist for the given inputs. Pure helper to make the wiring testable.
func makeImportedIdentityRecord(ns, name string, network erc8004.NetworkConfig, agentID *big.Int) *agentIdentityRecord {
	rec := newAgentIdentityRecord(ns, name)
	rec.Status = monetizeapi.UpsertAgentIdentityRegistration(rec.Status, network.Name, agentID.String())
	return rec
}
