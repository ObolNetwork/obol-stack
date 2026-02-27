package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/network"
	"github.com/urfave/cli/v3"
)

func rpcCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "rpc",
		Usage: "Manage eRPC upstreams and public RPC endpoints",
		Commands: []*cli.Command{
			rpcListCommand(cfg),
			rpcAddCommand(cfg),
			rpcRemoveCommand(cfg),
			rpcStatusCommand(cfg),
		},
	}
}

func rpcListCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List configured networks and their upstreams",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			networks, err := network.ListRPCNetworks(cfg)
			if err != nil {
				return fmt.Errorf("failed to read eRPC config: %w", err)
			}

			if len(networks) == 0 {
				fmt.Println("No networks configured in eRPC.")
				return nil
			}

			for _, net := range networks {
				alias := net.Alias
				if alias == "" {
					alias = fmt.Sprintf("chain-%d", net.ChainID)
				}
				fmt.Printf("\n%s (chain ID: %d)\n", alias, net.ChainID)
				if len(net.Upstreams) == 0 {
					fmt.Printf("  (no upstreams)\n")
				} else {
					for _, u := range net.Upstreams {
						fmt.Printf("  %-35s %s\n", u.ID, u.Endpoint)
					}
				}
			}
			fmt.Println()
			return nil
		},
	}
}

func rpcAddCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "add",
		Usage:     "Add public RPCs for a chain from ChainList",
		ArgsUsage: "<chain-name-or-id>",
		Description: `Fetches free, public RPC endpoints from ChainList for the specified chain
and adds them to the eRPC gateway. Supports chain names (e.g., base, arbitrum,
optimism) or numeric chain IDs (e.g., 8453).

Examples:
  obol rpc add base
  obol rpc add arbitrum
  obol rpc add 137
  obol rpc add --count 5 optimism`,
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "count",
				Usage: "Maximum number of RPCs to add",
				Value: 3,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() == 0 {
				return fmt.Errorf("chain name or ID required\n\nExamples:\n  obol rpc add base\n  obol rpc add 8453\n  obol rpc add arbitrum")
			}

			chainArg := cmd.Args().First()
			chainID, chainName, err := network.ResolveChainID(chainArg)
			if err != nil {
				return err
			}

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

			// Cap at user-specified count.
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

			fmt.Printf("\nAdding to eRPC gateway...\n")
			if err := network.AddPublicRPCs(cfg, chainID, chainName, endpoints); err != nil {
				return fmt.Errorf("failed to add RPCs: %w", err)
			}

			fmt.Printf("Added %d public RPCs for %s (chain ID: %d) to eRPC\n", len(endpoints), chainName, chainID)
			fmt.Printf("eRPC restarting to pick up new configuration.\n")
			return nil
		},
	}
}

func rpcRemoveCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "remove",
		Usage:     "Remove public RPCs for a chain from eRPC",
		ArgsUsage: "<chain-name-or-id>",
		Description: `Removes all ChainList-sourced RPC endpoints for the specified chain
from the eRPC gateway. Does not affect manually configured or local upstreams.

Examples:
  obol rpc remove base
  obol rpc remove 8453`,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() == 0 {
				return fmt.Errorf("chain name or ID required\n\nExamples:\n  obol rpc remove base\n  obol rpc remove 8453")
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
			fmt.Printf("eRPC restarting to pick up new configuration.\n")
			return nil
		},
	}
}

func rpcStatusCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "Show eRPC health and upstream counts",
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
				// Sort chain IDs for stable output.
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

// chainIDToName returns a human-readable name for a chain ID, or the ID as string.
func chainIDToName(chainID int) string {
	// Reverse lookup from the chainNames map in the network package.
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
