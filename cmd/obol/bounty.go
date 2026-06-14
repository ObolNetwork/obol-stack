package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
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
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/ObolNetwork/obol-stack/internal/x402/escrow"
	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/urfave/cli/v3"
)

// Voucher ferry annotations — must match the serviceoffer-controller's
// bounty_eval.go constants exactly (the CLI writes, the controller reads; the
// controller never signs and escrow endpoint/credentials never ride in here).
const (
	bountyRewardVoucherAnnotation = "obol.org/reward-voucher"
	bountyBondVoucherAnnotation   = "obol.org/bond-voucher"
	bountyEvalVoucherAnnotation   = "obol.org/eval-voucher"
	bountyEvalVoucherR1Annotation = "obol.org/eval-voucher-r1"
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
			bountyFundCommand(cfg),
			bountyClaimCommand(cfg),
			bountySubmitCommand(cfg),
			bountyFeedbackCommand(cfg),
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
					&cli.StringFlag{Name: "namespace", Aliases: []string{"n"}, Usage: "Namespace (with --bounty)", Value: "hermes-obol-agent"},
					&cli.StringFlag{Name: "network", Usage: "Chain", Value: "base-sepolia"},
					&cli.StringFlag{Name: "bounty", Usage: "Bounty name — derives the request hash from the bounty UID + --address"},
					&cli.StringFlag{Name: "address", Usage: "Your evaluator address (0x...; required with --bounty)"},
					&cli.StringFlag{Name: "request-hash", Usage: "Explicit validation request hash (bytes32, 0x...) — overrides --bounty derivation"},
					&cli.IntFlag{Name: "response", Usage: "[REQUIRED] Your 0-100 verdict score", Required: true},
					&cli.StringFlag{Name: "response-uri", Usage: "Optional URI of your evaluation report"},
					&cli.StringFlag{Name: "tag", Usage: "Optional tag (e.g. the task type ref)"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					response := cmd.Int("response")
					if response < 0 || response > 100 {
						return fmt.Errorf("--response %d out of range 0-100", response)
					}
					requestHash, err := resolveEvalRequestHash(cfg, cmd)
					if err != nil {
						return err
					}
					registry, err := erc8004.ValidationRegistryAddress(cmd.String("network"))
					if err != nil {
						return err
					}
					calldata, err := erc8004.EncodeValidationResponse(
						requestHash,
						uint8(response),
						cmd.String("response-uri"),
						common.Hash{},
						cmd.String("tag"),
					)
					if err != nil {
						return err
					}
					fmt.Printf("Request hash: %s\n", requestHash.Hex())
					fmt.Printf("ValidationRegistry (%s): %s\n", cmd.String("network"), registry)
					fmt.Printf("Calldata: 0x%x\n", calldata)
					fmt.Println("Submit with YOUR wallet (e.g. the agent remote-signer or cast send) — then pass the tx hash to `obol bounty eval reveal --validation-tx`.")
					return nil
				},
			},
			bountyEvalFundCommand(cfg),
		},
	}
}

// resolveEvalRequestHash returns the explicit --request-hash override, or
// derives the hash from the named bounty's UID + the evaluator --address via
// erc8004.BountyEvalRequestHash (the controller grounds reveals against the
// exact same derivation).
func resolveEvalRequestHash(cfg *config.Config, cmd *cli.Command) (common.Hash, error) {
	if raw := strings.TrimSpace(cmd.String("request-hash")); raw != "" {
		return common.HexToHash(raw), nil
	}
	name := strings.TrimSpace(cmd.String("bounty"))
	address := strings.TrimSpace(cmd.String("address"))
	if name == "" || address == "" {
		return common.Hash{}, fmt.Errorf("pass --request-hash 0x..., or --bounty <name> with --address 0x... to derive it from the bounty UID")
	}
	if !common.IsHexAddress(address) {
		return common.Hash{}, fmt.Errorf("--address %q is not a 0x address", address)
	}
	sb, err := getBountyCLI(cfg, cmd.String("namespace"), name)
	if err != nil {
		return common.Hash{}, err
	}
	if sb.UID == "" {
		return common.Hash{}, fmt.Errorf("bounty %s has no UID — cannot derive the request hash", name)
	}
	return erc8004.BountyEvalRequestHash(string(sb.UID), address), nil
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
			namespace := cmd.String("namespace")
			sb, err := getBountyCLI(cfg, namespace, name)
			if err != nil {
				return err
			}

			fmt.Printf("%s  (%s)\n", sb.Name, sb.Spec.Task.TypeRef)
			fmt.Printf("  Phase:   %s\n", sb.Status.Phase)
			fmt.Printf("  Reward:  %s %s on %s  (escrow: %s)\n", sb.Spec.Reward.Amount, sb.Spec.Reward.Asset.Symbol, sb.Spec.Reward.Network, valueOr(sb.Status.EscrowState, "not reserved"))
			if sb.Status.EscrowSpender != "" {
				fmt.Printf("  Escrow spender: %s  (bind your Permit2 vouchers to this executor)\n", sb.Status.EscrowSpender)
			}
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
			if seed := sb.Status.PanelSeed; seed != nil {
				fmt.Printf("  Panel seed: source=%s", seed.Source)
				if seed.Round > 0 {
					fmt.Printf(" round=%d", seed.Round)
				}
				fmt.Println()
			}
			if len(sb.Status.Evaluations) > 0 {
				fmt.Printf("  Evaluations (quorum k=%d, median>=50 verifies):\n", sb.Spec.Eval.K)
				if sb.Status.RevealDeadline != nil {
					fmt.Printf("    reveal window closes %s\n", sb.Status.RevealDeadline.UTC().Format(time.RFC3339))
				}
				printBountyEvaluations(sb.Status.Evaluations, "    ")
				if sb.Status.EvalBudgetState != "" {
					fmt.Printf("    eval budget: %s", sb.Status.EvalBudgetState)
					if sb.Status.EvalPayoutTxHash != "" {
						fmt.Printf("  payout=%s", sb.Status.EvalPayoutTxHash)
					}
					fmt.Println()
				}
			}
			if esc := sb.Status.Escalation; esc != nil {
				fmt.Printf("  Escalation (round %d): %s\n", esc.Round, valueOr(esc.Reason, "-"))
				fmt.Printf("    budget: %s\n", valueOr(esc.BudgetState, "not reserved"))
				if esc.VoucherDeadline != nil {
					fmt.Printf("    voucher deadline %s\n", esc.VoucherDeadline.UTC().Format(time.RFC3339))
				}
				if esc.RevealDeadline != nil {
					fmt.Printf("    reveal window closes %s\n", esc.RevealDeadline.UTC().Format(time.RFC3339))
				}
				for _, seat := range esc.Panel {
					fmt.Printf("    panel: %s  seat=%s\n", seat.Address, seat.Seat)
				}
				printBountyEvaluations(esc.Evaluations, "    ")
			}
			fmt.Println("  Conditions:")
			for _, condition := range sb.Status.Conditions {
				fmt.Printf("    %-15s %-5s %-22s %s\n", condition.Type, condition.Status, condition.Reason, condition.Message)
			}
			printBountyVoucherNextSteps(sb, namespace)
			return nil
		},
	}
}

func bountyClaimCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "claim",
		Usage:     "Claim a bounty as a fulfiller (binds your payout address; optionally sign the self-bond voucher)",
		ArgsUsage: "<name>",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "namespace", Aliases: []string{"n"}, Usage: "Namespace", Value: "hermes-obol-agent"},
			&cli.StringFlag{Name: "address", Usage: "[REQUIRED] Fulfiller payout address (0x...)", Required: true},
			&cli.StringFlag{Name: "commit", Usage: "Optional commit hash (binds you to a specific deliverable before reveal)"},
			&cli.StringFlag{Name: "bond-key", Usage: "Hex private key to sign the self-bond Permit2 voucher locally (or use --bond-signer-url)"},
			&cli.StringFlag{Name: "bond-signer-url", Usage: "Remote-signer base URL to sign the self-bond voucher without exposing a key"},
			&cli.StringFlag{Name: "bond-recipient", Usage: "Bond forfeiture recipient (default: the poster's spec.reward.payTo address)"},
			&cli.StringFlag{Name: "spender", Usage: "Escrow facilitator address to bind as the only executor (default: status.escrowSpender)"},
			&cli.IntFlag{Name: "deadline-hours", Usage: "Bond voucher expiry in hours from now", Value: 72},
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
			if err := annotateBountyCLI(cfg, cmd.String("namespace"), name, annotations); err != nil {
				return err
			}

			// Optional self-bond voucher: the FULFILLER's own funds, signed by
			// their wallet (never the controller's), forfeited to the poster
			// only on rejected work.
			bondKey, bondSigner := cmd.String("bond-key"), cmd.String("bond-signer-url")
			if bondKey == "" && bondSigner == "" {
				return nil
			}
			return attachBountyBondVoucher(ctx, cfg, cmd, name, bondKey, bondSigner)
		},
	}
}

// attachBountyBondVoucher builds, signs, and ferries the fulfiller's self-bond
// voucher (annotation obol.org/bond-voucher, nonce leg bond). The recipient is
// the poster's payout address (spec.reward.payTo) — the bond is forfeited TO
// the poster on rejected work — overridable / required via --bond-recipient
// when the spec field is absent.
func attachBountyBondVoucher(ctx context.Context, cfg *config.Config, cmd *cli.Command, name, bondKey, bondSigner string) error {
	namespace := cmd.String("namespace")
	sb, err := getBountyCLI(cfg, namespace, name)
	if err != nil {
		return err
	}
	bond := sb.Spec.Trust.SelfBond
	if strings.TrimSpace(bond.Amount) == "" {
		return fmt.Errorf("bounty %s declares no self-bond (spec.trust.selfBond.amount is empty) — nothing to sign", name)
	}
	recipient := cmd.String("bond-recipient")
	if recipient == "" {
		recipient = sb.Spec.Reward.PayTo
	}
	if recipient == "" {
		return fmt.Errorf("bounty %s has no poster payout address (spec.reward.payTo) — pass --bond-recipient 0x... explicitly", name)
	}
	if !common.IsHexAddress(recipient) {
		return fmt.Errorf("bond recipient %q is not a 0x address", recipient)
	}

	symbol := bond.Token
	if symbol == "" {
		symbol = sb.Spec.Reward.Asset.Symbol
	}
	token, err := resolveBountyToken(symbol, sb.Spec.Reward.Network)
	if err != nil {
		return err
	}
	amount, err := humanToAtomic(bond.Amount, token.Decimals)
	if err != nil {
		return fmt.Errorf("bond amount: %w", err)
	}
	spender, err := resolveBountySpender(cmd.String("spender"), sb.Status.EscrowSpender)
	if err != nil {
		return err
	}

	voucher := escrow.Permit2Voucher{
		Token:    token.Address,
		Network:  sb.Spec.Reward.Network,
		Spender:  spender,
		Nonce:    bountyVoucherNonce(string(sb.UID), "bond"),
		Deadline: bountyVoucherDeadline(int64(cmd.Int("deadline-hours"))),
		Recipients: []escrow.BatchRecipient{
			{Address: common.HexToAddress(recipient).Hex(), Amount: amount},
		},
	}
	fmt.Printf("Self-bond: %s %s (%s atomic) -> poster %s on %s (refundable; forfeited only on rejected work)\n",
		bond.Amount, symbol, amount, common.HexToAddress(recipient).Hex(), sb.Spec.Reward.Network)
	return attachBountyVoucher(ctx, cfg, namespace, name, bountyBondVoucherAnnotation, &voucher, bondKey, bondSigner)
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

// ── poster-side voucher signing (fund / claim-bond / eval fund) ─────────────
//
// A Permit2 voucher is the poster's (or fulfiller's, for the bond) signed
// authorization the escrow facilitator executes. The CLI signs it locally
// (--key) or via the agent remote-signer (--signer-url) and ferries it to the
// controller on an annotation. The controller NEVER signs — it only attaches
// the voucher to the matching escrow reservation.

// bountyVoucherNonce derives the Permit2 unordered nonce DETERMINISTICALLY as
// the uint256 of keccak256("<bountyUID>|<leg>") with leg one of reward, bond,
// eval, eval-r1. Re-running a fund command re-signs the SAME nonce, so
// re-funding is idempotent and a nonce already consumed on-chain can never be
// double-captured.
func bountyVoucherNonce(bountyUID, leg string) string {
	return new(big.Int).SetBytes(ethcrypto.Keccak256([]byte(bountyUID + "|" + leg))).String()
}

// humanToAtomic converts a human-unit decimal amount (e.g. "500.00") to
// atomic token units ("500000000" at 6 decimals) without float rounding.
// Shared with the controller's settle paths via escrow.HumanToAtomic so the
// CLI-signed voucher seats and the controller's capture recipients can never
// drift apart in units.
func humanToAtomic(amount string, decimals int) (string, error) {
	return escrow.HumanToAtomic(amount, decimals)
}

// resolveBountySpender picks the escrow facilitator address the voucher must
// bind as its only executor: the --spender override, else status.escrowSpender
// (ferried from the facilitator's reserve receipt).
func resolveBountySpender(override, statusSpender string) (string, error) {
	if override != "" {
		if !common.IsHexAddress(override) {
			return "", fmt.Errorf("--spender %q is not a 0x address", override)
		}
		return common.HexToAddress(override).Hex(), nil
	}
	if strings.TrimSpace(statusSpender) == "" {
		return "", fmt.Errorf("status.escrowSpender is not set yet and no --spender was given — the escrow facilitator reports its address on the first reserve receipt; wait for the controller to reconcile (obol bounty status) or pass --spender 0x... explicitly")
	}
	if !common.IsHexAddress(statusSpender) {
		return "", fmt.Errorf("status.escrowSpender %q is not a 0x address — pass --spender explicitly", statusSpender)
	}
	return common.HexToAddress(statusSpender).Hex(), nil
}

// resolveBountyToken looks the payment token up in the x402 registry and
// returns its contract address + decimals for the given network.
func resolveBountyToken(symbol, network string) (x402verifier.TokenEntry, error) {
	entry, ok := x402verifier.ResolveToken(symbol, network)
	if !ok {
		return x402verifier.TokenEntry{}, fmt.Errorf("token %q is not registered on network %q (supported: %s)",
			symbol, network, strings.Join(x402verifier.SupportedTokens(), ", "))
	}
	return entry, nil
}

// signBountyVoucher signs the voucher with the local hex key or the remote
// signer, then verifies the result against the spender binding. Exactly the
// poster's wallet authorizes funds — the controller never signs.
func signBountyVoucher(ctx context.Context, v *escrow.Permit2Voucher, keyHex, signerURL string) error {
	chainID, err := escrow.ChainIDForNetwork(v.Network)
	if err != nil {
		return err
	}
	switch {
	case keyHex != "":
		key, err := ethcrypto.HexToECDSA(strings.TrimPrefix(strings.TrimPrefix(keyHex, "0x"), "0X"))
		if err != nil {
			return fmt.Errorf("parse signing key: %w", err)
		}
		if err := escrow.SignVoucher(v, chainID, key); err != nil {
			return err
		}
	case signerURL != "":
		signer := erc8004.NewRemoteSigner(signerURL)
		addr, err := signer.GetAddress(ctx)
		if err != nil {
			return err
		}
		v.Owner = addr.Hex()
		_, remote, err := escrow.VoucherTypedData(*v, chainID)
		if err != nil {
			return err
		}
		sig, err := signer.SignTypedData(ctx, addr, remote)
		if err != nil {
			return err
		}
		v.Signature = sig
	default:
		return fmt.Errorf("no signer: pass --key <hex> or --signer-url <url> — the controller NEVER signs; only your wallet can authorize funds")
	}
	return escrow.VerifyVoucher(*v, chainID, common.HexToAddress(v.Spender))
}

// attachBountyVoucher signs the voucher and ferries it to the controller on
// the given annotation.
func attachBountyVoucher(ctx context.Context, cfg *config.Config, namespace, name, annotation string, v *escrow.Permit2Voucher, keyHex, signerURL string) error {
	if err := signBountyVoucher(ctx, v, keyHex, signerURL); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	fmt.Printf("Voucher signed by %s (spender %s, nonce %s, deadline %s)\n",
		v.Owner, v.Spender, v.Nonce, time.Unix(v.Deadline, 0).UTC().Format(time.RFC3339))
	fmt.Println("Nonce is deterministic per (bounty, leg): re-running re-signs the same nonce, so re-funding is idempotent and a consumed nonce can never be double-captured.")
	return annotateBountyCLI(cfg, namespace, name, []string{annotation + "=" + string(raw)})
}

// getBountyCLI fetches and decodes one ServiceBounty.
func getBountyCLI(cfg *config.Config, namespace, name string) (*monetizeapi.ServiceBounty, error) {
	bin, kc := kubectl.Paths(cfg)
	out, err := kubectl.Output(bin, kc, "get", bountyResource(), name, "-n", namespace, "-o", "json")
	if err != nil {
		return nil, err
	}
	var sb monetizeapi.ServiceBounty
	if err := json.Unmarshal([]byte(out), &sb); err != nil {
		return nil, fmt.Errorf("decode bounty: %w", err)
	}
	return &sb, nil
}

// bountyVoucherDeadline turns --deadline-hours into a unix voucher expiry.
func bountyVoucherDeadline(hours int64) int64 {
	return time.Now().Add(time.Duration(hours) * time.Hour).Unix()
}

// bountyFundCommand signs + attaches the poster's Permit2 reward voucher:
// one recipient seat binding the claimed fulfiller to the full reward amount.
func bountyFundCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "fund",
		Usage:     "Sign + attach the reward escrow voucher (your wallet signs; the controller NEVER does)",
		ArgsUsage: "<name>",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "namespace", Aliases: []string{"n"}, Usage: "Namespace", Value: "hermes-obol-agent"},
			&cli.StringFlag{Name: "key", Usage: "Hex private key to sign the Permit2 voucher locally (or use --signer-url)"},
			&cli.StringFlag{Name: "signer-url", Usage: "Remote-signer base URL (e.g. http://127.0.0.1:9000) to sign without exposing a key"},
			&cli.StringFlag{Name: "spender", Usage: "Escrow facilitator address to bind as the only executor (default: status.escrowSpender)"},
			&cli.IntFlag{Name: "deadline-hours", Usage: "Voucher expiry in hours from now (the hard on-chain guarantee)", Value: 72},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			name := cmd.Args().First()
			if name == "" {
				return fmt.Errorf("missing bounty name: obol bounty fund <name> (--key <hex> | --signer-url <url>)")
			}
			namespace := cmd.String("namespace")
			sb, err := getBountyCLI(cfg, namespace, name)
			if err != nil {
				return err
			}
			if len(sb.Status.Claims) == 0 || sb.Status.Claims[0].FulfillerAddress == "" {
				return fmt.Errorf("bounty %s has no claim yet — the reward voucher binds the fulfiller's payout seat, so fund AFTER `obol bounty claim`", name)
			}
			fulfiller := sb.Status.Claims[0].FulfillerAddress

			token, err := resolveBountyToken(sb.Spec.Reward.Asset.Symbol, sb.Spec.Reward.Network)
			if err != nil {
				return err
			}
			amount, err := humanToAtomic(sb.Spec.Reward.Amount, token.Decimals)
			if err != nil {
				return fmt.Errorf("reward amount: %w", err)
			}
			spender, err := resolveBountySpender(cmd.String("spender"), sb.Status.EscrowSpender)
			if err != nil {
				return err
			}

			voucher := escrow.Permit2Voucher{
				Token:    token.Address,
				Network:  sb.Spec.Reward.Network,
				Spender:  spender,
				Nonce:    bountyVoucherNonce(string(sb.UID), "reward"),
				Deadline: bountyVoucherDeadline(int64(cmd.Int("deadline-hours"))),
				Recipients: []escrow.BatchRecipient{
					{Address: fulfiller, Amount: amount},
				},
			}
			fmt.Printf("Funding reward: %s %s (%s atomic) -> fulfiller %s on %s\n",
				sb.Spec.Reward.Amount, sb.Spec.Reward.Asset.Symbol, amount, fulfiller, sb.Spec.Reward.Network)
			return attachBountyVoucher(ctx, cfg, namespace, name, bountyRewardVoucherAnnotation,
				&voucher, cmd.String("key"), cmd.String("signer-url"))
		},
	}
}

// bountyEvalFundRecipients mirrors the controller's evalBudgetTotal math for
// round 0 (counting seats: full price, probation at half price, shadows free)
// and reserveEscalationBudget for round 1 (every seat full price).
func bountyEvalFundRecipients(panel []monetizeapi.ServiceBountyPanelSeat, perAtomic *big.Int, escalation bool) []escrow.BatchRecipient {
	half := new(big.Int).Div(perAtomic, big.NewInt(2))
	var recipients []escrow.BatchRecipient
	for _, seat := range panel {
		if !escalation && seat.Seat == monetizeapi.PanelSeatShadow {
			continue // shadows evaluate free — never a paid voucher seat
		}
		amount := perAtomic
		if !escalation && seat.Seat == monetizeapi.PanelSeatProbation {
			amount = half // newcomer discount passed to the poster
		}
		recipients = append(recipients, escrow.BatchRecipient{Address: seat.Address, Amount: amount.String()})
	}
	return recipients
}

// bountyEvalFundCommand signs + attaches the poster's eval-budget voucher:
// one seat per counting panel evaluator. When the escalation budget is
// AwaitingVoucher it targets the round-1 panel instead (full price, voucher
// annotation obol.org/eval-voucher-r1, nonce leg eval-r1).
func bountyEvalFundCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "fund",
		Usage:     "Sign + attach the poster-funded eval-budget voucher (evaluators are paid win-or-lose; the controller NEVER signs)",
		ArgsUsage: "<name>",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "namespace", Aliases: []string{"n"}, Usage: "Namespace", Value: "hermes-obol-agent"},
			&cli.StringFlag{Name: "key", Usage: "Hex private key to sign the Permit2 voucher locally (or use --signer-url)"},
			&cli.StringFlag{Name: "signer-url", Usage: "Remote-signer base URL to sign without exposing a key"},
			&cli.StringFlag{Name: "spender", Usage: "Escrow facilitator address to bind as the only executor (default: status.escrowSpender)"},
			&cli.IntFlag{Name: "deadline-hours", Usage: "Voucher expiry in hours from now", Value: 72},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			name := cmd.Args().First()
			if name == "" {
				return fmt.Errorf("missing bounty name: obol bounty eval fund <name> (--key <hex> | --signer-url <url>)")
			}
			namespace := cmd.String("namespace")
			sb, err := getBountyCLI(cfg, namespace, name)
			if err != nil {
				return err
			}
			per := strings.TrimSpace(sb.Spec.Eval.Payment.PerEvaluator)
			if per == "" {
				return fmt.Errorf("bounty %s has no eval payment leg (spec.eval.payment.perEvaluator is empty) — nothing to fund", name)
			}
			token, err := resolveBountyToken(sb.Spec.Eval.Payment.Asset, sb.Spec.Reward.Network)
			if err != nil {
				return err
			}
			perAtomicStr, err := humanToAtomic(per, token.Decimals)
			if err != nil {
				return fmt.Errorf("perEvaluator amount: %w", err)
			}
			perAtomic, _ := new(big.Int).SetString(perAtomicStr, 10)

			// Escalation targeting: a round-1 panel waiting on its budget wins.
			leg, annotation := "eval", bountyEvalVoucherAnnotation
			panel := sb.Status.EvaluatorPanel
			escalation := false
			if esc := sb.Status.Escalation; esc != nil && esc.BudgetState == escrow.StateAwaitingVoucher {
				leg, annotation = "eval-r1", bountyEvalVoucherR1Annotation
				panel = esc.Panel
				escalation = true
			}
			if len(panel) == 0 {
				return fmt.Errorf("bounty %s has no evaluator panel selected yet — wait for the controller to draw the panel (obol bounty status)", name)
			}
			recipients := bountyEvalFundRecipients(panel, perAtomic, escalation)
			if len(recipients) == 0 {
				return fmt.Errorf("bounty %s panel has no counting seats to fund", name)
			}
			spender, err := resolveBountySpender(cmd.String("spender"), sb.Status.EscrowSpender)
			if err != nil {
				return err
			}

			voucher := escrow.Permit2Voucher{
				Token:      token.Address,
				Network:    sb.Spec.Reward.Network,
				Spender:    spender,
				Nonce:      bountyVoucherNonce(string(sb.UID), leg),
				Deadline:   bountyVoucherDeadline(int64(cmd.Int("deadline-hours"))),
				Recipients: recipients,
			}
			round := "round-0 quorum"
			if escalation {
				round = fmt.Sprintf("escalation round %d", sb.Status.Escalation.Round)
			}
			fmt.Printf("Funding eval budget (%s): %d seat(s) x %s %s on %s (probation seats at half price)\n",
				round, len(recipients), per, sb.Spec.Eval.Payment.Asset, sb.Spec.Reward.Network)
			return attachBountyVoucher(ctx, cfg, namespace, name, annotation,
				&voucher, cmd.String("key"), cmd.String("signer-url"))
		},
	}
}

// bountyFeedbackCommand prints ERC-8004 giveFeedback calldata for the poster
// to score the fulfiller from the settled verdict — submitted with the
// poster's OWN wallet, exactly like `obol bounty eval calldata`.
func bountyFeedbackCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "feedback",
		Usage:     "Print ERC-8004 giveFeedback calldata for the fulfiller, scored from the verdict (the controller NEVER signs)",
		ArgsUsage: "<name>",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "namespace", Aliases: []string{"n"}, Usage: "Namespace", Value: "hermes-obol-agent"},
			&cli.Int64Flag{Name: "agent-id", Usage: "[REQUIRED] The fulfiller's ERC-8004 agent id (Identity Registry tokenId)", Required: true},
			&cli.StringFlag{Name: "feedback-uri", Usage: "Optional URI of the bounty report backing the feedback"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			name := cmd.Args().First()
			if name == "" {
				return fmt.Errorf("missing bounty name: obol bounty feedback <name> --agent-id N")
			}
			sb, err := getBountyCLI(cfg, cmd.String("namespace"), name)
			if err != nil {
				return err
			}
			verdictSpoken := false
			for _, condition := range sb.Status.Conditions {
				if condition.Type == "Verified" {
					verdictSpoken = true
					break
				}
			}
			if !verdictSpoken {
				return fmt.Errorf("bounty %s has no Verified verdict yet — feedback scores the settled verdict (status.weightedScore)", name)
			}
			score := sb.Status.WeightedScore
			if score < 0 || score > 100 {
				return fmt.Errorf("status.weightedScore %d out of range 0-100", score)
			}

			network := sb.Spec.Reward.Network
			registry, err := erc8004.ReputationRegistryAddress(network)
			if err != nil {
				return err
			}
			calldata, err := erc8004.EncodeGiveFeedback(
				big.NewInt(cmd.Int64("agent-id")),
				big.NewInt(score),
				0, // score is already 0-100, no fixed-point scaling
				sb.Spec.Task.TypeRef,
				"obol-bounty",
				"",
				cmd.String("feedback-uri"),
				common.Hash{},
			)
			if err != nil {
				return err
			}
			fmt.Printf("Feedback: poster -> fulfiller %s, score %d/100 (from the %s verdict)\n",
				valueOr(firstClaimAddress(sb), "<unclaimed>"), score, valueOr(conditionReasonCLI(sb.Status.Conditions, "Verified"), "Verified"))
			fmt.Printf("ReputationRegistry (%s): %s\n", network, registry)
			fmt.Printf("Calldata: 0x%x\n", calldata)
			fmt.Println("Submit with YOUR wallet (e.g. the agent remote-signer or cast send) — then pass the tx hash to `obol bounty eval reveal --validation-tx`.")
			return nil
		},
	}
}

// printBountyEvaluations renders one round's evaluation lines with the
// grounded marker: [grounded] means the reveal is backed by an on-chain
// ERC-8004 validation entry for this bounty's eval-request hash.
func printBountyEvaluations(evaluations []monetizeapi.ServiceBountyEvaluation, indent string) {
	for _, ev := range evaluations {
		score := "-"
		if ev.Phase == "Revealed" {
			score = fmt.Sprintf("%d", ev.Score)
		}
		grounded := ""
		if ev.Grounded {
			grounded = "  [grounded]"
		}
		fmt.Printf("%s%s  seat=%-9s phase=%-10s score=%-4s withinBand=%-5v paid=%v%s\n",
			indent, ev.Address, valueOr(ev.Seat, "open"), ev.Phase, score, ev.WithinBand, ev.Paid, grounded)
	}
}

// printBountyVoucherNextSteps prints the exact fund command for every escrow
// leg parked in AwaitingVoucher — the facilitator verified the reservation and
// is waiting for a signed Permit2 voucher to ferry in.
func printBountyVoucherNextSteps(sb *monetizeapi.ServiceBounty, namespace string) {
	awaiting := escrow.StateAwaitingVoucher
	if sb.Status.EscrowState == awaiting {
		fmt.Printf("  Next: reward escrow is awaiting its voucher — run:\n")
		fmt.Printf("    obol bounty fund %s -n %s (--key <hex> | --signer-url <url>)\n", sb.Name, namespace)
	}
	if sb.Status.EvalBudgetState == awaiting {
		fmt.Printf("  Next: eval budget is awaiting its voucher — run:\n")
		fmt.Printf("    obol bounty eval fund %s -n %s (--key <hex> | --signer-url <url>)\n", sb.Name, namespace)
	}
	if esc := sb.Status.Escalation; esc != nil && esc.BudgetState == awaiting {
		fmt.Printf("  Next: escalation eval budget is awaiting its voucher — run:\n")
		fmt.Printf("    obol bounty eval fund %s -n %s (--key <hex> | --signer-url <url>)  # auto-targets the escalation panel\n", sb.Name, namespace)
	}
	if sb.Status.BondState == awaiting {
		fmt.Printf("  Next: self-bond is awaiting its voucher — re-run claim with bond signing:\n")
		fmt.Printf("    obol bounty claim %s -n %s --address <0x...> (--bond-key <hex> | --bond-signer-url <url>)\n", sb.Name, namespace)
	}
}

func firstClaimAddress(sb *monetizeapi.ServiceBounty) string {
	if len(sb.Status.Claims) == 0 {
		return ""
	}
	return sb.Status.Claims[0].FulfillerAddress
}

func conditionReasonCLI(conditions []monetizeapi.Condition, condType string) string {
	for _, condition := range conditions {
		if condition.Type == condType {
			return condition.Reason
		}
	}
	return ""
}
