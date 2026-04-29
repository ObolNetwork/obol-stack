package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// DefaultInstanceID is the canonical default-agent instance that runs both
// user-facing inference and agent-mode monetize/heartbeat reconciliation.
const DefaultInstanceID = agentruntime.DefaultInstanceID

// Init provisions the stack-managed Hermes default agent and removes the legacy
// monetize heartbeat. ServiceOffer reconciliation is now handled by the
// dedicated serviceoffer-controller in the x402 namespace.
func Init(cfg *config.Config, u *ui.UI) error {
	if err := hermes.SetupDefault(cfg, u); err != nil {
		return fmt.Errorf("failed to set up default Hermes agent: %w", err)
	}

	if err := removeHeartbeatFile(cfg, u); err != nil {
		return fmt.Errorf("failed to remove HEARTBEAT.md: %w", err)
	}

	u.Success("Controller-based monetization enabled")
	return nil
}

func removeHeartbeatFile(cfg *config.Config, u *ui.UI) error {
	targets := []struct {
		runtime agentruntime.Runtime
		path    string
	}{
		{
			runtime: agentruntime.Hermes,
			path:    filepath.Join(agentruntime.WorkspacePath(cfg, agentruntime.Hermes, DefaultInstanceID), "HEARTBEAT.md"),
		},
		{
			runtime: agentruntime.OpenClaw,
			path:    filepath.Join(agentruntime.WorkspacePath(cfg, agentruntime.OpenClaw, DefaultInstanceID), "HEARTBEAT.md"),
		},
	}

	for _, target := range targets {
		if err := os.Remove(target.path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if os.IsPermission(err) {
				if podErr := removeHeartbeatFileInPod(cfg, target.runtime); podErr != nil {
					u.Warnf("Could not remove legacy HEARTBEAT.md from %s host path: %v", target.runtime, err)
					continue
				}
				u.Successf("Legacy HEARTBEAT.md removed from %s runtime", agentruntime.Describe(target.runtime).DisplayName)
				continue
			}
			return err
		}
		u.Successf("Legacy HEARTBEAT.md removed from %s", target.path)
	}

	return nil
}

func removeHeartbeatFileInPod(cfg *config.Config, runtime agentruntime.Runtime) error {
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return err
	}

	desc := agentruntime.Describe(runtime)
	containerPath := filepath.Join("/data", desc.HomeDir, "workspace", "HEARTBEAT.md")
	cmd := exec.Command(
		filepath.Join(cfg.BinDir, "kubectl"),
		"exec",
		"-n", agentruntime.Namespace(runtime, DefaultInstanceID),
		"-c", desc.ServiceName,
		"deploy/"+desc.ServiceName,
		"--",
		"rm", "-f", containerPath,
	)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	return cmd.Run()
}
