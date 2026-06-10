package main

import (
	"context"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/stackbackup"
	"github.com/urfave/cli/v3"
)

// stackExportCommand backs the `obol stack export` subcommand.
func stackExportCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "export",
		Usage: "Export the full stack (agents, wallets, config, offers) to an archive",
		Description: `Creates a tar.gz capturing everything a fresh 'obol stack import' needs:
host config (helmfiles, sell offer descriptors), agent data dirs (memory,
sessions, remote-signer keystores), encrypted wallet backups, and the
cluster resources that only live in etcd (Agent CRs, ServiceOffers,
LiteLLM/eRPC configuration).

The archive contains keystore passwords and provider API keys — store it
like a secret. Network chain data is excluded (re-syncable).`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "output",
				Usage: "Archive path (default: obol-stack-backup-<id>-<timestamp>.tar.gz)",
			},
			&cli.StringFlag{
				Name:  "passphrase",
				Usage: "Wallet encryption passphrase (empty string = no encryption)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			_, err := stackbackup.Export(cfg, stackbackup.ExportOptions{
				Output:      cmd.String("output"),
				Passphrase:  cmd.String("passphrase"),
				HasPassFlag: cmd.IsSet("passphrase"),
			}, getUI(cmd))
			return err
		},
	}
}

// stackImportCommand backs the `obol stack import` subcommand.
func stackImportCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "import",
		Usage:     "Restore a stack from an 'obol stack export' archive",
		ArgsUsage: "<archive.tar.gz>",
		Description: `Restores host config and agent data first, so the next 'obol stack up'
mounts the right agent brains and wallet keystores. When the cluster is
already running, etcd-resident resources (Agent CRs, ServiceOffers, LiteLLM
and eRPC config) are re-applied and agent instances re-synced.

Typical flow on a clean host:
  obol stack import backup.tar.gz   # restores host state
  obol stack up                     # cluster comes up with restored data
  obol stack import backup.tar.gz --cluster-only`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "force",
				Usage: "Overwrite an existing stack config",
			},
			&cli.BoolFlag{
				Name:  "skip-cluster",
				Usage: "Restore host state only; never touch the cluster",
			},
			&cli.BoolFlag{
				Name:  "cluster-only",
				Usage: "Re-apply cluster resources only (after 'obol stack up')",
			},
			&cli.BoolFlag{
				Name:  "skip-sync",
				Usage: "Do not re-sync agent instances after applying cluster resources",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Args().Len() != 1 {
				return cli.Exit("usage: obol stack import <archive.tar.gz>", 1)
			}
			return stackbackup.Import(cfg, stackbackup.ImportOptions{
				Input:       cmd.Args().First(),
				Force:       cmd.Bool("force"),
				SkipCluster: cmd.Bool("skip-cluster"),
				ClusterOnly: cmd.Bool("cluster-only"),
				SkipSync:    cmd.Bool("skip-sync"),
			}, getUI(cmd))
		},
	}
}
