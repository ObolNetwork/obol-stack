package agent

import (
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
)

// Init sets up an Obol Agent by running the OpenClaw onboard flow.
func Init(cfg *config.Config) error {
	return openclaw.Onboard(cfg, openclaw.OnboardOptions{
		Sync:        true,
		Interactive: true,
	})
}
