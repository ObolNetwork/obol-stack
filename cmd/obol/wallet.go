package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/urfave/cli/v3"
)

const defaultWalletInstance = "obol-agent"

func walletCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "wallet",
		Usage: "Manage the Obol agent wallet",
		Commands: []*cli.Command{
			{
				Name:  "import",
				Usage: "Import a private key as the Obol agent wallet",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "private-key-file",
						Usage:    "Path to a file containing the 0x-prefixed private key",
						Required: true,
					},
					&cli.StringFlag{
						Name:  "instance",
						Usage: "OpenClaw instance to update",
						Value: defaultWalletInstance,
					},
					&cli.BoolFlag{
						Name:  "force",
						Usage: "Overwrite an existing wallet",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					instance := cmd.String("instance")
					if instance == "" {
						instance = defaultWalletInstance
					}
					return openclaw.ImportPrivateKeyWalletCmd(cfg, instance, openclaw.ImportPrivateKeyWalletOptions{
						PrivateKeyFile: cmd.String("private-key-file"),
						Force:          cmd.Bool("force"),
						ApplyCluster:   walletClusterAvailable(cfg),
					}, getUI(cmd))
				},
			},
		},
	}
}

func walletClusterAvailable(cfg *config.Config) bool {
	if cfg == nil || cfg.ConfigDir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(cfg.ConfigDir, "kubeconfig.yaml"))
	return err == nil
}
