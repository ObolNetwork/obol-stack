package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/enclave"
	"github.com/ObolNetwork/obol-stack/internal/inference"
	"github.com/mark3labs/x402-go"
	"github.com/urfave/cli/v2"
)

// inferenceCommand returns the inference management command group.
func inferenceCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "inference",
		Usage: "Manage paid inference services (x402 + Secure Enclave)",
		Subcommands: []*cli.Command{
			inferenceServeCommand(cfg),
			inferencePubkeyCommand(cfg),
		},
	}
}

// inferenceServeCommand starts the x402 inference gateway.
func inferenceServeCommand(_ *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "serve",
		Usage: "Start the x402 inference gateway (local process)",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "listen",
				Aliases: []string{"l"},
				Usage:   "Listen address for the gateway",
				Value:   ":8402",
			},
			&cli.StringFlag{
				Name:    "upstream",
				Aliases: []string{"u"},
				Usage:   "Upstream inference service URL",
				Value:   "http://localhost:11434",
			},
			&cli.StringFlag{
				Name:     "wallet",
				Aliases:  []string{"w"},
				Usage:    "USDC recipient wallet address",
				EnvVars:  []string{"X402_WALLET"},
				Required: true,
			},
			&cli.StringFlag{
				Name:  "price",
				Usage: "USDC price per inference request",
				Value: "0.001",
			},
			&cli.StringFlag{
				Name:  "chain",
				Usage: "Blockchain network for payments (base, base-sepolia, polygon, polygon-amoy, avalanche, avalanche-fuji)",
				Value: "base-sepolia",
			},
			&cli.StringFlag{
				Name:  "facilitator",
				Usage: "x402 facilitator service URL",
				Value: "https://facilitator.x402.rs",
			},
			&cli.StringFlag{
				Name:    "enclave-tag",
				Aliases: []string{"e"},
				Usage: "Keychain application tag for the Secure Enclave key. " +
					"When set, clients may encrypt request bodies with the SE public key " +
					"(retrieved via GET /v1/enclave/pubkey) so that plaintext is only " +
					"visible inside the hardware enclave, never to the gateway operator.",
				EnvVars: []string{"OBOL_ENCLAVE_TAG"},
			},
		},
		Action: func(c *cli.Context) error {
			chain, err := resolveChain(c.String("chain"))
			if err != nil {
				return err
			}

			gw, err := inference.NewGateway(inference.GatewayConfig{
				ListenAddr:      c.String("listen"),
				UpstreamURL:     c.String("upstream"),
				WalletAddress:   c.String("wallet"),
				PricePerRequest: c.String("price"),
				Chain:           chain,
				FacilitatorURL:  c.String("facilitator"),
				EnclaveTag:      c.String("enclave-tag"),
			})
			if err != nil {
				return fmt.Errorf("failed to create gateway: %w", err)
			}

			// Handle graceful shutdown.
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				fmt.Println("\nShutting down gateway...")
				if err := gw.Stop(); err != nil {
					fmt.Fprintf(os.Stderr, "shutdown error: %v\n", err)
				}
			}()

			return gw.Start()
		},
	}
}

// inferencePubkeyCommand prints the Secure Enclave public key for a given tag.
// This is the obol equivalent of `ecloud compute app info` — it exposes the
// hardware-bound identity that clients use to encrypt inference requests.
func inferencePubkeyCommand(_ *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "pubkey",
		Usage: "Print the Secure Enclave public key for an enclave tag",
		Description: `Loads or generates the SE-backed P-256 key identified by TAG and prints its
public key information.  Clients use this public key to encrypt inference
requests (Content-Type: application/x-obol-encrypted) so that the request
body is only decryptable inside the Secure Enclave co-processor.

Analogous to 'ecloud compute app info' which exposes an app's hardware-bound
deterministic identity.`,
		ArgsUsage: "<tag>",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "json",
				Aliases: []string{"j"},
				Usage:   "Output as JSON",
			},
		},
		Action: func(c *cli.Context) error {
			tag := c.Args().First()
			if tag == "" {
				return fmt.Errorf("usage: obol inference pubkey <tag>")
			}

			k, err := enclave.NewKey(tag)
			if err != nil {
				return fmt.Errorf("enclave key: %w", err)
			}

			if c.Bool("json") {
				out := map[string]any{
					"pubkey":     hex.EncodeToString(k.PublicKeyBytes()),
					"tag":        k.Tag(),
					"persistent": k.Persistent(),
					"algorithm":  "ECIES-P256-HKDF-SHA256-AES256GCM",
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			fmt.Printf("Tag:        %s\n", k.Tag())
			fmt.Printf("Pubkey:     %s\n", hex.EncodeToString(k.PublicKeyBytes()))
			fmt.Printf("Persistent: %v\n", k.Persistent())
			fmt.Printf("Algorithm:  ECIES-P256-HKDF-SHA256-AES256GCM\n")
			if !k.Persistent() {
				fmt.Println()
				fmt.Println("NOTE: Key is ephemeral (binary lacks keychain entitlement).")
				fmt.Println("      Sign the binary with the keychain entitlement for production use.")
			}
			return nil
		},
	}
}

// resolveChain maps a chain name string to an x402 ChainConfig.
func resolveChain(name string) (x402.ChainConfig, error) {
	switch name {
	case "base", "base-mainnet":
		return x402.BaseMainnet, nil
	case "base-sepolia":
		return x402.BaseSepolia, nil
	case "polygon", "polygon-mainnet":
		return x402.PolygonMainnet, nil
	case "polygon-amoy":
		return x402.PolygonAmoy, nil
	case "avalanche", "avalanche-mainnet":
		return x402.AvalancheMainnet, nil
	case "avalanche-fuji":
		return x402.AvalancheFuji, nil
	default:
		return x402.ChainConfig{}, fmt.Errorf("unsupported chain: %s (use: base, base-sepolia, polygon, polygon-amoy, avalanche, avalanche-fuji)", name)
	}
}
