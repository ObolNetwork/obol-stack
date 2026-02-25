package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/tunnel"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/urfave/cli/v3"
)

func monetizeCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "monetize",
		Usage: "Manage payment gating, pricing, and on-chain registration",
		Commands: []*cli.Command{
			monetizeRegisterCommand(cfg),
			monetizePricingCommand(cfg),
			monetizeStatusCommand(cfg),
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// register
// ─────────────────────────────────────────────────────────────────────────────

func monetizeRegisterCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "register",
		Usage: "Register service on ERC-8004 Identity Registry",
		Description: `Mints an agent NFT on the ERC-8004 Identity Registry.
The agent URI points to a /.well-known/agent-registration.json document
that describes the service endpoints and x402 payment support.

Requires a funded Base Sepolia wallet (private key).`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "private-key",
				Usage:   "DEPRECATED: use --private-key-file or ERC8004_PRIVATE_KEY env var instead",
				Sources: cli.EnvVars("ERC8004_PRIVATE_KEY"),
			},
			&cli.StringFlag{
				Name:  "private-key-file",
				Usage: "Path to file containing secp256k1 private key (hex)",
			},
			&cli.StringFlag{
				Name:  "rpc-url",
				Usage: "Base Sepolia JSON-RPC URL",
				Value: erc8004.DefaultRPCURL,
			},
			&cli.StringFlag{
				Name:  "endpoint",
				Usage: "Service endpoint URL (auto-detected from tunnel if not set)",
			},
			&cli.StringFlag{
				Name:  "name",
				Usage: "Agent name",
				Value: "Obol Stack",
			},
			&cli.StringFlag{
				Name:  "description",
				Usage: "Agent description",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			keyHex := cmd.String("private-key")
			if keyHex == "" {
				if keyFile := cmd.String("private-key-file"); keyFile != "" {
					data, err := os.ReadFile(keyFile)
					if err != nil {
						return fmt.Errorf("read private key file: %w", err)
					}
					keyHex = strings.TrimSpace(string(data))
				}
			}
			if keyHex == "" {
				return fmt.Errorf("private key required: use --private-key-file <path> or set ERC8004_PRIVATE_KEY")
			}
			if cmd.IsSet("private-key") {
				fmt.Fprintf(os.Stderr, "Warning: --private-key flag exposes key in process args. Use --private-key-file or ERC8004_PRIVATE_KEY env var instead.\n")
			}
			keyHex = strings.TrimPrefix(keyHex, "0x")

			key, err := crypto.HexToECDSA(keyHex)
			if err != nil {
				return fmt.Errorf("invalid private key: %w", err)
			}

			endpoint := cmd.String("endpoint")
			if endpoint == "" {
				// Try auto-detect from tunnel.
				tunnelURL, err := autoDetectEndpoint(cfg)
				if err != nil {
					return fmt.Errorf("--endpoint required (tunnel auto-detect failed: %v)", err)
				}
				endpoint = tunnelURL
				fmt.Printf("Auto-detected endpoint from tunnel: %s\n", endpoint)
			}

			agentURI := endpoint + "/.well-known/agent-registration.json"
			fmt.Printf("Registering agent on ERC-8004 Identity Registry (Base Sepolia)...\n")
			fmt.Printf("  Agent URI: %s\n", agentURI)
			fmt.Printf("  Registry:  %s\n", erc8004.IdentityRegistryBaseSepolia)

			client, err := erc8004.NewClient(ctx, cmd.String("rpc-url"))
			if err != nil {
				return fmt.Errorf("connect to Base Sepolia: %w", err)
			}
			defer client.Close()

			agentID, err := client.Register(ctx, key, agentURI)
			if err != nil {
				return fmt.Errorf("register: %w", err)
			}

			txAddr := crypto.PubkeyToAddress(key.PublicKey)
			fmt.Printf("\nAgent registered successfully!\n")
			fmt.Printf("  Agent ID:  %s\n", agentID.String())
			fmt.Printf("  Owner:     %s\n", txAddr.Hex())

			// Optionally set x402 metadata on the NFT.
			x402Meta := []byte(`{"x402":true}`)
			if err := client.SetMetadata(ctx, key, agentID, "x402", x402Meta); err != nil {
				fmt.Printf("  Warning: failed to set x402 metadata: %v\n", err)
			}

			// Persist registration record.
			store := erc8004.NewStore(cfg.ConfigDir)
			rec := &erc8004.RegistrationRecord{
				AgentID:  agentID.String(),
				AgentURI: agentURI,
				TxHash:   "", // TODO: capture from Register when we refactor
				Chain:    "base-sepolia",
				Registry: fmt.Sprintf("eip155:%d:%s", erc8004.BaseSepoliaChainID, erc8004.IdentityRegistryBaseSepolia),
			}
			if err := store.Save(rec); err != nil {
				fmt.Printf("  Warning: failed to save registration: %v\n", err)
			} else {
				fmt.Printf("  Saved to:  %s/x402/registration.json\n", cfg.ConfigDir)
			}

			return nil
		},
	}
}

// autoDetectEndpoint tries to discover the tunnel URL from the cluster.
func autoDetectEndpoint(cfg *config.Config) (string, error) {
	return tunnel.GetTunnelURL(cfg)
}

// ─────────────────────────────────────────────────────────────────────────────
// setup
// ─────────────────────────────────────────────────────────────────────────────

func monetizePricingCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "pricing",
		Usage: "Configure x402 pricing in the cluster",
		Description: `Patches the x402 verifier's pricing ConfigMap in the cluster.
Sets the wallet address and chain for payment collection.
Stakater Reloader auto-restarts the verifier pod on config changes.`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "wallet",
				Usage:    "USDC recipient wallet address (EVM)",
				Sources:  cli.EnvVars("X402_WALLET"),
				Required: true,
			},
			&cli.StringFlag{
				Name:  "chain",
				Usage: "Payment chain (base, base-sepolia)",
				Value: "base-sepolia",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			wallet := cmd.String("wallet")
			if err := x402verifier.ValidateWallet(wallet); err != nil {
				return err
			}
			return x402verifier.Setup(cfg, wallet, cmd.String("chain"))
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// status
// ─────────────────────────────────────────────────────────────────────────────

func monetizeStatusCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "Show pricing config and registration status",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// Show cluster pricing config.
			pricingCfg, err := x402verifier.GetPricingConfig(cfg)
			if err != nil {
				fmt.Printf("Cluster pricing: not available (%v)\n", err)
			} else {
				fmt.Printf("x402 Cluster Configuration:\n")
				fmt.Printf("  Wallet:      %s\n", valueOrNone(pricingCfg.Wallet))
				fmt.Printf("  Chain:       %s\n", valueOrNone(pricingCfg.Chain))
				fmt.Printf("  Facilitator: %s\n", valueOrNone(pricingCfg.FacilitatorURL))
				fmt.Printf("  Verify Only: %v\n", pricingCfg.VerifyOnly)
				fmt.Printf("  Routes:      %d\n", len(pricingCfg.Routes))
				for _, r := range pricingCfg.Routes {
					desc := r.Description
					if desc == "" {
						desc = "(no description)"
					}
					fmt.Printf("    %s → %s USDC  %s\n", r.Pattern, r.Price, desc)
				}
			}

			fmt.Println()

			// Show registration status.
			store := erc8004.NewStore(cfg.ConfigDir)
			rec, err := store.Load()
			if err != nil {
				if errors.Is(err, erc8004.ErrNoRegistration) {
					fmt.Printf("ERC-8004 Registration: not registered\n")
					fmt.Printf("  Run 'obol monetize register' to register on Base Sepolia\n")
				} else {
					fmt.Printf("ERC-8004 Registration: error (%v)\n", err)
				}
			} else {
				fmt.Printf("ERC-8004 Registration:\n")
				fmt.Printf("  Agent ID:  %s\n", rec.AgentID)
				fmt.Printf("  Agent URI: %s\n", rec.AgentURI)
				fmt.Printf("  Chain:     %s\n", rec.Chain)
				fmt.Printf("  Registry:  %s\n", rec.Registry)
				if rec.TxHash != "" {
					fmt.Printf("  Tx Hash:   %s\n", rec.TxHash)
				}
			}

			return nil
		},
	}
}

func valueOrNone(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}

