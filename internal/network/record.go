package network

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"gopkg.in/yaml.v3"
)

// Record-on-write for remote RPC upstreams. `obol network add/remove`
// mutate only the eRPC ConfigMap, which is lost on cluster recreation
// (plans/stack-export-import.md, Phase 2). AddPublicRPCs / AddCustomRPC /
// RemovePublicRPCs update this host-side record after a successful
// ConfigMap write; ReconcileRecordedRPCs replays it after `obol stack up`
// by re-invoking the same (idempotent) add functions.
//
// Local node upstreams (local-<network>-<id>) are intentionally NOT
// recorded — `obol network sync` re-registers them from the deployment's
// values.yaml, which is already on disk.
//
// Custom endpoints may carry paid-provider API keys in the URL; the record
// is 0600 in ConfigDir, the same convention as values-remote-signer.yaml.
// Never log the endpoint URL un-redacted (see redactRPCURL).

const rpcRecordVersion = 1

// RecordedRPCs is the host-side record of operator-added remote upstreams.
type RecordedRPCs struct {
	Version int                       `yaml:"version"`
	Chains  map[string]*RecordedChain `yaml:"chains"` // key: decimal chain ID
}

// RecordedChain holds the recorded upstream intent for one chain.
type RecordedChain struct {
	Name      string             `yaml:"name"`
	Chainlist *RecordedChainlist `yaml:"chainlist,omitempty"`
	Custom    *RecordedCustom    `yaml:"custom,omitempty"`
}

// RecordedChainlist snapshots the ChainList endpoints selected at add time,
// so replay is deterministic and offline (no re-fetch of chainlist.org).
type RecordedChainlist struct {
	ReadOnly  bool          `yaml:"readOnly"`
	Endpoints []RPCEndpoint `yaml:"endpoints"`
}

// RecordedCustom is a `network add --endpoint` upstream.
type RecordedCustom struct {
	Endpoint string `yaml:"endpoint"`
	ReadOnly bool   `yaml:"readOnly"`
}

func recordedRPCPath(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "rpc", "recorded-upstreams.yaml")
}

// ReconcileRecordedRPCs replays recorded remote upstreams into a (possibly
// fresh) cluster. Called after `obol stack up`. Best-effort per chain.
func ReconcileRecordedRPCs(cfg *config.Config, u *ui.UI) {
	rec, err := readRecordedRPCs(cfg)
	if err != nil {
		u.Warnf("Could not read recorded RPC upstreams: %v", err)
		return
	}
	if rec == nil || len(rec.Chains) == 0 {
		return
	}

	// Stable replay order for predictable logs.
	ids := make([]string, 0, len(rec.Chains))
	for id := range rec.Chains {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	u.Infof("Restoring %d recorded RPC chain(s) into eRPC...", len(ids))
	for _, id := range ids {
		chain := rec.Chains[id]
		chainID, err := strconv.Atoi(id)
		if err != nil {
			u.Warnf("Skipping recorded RPC chain %q: bad chain id", id)
			continue
		}
		if chain.Chainlist != nil && len(chain.Chainlist.Endpoints) > 0 {
			if err := AddPublicRPCs(cfg, chainID, chain.Name, chain.Chainlist.Endpoints, chain.Chainlist.ReadOnly); err != nil {
				u.Warnf("Could not restore ChainList RPCs for %s (%d): %v", chain.Name, chainID, err)
			} else {
				u.Successf("Restored %d ChainList RPC(s) for %s", len(chain.Chainlist.Endpoints), chain.Name)
			}
		}
		if chain.Custom != nil {
			if err := AddCustomRPC(cfg, chainID, chain.Name, chain.Custom.Endpoint, chain.Custom.ReadOnly); err != nil {
				u.Warnf("Could not restore custom RPC for %s (%d): %v", chain.Name, chainID, err)
			} else {
				u.Successf("Restored custom RPC for %s", chain.Name)
			}
		}
	}
}

// recordChainlistRPCs persists the ChainList selection for a chain.
func recordChainlistRPCs(cfg *config.Config, chainID int, chainName string, endpoints []RPCEndpoint, readOnly bool) error {
	return updateRecordedRPCs(cfg, chainID, chainName, func(c *RecordedChain) {
		c.Chainlist = &RecordedChainlist{ReadOnly: readOnly, Endpoints: endpoints}
	})
}

// recordCustomRPC persists a custom endpoint for a chain.
func recordCustomRPC(cfg *config.Config, chainID int, chainName, endpoint string, readOnly bool) error {
	return updateRecordedRPCs(cfg, chainID, chainName, func(c *RecordedChain) {
		c.Custom = &RecordedCustom{Endpoint: endpoint, ReadOnly: readOnly}
	})
}

// unrecordChainlistRPCs drops the ChainList record for a chain, mirroring
// RemovePublicRPCs (which leaves custom and local upstreams alone).
func unrecordChainlistRPCs(cfg *config.Config, chainID int) error {
	rec, err := readRecordedRPCs(cfg)
	if err != nil || rec == nil {
		return err
	}
	key := strconv.Itoa(chainID)
	chain, ok := rec.Chains[key]
	if !ok {
		return nil
	}
	chain.Chainlist = nil
	if chain.Custom == nil {
		delete(rec.Chains, key)
	}
	return writeRecordedRPCs(cfg, rec)
}

func updateRecordedRPCs(cfg *config.Config, chainID int, chainName string, mutate func(*RecordedChain)) error {
	rec, err := readRecordedRPCs(cfg)
	if err != nil {
		return err
	}
	if rec == nil {
		rec = &RecordedRPCs{Version: rpcRecordVersion}
	}
	if rec.Chains == nil {
		rec.Chains = map[string]*RecordedChain{}
	}
	key := strconv.Itoa(chainID)
	chain := rec.Chains[key]
	if chain == nil {
		chain = &RecordedChain{}
		rec.Chains[key] = chain
	}
	if chainName != "" {
		chain.Name = chainName
	}
	mutate(chain)
	return writeRecordedRPCs(cfg, rec)
}

func readRecordedRPCs(cfg *config.Config) (*RecordedRPCs, error) {
	data, err := os.ReadFile(recordedRPCPath(cfg))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec RecordedRPCs
	if err := yaml.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parse %s: %w", recordedRPCPath(cfg), err)
	}
	if rec.Version != rpcRecordVersion {
		return nil, fmt.Errorf("unsupported recorded-upstreams version %d", rec.Version)
	}
	return &rec, nil
}

func writeRecordedRPCs(cfg *config.Config, rec *RecordedRPCs) error {
	path := recordedRPCPath(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(rec)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
