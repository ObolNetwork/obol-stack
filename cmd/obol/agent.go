package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentmgr "github.com/ObolNetwork/obol-stack/internal/agent"
	"github.com/ObolNetwork/obol-stack/internal/agentcrd"
	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/urfave/cli/v3"
)

// agentDeleteWaitTimeout is how long deleteCRDAgent waits for the K8s
// finalizer to drain before reporting the delete as stuck. Sized to be
// longer than the controller's typical tearDownAgent (which deletes
// Deployment + Service + Secret + remote-signer manifests) but short
// enough that the user notices when a controller is unreachable or
// running a pre-agent-CRD image without finalizer handling.
const agentDeleteWaitTimeout = 60 * time.Second

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
				Name:      "new",
				Aliases:   []string{"onboard"},
				Usage:     "Create and deploy an agent instance",
				ArgsUsage: "[name]",
				Description: `With a positional name and CRD-path flags (--model, --skills,
--objective, --create-wallet) this declares an Agent custom resource and
seeds SOUL.md + the per-agent skills dir on the host.

Without a positional name, falls back to the legacy host-rendered
Hermes/OpenClaw onboard flow used by the master agent.`,
				Flags: []cli.Flag{
					agentRuntimeFlag("hermes"),
					&cli.StringFlag{
						Name:  "id",
						Usage: "Instance ID for the legacy onboard path (defaults to generated petname)",
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
					// CRD-path flags. Presence of any of these (or a positional
					// name argument) routes to the new sub-agent flow.
					&cli.StringFlag{
						Name:  "model",
						Usage: "Pin a LiteLLM model name for this agent (CRD path; defaults to cluster top-of-rank at first reconcile)",
					},
					&cli.StringFlag{
						Name:  "skills",
						Usage: "Comma-separated skill names to seed for this agent (CRD path)",
					},
					&cli.StringFlag{
						Name:  "objective",
						Usage: "Operator objective text substituted into SOUL.md (CRD path)",
					},
					&cli.BoolFlag{
						Name:  "create-wallet",
						Usage: "Provision a per-namespace remote-signer keystore for this agent (CRD path)",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					u := getUI(cmd)

					// CRD path: positional name, or any CRD-only flag set.
					useCRDPath := cmd.NArg() > 0 || cmd.IsSet("model") || cmd.IsSet("skills") || cmd.IsSet("objective") || cmd.IsSet("create-wallet")
					if err := validateAgentNewMode(useCRDPath, cmd.IsSet("runtime"), cmd.IsSet("id"), cmd.IsSet("force"), cmd.IsSet("no-sync")); err != nil {
						return err
					}
					if useCRDPath {
						return agentNewCRD(cfg, cmd, u)
					}

					// Legacy onboard path (master agent, additional Hermes/OpenClaw).
					runtime, err := parseAgentRuntime(cmd.String("runtime"))
					if err != nil {
						return err
					}

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
					u := getUI(cmd)

					// CRD-managed sub-agent path: when a positional name
					// matches an Agent CR (and no legacy instance exists
					// for it), delete the CR + host data dir. Mirrors the
					// dispatch shape used by `obol agent new`.
					if cmd.NArg() == 1 {
						name := strings.TrimSpace(cmd.Args().First())
						if isCRDAgent(cfg, name) && !hasLegacyInstance(cfg, name) {
							if !cmd.Bool("force") && u.IsTTY() {
								confirm, _ := u.Input(fmt.Sprintf("Delete Agent %q (namespace %s) and host data? [y/N]", name, agentcrd.Namespace(name)), "n")
								if !strings.EqualFold(strings.TrimSpace(confirm), "y") {
									u.Info("Aborted")
									return nil
								}
							}
							return deleteCRDAgent(cfg, name, cmd.Bool("force"), u)
						}
					}

					target, err := resolveAgentTarget(cfg, cmd.String("runtime"), cmd.Args().Slice())
					if err != nil {
						return err
					}
					return deleteAgentTarget(cfg, target, cmd.Bool("force"), u)
				},
			},
			agentUpdateCommand(cfg),
			agentWalletCommand(cfg),
		},
	}
}

// agentUpdateCommand implements `obol agent update <name>` for the
// CRD-declared sub-agents. Supports +foo,-bar diff syntax on --skills so
// users can layer changes without retyping the full list. Operates on a
// kubectl get → mutate → kubectl apply round-trip; no interactive flow yet.
func agentUpdateCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "update",
		Usage:     "Update a CRD-declared sub-agent (model, skills, objective)",
		ArgsUsage: "<name>",
		Description: `Updates spec fields on an existing Agent custom resource.

--skills accepts both replacement (a,b,c) and diff (+foo,-bar) syntax.
Diff entries leave existing skills in place; literal entries replace the
list wholesale. Mixing the two is rejected to avoid surprise.

Examples:
  obol agent update quant --model qwen3.5:35b
  obol agent update quant --skills +building-blocks,-gas
  obol agent update quant --skills addresses,gas        # replaces the list`,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "model", Usage: "Pin a different LiteLLM model"},
			&cli.StringFlag{Name: "skills", Usage: "Replacement list or +foo,-bar diff"},
			&cli.StringFlag{Name: "objective", Usage: "Replacement objective text"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 {
				return fmt.Errorf("agent name required: obol agent update <name>")
			}
			name := strings.TrimSpace(cmd.Args().First())
			if err := agentcrd.ValidateName(name); err != nil {
				return err
			}
			if err := kubectl.EnsureCluster(cfg); err != nil {
				return fmt.Errorf("Obol Stack is not running. Start it with `obol stack up` first")
			}

			bin, kc := kubectl.Paths(cfg)
			ns := agentcrd.Namespace(name)
			currentJSON, err := kubectl.Output(bin, kc, "get", "agent", name, "-n", ns, "-o", "json")
			if err != nil {
				return fmt.Errorf("agent %q not found in namespace %s", name, ns)
			}

			var doc map[string]any
			if err := json.Unmarshal([]byte(currentJSON), &doc); err != nil {
				return fmt.Errorf("decode agent: %w", err)
			}
			spec, _ := doc["spec"].(map[string]any)
			if spec == nil {
				spec = map[string]any{}
				doc["spec"] = spec
			}

			u := getUI(cmd)
			finalSkills := stringSliceFromAny(spec["skills"])
			objectiveChanged := false
			skillsRequested := false

			if cmd.IsSet("model") {
				spec["model"] = strings.TrimSpace(cmd.String("model"))
				u.Successf("model → %v", spec["model"])
			}
			if cmd.IsSet("objective") {
				spec["objective"] = strings.TrimSpace(cmd.String("objective"))
				objectiveChanged = true
				u.Successf("objective updated")
			}
			if cmd.IsSet("skills") {
				skillsRequested = true
				current := finalSkills
				updated, err := applySkillDiff(current, cmd.String("skills"))
				if err != nil {
					return err
				}
				finalSkills = updated
				out := make([]any, len(updated))
				for i, s := range updated {
					out[i] = s
				}
				spec["skills"] = out

				if added, removed := skillDelta(current, updated); len(added)+len(removed) > 0 {
					if len(added) > 0 {
						u.Successf("skills added: %s", strings.Join(added, ", "))
					}
					if len(removed) > 0 {
						u.Successf("skills removed: %s", strings.Join(removed, ", "))
					}
				}
			}

			if skillsRequested {
				if _, err := agentcrd.SeedHostFiles(cfg, name, finalSkills, stringValueFromAny(spec["objective"]), agentcrd.SeedOptions{
					OverwriteSoul: objectiveChanged,
					ExactSkills:   true,
				}); err != nil {
					return fmt.Errorf("sync host agent files: %w", err)
				}
			} else if objectiveChanged {
				if _, err := agentcrd.WriteSoul(cfg, name, stringValueFromAny(spec["objective"]), true); err != nil {
					return fmt.Errorf("sync host SOUL.md: %w", err)
				}
			}

			// Strip status before re-applying so we don't fight the controller.
			delete(doc, "status")

			if _, err := kubectlApplyOutput(cfg, doc); err != nil {
				return fmt.Errorf("apply Agent: %w", err)
			}
			u.Successf("Agent %s/%s updated", ns, name)
			return nil
		},
	}
}

// applySkillDiff returns the new skill list given the current list and a
// CLI-style spec. The spec is one of:
//   - all-literal (e.g. "addresses,gas"): wholesale replacement
//   - all-diff (e.g. "+foo,-bar"): additions/removals applied to current
//
// Mixing literal and diff entries is rejected.
func applySkillDiff(current []string, spec string) ([]string, error) {
	parts := strings.Split(spec, ",")
	hasLiteral, hasDiff := false, false
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p[0] == '+' || p[0] == '-' {
			hasDiff = true
		} else {
			hasLiteral = true
		}
	}
	if hasDiff && hasLiteral {
		return nil, fmt.Errorf("--skills must be all-replace (a,b,c) or all-diff (+foo,-bar), not mixed")
	}

	if hasLiteral {
		// Validate against agentcrd.ParseSkills which enforces naming
		// rules. Empty input is allowed and yields an empty list.
		return agentcrd.ParseSkills(spec)
	}

	out := append([]string(nil), current...)
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		op, name := p[0], strings.TrimSpace(p[1:])
		if name == "" {
			return nil, fmt.Errorf("empty skill name in %q", p)
		}
		switch op {
		case '+':
			if !containsString(out, name) {
				out = append(out, name)
			}
		case '-':
			out = removeString(out, name)
		}
	}
	return out, nil
}

func skillDelta(before, after []string) (added, removed []string) {
	beforeSet := map[string]bool{}
	for _, s := range before {
		beforeSet[s] = true
	}
	afterSet := map[string]bool{}
	for _, s := range after {
		afterSet[s] = true
		if !beforeSet[s] {
			added = append(added, s)
		}
	}
	for _, s := range before {
		if !afterSet[s] {
			removed = append(removed, s)
		}
	}
	return added, removed
}

func stringSliceFromAny(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func stringValueFromAny(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func removeString(values []string, needle string) []string {
	out := values[:0]
	for _, v := range values {
		if v != needle {
			out = append(out, v)
		}
	}
	return out
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
				Usage:     "Back up wallet keys for an agent instance",
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
					switch target.Runtime {
					case agentruntime.Hermes:
						return hermes.BackupWalletCmd(cfg, target.ID, hermes.BackupWalletOptions{
							Output:      cmd.String("output"),
							Passphrase:  cmd.String("passphrase"),
							HasPassFlag: cmd.IsSet("passphrase"),
						}, getUI(cmd))
					case agentruntime.OpenClaw:
						return openclaw.BackupWalletCmd(cfg, target.ID, openclaw.BackupWalletOptions{
							Output:      cmd.String("output"),
							Passphrase:  cmd.String("passphrase"),
							HasPassFlag: cmd.IsSet("passphrase"),
						}, getUI(cmd))
					default:
						return fmt.Errorf("unsupported runtime %q", target.Runtime)
					}
				},
			},
			{
				Name:      "restore",
				Usage:     "Restore wallet keys for an agent instance",
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
					switch target.Runtime {
					case agentruntime.Hermes:
						return hermes.RestoreWalletCmd(cfg, target.ID, hermes.RestoreWalletOptions{
							Input:        cmd.String("input"),
							Passphrase:   cmd.String("passphrase"),
							HasPassFlag:  cmd.IsSet("passphrase"),
							Force:        cmd.Bool("force"),
							ApplyCluster: true,
						}, getUI(cmd))
					case agentruntime.OpenClaw:
						return openclaw.RestoreWalletCmd(cfg, target.ID, openclaw.RestoreWalletOptions{
							Input:       cmd.String("input"),
							Passphrase:  cmd.String("passphrase"),
							HasPassFlag: cmd.IsSet("passphrase"),
							Force:       cmd.Bool("force"),
						}, getUI(cmd))
					default:
						return fmt.Errorf("unsupported runtime %q", target.Runtime)
					}
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

	// CRD-managed sub-agents live alongside the legacy host-managed
	// instances. Include them only in the aggregate view so a runtime-
	// filtered listing stays round-trippable with the rest of the CLI.
	if shouldIncludeCRDAgents(runtimeValue) {
		crdAgents, _ := listCRDAgents(cfg)
		instances = append(instances, crdAgents...)
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

// isCRDAgent reports whether the named agent exists as an obol.org/Agent
// custom resource. Soft errors (cluster down, CRD missing) return false.
func isCRDAgent(cfg *config.Config, name string) bool {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return false
	}
	bin, kc := kubectl.Paths(cfg)
	out, err := kubectl.Output(bin, kc, "get", "agent", name, "-n", agentcrd.Namespace(name), "-o", "name", "--ignore-not-found")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) != ""
}

// hasLegacyInstance reports whether the name matches a host-managed
// Hermes/OpenClaw instance. Used to disambiguate `obol agent delete <name>`
// when both forms could conceivably be present.
func hasLegacyInstance(cfg *config.Config, name string) bool {
	for _, runtime := range []agentruntime.Runtime{agentruntime.Hermes, agentruntime.OpenClaw} {
		ids, err := agentruntime.ListInstanceIDs(cfg, runtime)
		if err != nil {
			continue
		}
		for _, id := range ids {
			if id == name {
				return true
			}
		}
	}
	return false
}

// listCRDAgents returns Agent CRs as agentListItems so they merge cleanly
// into the existing host-side listing. Best-effort: kubectl errors are
// returned but the caller treats them as soft failures (cluster down,
// CRD not yet installed, etc.).
func listCRDAgents(cfg *config.Config) ([]agentListItem, error) {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return nil, err
	}
	bin, kc := kubectl.Paths(cfg)
	out, err := kubectl.Output(bin, kc, "get", "agents.obol.org", "-A", "-o", "json")
	if err != nil {
		return nil, err
	}
	var doc struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Status struct {
				Endpoint string `json:"endpoint"`
				Phase    string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return nil, fmt.Errorf("decode agent list: %w", err)
	}
	items := make([]agentListItem, 0, len(doc.Items))
	for _, it := range doc.Items {
		url := it.Status.Endpoint
		if url == "" {
			url = "(pending controller)"
		}
		items = append(items, agentListItem{
			Runtime:   "agent-crd",
			ID:        it.Metadata.Name,
			Namespace: it.Metadata.Namespace,
			URL:       url,
		})
	}
	return items, nil
}

// deleteCRDAgent removes the Agent CR and its host-side data directory
// (skills + SOUL.md). Used by `obol agent delete <name>` when the
// argument matches a CRD-declared agent. Idempotent: missing cluster,
// missing CR, and missing host dir are all treated as "already gone".
//
// The CR delete uses --wait=false and the CLI polls separately so we
// can show a spinner and surface a clear error when the finalizer drain
// stalls (the common cause: a controller image that pre-dates Agent
// CRD support, which never removes the finalizer). With force=true a
// stuck delete is escalated to a finalizer strip so the user can
// recover without hand-running kubectl patch.
func deleteCRDAgent(cfg *config.Config, name string, force bool, u *ui.UI) error {
	if err := agentcrd.ValidateName(name); err != nil {
		return err
	}

	ns := agentcrd.Namespace(name)

	if err := kubectl.EnsureCluster(cfg); err == nil {
		bin, kc := kubectl.Paths(cfg)

		// Fire-and-watch: send the DELETE request immediately, then poll
		// for absence under a spinner. --wait=false makes kubectl return
		// after the API server accepts the request (DeletionTimestamp
		// set) rather than blocking on finalizer drain. That lets us
		// surface clear progress and a recovery hint when drain stalls.
		if err := kubectl.Run(bin, kc, "delete", "agent", name, "-n", ns, "--ignore-not-found", "--wait=false"); err != nil {
			return fmt.Errorf("delete Agent: %w", err)
		}

		drainErr := u.RunWithSpinner(
			fmt.Sprintf("Waiting for Agent %s/%s finalizer to drain", ns, name),
			func() error {
				return waitForAgentGone(cfg, name, ns, agentDeleteWaitTimeout)
			},
		)
		if drainErr != nil {
			if !force {
				return fmt.Errorf("%w\n\nThe controller hasn't drained the Agent finalizer. "+
					"Re-run with --force to strip the finalizer and complete the deletion locally.",
					drainErr)
			}
			u.Warnf("Drain timed out; stripping Agent finalizer (--force)")
			if err := stripAgentFinalizers(cfg, name, ns); err != nil {
				return fmt.Errorf("force-strip finalizer: %w", err)
			}
			// One more wait pass — finalizer stripped, the CR should
			// drop within a second or two. Short timeout: if this also
			// stalls, something more fundamental is wrong.
			if err := waitForAgentGone(cfg, name, ns, 10*time.Second); err != nil {
				return fmt.Errorf("agent still present after finalizer strip: %w", err)
			}
		}
		u.Successf("Agent %s/%s deleted", ns, name)

		// The agent finalizer tears down the agent's children but
		// deliberately leaves the namespace — and nothing deletes the
		// agent's ServiceOffers, which would otherwise survive stuck on
		// WaitingForAgent and, worse, reconcile back to Ready (selling
		// to the DELETED agent's payTo) if an agent with the same name
		// is ever recreated. Delete them here; the offer finalizer
		// cleans up the route/middleware/registration children.
		if err := kubectl.Run(bin, kc, "delete", "serviceoffers.obol.org", "--all", "-n", ns, "--ignore-not-found", "--wait=false"); err != nil {
			u.Warnf("could not delete ServiceOffers in %s: %v — clean up with `obol sell delete <offer> -n %s`", ns, err, ns)
		}

		// Drop the resume-ledger entries for those offers. Scoped to the
		// cluster-reachable branch on purpose: when the cluster is
		// unreachable the CRs survive, and the ledger must keep covering
		// them.
		if removed, err := removePersistedServiceOffersInNamespace(cfg, ns); err != nil {
			u.Warnf("could not clean persisted sell offers for %s: %v", ns, err)
		} else if removed > 0 {
			u.Dim(fmt.Sprintf("Removed %d persisted sell offer manifest(s) for %s", removed, ns))
		}
	} else {
		u.Dim("Cluster unreachable; skipping CR deletion (host-side files only)")
	}

	// Wipe host-side data root for this agent. Confirm via dim log so the
	// user can see what we removed; intentionally no prompt because
	// `obol agent delete` already prompts for confirmation upstream.
	root := filepath.Dir(filepath.Dir(agentcrd.HostHomePath(cfg, name))) // .../<DataDir>/agent-<name>
	if _, err := os.Stat(root); err == nil {
		if err := os.RemoveAll(root); err != nil {
			return fmt.Errorf("remove host data dir %s: %w", root, err)
		}
		u.Dim("Removed host data dir " + root)
	}
	return nil
}

// waitForAgentGone polls for the Agent CR's absence. Returns nil once
// kubectl reports the CR is gone (either fully GC'd or never existed),
// and a timeout error otherwise. The caller decides what to do with a
// timeout — surface to the user or escalate to a finalizer strip.
func waitForAgentGone(cfg *config.Config, name, ns string, timeout time.Duration) error {
	bin, kc := kubectl.Paths(cfg)
	deadline := time.Now().Add(timeout)
	for {
		out, err := kubectl.Output(bin, kc, "get", "agent", name, "-n", ns, "-o", "name", "--ignore-not-found")
		if err != nil {
			return err
		}
		if strings.TrimSpace(out) == "" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for Agent %s/%s to be removed", timeout, ns, name)
		}
		time.Sleep(time.Second)
	}
}

// stripAgentFinalizers clears spec.metadata.finalizers via a JSON merge
// patch. The K8s GC then completes the deletion that was stuck. Used by
// deleteCRDAgent under --force when the controller can't drain the
// finalizer (typically a stale controller image).
func stripAgentFinalizers(cfg *config.Config, name, ns string) error {
	bin, kc := kubectl.Paths(cfg)
	return kubectl.Run(bin, kc, "patch", "agent", name, "-n", ns,
		"--type=merge", "-p", `{"metadata":{"finalizers":[]}}`)
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

func shouldIncludeCRDAgents(runtimeValue string) bool {
	switch strings.ToLower(strings.TrimSpace(runtimeValue)) {
	case "", "all":
		return true
	default:
		return false
	}
}

func validateAgentNewMode(useCRDPath, runtimeSet, idSet, forceSet, noSyncSet bool) error {
	if !useCRDPath {
		return nil
	}
	var legacy []string
	if runtimeSet {
		legacy = append(legacy, "--runtime")
	}
	if idSet {
		legacy = append(legacy, "--id")
	}
	if forceSet {
		legacy = append(legacy, "--force")
	}
	if noSyncSet {
		legacy = append(legacy, "--no-sync")
	}
	if len(legacy) == 0 {
		return nil
	}
	return fmt.Errorf("CRD agent creation does not support legacy flags %s; use `obol agent new <name> --model/--skills/--objective/--create-wallet` or drop the positional name for legacy runtime onboarding", strings.Join(legacy, ", "))
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
