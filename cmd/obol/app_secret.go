package main

import (
	"context"
	"errors"

	"github.com/ObolNetwork/obol-stack/internal/app"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/urfave/cli/v3"
)

const (
	appSecretSetCmd   = "set"
	appSecretListCmd  = "list"
	appIdentifierArgs = "[<app>/<id>]"
	appNamespaceFlag  = "namespace"
	appUpgradeCmd     = "upgrade"
)

// appSecretCommand is `obol app secret`: Secrets in an app's namespace,
// written from files read in-process and listed without values.
func appSecretCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "secret",
		Usage: "Manage Secrets in an app's namespace (values are never printed)",
		Commands: []*cli.Command{
			{
				Name:      appSecretSetCmd,
				Usage:     "Create or update a Secret from files",
				ArgsUsage: "<app>[/<id>] <secret-name>",
				Description: `Creates or updates a Secret in the app's namespace. obol reads the files
itself and hands the Secret to kubectl on stdin: values never appear in
arguments, output or logs. Keys this command set before are replaced; keys
added by other tools stay.

Example (dv-pod central monitoring, then set
centralMonitoring.existingSecret: obol-monitoring in values.yaml):
  obol app secret set dv-pod/hoodi obol-monitoring --from-file token=./monitoring-token`,
				Flags: []cli.Flag{
					&cli.StringSliceFlag{
						Name:  "from-env-file",
						Usage: "File of KEY=VALUE lines, one key each (repeatable)",
					},
					&cli.StringSliceFlag{
						Name:  "from-file",
						Usage: "key=path, or path (key = file name); the file content is the value (repeatable)",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.NArg() != 2 {
						return errors.New("usage: obol app secret set <app>[/<id>] <secret-name> --from-env-file <file> | --from-file key=path")
					}

					identifier, err := app.ResolveExplicitInstance(cfg, cmd.Args().Get(0))
					if err != nil {
						return err
					}

					return app.SetSecret(cfg, getUI(cmd), identifier, cmd.Args().Get(1), app.SecretSetOptions{
						EnvFiles: cmd.StringSlice("from-env-file"),
						Files:    cmd.StringSlice("from-file"),
					})
				},
			},
			{
				Name:      appSecretListCmd,
				Usage:     "List Secrets in an app's namespace (names and keys only)",
				ArgsUsage: appIdentifierArgs,
				Action: func(ctx context.Context, cmd *cli.Command) error {
					identifier, _, err := app.ResolveInstance(cfg, cmd.Args().Slice())
					if err != nil {
						return err
					}

					return app.ListSecrets(cfg, getUI(cmd), identifier)
				},
			},
		},
	}
}
