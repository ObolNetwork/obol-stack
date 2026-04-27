package main

import (
	"context"
	"fmt"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/urfave/cli/v3"
)

func hermesCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:            "hermes",
		Aliases:         []string{"herme"},
		Usage:           "Run native Hermes CLI against a deployed Hermes instance",
		ArgsUsage:       "[--agent <instance-name>] [hermes args...]",
		Description:     "Passes arguments through to the native Hermes CLI in the selected deployed instance. Defaults to obol-agent when available.",
		SkipFlagParsing: true,
		HideHelp:        true,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			id, hermesArgs, err := hermes.ResolveCLIInvocation(cfg, cmd.Args().Slice())
			if err != nil {
				return fmt.Errorf("%w\n\nUsage:\n  obol hermes [--agent <instance-name>] [hermes args...]\n\nExamples:\n  obol hermes chat -q \"hello\"\n  obol hermes skills list\n  obol hermes --agent research config show", err)
			}

			return hermes.CLI(cfg, id, hermesArgs)
		},
	}
}
