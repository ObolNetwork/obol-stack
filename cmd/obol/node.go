package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/stack"
	"github.com/urfave/cli/v3"
)

// nodeCommand groups commands for adding and inspecting worker nodes that join
// this stack's cluster. Multi-node only makes sense on the k3s backend — a
// k3d/Docker master's flannel overlay is not routable off-host — so the
// subcommands guard on the active backend.
func nodeCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "node",
		Usage: "Add and inspect worker nodes that join this stack's cluster (k3s backend)",
		Commands: []*cli.Command{
			nodeTokenCommand(cfg),
			nodeListCommand(cfg),
		},
	}
}

func nodeTokenCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "token",
		Usage: "Print the join command for adding a Linux worker node to this k3s cluster",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "server-url",
				Usage: "Override the K3S_URL agents dial (default https://<this-host-LAN-IP>:6443)",
			},
			&cli.BoolFlag{Name: "json", Usage: "Output machine-readable JSON"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			backend, err := stack.LoadBackend(cfg)
			if err != nil {
				return err
			}

			if backend.Name() != stack.BackendK3s {
				return fmt.Errorf(
					"obol node requires the k3s backend (current backend: %q)\n"+
						"A k3d/Docker master cannot accept remote node joins — its flannel overlay is not routable off-host.\n"+
						"Re-init on a Linux host with: obol stack init --backend k3s",
					backend.Name())
			}

			token, err := stack.ReadK3sNodeToken(cfg)
			if err != nil {
				return err
			}

			serverURL := stack.K3sServerURL(cmd.String("server-url"))
			version := stack.K3sBinaryVersion(cfg)
			joinCmd := stack.K3sAgentJoinCommand(serverURL, token, version)

			if cmd.Bool("json") {
				out, _ := json.MarshalIndent(map[string]string{
					"serverUrl":   serverURL,
					"token":       token,
					"version":     version,
					"joinCommand": joinCmd,
				}, "", "  ")
				fmt.Println(string(out))

				return nil
			}

			u.Info("Run this on a Linux worker node to join the cluster:")
			fmt.Printf("\n  %s\n\n", joinCmd)
			u.Detail("Server", serverURL)
			u.Dim("Multi-homed / Wi-Fi node? append:  --node-ip <node-LAN-IP> --flannel-iface <iface>")
			u.Dim("GPU node? label it at join:        --node-label obol.tech/accelerator=nvidia")

			return nil
		},
	}
}

func nodeListCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List cluster nodes with their accelerator labels",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := kubectl.EnsureCluster(cfg); err != nil {
				return err
			}

			bin, kc := kubectl.Paths(cfg)

			return kubectl.Run(bin, kc, "get", "nodes", "-o", "wide", "-L", "obol.tech/accelerator")
		},
	}
}
