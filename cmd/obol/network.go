package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/network"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/validate"
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
			networkERPCCommand(cfg),
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
				usage = "Override " + field.Name
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
// local_nodes keeps its original meaning (installable network types) for
// backward compatibility; installed deployments are in deployments.
type networkListResult struct {
	Deployments []network.Deployment  `json:"deployments"`
	LocalNodes  []string              `json:"local_nodes"`
	RPCs        []networkListRPCEntry `json:"rpcs"`
}

type networkListRPCEntry struct {
	Alias     string `json:"alias"`
	ChainID   int    `json:"chain_id"`
	Upstreams int    `json:"upstreams"`
}

func networkListCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List installed local nodes, installable network types and remote RPCs",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			deployments := network.InstalledDeployments(cfg)

			if u.IsJSON() {
				result := networkListResult{
					Deployments: deployments,
					LocalNodes:  []string{},
					RPCs:        []networkListRPCEntry{},
				}

				// Installable network types (best-effort).
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

			// Installed local node deployments.
			u.Info("Local Nodes:")
			if len(deployments) == 0 {
				u.Print("  (none installed — obol network install <type>)")
			} else {
				printDeployments(u, deployments)
			}

			if types, err := embed.GetAvailableNetworks(); err == nil && len(types) > 0 {
				u.Dim(fmt.Sprintf("  Installable types: %s (obol network install <type> --help)", strings.Join(types, ", ")))
			}

			u.Blank()

			// Show remote RPC networks from eRPC config.
			u.Info("Remote RPCs:")
			rpcNetworks, err := network.ListRPCNetworks(cfg)
			if err != nil {
				u.Printf("  (unable to read eRPC config: %v)", err)
				return nil
			}

			if len(rpcNetworks) == 0 {
				u.Info("  (none configured)")
			} else {
				for _, net := range rpcNetworks {
					alias := net.Alias
					if alias == "" {
						alias = fmt.Sprintf("chain-%d", net.ChainID)
					}
					u.Printf("  %-20s chain=%-8d %d upstream(s)", alias, net.ChainID, len(net.Upstreams))
				}
			}

			return nil
		},
	}
}

// printDeployments renders installed deployments as an aligned table.
func printDeployments(u *ui.UI, deployments []network.Deployment) {
	dash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}

	u.Printf("  %-26s %-9s %-11s %-11s %s", "DEPLOYMENT", "NETWORK", "EXECUTION", "CONSENSUS", "MODE")
	for _, d := range deployments {
		if d.Error != "" {
			u.Printf("  %-26s (%s)", d.Identifier(), d.Error)
			continue
		}

		u.Printf("  %-26s %-9s %-11s %-11s %s", d.Identifier(), dash(d.Network),
			dash(d.ExecutionClient), dash(d.ConsensusClient), dash(d.Mode))
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
			&cli.StringFlag{
				Name:  "from-json",
				Usage: "Read network config from JSON file (or - for stdin) instead of flags",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			// --from-json: read network add config from file/stdin.
			if jsonPath := cmd.String("from-json"); jsonPath != "" {
				data, err := readJSONInput(jsonPath)
				if err != nil {
					return err
				}
				var netCfg struct {
					Chain       string `json:"chain"`
					Endpoint    string `json:"endpoint"`
					AllowWrites bool   `json:"allowWrites"`
				}
				if err := json.Unmarshal(data, &netCfg); err != nil {
					return fmt.Errorf("parse JSON network config: %w", err)
				}
				if netCfg.Chain == "" {
					return fmt.Errorf("chain is required in JSON input")
				}

				chainID, chainName, err := network.ResolveChainID(netCfg.Chain)
				if err != nil {
					return err
				}

				readOnly := !netCfg.AllowWrites

				if netCfg.Endpoint != "" {
					u.Infof("Adding custom RPC for %s (chain ID: %d): %s", chainName, chainID, redactRPCURL(netCfg.Endpoint))
					if err := network.AddCustomRPC(cfg, chainID, chainName, netCfg.Endpoint, readOnly); err != nil {
						return fmt.Errorf("failed to add custom RPC: %w", err)
					}
					u.Successf("Added custom RPC for %s (chain ID: %d) to eRPC", chainName, chainID)
					return nil
				}

				// ChainList mode with defaults.
				maxCount := 3
				u.Infof("Fetching public RPCs for %s (chain ID: %d) from ChainList...", chainName, chainID)
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
				u.Infof("Found %d quality RPCs for %s", len(endpoints), chainName)
				if err := network.AddPublicRPCs(cfg, chainID, chainName, endpoints, readOnly); err != nil {
					return fmt.Errorf("failed to add RPCs: %w", err)
				}
				u.Successf("Added %d RPCs for %s (chain ID: %d) to eRPC", len(endpoints), chainName, chainID)
				return nil
			}

			if cmd.NArg() == 0 {
				return errors.New("chain name or ID required\n\nExamples:\n  obol network add base\n  obol network add base-sepolia --endpoint http://host.k3d.internal:8545")
			}

			chainArg := cmd.Args().First()

			chainID, chainName, err := network.ResolveChainID(chainArg)
			if err != nil {
				return err
			}

			readOnly := !cmd.Bool("allow-writes")

			// Custom endpoint mode.
			if endpoint := cmd.String("endpoint"); endpoint != "" {
				if err := validate.URL(endpoint); err != nil {
					return fmt.Errorf("invalid --endpoint: %w", err)
				}
				u.Infof("Adding custom RPC for %s (chain ID: %d): %s", chainName, chainID, redactRPCURL(endpoint))
				if readOnly {
					u.Infof("  Write methods blocked (use --allow-writes to enable)")
				}

				if err := network.AddCustomRPC(cfg, chainID, chainName, endpoint, readOnly); err != nil {
					return fmt.Errorf("failed to add custom RPC: %w", err)
				}
				u.Successf("Added custom RPC for %s (chain ID: %d) to eRPC", chainName, chainID)
				return nil
			}

			// ChainList mode.
			maxCount := cmd.Int("count")
			if maxCount <= 0 {
				maxCount = 3
			}

			u.Infof("Fetching public RPCs for %s (chain ID: %d) from ChainList...", chainName, chainID)

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

			u.Infof("Found %d quality RPCs for %s:", len(endpoints), chainName)
			for i, ep := range endpoints {
				u.Printf("  %d. %s (tracking: %s)\n", i+1, ep.URL, ep.Tracking)
			}

			if readOnly {
				u.Warnf("Write methods blocked (use --allow-writes to enable)")
			}

			u.Infof("Adding to eRPC gateway...")
			if err := network.AddPublicRPCs(cfg, chainID, chainName, endpoints, readOnly); err != nil {
				return fmt.Errorf("failed to add RPCs: %w", err)
			}

			u.Successf("Added %d RPCs for %s (chain ID: %d) to eRPC", len(endpoints), chainName, chainID)
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
			u := getUI(cmd)

			if cmd.NArg() == 0 {
				return errors.New("chain name or ID required\n\nExamples:\n  obol network remove base\n  obol network remove 8453")
			}

			chainArg := cmd.Args().First()

			chainID, chainName, err := network.ResolveChainID(chainArg)
			if err != nil {
				return err
			}

			u.Infof("Removing ChainList RPCs for %s (chain ID: %d)...", chainName, chainID)

			if err := network.RemovePublicRPCs(cfg, chainID); err != nil {
				return err
			}

			u.Successf("Removed ChainList RPCs for %s (chain ID: %d) from eRPC", chainName, chainID)
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// network status — eRPC health + live local node sync state
// ---------------------------------------------------------------------------

// networkStatusResult is the JSON shape of `network status`.
type networkStatusResult struct {
	ERPC  networkERPCStatus    `json:"erpc"`
	Nodes []network.NodeStatus `json:"nodes"`
}

type networkERPCStatus struct {
	Pod       string                  `json:"pod"`
	Upstreams []networkERPCChainCount `json:"upstreams"`
}

type networkERPCChainCount struct {
	ChainID   int    `json:"chain_id"`
	Name      string `json:"name"`
	Upstreams int    `json:"upstreams"`
}

func networkStatusCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "status",
		Usage:     "Show eRPC gateway health and the sync state of installed local nodes",
		ArgsUsage: "[<network>/<id>]",
		Description: `Shows eRPC upstreams and, for each installed ethereum deployment, live
execution (eth_syncing, head block, peers, client version) and consensus
(/eth/v1/node/syncing, peers, version) state plus a one-line verdict.
The node APIs are reached through short-lived kubectl port-forwards on the
stack kubeconfig; nothing is exposed on obol.stack.

Examples:
  obol network status
  obol network status ethereum/hoodi
  obol -o json network status ethereum/hoodi`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "no-probe",
				Usage: "Skip live node API probes (eRPC and installed deployments only)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			deployments, err := selectDeployments(network.InstalledDeployments(cfg), cmd.Args().First())
			if err != nil {
				return err
			}

			podStatus, upstreamCounts, err := network.GetERPCStatus(cfg)
			if err != nil {
				return fmt.Errorf("failed to get eRPC status: %w", err)
			}

			result := networkStatusResult{
				ERPC:  networkERPCStatus{Pod: strings.TrimSpace(podStatus), Upstreams: []networkERPCChainCount{}},
				Nodes: []network.NodeStatus{},
			}

			var chainIDs []int
			for id := range upstreamCounts {
				chainIDs = append(chainIDs, id)
			}
			sort.Ints(chainIDs)
			for _, id := range chainIDs {
				result.ERPC.Upstreams = append(result.ERPC.Upstreams, networkERPCChainCount{
					ChainID: id, Name: chainIDToName(id), Upstreams: upstreamCounts[id],
				})
			}

			if cmd.Bool("no-probe") {
				for _, d := range deployments {
					result.Nodes = append(result.Nodes, network.NodeStatus{
						Deployment: d,
						Pods:       []network.PodStatus{},
						Verdict:    network.Verdict{State: network.VerdictNotProbed, Summary: "not probed (--no-probe)"},
					})
				}
			} else if len(deployments) > 0 {
				probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
				defer cancel()
				result.Nodes = network.NewKubectlNodeProber(cfg).ProbeAll(probeCtx, deployments)
			}

			if u.IsJSON() {
				return u.JSON(result)
			}

			printERPCStatus(u, result.ERPC)
			for _, n := range result.Nodes {
				u.Blank()
				printNodeStatus(u, n)
			}

			if len(deployments) == 0 {
				u.Blank()
				u.Dim("No local nodes installed (obol network install ethereum --help)")
			}

			return nil
		},
	}
}

// selectDeployments narrows deployments to the one named by arg: an exact
// "<type>/<id>", its namespace "<type>-<id>", or a type with exactly one
// deployment. An empty arg selects all.
func selectDeployments(all []network.Deployment, arg string) ([]network.Deployment, error) {
	if arg == "" {
		return all, nil
	}

	var byType []network.Deployment
	for _, d := range all {
		if arg == d.Identifier() || arg == d.Namespace {
			return []network.Deployment{d}, nil
		}
		if arg == d.Type {
			byType = append(byType, d)
		}
	}

	if len(byType) == 1 {
		return byType, nil
	}

	ids := make([]string, 0, len(all))
	for _, d := range all {
		ids = append(ids, d.Identifier())
	}

	if len(ids) == 0 {
		return nil, fmt.Errorf("no network deployment %q: none installed", arg)
	}

	return nil, fmt.Errorf("no single network deployment matches %q; installed: %s", arg, strings.Join(ids, ", "))
}

func printERPCStatus(u *ui.UI, st networkERPCStatus) {
	u.Bold("eRPC gateway")
	if st.Pod != "" {
		for line := range strings.SplitSeq(st.Pod, "\n") {
			u.Printf("  %s", strings.TrimSpace(line))
		}
	} else {
		u.Dim("  (no pods found)")
	}

	if len(st.Upstreams) == 0 {
		u.Dim("  (no upstreams configured)")
		return
	}

	for _, c := range st.Upstreams {
		u.Printf("  %-20s (chain %d): %d upstream(s)", c.Name, c.ChainID, c.Upstreams)
	}
}

func printNodeStatus(u *ui.UI, n network.NodeStatus) {
	head := n.Identifier()
	if n.Network != "" {
		parts := []string{n.Network}
		if n.ExecutionClient != "" || n.ConsensusClient != "" {
			parts = append(parts, n.ExecutionClient+" + "+n.ConsensusClient)
		}
		if n.Mode != "" {
			parts = append(parts, n.Mode)
		}
		head += "  (" + strings.Join(parts, " · ") + ")"
	}
	u.Bold(head)

	verdict := n.Verdict.State + ": " + n.Verdict.Summary
	switch n.Verdict.State {
	case network.VerdictSynced:
		u.Success(verdict)
	case network.VerdictSyncing, network.VerdictWaiting, network.VerdictNotProbed, network.VerdictNotSupported:
		u.Info(verdict)
	default:
		u.Warn(verdict)
	}

	for _, w := range n.Verdict.Warnings {
		u.Warn(w)
	}

	if n.Verdict.State == network.VerdictNotProbed || n.Verdict.State == network.VerdictNotSupported {
		return
	}

	if n.PodsError != "" {
		u.Detail("Pods", "(unavailable: "+n.PodsError+")")
	} else if len(n.Pods) > 0 {
		var pods []string
		for _, p := range n.Pods {
			s := p.Component + " " + p.Phase
			if p.Ready {
				s += " ready"
			} else if p.Reason != "" {
				s += " " + p.Reason
			}
			if p.Restarts > 0 {
				s += fmt.Sprintf(" (%d restarts)", p.Restarts)
			}
			pods = append(pods, s)
		}
		u.Detail("Pods", strings.Join(pods, " · "))
	}

	if el := n.Execution; el != nil {
		if !el.Reachable {
			u.Detail("Execution", "unreachable: "+el.Error)
		} else {
			line := fmt.Sprintf("head %d", el.HeadBlock)
			if el.Syncing {
				line = fmt.Sprintf("syncing %d/%d", el.CurrentBlock, el.HighestBlock)
			}
			if el.Peers != nil {
				line += fmt.Sprintf(" · %d peers", *el.Peers)
			}
			if el.ClientVersion != "" {
				line += " · " + el.ClientVersion
			}
			u.Detail("Execution", line)
		}
	}

	if cl := n.Consensus; cl != nil {
		if !cl.Reachable {
			u.Detail("Consensus", "unreachable: "+cl.Error)
		} else {
			line := fmt.Sprintf("head slot %d · distance %d · syncing=%t optimistic=%t el_offline=%t",
				cl.HeadSlot, cl.SyncDistance, cl.IsSyncing, cl.IsOptimistic, cl.ELOffline)
			if cl.Peers != nil {
				line += fmt.Sprintf(" · %d peers", *cl.Peers)
			}
			if cl.Version != "" {
				line += " · " + cl.Version
			}
			u.Detail("Consensus", line)
		}
	}
}

// ---------------------------------------------------------------------------
// network erpc — durable operator eRPC config that survives stack up (#763).
// Verbs match sell-info set/reset (host-side intent) + status.
// ---------------------------------------------------------------------------

func networkERPCCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "erpc",
		Usage: "Manage durable operator eRPC config (host-side; re-applied on stack up so local baskets are not lost)",
		Commands: []*cli.Command{
			{
				Name:  "set",
				Usage: "Set durable eRPC config from a YAML file and merge it into the live ConfigMap",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "file",
						Aliases:  []string{"f"},
						Usage:    "YAML fragment: networks, upstreams, rateLimiters, cachePoliciesAdd (saved to $CONFIG_DIR/rpc/erpc-overlay.yaml)",
						Required: true,
					},
				},
				Action: func(_ context.Context, cmd *cli.Command) error {
					return network.SetERPC(cfg, getUI(cmd), cmd.String("file"))
				},
			},
			{
				Name:  "status",
				Usage: "Show the durable eRPC config on disk",
				Action: func(_ context.Context, cmd *cli.Command) error {
					u := getUI(cmd)
					st, err := network.StatusERPC(cfg)
					if err != nil {
						return err
					}
					u.Printf("eRPC operator config\n")
					u.Printf("====================\n\n")
					u.Printf("Path: %s\n", st.Path)
					if !st.Present {
						u.Info("Status: not set")
						u.Info("Set with: obol network erpc set -f <file.yaml>")
						return nil
					}
					u.Printf("Status: set (v%d, hash %s)\n", st.Version, st.ContentHash)
					switch st.ClusterSync {
					case "":
						u.Info("Cluster: unknown (could not reach cluster to check for drift)")
					case network.ERPCSyncInSync:
						u.Info("Cluster: in-sync")
					case network.ERPCSyncNotApplied:
						u.Warn("Cluster: not-applied (overlay is on disk but not on the live ConfigMap — run `obol network erpc set -f <file>` or `obol stack up`)")
					default:
						u.Warnf("Cluster: %s (live ConfigMap does not match the on-disk overlay — re-run `obol network erpc set -f <file>`)", st.ClusterSync)
					}
					u.Printf("Networks (%d):\n", st.NetworkCount)
					for _, k := range st.NetworkKeys {
						u.Printf("  - %s\n", k)
					}
					if st.NetworkCount == 0 {
						u.Info("  (none)")
					}
					u.Printf("Upstreams (%d):\n", st.UpstreamCount)
					for _, id := range st.UpstreamIDs {
						u.Printf("  - %s\n", id)
					}
					if st.UpstreamCount == 0 {
						u.Info("  (none)")
					}
					u.Printf("Rate-limit budgets: %d\n", st.BudgetCount)
					u.Printf("Cache policies to add: %d\n", st.CachePolicyAdd)
					return nil
				},
			},
			{
				Name:  "reset",
				Usage: "Reset durable eRPC config (strip from live ConfigMap and delete host file)",
				Action: func(_ context.Context, cmd *cli.Command) error {
					return network.ResetERPC(cfg, getUI(cmd))
				},
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
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
		999:      "HyperEVM",
		534352:   "Scroll",
		560048:   "Hoodi",
		11155111: "Sepolia",
	}
	if name, ok := names[chainID]; ok {
		return name
	}

	return fmt.Sprintf("Chain %d", chainID)
}

// paidRPCDomains is the set of registered domains (eTLD+1) we recognize as
// paid RPC providers. When the URL's host matches one of these (or any
// subdomain of it), redactRPCURL collapses the URL down to "scheme://[REDACTED].<domain>[:port]/[REDACTED]"
// so logs only ever surface the provider, not which instance, key, or
// account is in use. Keep in lockstep with flows/lib.sh::scrub_secrets.
var paidRPCDomains = []string{
	"alchemy.com",
	"infura.io",
	"quiknode.pro",
	"drpc.live",
	"drpc.org",
}

// redactRPCURL collapses a paid-RPC URL down to its top-level provider
// domain. Subdomain, path, query, and fragment are all replaced with
// [REDACTED] so the operator can still see which provider they pointed at
// without leaking which network, instance, or api key is in use.
//
// Hosts that don't match a known paid-RPC domain are returned unchanged.
func redactRPCURL(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}

	hostOnly := strings.ToLower(u.Hostname())
	port := u.Port()

	domain := matchPaidRPCDomain(hostOnly)
	if domain == "" {
		return raw
	}

	// Build "[REDACTED].<domain>" only when there is a real subdomain.
	hostOut := domain
	if hostOnly != domain {
		hostOut = "[REDACTED]." + domain
	}
	if port != "" {
		hostOut += ":" + port
	}

	out := u.Scheme + "://"
	if u.User != nil {
		out += "[REDACTED]@"
	}
	out += hostOut
	if u.Path != "" && u.Path != "/" {
		out += "/[REDACTED]"
	}
	if u.RawQuery != "" {
		out += "?[REDACTED]"
	}
	if u.Fragment != "" {
		out += "#[REDACTED]"
	}
	return out
}

// matchPaidRPCDomain returns the registered domain when host matches one
// of paidRPCDomains exactly or as a subdomain. Returns "" otherwise.
func matchPaidRPCDomain(host string) string {
	for _, d := range paidRPCDomains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return d
		}
	}
	return ""
}
