package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/inference"
	"github.com/mark3labs/x402-go"
	"github.com/urfave/cli/v2"
)

// inferenceCommand returns the inference management command group
func inferenceCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "inference",
		Usage: "Manage paid inference services (x402)",
		Subcommands: []*cli.Command{
			{
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
						Usage: "Blockchain network for payments (base, base-sepolia)",
						Value: "base-sepolia",
					},
					&cli.StringFlag{
						Name:  "facilitator",
						Usage: "x402 facilitator service URL",
						Value: "https://facilitator.x402.rs",
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
					})
					if err != nil {
						return fmt.Errorf("failed to create gateway: %w", err)
					}

					// Handle graceful shutdown
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
			},
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
