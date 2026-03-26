package main

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/network"
	"github.com/urfave/cli/v3"
)

// networkCommand returns the network management command group with dynamic subcommands
func networkCommand(cfg *config.Config) *cli.Command {
	// Build install subcommands dynamically from embedded networks
	installSubcommands := buildNetworkInstallCommands(cfg)

	return &cli.Command{
		Name:  "network",
		Usage: "Manage blockchain networks (local nodes + remote RPCs)",
		Commands: []*cli.Command{
			networkListCommand(cfg),
			{
				Name:     "install",
				Usage:    "Install and deploy a local blockchain node",
				Commands: installSubcommands,
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return cli.ShowSubcommandHelp(cmd)
				},
			},
			{
				Name:      "sync",
				Usage:     "Deploy or update a network deployment to the cluster",
				ArgsUsage: "[<network>/<id>]",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "all",
						Aliases: []string{"a"},
						Usage:   "Sync all installed network deployments",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					u := getUI(cmd)
					if cmd.Bool("all") {
						return network.SyncAll(cfg, u)
					}
					identifier, _, err := network.ResolveInstance(cfg, cmd.Args().Slice())
					if err != nil {
						return fmt.Errorf("%w\n\nOr use --all to sync all deployments", err)
					}
					return network.Sync(cfg, u, identifier)
				},
			},
			{
				Name:      "delete",
				Usage:     "Remove network deployment and clean up cluster resources",
				ArgsUsage: "[<network>/<id>]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					identifier, _, err := network.ResolveInstance(cfg, cmd.Args().Slice())
					if err != nil {
						return err
					}
					return network.Delete(cfg, getUI(cmd), identifier)
				},
			},
			networkAddCommand(cfg),
			networkRemoveCommand(cfg),
			networkStatusCommand(cfg),
		},
	}
}

// buildNetworkInstallCommands dynamically creates install subcommands for each embedded network
func buildNetworkInstallCommands(cfg *config.Config) []*cli.Command {
	// Get all embedded networks
	networks, err := embed.GetAvailableNetworks()
	if err != nil {
		return nil
	}

	var commands []*cli.Command
	for _, networkName := range networks {
		// Parse the embedded values template to get fields
		fields, err := network.ParseTemplateFields(networkName)
		if err != nil {
			// Skip networks we can't parse
			continue
		}

		// Build flags from template fields
		flags := []cli.Flag{
			// id flag is always present (special case - not parsed from template)
			&cli.StringFlag{
				Name:     "id",
				Usage:    fmt.Sprintf("Deployment identifier for namespace (e.g., 'my-node' becomes '%s-my-node', defaults to generated petname)", networkName),
				Required: false,
			},
			// force flag to allow overwriting existing deployments
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Usage:   "Overwrite existing deployment configuration if it already exists",
			},
		}

		// Add flags from parsed template fields
		for _, field := range fields {
			// Build usage string
			usage := field.Description
			if usage == "" {
				usage = fmt.Sprintf("Override %s", field.Name)
			}

			// Mark as required if no default value
			if field.Required {
				usage = "[REQUIRED] " + usage
			}

			// Add enum options if available
			if len(field.EnumValues) > 0 {
				usage += fmt.Sprintf(" [options: %s]", strings.Join(field.EnumValues, ", "))
			}

			// Add default value
			if field.DefaultValue != "" {
				usage += fmt.Sprintf(" (default: %s)", field.DefaultValue)
			}

			flags = append(flags, &cli.StringFlag{
				Name:     field.FlagName,
				Usage:    usage,
				Required: field.Required,
			})
		}

		// Create the network-specific install command
		netName := networkName // Capture for closure
		netFields := fields    // Capture for validation
		commands = append(commands, &cli.Command{
			Name:  netName,
			Usage: fmt.Sprintf("Install %s network", netName),
			Flags: flags,
			Action: func(ctx context.Context, cmd *cli.Command) error {
				// Collect and validate flag values
				overrides := make(map[string]string)

				// Collect id flag (special case - not in parsed fields)
				if idValue := cmd.String("id"); idValue != "" {
					overrides["id"] = idValue
				}

				// Collect parsed template fields
				for _, field := range netFields {
					value := cmd.String(field.FlagName)
					if value != "" {
						// Validate enum constraint if defined
						if len(field.EnumValues) > 0 {
							valid := slices.Contains(field.EnumValues, value)
							if !valid {
								return fmt.Errorf("invalid value '%s' for --%s. Valid options: %s",
									value, field.FlagName, strings.Join(field.EnumValues, ", "))
							}
						}
						overrides[field.FlagName] = value
					}
				}

				// Get force flag
				force := cmd.Bool("force")

				return network.Install(cfg, getUI(cmd), netName, overrides, force)
			},
		})
	}

	return commands
}

// ---------------------------------------------------------------------------
// network list — unified local nodes + remote RPCs
// ---------------------------------------------------------------------------

// networkListResult is the JSON-serialisable result for `network list`.
type networkListResult struct {
	LocalNodes []string              `json:"local_nodes"`
	RPCs       []networkListRPCEntry `json:"rpcs"`
}

type networkListRPCEntry struct {
	Alias     string `json:"alias"`
	ChainID   int    `json:"chain_id"`
	Upstreams int    `json:"upstreams"`
}

func networkListCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List all networks (local nodes + remote RPCs)",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			if u.IsJSON() {
				result := networkListResult{}

				// Collect local nodes (best-effort).
				if nodes, err := embed.GetAvailableNetworks(); err == nil {
					result.LocalNodes = nodes
				}

				// Collect remote RPCs.
				if rpcNetworks, err := network.ListRPCNetworks(cfg); err == nil {
					for _, net := range rpcNetworks {
						alias := net.Alias
						if alias == "" {
							alias = fmt.Sprintf("chain-%d", net.ChainID)
						}
						result.RPCs = append(result.RPCs, networkListRPCEntry{
							Alias:     alias,
							ChainID:   net.ChainID,
							Upstreams: len(net.Upstreams),
						})
					}
				}

				return u.JSON(result)
			}

			// Show local node deployments.
			fmt.Println("Local Nodes:")
			if err := network.List(cfg, u); err != nil {
				fmt.Printf("  (unable to list: %v)\n", err)
			}

			fmt.Println()

			// Show remote RPC networks from eRPC config.
			fmt.Println("Remote RPCs:")
			rpcNetworks, err := network.ListRPCNetworks(cfg)
			if err != nil {
				fmt.Printf("  (unable to read eRPC config: %v)\n", err)
				return nil
			}
			if len(rpcNetworks) == 0 {
				fmt.Println("  (none configured)")
			} else {
				for _, net := range rpcNetworks {
					alias := net.Alias
					if alias == "" {
						alias = fmt.Sprintf("chain-%d", net.ChainID)
					}
					fmt.Printf("  %-20s chain=%-8d %d upstream(s)\n", alias, net.ChainID, len(net.Upstreams))
				}
			}
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// network add — remote RPCs (was: rpc add)
// ---------------------------------------------------------------------------

func networkAddCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "add",
		Usage:     "Add remote RPC endpoints for a chain to the eRPC gateway",
		ArgsUsage: "<chain-name-or-id>",
		Description: `Adds RPC endpoints for the specified chain to the eRPC gateway.
By default, remote upstreams are read-only (write methods blocked).

Without --endpoint, fetches free public RPCs from ChainList.
With --endpoint, adds a custom RPC endpoint directly.

Examples:
  obol network add base
  obol network add base-sepolia --endpoint http://host.k3d.internal:8545
  obol network add base --allow-writes`,
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "count",
				Usage: "Maximum number of RPCs to add (ChainList mode only)",
				Value: 3,
			},
			&cli.StringFlag{
				Name:  "endpoint",
				Usage: "Custom RPC endpoint URL (skips ChainList, adds directly)",
			},
			&cli.BoolFlag{
				Name:  "allow-writes",
				Usage: "Allow write methods (eth_sendRawTransaction, eth_sendTransaction)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() == 0 {
				return fmt.Errorf("chain name or ID required\n\nExamples:\n  obol network add base\n  obol network add base-sepolia --endpoint http://host.k3d.internal:8545")
			}

			chainArg := cmd.Args().First()
			chainID, chainName, err := network.ResolveChainID(chainArg)
			if err != nil {
				return err
			}

			readOnly := !cmd.Bool("allow-writes")

			// Custom endpoint mode.
			if endpoint := cmd.String("endpoint"); endpoint != "" {
				fmt.Printf("Adding custom RPC for %s (chain ID: %d): %s\n", chainName, chainID, endpoint)
				if readOnly {
					fmt.Printf("  Write methods blocked (use --allow-writes to enable)\n")
				}
				if err := network.AddCustomRPC(cfg, chainID, chainName, endpoint, readOnly); err != nil {
					return fmt.Errorf("failed to add custom RPC: %w", err)
				}
				fmt.Printf("Added custom RPC for %s (chain ID: %d) to eRPC\n", chainName, chainID)
				return nil
			}

			// ChainList mode.
			maxCount := int(cmd.Int("count"))
			if maxCount <= 0 {
				maxCount = 3
			}

			fmt.Printf("Fetching public RPCs for %s (chain ID: %d) from ChainList...\n", chainName, chainID)

			endpoints, displayName, err := network.FetchChainListRPCs(chainID, nil)
			if err != nil {
				return fmt.Errorf("failed to fetch RPCs: %w", err)
			}

			if len(endpoints) == 0 {
				return fmt.Errorf("no free public RPCs found for chain ID %d", chainID)
			}

			if len(endpoints) > maxCount {
				endpoints = endpoints[:maxCount]
			}

			if displayName != "" {
				chainName = displayName
			}

			fmt.Printf("Found %d quality RPCs for %s:\n", len(endpoints), chainName)
			for i, ep := range endpoints {
				fmt.Printf("  %d. %s (tracking: %s)\n", i+1, ep.URL, ep.Tracking)
			}

			if readOnly {
				fmt.Printf("\nWrite methods blocked (use --allow-writes to enable)\n")
			}

			fmt.Printf("Adding to eRPC gateway...\n")
			if err := network.AddPublicRPCs(cfg, chainID, chainName, endpoints, readOnly); err != nil {
				return fmt.Errorf("failed to add RPCs: %w", err)
			}

			fmt.Printf("Added %d RPCs for %s (chain ID: %d) to eRPC\n", len(endpoints), chainName, chainID)
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// network remove — remote RPCs (was: rpc remove)
// ---------------------------------------------------------------------------

func networkRemoveCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "remove",
		Usage:     "Remove remote RPC endpoints for a chain from eRPC",
		ArgsUsage: "<chain-name-or-id>",
		Description: `Removes all ChainList-sourced RPC endpoints for the specified chain.
Does not affect local node upstreams or manually configured upstreams.

Examples:
  obol network remove base
  obol network remove 8453`,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() == 0 {
				return fmt.Errorf("chain name or ID required\n\nExamples:\n  obol network remove base\n  obol network remove 8453")
			}

			chainArg := cmd.Args().First()
			chainID, chainName, err := network.ResolveChainID(chainArg)
			if err != nil {
				return err
			}

			fmt.Printf("Removing ChainList RPCs for %s (chain ID: %d)...\n", chainName, chainID)

			if err := network.RemovePublicRPCs(cfg, chainID); err != nil {
				return err
			}

			fmt.Printf("Removed ChainList RPCs for %s (chain ID: %d) from eRPC\n", chainName, chainID)
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// network status — eRPC health (was: rpc status)
// ---------------------------------------------------------------------------

func networkStatusCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "Show eRPC gateway health and upstream counts",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			podStatus, upstreamCounts, err := network.GetERPCStatus(cfg)
			if err != nil {
				return fmt.Errorf("failed to get eRPC status: %w", err)
			}

			fmt.Printf("eRPC Gateway Status\n")
			fmt.Printf("====================\n\n")

			fmt.Printf("Pod:\n")
			if podStatus != "" {
				fmt.Printf("  %s\n", podStatus)
			} else {
				fmt.Printf("  (no pods found)\n")
			}

			fmt.Printf("\nUpstreams per chain:\n")
			if len(upstreamCounts) == 0 {
				fmt.Printf("  (no upstreams configured)\n")
			} else {
				var chainIDs []int
				for id := range upstreamCounts {
					chainIDs = append(chainIDs, id)
				}
				sort.Ints(chainIDs)

				for _, id := range chainIDs {
					name := chainIDToName(id)
					fmt.Printf("  %-20s (chain %d): %d upstream(s)\n", name, id, upstreamCounts[id])
				}
			}

			return nil
		},
	}
}

// chainIDToName returns a human-readable name for a chain ID.
func chainIDToName(chainID int) string {
	names := map[int]string{
		1:        "Ethereum Mainnet",
		10:       "Optimism",
		56:       "BNB Chain",
		100:      "Gnosis",
		137:      "Polygon",
		250:      "Fantom",
		324:      "zkSync Era",
		8453:     "Base",
		42161:    "Arbitrum One",
		42220:    "Celo",
		43114:    "Avalanche",
		59144:    "Linea",
		84532:    "Base Sepolia",
		534352:   "Scroll",
		560048:   "Hoodi",
		11155111: "Sepolia",
	}
	if name, ok := names[chainID]; ok {
		return name
	}
	return fmt.Sprintf("Chain %d", chainID)
}
