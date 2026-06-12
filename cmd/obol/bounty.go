package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ObolNetwork/obol-stack/internal/bounty"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli/v3"
)

// bountyCommand is the demand-side counterpart to `obol sell`: post a
// ServiceBounty (escrowed reward for work) instead of a ServiceOffer. Task
// types are discovered dynamically from the embedded catalog — exactly like
// `obol network install <chain>` builds a subcommand per embedded network — so
// `obol bounty post` lists only the types live in this release.
func bountyCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "bounty",
		Usage: "Post and manage ServiceBounties (demand-side: pay for benchmarks, fine-tunes, serving)",
		Commands: []*cli.Command{
			{
				Name:     "post",
				Usage:    "Post a bounty for a task type (run `obol bounty post` to list the available types)",
				Commands: buildBountyPostCommands(cfg),
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return cli.ShowSubcommandHelp(cmd)
				},
			},
			bountyTypesCommand(cfg),
			bountyListCommand(cfg),
			bountyStatusCommand(cfg),
			bountyClaimCommand(cfg),
			bountySubmitCommand(cfg),
			bountyVerdictCommand(cfg, "accept", "Accept the submission (poster verdict; releases the escrowed reward)"),
			bountyVerdictCommand(cfg, "reject", "Reject the submission (poster verdict; escrow stays held until deadline refund)"),
			bountyEvalCommand(cfg),
		},
	}
}

// bountyEvalCommand carries the evaluator-side commit-reveal verbs. Commitments
// are address-bound (hash includes the evaluator address) and the controller
// opens the reveal window only after K commitments are in — committing first
// and revealing later is the protocol, not a convenience.
func bountyEvalCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "eval",
		Usage: "Evaluator verbs: enroll in the pool, commit and reveal quorum scores",
		Commands: []*cli.Command{
			{
				Name:      "enroll",
				Usage:     "Enroll as an evaluator (joins the selection pool at the Shadow tier)",
				ArgsUsage: "<name>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "namespace", Aliases: []string{"n"}, Usage: "Namespace", Value: "hermes-obol-agent"},
					&cli.StringFlag{Name: "address", Usage: "[REQUIRED] Evaluator payout/identity address (0x...)", Required: true},
					&cli.StringFlag{Name: "task-types", Usage: "Comma-separated task-type refs you can re-run", Value: "benchmark@v1"},
					&cli.StringFlag{Name: "attestation-scheme", Usage: "Device attestation scheme [none|secure-enclave]", Value: "none"},
					&cli.BoolFlag{Name: "dry-run", Usage: "Print the EvaluatorEnrollment manifest instead of applying it"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					name := cmd.Args().First()
					if name == "" {
						return fmt.Errorf("missing enrollment name: obol bounty eval enroll <name> --address 0x...")
					}
					enrollment := monetizeapi.EvaluatorEnrollment{
						TypeMeta: metav1.TypeMeta{
							APIVersion: monetizeapi.Group + "/" + monetizeapi.Version,
							Kind:       monetizeapi.EvaluatorEnrollmentKind,
						},
						ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cmd.String("namespace")},
						Spec: monetizeapi.EvaluatorEnrollmentSpec{
							Address:     cmd.String("address"),
							TaskTypes:   strings.Split(cmd.String("task-types"), ","),
							Attestation: monetizeapi.EvaluatorAttestation{Scheme: cmd.String("attestation-scheme")},
						},
					}
					if cmd.Bool("dry-run") {
						out, err := json.MarshalIndent(enrollment, "", "  ")
						if err != nil {
							return err
						}
						fmt.Printf("# EvaluatorEnrollment (dry-run)\n%s\n", out)
						return nil
					}
					out, err := kubectlApplyOutput(cfg, enrollment)
					if err != nil {
						return fmt.Errorf("apply EvaluatorEnrollment: %w", err)
					}
					fmt.Print(out)
					fmt.Println("Enrolled at the Shadow tier: you'll be randomly assigned shadow seats; agreements with the quorum median climb the ladder.")
					return nil
				},
			},
			{
				Name:  "pool",
				Usage: "List the enrolled evaluator pool",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "namespace", Aliases: []string{"n"}, Usage: "Namespace (default: all namespaces)"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					bin, kc := kubectl.Paths(cfg)
					args := []string{"get", "evaluatorenrollments.obol.org", "-o", "wide"}
					if ns := cmd.String("namespace"); ns != "" {
						args = append(args, "-n", ns)
					} else {
						args = append(args, "-A")
					}
					out, err := kubectl.Output(bin, kc, args...)
					if err != nil {
						return err
					}
					fmt.Print(out)
					return nil
				},
			},
			{
				Name:      "commit",
				Usage:     "Commit your score (only the address-bound hash is published; keep the salt for reveal)",
				ArgsUsage: "<name>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "namespace", Aliases: []string{"n"}, Usage: "Namespace", Value: "hermes-obol-agent"},
					&cli.StringFlag{Name: "address", Usage: "[REQUIRED] Evaluator address (0x...)", Required: true},
					&cli.IntFlag{Name: "score", Usage: "[REQUIRED] Verdict score 0-100 (>=50 verifies)", Required: true},
					&cli.StringFlag{Name: "salt", Usage: "[REQUIRED] Random salt — KEEP IT; the reveal is unverifiable without it", Required: true},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					name := cmd.Args().First()
					if name == "" {
						return fmt.Errorf("missing bounty name: obol bounty eval commit <name> --address 0x... --score N --salt s")
					}
					score := int64(cmd.Int("score"))
					if score < 0 || score > 100 {
						return fmt.Errorf("--score %d out of range 0-100", score)
					}
					addr := strings.ToLower(cmd.String("address"))
					hash := monetizeapi.EvalCommitHash(score, cmd.String("salt"), addr)
					fmt.Printf("Committing %s (score and salt stay local — reveal with the SAME --score and --salt)\n", hash)
					return annotateBountyCLI(cfg, cmd.String("namespace"), name,
						[]string{"obol.org/eval-commit-" + addr + "=" + hash})
				},
			},
			{
				Name:      "reveal",
				Usage:     "Reveal your committed score (accepted once K commitments are in)",
				ArgsUsage: "<name>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "namespace", Aliases: []string{"n"}, Usage: "Namespace", Value: "hermes-obol-agent"},
					&cli.StringFlag{Name: "address", Usage: "[REQUIRED] Evaluator address (0x...)", Required: true},
					&cli.IntFlag{Name: "score", Usage: "[REQUIRED] The committed score", Required: true},
					&cli.StringFlag{Name: "salt", Usage: "[REQUIRED] The committed salt", Required: true},
					&cli.StringFlag{Name: "validation-tx", Usage: "Optional ERC-8004 validationResponse tx hash you submitted on-chain (recorded as provenance)"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					name := cmd.Args().First()
					if name == "" {
						return fmt.Errorf("missing bounty name: obol bounty eval reveal <name> --address 0x... --score N --salt s")
					}
					payload := map[string]any{
						"score": int64(cmd.Int("score")),
						"salt":  cmd.String("salt"),
					}
					if tx := cmd.String("validation-tx"); tx != "" {
						payload["validationTx"] = tx
					}
					raw, err := json.Marshal(payload)
					if err != nil {
						return err
					}
					addr := strings.ToLower(cmd.String("address"))
					return annotateBountyCLI(cfg, cmd.String("namespace"), name,
						[]string{"obol.org/eval-reveal-" + addr + "=" + string(raw)})
				},
			},
			{
				Name:  "calldata",
				Usage: "Print ERC-8004 validationResponse calldata for your wallet to submit (the controller NEVER signs)",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "network", Usage: "Chain", Value: "base-sepolia"},
					&cli.StringFlag{Name: "request-hash", Usage: "[REQUIRED] The validation request hash (bytes32, 0x...)", Required: true},
					&cli.IntFlag{Name: "response", Usage: "[REQUIRED] Your 0-100 verdict score", Required: true},
					&cli.StringFlag{Name: "response-uri", Usage: "Optional URI of your evaluation report"},
					&cli.StringFlag{Name: "tag", Usage: "Optional tag (e.g. the task type ref)"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					response := cmd.Int("response")
					if response < 0 || response > 100 {
						return fmt.Errorf("--response %d out of range 0-100", response)
					}
					registry, err := erc8004.ValidationRegistryAddress(cmd.String("network"))
					if err != nil {
						return err
					}
					calldata, err := erc8004.EncodeValidationResponse(
						common.HexToHash(cmd.String("request-hash")),
						uint8(response),
						cmd.String("response-uri"),
						common.Hash{},
						cmd.String("tag"),
					)
					if err != nil {
						return err
					}
					fmt.Printf("ValidationRegistry (%s): %s\n", cmd.String("network"), registry)
					fmt.Printf("Calldata: 0x%x\n", calldata)
					fmt.Println("Submit with YOUR wallet (e.g. the agent remote-signer or cast send) — then pass the tx hash to `obol bounty eval reveal --validation-tx`.")
					return nil
				},
			},
		},
	}
}

// bountyTypesCommand lists the enabled task-type catalog with its eval/pricing
// policy, so an operator can see what bounties are postable and on what terms.
func bountyTypesCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "types",
		Usage: "List the available ServiceBounty task types (the dynamic catalog)",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			types, err := bounty.Enabled()
			if err != nil {
				return err
			}
			if len(types) == 0 {
				fmt.Println("No bounty task types are enabled in this release.")
				return nil
			}
			for _, t := range types {
				fmt.Printf("• %-14s %s\n", t.Ref(), t.Summary)
				fmt.Printf("    runner=%s  acceptance=%s  eval-k=%d paid-in=%s/%s  hardware-proof=%s\n",
					t.Runner, t.Acceptance.Method, t.Eval.DefaultK,
					t.Eval.Payment.Asset, t.Eval.Payment.Settle, t.HardwareProof)
			}
			return nil
		},
	}
}

// commonBountyFlags are shared by every `obol bounty post <type>` subcommand.
// The bounty name is positional (`obol bounty post benchmark <name>`), matching
// `obol sell http <name>`.
func commonBountyFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "namespace", Aliases: []string{"n"}, Usage: "Namespace for the ServiceBounty", Value: "hermes-obol-agent"},
		&cli.StringFlag{Name: "model", Usage: "Target model id (spec.task.targetModel.name)"},
		&cli.StringFlag{Name: "runtime", Usage: "Target model runtime", Value: "vllm"},
		&cli.StringFlag{Name: "reward", Usage: "[REQUIRED] Reward amount in human units (e.g. 500.00)", Required: true},
		&cli.StringFlag{Name: "asset", Usage: "Reward asset symbol", Value: "USDC"},
		&cli.StringFlag{Name: "chain", Usage: "Payment network", Value: "base"},
		&cli.StringFlag{Name: "pay-to", Usage: "Escrow-return / poster address (0x...)"},
		&cli.StringFlag{Name: "escrow-scheme", Usage: "x402 escrow scheme [upto|authCapture]", Value: "upto"},
		&cli.StringFlag{Name: "facilitator", Usage: "x402 facilitator URL", Value: "https://x402.gcp.obol.tech"},
		&cli.StringFlag{Name: "deadline", Usage: "RFC3339 deadline (e.g. 2026-07-01T00:00:00Z)"},
		&cli.IntFlag{Name: "max-fulfillers", Usage: "Max paid fulfillers (1 = single-winner)", Value: 1},
		&cli.IntFlag{Name: "eval-k", Usage: "Evaluators to sample (defaults to the task type's defaultK)"},
		&cli.BoolFlag{Name: "dangerously-skip-verification", Usage: "Skip the evaluator quorum: poster-as-judge, bounty marked unverified, no reputation feedback emitted"},
		&cli.StringFlag{Name: "hardware-proof", Usage: "Hardware proof strength [self-report|gpu-attestation|evaluator-measured] (defaults to the task type's policy)"},
		&cli.StringFlag{Name: "tolerance", Usage: "Per-metric acceptance bands, metric=band pairs (e.g. totalScore=0.05,mmlu=0.01); overlays the task type's defaults"},
		&cli.StringFlag{Name: "dataset-commit", Usage: "Merkle root committing the (partially private) eval dataset"},
		&cli.StringFlag{Name: "private-fraction", Usage: "Fraction of dataset rows kept private, 0..1 (e.g. 0.2); revealed only to sampled evaluators"},
		&cli.StringFlag{Name: "bond", Usage: "Optional refundable self-bond amount (own funds; never slashed)"},
		&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "Skip the cost-preview confirmation"},
		&cli.BoolFlag{Name: "dry-run", Usage: "Print the ServiceBounty manifest instead of applying it"},
	}
}

// buildBountyPostCommands creates one `post` subcommand per ENABLED task type,
// with flags generated from that type's param schema.
func buildBountyPostCommands(cfg *config.Config) []*cli.Command {
	types, err := bounty.Enabled()
	if err != nil {
		return nil
	}

	var commands []*cli.Command
	for _, t := range types {
		flags := commonBountyFlags()
		for _, p := range t.Params {
			usage := p.Description
			if usage == "" {
				usage = "Set " + p.Name
			}
			if len(p.Enum) > 0 {
				usage += fmt.Sprintf(" [options: %s]", strings.Join(p.Enum, ", "))
			}
			required := p.Required && p.Default == ""
			if required {
				usage = "[REQUIRED] " + usage
			}
			flags = append(flags, &cli.StringFlag{
				Name:     paramFlagName(p.Name),
				Usage:    usage,
				Value:    p.Default,
				Required: required,
			})
		}

		tt := t // capture for the closure
		commands = append(commands, &cli.Command{
			Name:      tt.ID,
			Usage:     tt.Summary,
			ArgsUsage: "<name>",
			Flags:     flags,
			Action: func(ctx context.Context, cmd *cli.Command) error {
				return postBounty(cfg, ui.New(false), cmd, tt)
			},
		})
	}

	return commands
}

// paramFlagName converts a task-package param name to the CLI's kebab-case
// flag convention, e.g. hardwareClass -> hardware-class (the same mapping
// network.fieldNameToFlagName applies to template fields).
func paramFlagName(param string) string {
	var b strings.Builder
	for i, r := range param {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteRune('-')
		}
		b.WriteRune(r)
	}

	return strings.ToLower(b.String())
}

// postBounty builds a ServiceBounty CR from the flags + task-type defaults,
// shows the two-leg cost preview (reward escrow + OBOL eval bill), confirms in
// a TTY, and applies the manifest.
func postBounty(cfg *config.Config, u *ui.UI, cmd *cli.Command, t bounty.TaskType) error {
	name := cmd.Args().First()
	if name == "" {
		return fmt.Errorf("missing bounty name: obol bounty post %s <name> [flags]", t.ID)
	}

	// Collect + validate the type's params against its schema. Flags are the
	// kebab-case form of the param name; the CR keeps the package's name.
	params := make(map[string]string)
	for _, p := range t.Params {
		flag := paramFlagName(p.Name)
		v := cmd.String(flag)
		if v == "" {
			v = p.Default
		}
		if p.Required && v == "" {
			return fmt.Errorf("--%s is required for task type %s", flag, t.Ref())
		}
		if len(p.Enum) > 0 && v != "" && !slices.Contains(p.Enum, v) {
			return fmt.Errorf("--%s=%q is not one of [%s]", flag, v, strings.Join(p.Enum, ", "))
		}
		if v != "" {
			params[p.Name] = v
		}
	}

	evalK := int64(cmd.Int("eval-k"))
	if evalK == 0 {
		evalK = int64(t.Eval.DefaultK)
	}

	evalMode := monetizeapi.EvalModeRequired
	if cmd.Bool("dangerously-skip-verification") {
		evalMode = monetizeapi.EvalModeDangerouslySkipped
	}

	hardwareProof := cmd.String("hardware-proof")
	if hardwareProof == "" {
		hardwareProof = t.HardwareProof
	}
	switch hardwareProof {
	case "", "self-report", "gpu-attestation", "evaluator-measured":
	default:
		return fmt.Errorf("--hardware-proof=%q is not one of [self-report, gpu-attestation, evaluator-measured]", hardwareProof)
	}

	// Tolerance: the task type's bands, overlaid by --tolerance metric=band
	// pairs (BenchLocal-style packs have their own metric keys).
	tolerance := make(map[string]string, len(t.Acceptance.Tolerance))
	for k, v := range t.Acceptance.Tolerance {
		tolerance[k] = v
	}
	if raw := cmd.String("tolerance"); raw != "" {
		for _, pair := range strings.Split(raw, ",") {
			metric, band, ok := strings.Cut(strings.TrimSpace(pair), "=")
			if !ok || metric == "" || band == "" {
				return fmt.Errorf("--tolerance entry %q is not metric=band", pair)
			}
			tolerance[metric] = band
		}
	}

	var deadline *metav1.Time
	if d := cmd.String("deadline"); d != "" {
		parsed, err := time.Parse(time.RFC3339, d)
		if err != nil {
			return fmt.Errorf("--deadline %q is not RFC3339 (e.g. 2026-07-01T00:00:00Z): %w", d, err)
		}
		deadline = &metav1.Time{Time: parsed}
	}

	sb := monetizeapi.ServiceBounty{
		TypeMeta: metav1.TypeMeta{
			APIVersion: monetizeapi.Group + "/" + monetizeapi.Version,
			Kind:       monetizeapi.ServiceBountyKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cmd.String("namespace"),
		},
		Spec: monetizeapi.ServiceBountySpec{
			Task: monetizeapi.ServiceBountyTask{
				TypeRef:       t.Ref(),
				Params:        params,
				TargetModel:   monetizeapi.ServiceOfferModel{Name: cmd.String("model"), Runtime: cmd.String("runtime")},
				HardwareProof: hardwareProof,
				DatasetCommit: monetizeapi.ServiceBountyDatasetCommit{
					Root:            cmd.String("dataset-commit"),
					PrivateFraction: cmd.String("private-fraction"),
				},
			},
			Acceptance: monetizeapi.ServiceBountyAcceptance{
				Method:       t.Acceptance.Method,
				Tolerance:    tolerance,
				CommitReveal: t.Acceptance.CommitReveal,
			},
			Reward: monetizeapi.ServiceBountyReward{
				Network: cmd.String("chain"),
				PayTo:   cmd.String("pay-to"),
				Asset:   monetizeapi.ServiceOfferAsset{Symbol: cmd.String("asset")},
				Amount:  cmd.String("reward"),
				Escrow: monetizeapi.ServiceBountyEscrow{
					Scheme:      cmd.String("escrow-scheme"),
					Facilitator: cmd.String("facilitator"),
					Mode:        "auto",
				},
			},
			Eval: monetizeapi.ServiceBountyEval{
				K:         evalK,
				Mode:      evalMode,
				Selection: t.Eval.Selection,
				Payment: monetizeapi.ServiceBountyEvalPayment{
					Asset:        t.Eval.Payment.Asset,
					PerEvaluator: t.Eval.Payment.PerEvaluator,
					FundedBy:     t.Eval.Payment.FundedBy,
					Settle:       t.Eval.Payment.Settle,
				},
			},
			Trust:         monetizeapi.ServiceBountyTrust{ReputationGate: true},
			Deadline:      deadline,
			MaxFulfillers: int64(cmd.Int("max-fulfillers")),
		},
	}

	if bond := cmd.String("bond"); bond != "" {
		sb.Spec.Trust.SelfBond = monetizeapi.ServiceBountySelfBond{Required: true, Amount: bond, Token: cmd.String("asset")}
	}

	if cmd.Bool("dry-run") {
		out, err := json.MarshalIndent(sb, "", "  ")
		if err != nil {
			return err
		}
		fmt.Printf("# ServiceBounty (dry-run)\n%s\n", out)
		return nil
	}

	printBountyCostPreview(u, &sb, t)
	if !cmd.Bool("yes") && !u.Confirm("Proceed?", true) {
		return fmt.Errorf("aborted")
	}

	applyOut, err := kubectlApplyOutput(cfg, sb)
	if err != nil {
		return fmt.Errorf("apply ServiceBounty: %w", err)
	}
	fmt.Print(applyOut)
	fmt.Printf("\nBounty posted. Check status: obol bounty status %s -n %s\n", name, sb.Namespace)
	return nil
}

// printBountyCostPreview shows the poster's full commitment before apply: the
// escrowed reward leg AND the OBOL eval bill (k × perEvaluator, paid to
// evaluators win-or-lose). Verification-by-default means the eval line is the
// part posters haven't already priced in — never let it surprise them.
func printBountyCostPreview(u *ui.UI, sb *monetizeapi.ServiceBounty, t bounty.TaskType) {
	u.Print("──────────────────────────────────────────────────────────────")
	u.Print(fmt.Sprintf("  Bounty:        %s (%s)", sb.Name, sb.Spec.Task.TypeRef))
	u.Print(fmt.Sprintf("  Reward:        %s %s on %s (%s escrow)",
		sb.Spec.Reward.Amount, sb.Spec.Reward.Asset.Symbol, sb.Spec.Reward.Network, sb.Spec.Reward.Escrow.Scheme))
	if sb.Spec.Eval.Mode == monetizeapi.EvalModeDangerouslySkipped {
		u.Warnf("  Verification:  SKIPPED (--dangerously-skip-verification) — poster-as-judge, bounty marked unverified, no reputation feedback")
	} else {
		per := sb.Spec.Eval.Payment.PerEvaluator
		line := fmt.Sprintf("  Verification:  %d evaluators × %s %s", sb.Spec.Eval.K, per, sb.Spec.Eval.Payment.Asset)
		if perF, err := strconv.ParseFloat(per, 64); err == nil {
			line += fmt.Sprintf(" = %.2f %s", float64(sb.Spec.Eval.K)*perF, sb.Spec.Eval.Payment.Asset)
		}
		u.Print(line + " (poster-funded, paid win-or-lose)")
	}
	if sb.Spec.Trust.SelfBond.Required {
		u.Print(fmt.Sprintf("  Fulfiller bond: %s %s (refundable; forfeited on rejected work)", sb.Spec.Trust.SelfBond.Amount, sb.Spec.Trust.SelfBond.Token))
	}
	if sb.Spec.Deadline != nil {
		u.Print(fmt.Sprintf("  Deadline:      %s (auto-refund past it)", sb.Spec.Deadline.UTC().Format(time.RFC3339)))
	}
	u.Print("──────────────────────────────────────────────────────────────")
}

// ── lifecycle verbs ─────────────────────────────────────────────────────────
//
// claim/submit/accept/reject write the controller's annotation channel
// (obol.org/claim|commit|submit|verdict); the reconcile loop validates and
// promotes them into controller-owned status.

func bountyResource() string { return "servicebounties.obol.org" }

func bountyListCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List ServiceBounties",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "namespace", Aliases: []string{"n"}, Usage: "Namespace (default: all namespaces)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			bin, kc := kubectl.Paths(cfg)
			args := []string{"get", bountyResource()}
			if ns := cmd.String("namespace"); ns != "" {
				args = append(args, "-n", ns)
			} else {
				args = append(args, "-A")
			}
			out, err := kubectl.Output(bin, kc, args...)
			if err != nil {
				return err
			}
			fmt.Print(out)
			return nil
		},
	}
}

func bountyStatusCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "status",
		Usage:     "Show a bounty's phase, conditions, claims, and escrow state",
		ArgsUsage: "<name>",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "namespace", Aliases: []string{"n"}, Usage: "Namespace", Value: "hermes-obol-agent"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			name := cmd.Args().First()
			if name == "" {
				return fmt.Errorf("missing bounty name: obol bounty status <name>")
			}
			bin, kc := kubectl.Paths(cfg)
			out, err := kubectl.Output(bin, kc, "get", bountyResource(), name, "-n", cmd.String("namespace"), "-o", "json")
			if err != nil {
				return err
			}

			var sb monetizeapi.ServiceBounty
			if err := json.Unmarshal([]byte(out), &sb); err != nil {
				return fmt.Errorf("decode bounty: %w", err)
			}

			fmt.Printf("%s  (%s)\n", sb.Name, sb.Spec.Task.TypeRef)
			fmt.Printf("  Phase:   %s\n", sb.Status.Phase)
			fmt.Printf("  Reward:  %s %s on %s  (escrow: %s)\n", sb.Spec.Reward.Amount, sb.Spec.Reward.Asset.Symbol, sb.Spec.Reward.Network, valueOr(sb.Status.EscrowState, "not reserved"))
			if sb.Status.CaptureTxHash != "" {
				fmt.Printf("  Payout:  %s\n", sb.Status.CaptureTxHash)
			}
			if sb.Status.RefundTxHash != "" {
				fmt.Printf("  Refund:  %s\n", sb.Status.RefundTxHash)
			}
			if sb.Status.ReportURI != "" {
				fmt.Printf("  Report:  %s\n", sb.Status.ReportURI)
			}
			for _, claim := range sb.Status.Claims {
				fmt.Printf("  Claim:   %s  phase=%s commit=%s\n", claim.FulfillerAddress, claim.Phase, valueOr(claim.CommitHash, "-"))
			}
			if sb.Status.BondState != "" {
				fmt.Printf("  Bond:    %s\n", sb.Status.BondState)
			}
			if len(sb.Status.Evaluations) > 0 {
				fmt.Printf("  Evaluations (quorum k=%d, median>=50 verifies):\n", sb.Spec.Eval.K)
				if sb.Status.RevealDeadline != nil {
					fmt.Printf("    reveal window closes %s\n", sb.Status.RevealDeadline.UTC().Format(time.RFC3339))
				}
				for _, ev := range sb.Status.Evaluations {
					score := "-"
					if ev.Phase == "Revealed" {
						score = fmt.Sprintf("%d", ev.Score)
					}
					fmt.Printf("    %s  seat=%-9s phase=%-10s score=%-4s withinBand=%-5v paid=%v\n",
						ev.Address, valueOr(ev.Seat, "open"), ev.Phase, score, ev.WithinBand, ev.Paid)
				}
				if sb.Status.EvalBudgetState != "" {
					fmt.Printf("    eval budget: %s", sb.Status.EvalBudgetState)
					if sb.Status.EvalPayoutTxHash != "" {
						fmt.Printf("  payout=%s", sb.Status.EvalPayoutTxHash)
					}
					fmt.Println()
				}
			}
			fmt.Println("  Conditions:")
			for _, condition := range sb.Status.Conditions {
				fmt.Printf("    %-15s %-5s %-22s %s\n", condition.Type, condition.Status, condition.Reason, condition.Message)
			}
			return nil
		},
	}
}

func bountyClaimCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "claim",
		Usage:     "Claim a bounty as a fulfiller (binds your payout address)",
		ArgsUsage: "<name>",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "namespace", Aliases: []string{"n"}, Usage: "Namespace", Value: "hermes-obol-agent"},
			&cli.StringFlag{Name: "address", Usage: "[REQUIRED] Fulfiller payout address (0x...)", Required: true},
			&cli.StringFlag{Name: "commit", Usage: "Optional commit hash (binds you to a specific deliverable before reveal)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			name := cmd.Args().First()
			if name == "" {
				return fmt.Errorf("missing bounty name: obol bounty claim <name> --address 0x...")
			}
			annotations := []string{"obol.org/claim=" + cmd.String("address")}
			if commit := cmd.String("commit"); commit != "" {
				annotations = append(annotations, "obol.org/commit="+commit)
			}
			return annotateBountyCLI(cfg, cmd.String("namespace"), name, annotations)
		},
	}
}

func bountySubmitCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "submit",
		Usage:     "Submit a deliverable for a claimed bounty",
		ArgsUsage: "<name>",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "namespace", Aliases: []string{"n"}, Usage: "Namespace", Value: "hermes-obol-agent"},
			&cli.StringFlag{Name: "result-hash", Usage: "[REQUIRED] Hash of the deliverable (reveals the commit)", Required: true},
			&cli.StringFlag{Name: "report-uri", Usage: "URI of the A2UI report (local agent hierarchy in v1)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			name := cmd.Args().First()
			if name == "" {
				return fmt.Errorf("missing bounty name: obol bounty submit <name> --result-hash 0x...")
			}
			submission, err := json.Marshal(map[string]string{
				"resultHash": cmd.String("result-hash"),
				"reportURI":  cmd.String("report-uri"),
			})
			if err != nil {
				return err
			}
			return annotateBountyCLI(cfg, cmd.String("namespace"), name, []string{"obol.org/submit=" + string(submission)})
		},
	}
}

func bountyVerdictCommand(cfg *config.Config, verdict, usage string) *cli.Command {
	return &cli.Command{
		Name:      verdict,
		Usage:     usage,
		ArgsUsage: "<name>",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "namespace", Aliases: []string{"n"}, Usage: "Namespace", Value: "hermes-obol-agent"},
			&cli.StringFlag{Name: "reason", Usage: "Reason (recorded in the Verified condition; reject only)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			name := cmd.Args().First()
			if name == "" {
				return fmt.Errorf("missing bounty name: obol bounty %s <name>", verdict)
			}
			value := verdict
			if verdict == "reject" {
				value = "reject:" + cmd.String("reason")
			}
			return annotateBountyCLI(cfg, cmd.String("namespace"), name, []string{"obol.org/verdict=" + value})
		},
	}
}

func annotateBountyCLI(cfg *config.Config, namespace, name string, annotations []string) error {
	bin, kc := kubectl.Paths(cfg)
	args := append([]string{"annotate", bountyResource(), name, "-n", namespace, "--overwrite"}, annotations...)
	out, err := kubectl.Output(bin, kc, args...)
	if err != nil {
		return err
	}
	fmt.Print(out)
	fmt.Printf("Check status: obol bounty status %s -n %s\n", name, namespace)
	return nil
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
