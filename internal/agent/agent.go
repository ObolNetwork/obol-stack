package agent

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// DefaultInstanceID is the canonical OpenClaw instance that runs both
// user-facing inference and agent-mode monetize/heartbeat reconciliation.
const DefaultInstanceID = "obol-agent"

// Init removes the legacy monetize heartbeat from the default OpenClaw instance.
// ServiceOffer reconciliation is now handled by the dedicated serviceoffer-controller
// in the x402 namespace rather than inside the OpenClaw runtime.
func Init(cfg *config.Config, u *ui.UI) error {
	if err := removeHeartbeatFile(cfg, u); err != nil {
		return fmt.Errorf("failed to remove HEARTBEAT.md: %w", err)
	}

	u.Success("Controller-based monetization enabled")
	return nil
}

func removeHeartbeatFile(cfg *config.Config, u *ui.UI) error {
	namespace := fmt.Sprintf("openclaw-%s", DefaultInstanceID)
	heartbeatPath := filepath.Join(cfg.DataDir, namespace, "openclaw-data", ".openclaw", "workspace", "HEARTBEAT.md")
	if err := os.Remove(heartbeatPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	u.Successf("Legacy HEARTBEAT.md removed from %s", heartbeatPath)
	return nil
}
