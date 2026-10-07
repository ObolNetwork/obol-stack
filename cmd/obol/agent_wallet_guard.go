package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/agentcrd"
	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
)

// walletDeleteGuard refuses to delete an agent that holds a wallet unless the
// operator passed --delete-wallet. Deleting the agent's namespace deletes its
// keystore volume/Secret (local-path reclaimPolicy Delete → rm -rf on the
// host), so funds at that address become unreachable without a backup.
func walletDeleteGuard(agent, address, backupCmd string, deleteWallet bool) error {
	if address == "" || deleteWallet {
		return nil
	}

	return fmt.Errorf("agent %s holds wallet %s; deleting it destroys the keystore and any funds become unreachable.\n"+
		"Back it up first:  %s\n"+
		"then re-run with --delete-wallet", agent, address, backupCmd)
}

// legacyAgentWalletAddress returns the public address of a host-rendered
// agent's keystore (Hermes/OpenClaw instance), "" if it has none. A keystore
// file whose address can't be read still counts as a wallet.
func legacyAgentWalletAddress(cfg *config.Config, runtime agentruntime.Runtime, id string) string {
	dir := agentruntime.KeystoreVolumePath(cfg, runtime, id)

	var found bool

	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		found = true

		return nil
	})
	if !found {
		return ""
	}

	addr := firstKeystoreAddress(dir)
	if addr == "" {
		return "(address unreadable)"
	}

	return addr
}

// firstKeystoreAddress reads the public "address" field of the first V3
// keystore under dir. Only the address is decoded — never key material.
func firstKeystoreAddress(dir string) string {
	var addr string

	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || addr != "" || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		var ks struct {
			Address string `json:"address"`
		}
		if json.Unmarshal(data, &ks) == nil && ks.Address != "" {
			addr = ks.Address
			if !strings.HasPrefix(addr, "0x") {
				addr = "0x" + addr
			}
		}

		return nil
	})

	return addr
}

// crdAgentWalletAddress returns the wallet address of a CRD sub-agent, or
// "(pending)" when it asked for a wallet that hasn't been reported yet.
func crdAgentWalletAddress(cfg *config.Config, name string) string {
	bin, kc := kubectl.Paths(cfg)

	out, err := kubectl.Output(bin, kc, "get", "agents.obol.org", name, "-n", agentcrd.Namespace(name),
		"-o", "jsonpath={.status.walletAddress}|{.spec.wallet.create}")
	if err != nil {
		return ""
	}

	addr, create, _ := strings.Cut(strings.TrimSpace(out), "|")
	switch {
	case addr != "":
		return addr
	case create == "true":
		return "(pending)"
	default:
		return ""
	}
}
