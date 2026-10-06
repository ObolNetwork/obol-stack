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
		Name:      "hermes",
		Usage:     "Run native Hermes CLI against a deployed Hermes instance",
		ArgsUsage: "[--agent <instance-name>] [hermes args...]",
		Description: `Forwards arguments to the native Hermes CLI inside the selected agent's pod
(defaults to obol-agent when present). Needs a running stack ('obol stack up').

To see the native Hermes commands and flags (stack must be running):
  obol hermes -- --help
  obol hermes <command> --help

Examples:
  obol hermes chat -q "hello"
  obol hermes skills list
  obol hermes --agent research config show`,
		SkipFlagParsing: true,
		HideHelp:        true,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// `obol hermes --help` describes this wrapper without needing a
			// cluster; `obol hermes -- --help` reaches the native help.
			if isHelpRequest(cmd.Args().Slice()) {
				return showPassthroughHelp(cmd)
			}

			id, hermesArgs, err := hermes.ResolveCLIInvocation(cfg, cmd.Args().Slice())
			if err != nil {
				return fmt.Errorf("%w\n\nUsage:\n  obol hermes [--agent <instance-name>] [hermes args...]\n\nExamples:\n  obol hermes chat -q \"hello\"\n  obol hermes skills list\n  obol hermes --agent research config show", err)
			}

			return hermes.CLI(cfg, id, hermesArgs)
		},
	}
}
