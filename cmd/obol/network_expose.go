package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/network"
	"github.com/urfave/cli/v3"
)

// networkExposeCommand builds `network expose`: a foreground, reconnecting
// forward of a local node's beacon / execution API to a local address.
func networkExposeCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "expose",
		Usage:     "Forward a local node's beacon and execution APIs to a local address (for Docker containers)",
		ArgsUsage: deploymentArgsUsage,
		Description: `Listens on --bind and forwards each connection to the node's beacon API
(svc/ethereum-beacon:5052) and/or execution JSON-RPC (svc/ethereum-execution:8545)
through kubectl port-forwards on the stack kubeconfig. It runs in the
foreground until Ctrl-C, checks the APIs every 15s and reconnects when
a pod restarts. The engine API (8551) is never exposed. With neither
--beacon nor --execution, both are forwarded.

Reaching it from Docker containers (a charon, a test validator client):
  Linux:           bind the docker0 address and use it from the container,
                   e.g. --bind 172.17.0.1 and http://172.17.0.1:5052
  Docker Desktop:  keep the default 127.0.0.1; containers use
                   http://host.docker.internal:5052

--bind takes an IP address. Loopback and private (RFC 1918, IPv6 ULA)
addresses are accepted. Anything else, including 0.0.0.0, can expose the
unauthenticated node APIs beyond this machine and needs --allow-public.

Examples:
  obol network expose ethereum/hoodi --beacon
  obol network expose ethereum/hoodi --beacon --bind 172.17.0.1 --beacon-port 15052`,
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "beacon", Usage: "Forward the beacon node API"},
			&cli.BoolFlag{Name: "execution", Usage: "Forward the execution client JSON-RPC API"},
			&cli.StringFlag{Name: "bind", Value: "127.0.0.1", Usage: "Local IP address to listen on (loopback or private unless --allow-public)"},
			&cli.IntFlag{Name: "beacon-port", Value: 5052, Usage: "Local port for the beacon API"},
			&cli.IntFlag{Name: "execution-port", Value: 8545, Usage: "Local port for the execution JSON-RPC API"},
			&cli.BoolFlag{Name: "allow-public", Usage: "Allow a --bind address that is neither loopback nor private, such as 0.0.0.0 (exposes the unauthenticated APIs)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			identifier, _, err := network.ResolveInstance(cfg, cmd.Args().Slice())
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()

			return network.Expose(ctx, cfg, getUI(cmd), network.ExposeOptions{
				Identifier:    identifier,
				Beacon:        cmd.Bool("beacon"),
				Execution:     cmd.Bool("execution"),
				Bind:          cmd.String("bind"),
				BeaconPort:    cmd.Int("beacon-port"),
				ExecutionPort: cmd.Int("execution-port"),
				AllowPublic:   cmd.Bool("allow-public"),
			})
		},
	}
}
