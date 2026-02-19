package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/enclave"
	"github.com/ObolNetwork/obol-stack/internal/inference"
	"github.com/mark3labs/x402-go"
	"github.com/urfave/cli/v2"
)

// inferenceCommand returns the inference management command group.
//
// Command hierarchy mirrors ecloud's `compute app` surface:
//
//	ecloud compute app create   → obol inference create
//	ecloud compute app deploy   → obol inference deploy  (create + serve)
//	ecloud compute app list     → obol inference list
//	ecloud compute app info     → obol inference info
//	ecloud compute app logs     → obol inference logs
//	ecloud compute app start    → obol inference start
//	ecloud compute app stop     → (Ctrl-C / obol inference stop  TODO: PID management)
//	ecloud compute app terminate→ obol inference delete
func inferenceCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "inference",
		Usage: "Manage SE-protected paid inference deployments (x402 + Secure Enclave)",
		Subcommands: []*cli.Command{
			inferenceCreateCommand(cfg),
			inferenceDeployCommand(cfg),
			inferenceListCommand(cfg),
			inferenceInfoCommand(cfg),
			inferenceDeleteCommand(cfg),
			inferencePubkeyCommand(cfg),
			inferenceServeCommand(cfg),
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// create
// ─────────────────────────────────────────────────────────────────────────────

func inferenceCreateCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "create",
		Usage:     "Register a new inference deployment",
		ArgsUsage: "<name>",
		Description: `Creates a named inference deployment and persists its configuration to disk.
The Secure Enclave key is generated on first use (obol inference deploy or serve).

Analogous to 'ecloud compute app deploy --name <name>'.`,
		Flags: deployFlags(),
		Action: func(c *cli.Context) error {
			name := c.Args().First()
			if name == "" {
				return fmt.Errorf("usage: obol inference create <name>")
			}
			store := inference.NewStore(cfg.ConfigDir)
			d := &inference.Deployment{
				Name:            name,
				EnclaveTag:      c.String("enclave-tag"),
				ListenAddr:      c.String("listen"),
				UpstreamURL:     c.String("upstream"),
				WalletAddress:   c.String("wallet"),
				PricePerRequest: c.String("price"),
				Chain:           c.String("chain"),
				FacilitatorURL:  c.String("facilitator"),
			}
			if err := store.Create(d, c.Bool("force")); err != nil {
				if errors.Is(err, inference.ErrDeploymentExists) {
					return fmt.Errorf("%w — use --force to overwrite", err)
				}
				return err
			}
			fmt.Printf("Created inference deployment %q\n", name)
			fmt.Printf("  Enclave tag: %s\n", d.EnclaveTag)
			fmt.Printf("  Upstream:    %s\n", d.UpstreamURL)
			fmt.Printf("  Listen:      %s\n", d.ListenAddr)
			fmt.Printf("\nRun: obol inference deploy %s\n", name)
			return nil
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// deploy  (create + start — same pattern as ecloud's deploy)
// ─────────────────────────────────────────────────────────────────────────────

func inferenceDeployCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "deploy",
		Usage:     "Create (or update) a deployment and start the gateway",
		ArgsUsage: "<name>",
		Description: `Combines obol inference create and obol inference serve into a single step.
If the deployment already exists, its config is updated with any supplied flags
and the gateway starts immediately.

Analogous to 'ecloud compute app deploy'.`,
		Flags: append(deployFlags(),
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Usage:   "Overwrite an existing deployment config",
			},
		),
		Action: func(c *cli.Context) error {
			name := c.Args().First()
			if name == "" {
				return fmt.Errorf("usage: obol inference deploy <name>")
			}

			store := inference.NewStore(cfg.ConfigDir)

			// Load existing or build new deployment.
			d, err := store.Get(name)
			if err != nil {
				if !errors.Is(err, inference.ErrDeploymentNotFound) {
					return err
				}
				d = &inference.Deployment{Name: name}
			}

			// Apply CLI flag overrides.
			applyFlags(c, d)

			if err := store.Create(d, true); err != nil {
				return err
			}

			return runGateway(d)
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// list
// ─────────────────────────────────────────────────────────────────────────────

func inferenceListCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List all inference deployments",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "json",
				Aliases: []string{"j"},
				Usage:   "Output as JSON array",
			},
		},
		Action: func(c *cli.Context) error {
			store := inference.NewStore(cfg.ConfigDir)
			deployments, err := store.List()
			if err != nil {
				return err
			}

			if c.Bool("json") {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(deployments)
			}

			if len(deployments) == 0 {
				fmt.Println("No inference deployments found.")
				fmt.Println("Run: obol inference create <name>")
				return nil
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tUPSTREAM\tLISTEN\tCHAIN\tCREATED")
			for _, d := range deployments {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					d.Name, d.UpstreamURL, d.ListenAddr, d.Chain, d.CreatedAt)
			}
			return tw.Flush()
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// info
// ─────────────────────────────────────────────────────────────────────────────

func inferenceInfoCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "info",
		Usage:     "Show deployment details and Secure Enclave public key",
		ArgsUsage: "<name>",
		Description: `Prints configuration and the SE public key for a deployment.
The public key is the hardware-bound identity clients use to encrypt requests.

Analogous to 'ecloud compute app info <app-id>'.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "json",
				Aliases: []string{"j"},
				Usage:   "Output as JSON",
			},
		},
		Action: func(c *cli.Context) error {
			name := c.Args().First()
			if name == "" {
				return fmt.Errorf("usage: obol inference info <name>")
			}

			store := inference.NewStore(cfg.ConfigDir)
			d, err := store.Get(name)
			if err != nil {
				return err
			}

			// Load (or generate) the SE key to expose the public key.
			k, keyErr := enclave.NewKey(d.EnclaveTag)

			if c.Bool("json") {
				out := map[string]any{
					"name":             d.Name,
					"enclave_tag":      d.EnclaveTag,
					"listen_addr":      d.ListenAddr,
					"upstream_url":     d.UpstreamURL,
					"wallet_address":   d.WalletAddress,
					"price_per_request": d.PricePerRequest,
					"chain":            d.Chain,
					"facilitator_url":  d.FacilitatorURL,
					"created_at":       d.CreatedAt,
					"updated_at":       d.UpdatedAt,
					"algorithm":        "ECIES-P256-HKDF-SHA256-AES256GCM",
				}
				if keyErr == nil {
					out["pubkey"] = hex.EncodeToString(k.PublicKeyBytes())
					out["persistent"] = k.Persistent()
				} else {
					out["pubkey_error"] = keyErr.Error()
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			fmt.Printf("Name:         %s\n", d.Name)
			fmt.Printf("Enclave tag:  %s\n", d.EnclaveTag)
			fmt.Printf("Algorithm:    ECIES-P256-HKDF-SHA256-AES256GCM\n")
			if keyErr == nil {
				fmt.Printf("Pubkey:       %s\n", hex.EncodeToString(k.PublicKeyBytes()))
				fmt.Printf("Persistent:   %v\n", k.Persistent())
			} else {
				fmt.Printf("Pubkey:       (unavailable: %v)\n", keyErr)
			}
			fmt.Println()
			fmt.Printf("Listen:       %s\n", d.ListenAddr)
			fmt.Printf("Upstream:     %s\n", d.UpstreamURL)
			fmt.Printf("Wallet:       %s\n", d.WalletAddress)
			fmt.Printf("Price:        %s USDC/request\n", d.PricePerRequest)
			fmt.Printf("Chain:        %s\n", d.Chain)
			fmt.Printf("Facilitator:  %s\n", d.FacilitatorURL)
			fmt.Printf("Created:      %s\n", d.CreatedAt)
			if d.UpdatedAt != "" {
				fmt.Printf("Updated:      %s\n", d.UpdatedAt)
			}
			return nil
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// delete
// ─────────────────────────────────────────────────────────────────────────────

func inferenceDeleteCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "delete",
		Usage:     "Remove an inference deployment",
		ArgsUsage: "<name>",
		Description: `Removes the deployment config from disk.
Use --purge-key to also delete the SE key from the macOS keychain.

Analogous to 'ecloud compute app terminate'.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "purge-key",
				Usage: "Also delete the Secure Enclave key from the keychain",
			},
		},
		Action: func(c *cli.Context) error {
			name := c.Args().First()
			if name == "" {
				return fmt.Errorf("usage: obol inference delete <name>")
			}

			store := inference.NewStore(cfg.ConfigDir)
			d, err := store.Get(name)
			if err != nil {
				return err
			}

			if c.Bool("purge-key") {
				if err := enclave.DeleteKey(d.EnclaveTag); err != nil {
					fmt.Fprintf(os.Stderr, "warning: could not delete SE key %q: %v\n", d.EnclaveTag, err)
				} else {
					fmt.Printf("Deleted SE key: %s\n", d.EnclaveTag)
				}
			}

			if err := store.Delete(name); err != nil {
				return err
			}
			fmt.Printf("Deleted inference deployment %q\n", name)
			return nil
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// pubkey
// ─────────────────────────────────────────────────────────────────────────────

func inferencePubkeyCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "pubkey",
		Usage:     "Print the Secure Enclave public key for a deployment or tag",
		ArgsUsage: "<name-or-tag>",
		Description: `Loads the SE-backed P-256 public key for a named deployment (or bare tag)
and prints it.  Clients use this key to encrypt inference requests.

Analogous to 'ecloud compute app info' which exposes the app's hardware-bound
identity.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "json",
				Aliases: []string{"j"},
				Usage:   "Output as JSON",
			},
		},
		Action: func(c *cli.Context) error {
			nameOrTag := c.Args().First()
			if nameOrTag == "" {
				return fmt.Errorf("usage: obol inference pubkey <name-or-tag>")
			}

			// Try to resolve as deployment name first, fall back to raw tag.
			store := inference.NewStore(cfg.ConfigDir)
			tag := nameOrTag
			if d, err := store.Get(nameOrTag); err == nil {
				tag = d.EnclaveTag
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
			}
			return nil
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// serve  (run gateway inline — low-level, no stored config required)
// ─────────────────────────────────────────────────────────────────────────────

func inferenceServeCommand(_ *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "serve",
		Usage: "Start the x402 inference gateway directly (no stored config)",
		Description: `Starts the gateway without requiring a named deployment.
For managed deployments use 'obol inference deploy'.`,
		Flags: append(deployFlags(),
			&cli.StringFlag{
				Name:     "wallet",
				Aliases:  []string{"w"},
				Usage:    "USDC recipient wallet address",
				EnvVars:  []string{"X402_WALLET"},
				Required: true,
			},
		),
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

// ─────────────────────────────────────────────────────────────────────────────
// shared helpers
// ─────────────────────────────────────────────────────────────────────────────

// deployFlags returns the common flags shared by create / deploy / serve.
func deployFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "listen",
			Aliases: []string{"l"},
			Usage:   "Gateway listen address",
			Value:   ":8402",
		},
		&cli.StringFlag{
			Name:    "upstream",
			Aliases: []string{"u"},
			Usage:   "Upstream inference service URL",
			Value:   "http://localhost:11434",
		},
		&cli.StringFlag{
			Name:    "wallet",
			Aliases: []string{"w"},
			Usage:   "USDC recipient wallet address",
			EnvVars: []string{"X402_WALLET"},
		},
		&cli.StringFlag{
			Name:  "price",
			Usage: "USDC price per inference request",
			Value: "0.001",
		},
		&cli.StringFlag{
			Name:  "chain",
			Usage: "Payment chain (base, base-sepolia, polygon, polygon-amoy, avalanche, avalanche-fuji)",
			Value: "base-sepolia",
		},
		&cli.StringFlag{
			Name:  "facilitator",
			Usage: "x402 facilitator URL",
			Value: "https://facilitator.x402.rs",
		},
		&cli.StringFlag{
			Name:    "enclave-tag",
			Aliases: []string{"e"},
			Usage:   "Keychain SE tag (default: com.obol.inference.<name>)",
			EnvVars: []string{"OBOL_ENCLAVE_TAG"},
		},
		&cli.BoolFlag{
			Name:    "force",
			Aliases: []string{"f"},
			Usage:   "Overwrite existing deployment config",
		},
	}
}

// applyFlags merges CLI flag values into an existing Deployment, leaving
// fields unchanged when the flag was not explicitly provided.
func applyFlags(c *cli.Context, d *inference.Deployment) {
	if v := c.String("enclave-tag"); v != "" {
		d.EnclaveTag = v
	}
	if v := c.String("listen"); v != "" {
		d.ListenAddr = v
	}
	if v := c.String("upstream"); v != "" {
		d.UpstreamURL = v
	}
	if v := c.String("wallet"); v != "" {
		d.WalletAddress = v
	}
	if v := c.String("price"); v != "" {
		d.PricePerRequest = v
	}
	if v := c.String("chain"); v != "" {
		d.Chain = v
	}
	if v := c.String("facilitator"); v != "" {
		d.FacilitatorURL = v
	}
}

// runGateway starts the inference gateway for a Deployment and blocks until
// shutdown.
func runGateway(d *inference.Deployment) error {
	chain, err := resolveChain(d.Chain)
	if err != nil {
		return err
	}

	gw, err := inference.NewGateway(inference.GatewayConfig{
		ListenAddr:      d.ListenAddr,
		UpstreamURL:     d.UpstreamURL,
		WalletAddress:   d.WalletAddress,
		PricePerRequest: d.PricePerRequest,
		Chain:           chain,
		FacilitatorURL:  d.FacilitatorURL,
		EnclaveTag:      d.EnclaveTag,
	})
	if err != nil {
		return fmt.Errorf("failed to create gateway: %w", err)
	}

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
		return x402.ChainConfig{}, fmt.Errorf("unsupported chain: %s", name)
	}
}
