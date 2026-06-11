package stackbackup

import (
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// PromptExportBeforePurge offers a full stack export before a destructive
// purge and returns true when one was written (callers can then skip
// narrower wallet-only prompts). Non-interactive shells get a warning but
// are never blocked, mirroring openclaw.PromptBackupBeforePurge.
//
// This closes the gap that cost real agent state: the pre-existing purge
// prompt covered OpenClaw wallets only — Hermes wallets and every agent's
// brain (sessions, memory, workspace) were destroyed silently.
func PromptExportBeforePurge(cfg *config.Config, u *ui.UI) bool {
	hermesWallets := hermes.FindInstancesWithWallets(cfg)
	openclawWallets := openclaw.FindInstancesWithWallets(cfg)
	dataNamespaces := selectDataNamespaces(cfg.DataDir)
	if len(hermesWallets)+len(openclawWallets)+len(dataNamespaces) == 0 {
		return false
	}

	u.Warn("Purging will destroy agent data (memory, sessions, wallets) and stack config.")
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
