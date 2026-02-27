package agent

import (
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// Init sets up an Obol Agent by running the OpenClaw onboard flow.
func Init(cfg *config.Config, u *ui.UI) error {
	return openclaw.Onboard(cfg, openclaw.OnboardOptions{
		Sync:        true,
		Interactive: true,
	}, u)
}
