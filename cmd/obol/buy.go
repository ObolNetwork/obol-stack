package main

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/buy"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/validate"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/urfave/cli/v3"
)

// In-pod paths used by `obol buy inference`. Hermes always has python3 in
// the venv and skills mounted at $OBOL_SKILLS_DIR (see
// internal/hermes/hermes.go where the env is wired). We reference the
// literal paths so we don't depend on shell expansion through `kubectl exec`.
const (
	hermesPython    = "/opt/hermes/.venv/bin/python3"
	hermesBuyPyPath = "/data/.hermes/obol-skills/buy-x402/scripts/buy.py"
	defaultBuyName  = "default-paid"
)

func buyCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "buy",
		Usage: "Buy access to remote services via x402 micropayments",
		Commands: []*cli.Command{
			buyInferenceCommand(cfg),
		},
	}
}

func buyInferenceCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "inference",
		Usage:     "Buy paid inference from an x402-gated seller via the obol-agent",
		ArgsUsage: "[<name>]",
		Description: `Pre-pays an x402-gated inference seller through the obol-agent's wallet.

Identity is verified before signing: the seller's
/.well-known/agent-registration.json must advertise the requested service
endpoint and list an ERC-8004 agentId that matches --expected-agent-id on the
same priced payment network that the seller advertises in its 402 response. Use
--no-verify-identity to bypass during development.

Today the baked default seller placeholders are not yet provisioned, so the
practical path is to pass an explicit --seller, --model, and either
--expected-agent-id or --no-verify-identity.

Examples:
	obol buy inference my-buy --seller https://seller.example/services/x \
	                     --model qwen3.5:9b --expected-agent-id 42 --budget 1
	obol buy inference --seller https://seller.example/services/x \
	                     --model qwen3.5:9b --no-verify-identity --budget 1
	obol buy inference --seller https://inference.v1337.org/services/aeon \
	                     --model aeon --no-verify-identity --budget 0.023 --token OBOL`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "seller",
				Usage: fmt.Sprintf("Seller endpoint (defaults to the Obol-operated %s demo seller placeholder)", x402verifier.DefaultBuySellerChain),
				Value: x402verifier.DefaultBuySellerURL,
			},
			&cli.StringFlag{
				Name:     "model",
				Usage:    "Remote model id to buy (required until a default seller/model are provisioned, e.g. qwen3.5:9b)",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "budget",
				Usage:    "Spending cap in the payment token (e.g. \"10\" for 10 USDC, or \"0.023\" for 0.023 OBOL). Converted to base units before passing to the agent.",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "token",
				Usage: fmt.Sprintf("Payment token to use (default \"USDC\"). Supported: %s. Must match the seller's priced asset.", strings.Join(x402verifier.SupportedTokens(), ", ")),
				Value: "USDC",
			},
			&cli.IntFlag{
				Name:  "expected-agent-id",
				Usage: "Expected ERC-8004 agentId of the seller (default: DefaultBuySellerAgentID)",
				Value: int(x402verifier.DefaultBuySellerAgentID),
			},
			&cli.BoolFlag{
				Name:  "no-verify-identity",
				Usage: "Skip the ERC-8004 identity pre-flight (NOT recommended)",
			},
			&cli.BoolFlag{
				Name:  "auto-refill",
				Usage: "Enable agent-managed refill of the auth pool",
			},
			&cli.IntFlag{
				Name:  "refill-threshold",
				Usage: "Sign more auths when remaining drops below this (requires --auto-refill)",
			},
			&cli.IntFlag{
				Name:  "refill-count",
				Usage: "How many auths to sign each refill (requires --auto-refill)",
			},
			&cli.StringFlag{
				Name:  "id",
				Usage: "Obol agent instance id",
				Value: agentruntime.DefaultInstanceID,
			},
			&cli.BoolFlag{
				Name:  "force",
				Usage: "Pass-through to buy.py: replace an existing PurchaseRequest and bypass certain balance/overwrite safety checks",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			name := cmd.Args().First()
			if name == "" {
				name = defaultBuyName
			}
			if err := validate.Name(name); err != nil {
				return err
			}

			seller := strings.TrimSpace(cmd.String("seller"))
			if seller == "" {
				return errors.New("--seller is empty and no DefaultBuySellerURL is configured")
			}

			token := strings.ToUpper(strings.TrimSpace(cmd.String("token")))
			budgetBase, err := budgetToBaseUnits(cmd.String("budget"), token)
			if err != nil {
				return err
			}

			u.Infof("Probing seller pricing at %s …", seller)
			pricing, err := buy.FetchSellerPricing(ctx, seller, cmd.String("model"))
			if err != nil {
				return fmt.Errorf("pricing pre-flight: %w", err)
			}

			if err := buy.ValidateTokenAgainstPricing(token, pricing); err != nil {
				return err
			}
			if err := buy.ValidateBudgetAgainstPricing(budgetBase, pricing); err != nil {
				return err
			}

			if !cmd.Bool("no-verify-identity") {
				expected := cmd.Int("expected-agent-id")
				if expected == 0 {
					return errors.New("expected-agent-id is 0 — set --expected-agent-id, configure DefaultBuySellerAgentID, or pass --no-verify-identity to bypass")
				}
				u.Infof("Verifying seller identity at %s …", seller)
				reg, err := buy.FetchSellerRegistration(ctx, seller)
				if err != nil {
					return fmt.Errorf("identity pre-flight: %w", err)
				}
				if err := buy.VerifySellerEndpoint(reg, seller); err != nil {
					return err
				}
				if err := buy.VerifyAgentIDForPricing(reg, int64(expected), pricing); err != nil {
					return err
				}
				u.Infof("Identity OK: agentId=%d", expected)
			} else {
				u.Warn("Skipping ERC-8004 identity check (--no-verify-identity).")
			}

			argv := buildBuyPyArgv(buyPyOptions{
				Name:            name,
				Seller:          seller,
				Model:           cmd.String("model"),
				BudgetMicro:     budgetBase,
				AutoRefill:      cmd.Bool("auto-refill"),
				RefillThreshold: cmd.Int("refill-threshold"),
				RefillCount:     cmd.Int("refill-count"),
				Force:           cmd.Bool("force"),
			})

			u.Infof("Dispatching to obol-agent (instance=%s) …", cmd.String("id"))
			return agentruntime.ExecInPod(cfg, agentruntime.Hermes, cmd.String("id"), argv)
		},
	}
}

// buyPyOptions captures everything needed to invoke `buy.py buy` inside the
// agent pod. Kept as a flat struct so buildBuyPyArgv stays trivially testable.
type buyPyOptions struct {
	Name            string
	Seller          string
	Model           string
	BudgetMicro     string
	AutoRefill      bool
	RefillThreshold int
	RefillCount     int
	Force           bool
}

// buildBuyPyArgv composes the argv for `python3 buy.py buy <name> ...`.
func buildBuyPyArgv(opts buyPyOptions) []string {
	argv := []string{
		hermesPython, hermesBuyPyPath, "buy", opts.Name,
		"--endpoint", opts.Seller,
		"--budget", opts.BudgetMicro,
	}
	if m := strings.TrimSpace(opts.Model); m != "" {
		argv = append(argv, "--model", m)
	}
	if opts.AutoRefill {
		argv = append(argv, "--auto-refill")
		if opts.RefillThreshold > 0 {
			argv = append(argv, "--refill-threshold", fmt.Sprintf("%d", opts.RefillThreshold))
		}
		if opts.RefillCount > 0 {
			argv = append(argv, "--refill-count", fmt.Sprintf("%d", opts.RefillCount))
		}
	}
	if opts.Force {
		argv = append(argv, "--force")
	}
	return argv
}

// budgetToBaseUnits parses a human-readable token amount (e.g. "10" for 10
// USDC, "0.023" for 0.023 OBOL) and returns the equivalent in atomic base
// units as a base-10 integer string. The token name is matched against the
// x402 token registry to determine the correct decimal precision.
//
// Uses big.Rat so we don't lose precision on fractional amounts. Rejects
// negatives and any value that isn't an integral number of base units (i.e.
// finer than the token's smallest denomination).
func budgetToBaseUnits(amount, token string) (string, error) {
	s := strings.TrimSpace(amount)
	if s == "" {
		return "", errors.New("--budget is empty")
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return "", fmt.Errorf("--budget %q is not a valid number", amount)
	}
	if r.Sign() <= 0 {
		return "", fmt.Errorf("--budget %q must be positive", amount)
	}

	tok := strings.ToUpper(strings.TrimSpace(token))
	if tok == "" {
		tok = "USDC"
	}

	// Look up decimals from any chain the token is registered on — the decimal
	// count is the same across all chains for a given token symbol.
	chains := x402verifier.ChainsForToken(tok)
	if len(chains) == 0 {
		supported := strings.Join(x402verifier.SupportedTokens(), ", ")
		return "", fmt.Errorf("--token %q is not a known payment token; supported tokens: %s", token, supported)
	}
	entry, _ := x402verifier.ResolveToken(tok, chains[0])
	decimals := entry.Decimals

	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	base := new(big.Rat).Mul(r, scale)
	if !base.IsInt() {
		return "", fmt.Errorf("--budget %q has more precision than %s supports (%d decimals)", amount, tok, decimals)
	}
	return base.Num().String(), nil
}
