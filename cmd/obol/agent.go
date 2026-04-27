package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentmgr "github.com/ObolNetwork/obol-stack/internal/agent"
	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/urfave/cli/v3"
)

type agentTarget struct {
	Runtime agentruntime.Runtime
	ID      string
}

type agentListItem struct {
	Runtime   string `json:"runtime"`
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
	URL       string `json:"url"`
}

func agentCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "agent",
		Usage: "Manage Obol agent instances",
		Commands: []*cli.Command{
			{
				Name:  "init",
				Usage: "Initialize the stack-managed Obol Agent",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return agentmgr.Init(cfg, getUI(cmd))
				},
			},
			{
				Name:    "new",
				Aliases: []string{"onboard"},
				Usage:   "Create and deploy an agent instance",
				Flags: []cli.Flag{
					agentRuntimeFlag("hermes"),
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
					runtime, err := parseAgentRuntime(cmd.String("runtime"))
					if err != nil {
						return err
					}

					u := getUI(cmd)
					switch runtime {
					case agentruntime.Hermes:
						return hermes.Onboard(cfg, hermes.OnboardOptions{
							ID:    cmd.String("id"),
							Force: cmd.Bool("force"),
							Sync:  !cmd.Bool("no-sync"),
						}, u)
					case agentruntime.OpenClaw:
						return openclaw.Onboard(cfg, openclaw.OnboardOptions{
							ID:          cmd.String("id"),
							Force:       cmd.Bool("force"),
							Sync:        !cmd.Bool("no-sync"),
							Interactive: u.IsTTY() && !u.IsJSON(),
						}, u)
					default:
						return fmt.Errorf("unsupported runtime: %s", runtime)
					}
				},
			},
			{
				Name:      "sync",
				Usage:     "Deploy or update an agent instance",
				ArgsUsage: "[instance-name]",
				Flags:     []cli.Flag{agentRuntimeFlag("")},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					target, err := resolveAgentTarget(cfg, cmd.String("runtime"), cmd.Args().Slice())
					if err != nil {
						return err
					}
					return syncAgentTarget(cfg, target, getUI(cmd))
				},
			},
			{
				Name:      "setup",
				Usage:     "Re-render runtime config from the current LiteLLM inventory",
				ArgsUsage: "[instance-name]",
				Flags:     []cli.Flag{agentRuntimeFlag("")},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					target, err := resolveAgentTarget(cfg, cmd.String("runtime"), cmd.Args().Slice())
					if err != nil {
						return err
					}
					return setupAgentTarget(cfg, target, getUI(cmd))
				},
			},
			{
				Name:      "auth",
				Aliases:   []string{"token"},
				Usage:     "Retrieve or regenerate an agent API token",
				ArgsUsage: "[instance-name]",
				Flags: []cli.Flag{
					agentRuntimeFlag(""),
					&cli.BoolFlag{
						Name:  "regenerate",
						Usage: "Delete and regenerate the API token (restarts the instance)",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					target, err := resolveAgentTarget(cfg, cmd.String("runtime"), cmd.Args().Slice())
					if err != nil {
						return err
					}

					u := getUI(cmd)
					if cmd.Bool("regenerate") {
						token, err := regenerateAgentToken(cfg, target, u)
						if err != nil {
							return err
						}
						u.Print(token)
						return nil
					}

					return printAgentToken(cfg, target, u)
				},
			},
			{
				Name:  "list",
				Usage: "List agent instances",
				Flags: []cli.Flag{agentRuntimeFlag("all")},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return listAgentInstances(cfg, cmd.String("runtime"), getUI(cmd))
				},
			},
			{
				Name:      "delete",
				Usage:     "Remove an agent instance and its cluster resources",
				ArgsUsage: "[instance-name]",
				Flags: []cli.Flag{
					agentRuntimeFlag(""),
					&cli.BoolFlag{
						Name:    "force",
						Aliases: []string{"f"},
						Usage:   "Skip confirmation prompt",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					target, err := resolveAgentTarget(cfg, cmd.String("runtime"), cmd.Args().Slice())
					if err != nil {
						return err
					}
					return deleteAgentTarget(cfg, target, cmd.Bool("force"), getUI(cmd))
				},
			},
			agentWalletCommand(cfg),
		},
	}
}

func agentWalletCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "wallet",
		Usage: "Manage agent wallets",
		Commands: []*cli.Command{
			{
				Name:      "address",
				Usage:     "Show the wallet address for an agent instance",
				ArgsUsage: "[instance-name]",
				Flags:     []cli.Flag{agentRuntimeFlag("")},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					target, err := resolveAgentTarget(cfg, cmd.String("runtime"), cmd.Args().Slice())
					if err != nil {
						return err
					}
					return printAgentWalletAddress(cfg, target, getUI(cmd))
				},
			},
			{
				Name:      "list",
				Usage:     "List wallets for agent instances",
				ArgsUsage: "[instance-name]",
				Flags:     []cli.Flag{agentRuntimeFlag("all")},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return listAgentWallets(cfg, cmd.String("runtime"), cmd.Args().Slice(), getUI(cmd))
				},
			},
			{
				Name:      "backup",
				Usage:     "Back up wallet keys for an OpenClaw agent instance",
				ArgsUsage: "[instance-name]",
				Flags: []cli.Flag{
					agentRuntimeFlag(""),
					&cli.StringFlag{
						Name:  "output",
						Usage: "Output file path",
					},
					&cli.StringFlag{
						Name:  "passphrase",
						Usage: "Encryption passphrase (empty string = no encryption)",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if err := kubectl.EnsureCluster(cfg); err != nil {
						return err
					}

					target, err := resolveAgentTarget(cfg, cmd.String("runtime"), cmd.Args().Slice())
					if err != nil {
						return err
					}
					if target.Runtime != agentruntime.OpenClaw {
						return errors.New("Hermes wallet backup needs a Hermes-native product decision; use OpenClaw backup only for OpenClaw instances")
					}
					return openclaw.BackupWalletCmd(cfg, target.ID, openclaw.BackupWalletOptions{
						Output:      cmd.String("output"),
						Passphrase:  cmd.String("passphrase"),
						HasPassFlag: cmd.IsSet("passphrase"),
					}, getUI(cmd))
				},
			},
			{
				Name:      "restore",
				Usage:     "Restore wallet keys for an OpenClaw agent instance",
				ArgsUsage: "[instance-name]",
				Flags: []cli.Flag{
					agentRuntimeFlag(""),
					&cli.StringFlag{
						Name:     "input",
						Usage:    "Backup file path",
						Required: true,
					},
					&cli.StringFlag{
						Name:  "passphrase",
						Usage: "Decryption passphrase",
					},
					&cli.BoolFlag{
						Name:  "force",
						Usage: "Overwrite existing wallet",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if err := kubectl.EnsureCluster(cfg); err != nil {
						return err
					}

					target, err := resolveAgentTarget(cfg, cmd.String("runtime"), cmd.Args().Slice())
					if err != nil {
						return err
					}
					if target.Runtime != agentruntime.OpenClaw {
						return errors.New("Hermes wallet restore needs a Hermes-native product decision; use OpenClaw restore only for OpenClaw instances")
					}
					return openclaw.RestoreWalletCmd(cfg, target.ID, openclaw.RestoreWalletOptions{
						Input:       cmd.String("input"),
						Passphrase:  cmd.String("passphrase"),
						HasPassFlag: cmd.IsSet("passphrase"),
						Force:       cmd.Bool("force"),
					}, getUI(cmd))
				},
			},
		},
	}
}

func agentRuntimeFlag(value string) cli.Flag {
	return &cli.StringFlag{
		Name:  "runtime",
		Usage: "Agent runtime: hermes, openclaw, or all",
		Value: value,
	}
}

func parseAgentRuntime(value string) (agentruntime.Runtime, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "hermes", "herme":
		return agentruntime.Hermes, nil
	case "openclaw":
		return agentruntime.OpenClaw, nil
	default:
		return "", fmt.Errorf("unsupported agent runtime %q (expected hermes or openclaw)", value)
	}
}

func resolveAgentTarget(cfg *config.Config, runtimeValue string, args []string) (agentTarget, error) {
	runtimeValue = strings.TrimSpace(runtimeValue)
	if runtimeValue != "" && runtimeValue != "all" {
		runtime, err := parseAgentRuntime(runtimeValue)
		if err != nil {
			return agentTarget{}, err
		}
		id, err := resolveRuntimeInstance(cfg, runtime, args, true)
		if err != nil {
			return agentTarget{}, err
		}
		return agentTarget{Runtime: runtime, ID: id}, nil
	}

	return resolveAnyAgentTarget(cfg, args)
}

func resolveAnyAgentTarget(cfg *config.Config, args []string) (agentTarget, error) {
	if len(args) > 0 {
		var matches []agentTarget
		for _, runtime := range []agentruntime.Runtime{agentruntime.Hermes, agentruntime.OpenClaw} {
			ids, err := agentruntime.ListInstanceIDs(cfg, runtime)
			if err != nil {
				return agentTarget{}, err
			}
			if containsString(ids, args[0]) {
				matches = append(matches, agentTarget{Runtime: runtime, ID: args[0]})
			}
		}

		switch len(matches) {
		case 1:
			return matches[0], nil
		case 0:
			return agentTarget{}, fmt.Errorf("agent instance %q not found", args[0])
		default:
			return agentTarget{}, fmt.Errorf("agent instance %q exists in multiple runtimes; specify --runtime hermes or --runtime openclaw", args[0])
		}
	}

	hermesIDs, err := agentruntime.ListInstanceIDs(cfg, agentruntime.Hermes)
	if err != nil {
		return agentTarget{}, err
	}
	if containsString(hermesIDs, agentruntime.DefaultInstanceID) {
		return agentTarget{Runtime: agentruntime.Hermes, ID: agentruntime.DefaultInstanceID}, nil
	}

	var all []agentTarget
	for _, id := range hermesIDs {
		all = append(all, agentTarget{Runtime: agentruntime.Hermes, ID: id})
	}
	openclawIDs, err := agentruntime.ListInstanceIDs(cfg, agentruntime.OpenClaw)
	if err != nil {
		return agentTarget{}, err
	}
	for _, id := range openclawIDs {
		all = append(all, agentTarget{Runtime: agentruntime.OpenClaw, ID: id})
	}

	switch len(all) {
	case 0:
		return agentTarget{}, errors.New("no agent instances found — run 'obol agent init' or 'obol agent new' to create one")
	case 1:
		return all[0], nil
	default:
		return agentTarget{}, fmt.Errorf("multiple agent instances found, specify one: %s", formatAgentTargets(all))
	}
}

func resolveRuntimeInstance(cfg *config.Config, runtime agentruntime.Runtime, args []string, preferDefault bool) (string, error) {
	ids, err := agentruntime.ListInstanceIDs(cfg, runtime)
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("no %s instances found — run 'obol agent new --runtime %s' to create one", agentruntime.Describe(runtime).DisplayName, runtime)
	}

	if len(args) > 0 {
		if containsString(ids, args[0]) {
			return args[0], nil
		}
		return "", fmt.Errorf("%s instance %q not found; available: %s", agentruntime.Describe(runtime).DisplayName, args[0], strings.Join(ids, ", "))
	}

	if preferDefault && runtime == agentruntime.Hermes && containsString(ids, agentruntime.DefaultInstanceID) {
		return agentruntime.DefaultInstanceID, nil
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	return "", fmt.Errorf("multiple %s instances found, specify one: %s", agentruntime.Describe(runtime).DisplayName, strings.Join(ids, ", "))
}

func syncAgentTarget(cfg *config.Config, target agentTarget, u *ui.UI) error {
	switch target.Runtime {
	case agentruntime.Hermes:
		return hermes.Sync(cfg, target.ID, u)
	case agentruntime.OpenClaw:
		return openclaw.Sync(cfg, target.ID, u)
	default:
		return fmt.Errorf("unsupported runtime: %s", target.Runtime)
	}
}

func setupAgentTarget(cfg *config.Config, target agentTarget, u *ui.UI) error {
	switch target.Runtime {
	case agentruntime.Hermes:
		return hermes.Setup(cfg, target.ID, hermes.SetupOptions{}, u)
	case agentruntime.OpenClaw:
		return openclaw.Setup(cfg, target.ID, openclaw.SetupOptions{}, u)
	default:
		return fmt.Errorf("unsupported runtime: %s", target.Runtime)
	}
}

func printAgentToken(cfg *config.Config, target agentTarget, u *ui.UI) error {
	switch target.Runtime {
	case agentruntime.Hermes:
		return hermes.Token(cfg, target.ID, u)
	case agentruntime.OpenClaw:
		return openclaw.Token(cfg, target.ID, u)
	default:
		return fmt.Errorf("unsupported runtime: %s", target.Runtime)
	}
}

func regenerateAgentToken(cfg *config.Config, target agentTarget, u *ui.UI) (string, error) {
	switch target.Runtime {
	case agentruntime.Hermes:
		return hermes.RegenerateToken(cfg, target.ID, u)
	case agentruntime.OpenClaw:
		return openclaw.RegenerateToken(cfg, target.ID, u)
	default:
		return "", fmt.Errorf("unsupported runtime: %s", target.Runtime)
	}
}

func deleteAgentTarget(cfg *config.Config, target agentTarget, force bool, u *ui.UI) error {
	switch target.Runtime {
	case agentruntime.Hermes:
		return hermes.Delete(cfg, target.ID, force, u)
	case agentruntime.OpenClaw:
		return openclaw.Delete(cfg, target.ID, force, u)
	default:
		return fmt.Errorf("unsupported runtime: %s", target.Runtime)
	}
}

func printAgentWalletAddress(cfg *config.Config, target agentTarget, u *ui.UI) error {
	var address string
	var err error

	switch target.Runtime {
	case agentruntime.Hermes:
		wallet, walletErr := hermes.ReadWalletMetadata(hermes.DeploymentPath(cfg, target.ID))
		if walletErr != nil {
			err = walletErr
		} else {
			address = wallet.Address
		}
	case agentruntime.OpenClaw:
		wallet, walletErr := openclaw.ReadWalletMetadata(openclaw.DeploymentPath(cfg, target.ID))
		if walletErr != nil {
			err = walletErr
		} else {
			address = wallet.Address
		}
	default:
		err = fmt.Errorf("unsupported runtime: %s", target.Runtime)
	}
	if err != nil {
		return err
	}

	u.Print(address)
	return nil
}

func listAgentInstances(cfg *config.Config, runtimeValue string, u *ui.UI) error {
	runtimes, err := listRuntimes(runtimeValue)
	if err != nil {
		return err
	}

	var instances []agentListItem
	for _, runtime := range runtimes {
		ids, err := agentruntime.ListInstanceIDs(cfg, runtime)
		if err != nil {
			return err
		}
		for _, id := range ids {
			instances = append(instances, agentListItem{
				Runtime:   string(runtime),
				ID:        id,
				Namespace: agentruntime.Namespace(runtime, id),
				URL:       "http://" + agentruntime.Hostname(runtime, id),
			})
		}
	}

	if u.IsJSON() {
		return u.JSON(instances)
	}
	if len(instances) == 0 {
		u.Print("No agent instances installed")
		u.Print("\nTo create one: obol agent new")
		return nil
	}

	u.Info("Agent instances:")
	u.Blank()
	for _, inst := range instances {
		u.Bold(fmt.Sprintf("  %s/%s", inst.Runtime, inst.ID))
		u.Detail("  Namespace", inst.Namespace)
		u.Detail("  URL", inst.URL)
		u.Blank()
	}
	u.Printf("Total: %d instance(s)", len(instances))
	return nil
}

func listAgentWallets(cfg *config.Config, runtimeValue string, args []string, u *ui.UI) error {
	runtimeValue = strings.TrimSpace(runtimeValue)
	if runtimeValue != "" && runtimeValue != "all" {
		runtime, err := parseAgentRuntime(runtimeValue)
		if err != nil {
			return err
		}

		id := ""
		if len(args) > 0 {
			id, err = resolveRuntimeInstance(cfg, runtime, args, false)
			if err != nil {
				return err
			}
		}
		return listWalletsForRuntime(cfg, runtime, id, u)
	}

	if len(args) > 0 {
		target, err := resolveAnyAgentTarget(cfg, args)
		if err != nil {
			return err
		}
		return listWalletsForRuntime(cfg, target.Runtime, target.ID, u)
	}

	if err := listWalletsForRuntime(cfg, agentruntime.Hermes, "", u); err != nil {
		return err
	}
	return listWalletsForRuntime(cfg, agentruntime.OpenClaw, "", u)
}

func listWalletsForRuntime(cfg *config.Config, runtime agentruntime.Runtime, id string, u *ui.UI) error {
	switch runtime {
	case agentruntime.Hermes:
		return hermes.ListWallets(cfg, id, u)
	case agentruntime.OpenClaw:
		return openclaw.ListWallets(cfg, id, u)
	default:
		return fmt.Errorf("unsupported runtime: %s", runtime)
	}
}

func listRuntimes(runtimeValue string) ([]agentruntime.Runtime, error) {
	switch strings.ToLower(strings.TrimSpace(runtimeValue)) {
	case "", "all":
		return []agentruntime.Runtime{agentruntime.Hermes, agentruntime.OpenClaw}, nil
	default:
		runtime, err := parseAgentRuntime(runtimeValue)
		if err != nil {
			return nil, err
		}
		return []agentruntime.Runtime{runtime}, nil
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func formatAgentTargets(targets []agentTarget) string {
	var formatted []string
	for _, target := range targets {
		formatted = append(formatted, fmt.Sprintf("%s/%s", target.Runtime, target.ID))
	}
	return strings.Join(formatted, ", ")
}
