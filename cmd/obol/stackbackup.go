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
				Name:  "file",
				Usage: "Archive path (default: obol-stack-backup-<id>-<timestamp>.tar.gz)",
			},
			&cli.StringFlag{
				Name:  "passphrase",
				Usage: "Wallet encryption passphrase (empty string = no encryption)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			_, err := stackbackup.Export(cfg, stackbackup.ExportOptions{
				Output:      cmd.String("file"),
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
		Description: `Restores host config and agent data (re-pointing absolute paths to this
host), brings the restored stack up with 'obol stack up' when its cluster is
not running, then re-applies etcd-resident resources (Agent CRs,
ServiceOffers, LiteLLM and eRPC config) and re-syncs agent instances.

On a clean host this is one command:
  obol stack import backup.tar.gz

Over a fresh 'obol stack init' (and 'up'), pass --force. A pre-import
cluster that holds no agents or offers is removed so the restored stack can
claim the ingress ports; one that does is never deleted.`,
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
				Usage: "Re-apply cluster resources only (host state already restored)",
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
			u := getUI(cmd)
			return stackbackup.Import(cfg, stackbackup.ImportOptions{
				Input:       cmd.Args().First(),
				Force:       cmd.Bool("force"),
				SkipCluster: cmd.Bool("skip-cluster"),
				ClusterOnly: cmd.Bool("cluster-only"),
				SkipSync:    cmd.Bool("skip-sync"),
				StackUp:     func() error { return runStackUp(ctx, cfg, u, false) },
			}, u)
		},
	}
}
