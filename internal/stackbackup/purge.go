package stackbackup

import (
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// PromptExportBeforePurge offers a full stack export before a destructive
// purge and returns true when one was written (callers can then skip
// narrower wallet-only prompts). Non-interactive shells get a warning but
// are never blocked, mirroring openclaw.PromptBackupBeforePurge.
//
// Offered for BOTH purge modes: a plain purge keeps the data dir but deletes
// the config dir, which holds every agent's keystore password
// (values-remote-signer.yaml) — the surviving keystores can't be unlocked
// without it. Either mode deletes the cluster, and with it the sub-agent
// wallets that exist only as in-cluster Secrets.
func PromptExportBeforePurge(cfg *config.Config, u *ui.UI, force bool) bool {
	hermesWallets := hermes.FindInstancesWithWallets(cfg)
	openclawWallets := openclaw.FindInstancesWithWallets(cfg)
	dataNamespaces := selectDataNamespaces(cfg.DataDir)
	if len(hermesWallets)+len(openclawWallets)+len(dataNamespaces) == 0 {
		return false
	}

	if force {
		u.Warn("Purging will destroy agent data (memory, sessions, wallets) and stack config.")
	} else {
		u.Warn("Purging deletes stack config, including the keystore passwords that unlock agent wallets.")
		u.Warnf("Keystores left in %s cannot be used without a backup; sub-agent wallets are destroyed with the cluster.", cfg.DataDir)
	}
	if kubectl.EnsureCluster(cfg) != nil || verifyClusterIdentity(cfg) != nil {
		u.Warn("Cluster is not running: a backup now cannot capture sub-agent wallets (obol agent new --create-wallet). Run 'obol stack up' first if you have any.")
	}
	if !u.IsTTY() {
		u.Warn("Run 'obol stack export' first to save a full backup")
		return false
	}
	u.Blank()
	if !u.Confirm("Create a full stack backup (agents, wallets, config) before purging?", true) {
		return false
	}

	path, err := Export(cfg, ExportOptions{}, u)
	if err != nil {
		u.Warnf("Stack export failed: %v", err)
		return false
	}
	u.Printf("Restore later with: obol stack import %s", path)
	u.Blank()
	return true
}
