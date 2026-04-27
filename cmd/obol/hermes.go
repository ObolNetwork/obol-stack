package main

import (
	"context"
	"errors"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/urfave/cli/v3"
)

func hermesCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:    "hermes",
		Aliases: []string{"herme"},
		Usage:   "Manage Hermes agent instances",
		Commands: []*cli.Command{
			{
				Name:  "onboard",
				Usage: "Create and deploy a Hermes instance",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "id",
						Usage: "Instance ID (defaults to generated petname)",
					},
					&cli.BoolFlag{
						Name:    "force",
						Aliases: []string{"f"},
						Usage:   "Overwrite existing instance",
					},
					&cli.BoolFlag{
						Name:  "no-sync",
						Usage: "Only scaffold config, don't deploy to cluster",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return hermes.Onboard(cfg, hermes.OnboardOptions{
						ID:    cmd.String("id"),
						Force: cmd.Bool("force"),
						Sync:  !cmd.Bool("no-sync"),
					}, getUI(cmd))
				},
			},
			{
				Name:      "sync",
				Usage:     "Deploy or update a Hermes instance",
				ArgsUsage: "[instance-name]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id, _, err := hermes.ResolveInstance(cfg, cmd.Args().Slice())
					if err != nil {
						return err
					}
					return hermes.Sync(cfg, id, getUI(cmd))
				},
			},
			{
				Name:      "token",
				Usage:     "Retrieve or regenerate the Hermes API server token",
				ArgsUsage: "[instance-name]",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "regenerate",
						Usage: "Delete and regenerate the API server token (restarts the instance)",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id, _, err := hermes.ResolveInstance(cfg, cmd.Args().Slice())
					if err != nil {
						return err
					}

					u := getUI(cmd)
					if cmd.Bool("regenerate") {
						newToken, err := hermes.RegenerateToken(cfg, id, u)
						if err != nil {
							return err
						}
						u.Print(newToken)
						return nil
					}

					return hermes.Token(cfg, id, u)
				},
			},
			{
				Name:  "list",
				Usage: "List Hermes instances",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return hermes.List(cfg, getUI(cmd))
				},
			},
			{
				Name:      "delete",
				Usage:     "Remove a Hermes instance and its cluster resources",
				ArgsUsage: "[instance-name]",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "force",
						Aliases: []string{"f"},
						Usage:   "Skip confirmation prompt",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id, _, err := hermes.ResolveInstance(cfg, cmd.Args().Slice())
					if err != nil {
						return err
					}
					return hermes.Delete(cfg, id, cmd.Bool("force"), getUI(cmd))
				},
			},
			{
				Name:      "setup",
				Usage:     "Re-render Hermes config from the current LiteLLM inventory",
				ArgsUsage: "[instance-name]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id, _, err := hermes.ResolveInstance(cfg, cmd.Args().Slice())
					if err != nil {
						return err
					}
					return hermes.Setup(cfg, id, hermes.SetupOptions{}, getUI(cmd))
				},
			},
			{
				Name:      "dashboard",
				Usage:     "Pending product decision for Hermes-native dashboard behavior",
				ArgsUsage: "[instance-name]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return errors.New("Hermes dashboard semantics diverge from OpenClaw; choose a native Hermes dashboard flow or an Obol wrapper before enabling this command")
				},
			},
			{
				Name:  "wallet",
				Usage: "Inspect Hermes instance wallets",
				Commands: []*cli.Command{
					{
						Name:      "address",
						Usage:     "Show the wallet address for a Hermes instance",
						ArgsUsage: "[instance-name]",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							args := cmd.Args().Slice()

							if len(args) == 0 {
								addr, err := hermes.ResolveWalletAddress(cfg)
								if err != nil {
									return err
								}
								getUI(cmd).Print(addr)
								return nil
							}

							id, _, err := hermes.ResolveInstance(cfg, args)
							if err != nil {
								return err
							}

							walletInfo, err := hermes.ReadWalletMetadata(hermes.DeploymentPath(cfg, id))
							if err != nil {
								return err
							}
							getUI(cmd).Print(walletInfo.Address)
							return nil
						},
					},
					{
						Name:      "list",
						Usage:     "List wallets for Hermes instances",
						ArgsUsage: "[instance-name]",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							args := cmd.Args().Slice()

							var id string
							if len(args) > 0 {
								var err error
								id, _, err = hermes.ResolveInstance(cfg, args)
								if err != nil {
									return err
								}
							}

							return hermes.ListWallets(cfg, id, getUI(cmd))
						},
					},
				},
			},
			{
				Name:            "skills",
				Usage:           "Run native Hermes skills commands against a deployed instance",
				ArgsUsage:       "[instance-name] [-- <hermes skills args...>]",
				SkipFlagParsing: true,
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id, remaining, err := hermes.ResolveInstance(cfg, cmd.Args().Slice())
					if err != nil {
						return err
					}

					return hermes.Skills(cfg, id, rawArgsAfterSeparator(remaining))
				},
			},
		},
	}
}

func rawArgsAfterSeparator(args []string) []string {
	for i, arg := range args {
		if arg == "--" {
			return args[i+1:]
		}
	}
	return args
}
