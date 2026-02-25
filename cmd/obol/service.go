package main

import (
	"context"
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
	"github.com/ObolNetwork/obol-stack/internal/tee"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/mark3labs/x402-go"
	"github.com/urfave/cli/v3"
)

// serviceCommand returns the service management command group.
//
// Command hierarchy mirrors ecloud's `compute app` surface:
//
//	ecloud compute app create   -> obol service create
//	ecloud compute app deploy   -> obol service deploy  (create + serve)
//	ecloud compute app list     -> obol service list
//	ecloud compute app info     -> obol service info
//	ecloud compute app logs     -> obol service logs
//	ecloud compute app start    -> obol service start
//	ecloud compute app stop     -> (Ctrl-C / obol service stop  TODO: PID management)
//	ecloud compute app terminate-> obol service delete
func serviceCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "service",
		Usage: "Manage encrypted compute service deployments",
		Commands: []*cli.Command{
			serviceCreateCommand(cfg),
			serviceDeployCommand(cfg),
			serviceListCommand(cfg),
			serviceInfoCommand(cfg),
			serviceDeleteCommand(cfg),
			servicePubkeyCommand(cfg),
			serviceServeCommand(cfg),
		},
	}
}

// ---------------------------------------------------------------------------
// create
// ---------------------------------------------------------------------------

func serviceCreateCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "create",
		Usage:     "Register a new service deployment",
		ArgsUsage: "[options] <name>",
		Description: `Creates a named service deployment and persists its configuration to disk.
The Secure Enclave key is generated on first use (obol service deploy or serve).

The deployment name can be supplied as a positional argument (flags first) or
via --name (any order):
  obol service create --wallet <addr> [flags] <name>
  obol service create --name <name> --wallet <addr> [flags]

Analogous to 'ecloud compute app deploy --name <name>'.`,
		Flags: append(deployFlags(),
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Usage:   "Overwrite existing deployment config",
			},
		),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			name := cmd.String("name")
			if name == "" {
				name = cmd.Args().First()
			}
			if name == "" {
				return fmt.Errorf("usage: obol service create [options] <name>")
			}
			store := inference.NewStore(cfg.ConfigDir)
			d := &inference.Deployment{
				Name:            name,
				EnclaveTag:      cmd.String("enclave-tag"),
				ListenAddr:      cmd.String("listen"),
				UpstreamURL:     cmd.String("upstream"),
				WalletAddress:   cmd.String("wallet"),
				PricePerRequest: cmd.String("price"),
				Chain:           cmd.String("chain"),
				FacilitatorURL:  cmd.String("facilitator"),
				TEEType:         cmd.String("tee"),
				ModelHash:       cmd.String("model-hash"),
			}
			if err := store.Create(d, cmd.Bool("force")); err != nil {
				if errors.Is(err, inference.ErrDeploymentExists) {
					return fmt.Errorf("%w — use --force to overwrite", err)
				}
				return err
			}
			fmt.Printf("Created service deployment %q\n", name)
			fmt.Printf("  Enclave tag: %s\n", d.EnclaveTag)
			fmt.Printf("  Upstream:    %s\n", d.UpstreamURL)
			fmt.Printf("  Listen:      %s\n", d.ListenAddr)
			fmt.Printf("\nRun: obol service deploy %s\n", name)
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// deploy  (create + start -- same pattern as ecloud's deploy)
// ---------------------------------------------------------------------------

func serviceDeployCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "deploy",
		Usage:     "Create (or update) a deployment and start the service gateway",
		ArgsUsage: "[options] <name>",
		Description: `Combines obol service create and obol service serve into a single step.
If the deployment already exists, its config is updated with any supplied flags
and the gateway starts immediately.

The deployment name can be supplied as a positional argument (flags first) or
via --name (any order):
  obol service deploy --wallet <addr> [flags] <name>
  obol service deploy --name <name> --wallet <addr> [flags]

Analogous to 'ecloud compute app deploy'.`,
		Flags: deployFlags(),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			name := cmd.String("name")
			if name == "" {
				name = cmd.Args().First()
			}
			if name == "" {
				return fmt.Errorf("usage: obol service deploy [options] <name>")
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
			applyFlags(cmd, d)

			// Validate required fields before writing config.
			if d.WalletAddress == "" {
				return fmt.Errorf("wallet address required — use --wallet <addr> or set X402_WALLET")
			}
			if err := x402verifier.ValidateWallet(d.WalletAddress); err != nil {
				return err
			}

			if err := store.Create(d, true); err != nil {
				return err
			}

			return runGateway(d)
		},
	}
}

// ---------------------------------------------------------------------------
// list
// ---------------------------------------------------------------------------

func serviceListCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List all service deployments",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "json",
				Aliases: []string{"j"},
				Usage:   "Output as JSON array",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			store := inference.NewStore(cfg.ConfigDir)
			deployments, err := store.List()
			if err != nil {
				return err
			}

			if cmd.Bool("json") {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(deployments)
			}

			if len(deployments) == 0 {
				fmt.Println("No service deployments found.")
				fmt.Println("Run: obol service create <name>")
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

// ---------------------------------------------------------------------------
// info
// ---------------------------------------------------------------------------

func serviceInfoCommand(cfg *config.Config) *cli.Command {
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
		Action: func(ctx context.Context, cmd *cli.Command) error {
			name := cmd.Args().First()
			if name == "" {
				return fmt.Errorf("usage: obol service info <name>")
			}

			store := inference.NewStore(cfg.ConfigDir)
			d, err := store.Get(name)
			if err != nil {
				return err
			}

			// Load (or generate) the key to expose the public key.
			// Use TEE backend when TEEType is set, otherwise use SE.
			var k enclave.Key
			var keyErr error
			if d.TEEType != "" {
				k, keyErr = tee.NewKey(d.EnclaveTag, d.ModelHash)
			} else {
				k, keyErr = enclave.NewKey(d.EnclaveTag)
			}

			if cmd.Bool("json") {
				out := map[string]any{
					"name":              d.Name,
					"enclave_tag":       d.EnclaveTag,
					"listen_addr":       d.ListenAddr,
					"upstream_url":      d.UpstreamURL,
					"wallet_address":    d.WalletAddress,
					"price_per_request": d.PricePerRequest,
					"chain":             d.Chain,
					"facilitator_url":   d.FacilitatorURL,
					"created_at":        d.CreatedAt,
					"updated_at":        d.UpdatedAt,
					"algorithm":         "ECIES-P256-HKDF-SHA256-AES256GCM",
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

// ---------------------------------------------------------------------------
// delete
// ---------------------------------------------------------------------------

func serviceDeleteCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "delete",
		Usage:     "Remove a service deployment",
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
		Action: func(ctx context.Context, cmd *cli.Command) error {
			name := cmd.Args().First()
			if name == "" {
				return fmt.Errorf("usage: obol service delete <name>")
			}

			store := inference.NewStore(cfg.ConfigDir)
			d, err := store.Get(name)
			if err != nil {
				return err
			}

			if cmd.Bool("purge-key") {
				if err := enclave.DeleteKey(d.EnclaveTag); err != nil {
					fmt.Fprintf(os.Stderr, "warning: could not delete SE key %q: %v\n", d.EnclaveTag, err)
				} else {
					fmt.Printf("Deleted SE key: %s\n", d.EnclaveTag)
				}
			}

			if err := store.Delete(name); err != nil {
				return err
			}
			fmt.Printf("Deleted service deployment %q\n", name)
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// pubkey
// ---------------------------------------------------------------------------

func servicePubkeyCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "pubkey",
		Usage:     "Print the Secure Enclave public key for a deployment or tag",
		ArgsUsage: "<name-or-tag>",
		Description: `Loads the SE-backed P-256 public key for a named deployment (or bare tag)
and prints it.  Clients use this key to encrypt service requests.

Analogous to 'ecloud compute app info' which exposes the app's hardware-bound
identity.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "json",
				Aliases: []string{"j"},
				Usage:   "Output as JSON",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			nameOrTag := cmd.Args().First()
			if nameOrTag == "" {
				return fmt.Errorf("usage: obol service pubkey <name-or-tag>")
			}

			// Try to resolve as deployment name first, fall back to raw tag.
			store := inference.NewStore(cfg.ConfigDir)
			tag := nameOrTag
			var teeType, modelHash string
			if d, err := store.Get(nameOrTag); err == nil {
				tag = d.EnclaveTag
				teeType = d.TEEType
				modelHash = d.ModelHash
			}

			var k enclave.Key
			var keyErr error
			if teeType != "" {
				k, keyErr = tee.NewKey(tag, modelHash)
			} else {
				k, keyErr = enclave.NewKey(tag)
			}
			if keyErr != nil {
				return fmt.Errorf("key: %w", keyErr)
			}

			if cmd.Bool("json") {
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

// ---------------------------------------------------------------------------
// serve  (run gateway inline -- low-level, no stored config required)
// ---------------------------------------------------------------------------

func serviceServeCommand(_ *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "serve",
		Usage: "Start the service gateway directly (no stored config)",
		Description: `Starts the gateway without requiring a named deployment.
For managed deployments use 'obol service deploy'.`,
		Flags: deployFlags(),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.String("wallet") == "" {
				return fmt.Errorf("usage: obol service serve --wallet <address> [flags]")
			}
			if err := x402verifier.ValidateWallet(cmd.String("wallet")); err != nil {
				return err
			}

			teeType := cmd.String("tee")
			modelHash := cmd.String("model-hash")
			if teeType != "" {
				if _, err := tee.ParseTEEType(teeType); err != nil {
					return err
				}
				if modelHash == "" {
					return fmt.Errorf("--model-hash is required when --tee is set")
				}
			}

			chain, err := resolveChain(cmd.String("chain"))
			if err != nil {
				return err
			}

			gw, err := inference.NewGateway(inference.GatewayConfig{
				ListenAddr:      cmd.String("listen"),
				UpstreamURL:     cmd.String("upstream"),
				WalletAddress:   cmd.String("wallet"),
				PricePerRequest: cmd.String("price"),
				Chain:           chain,
				FacilitatorURL:  cmd.String("facilitator"),
				EnclaveTag:      cmd.String("enclave-tag"),
				TEEType:         teeType,
				ModelHash:       modelHash,
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

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

// deployFlags returns the common flags shared by create / deploy / serve.
func deployFlags() []cli.Flag {
	return []cli.Flag{
		// name is provided as both a positional arg and a flag so that
		// users can write either:
		//   obol service deploy --wallet addr <name>   (flags first)
		//   obol service deploy --name <name> --wallet addr  (flag form)
		// urfave/cli v2 stops flag parsing at the first positional arg, so
		// the flag form is necessary when the name comes before other flags.
		&cli.StringFlag{
			Name:    "name",
			Aliases: []string{"n"},
			Usage:   "Deployment name (alternative to positional argument)",
		},
		&cli.StringFlag{
			Name:    "listen",
			Aliases: []string{"l"},
			Usage:   "Gateway listen address",
			Value:   ":8402",
		},
		&cli.StringFlag{
			Name:    "upstream",
			Aliases: []string{"u"},
			Usage:   "Upstream service URL",
			Value:   "http://localhost:11434",
		},
		&cli.StringFlag{
			Name:    "wallet",
			Aliases: []string{"w"},
			Usage:   "USDC recipient wallet address",
			Sources: cli.EnvVars("X402_WALLET"),
		},
		&cli.StringFlag{
			Name:  "price",
			Usage: "USDC price per request",
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
			Sources: cli.EnvVars("OBOL_ENCLAVE_TAG"),
		},
		&cli.BoolFlag{
			Name:  "vm",
			Usage: "Run Ollama inside an Apple Containerization Linux micro-VM (requires apple/container CLI, macOS 15+)",
		},
		&cli.StringFlag{
			Name:  "vm-image",
			Usage: "OCI image for the service container",
			Value: "ollama/ollama:latest",
		},
		&cli.IntFlag{
			Name:  "vm-cpus",
			Usage: "vCPUs to allocate to the VM",
			Value: 4,
		},
		&cli.IntFlag{
			Name:  "vm-memory",
			Usage: "RAM to allocate to the VM in MiB",
			Value: 8192,
		},
		&cli.IntFlag{
			Name:  "vm-host-port",
			Usage: "Host-local port mapped from the container's Ollama port 11434 (default 11435)",
			Value: 11435,
		},
		&cli.StringFlag{
			Name:    "tee",
			Usage:   "Linux TEE backend: tdx, snp, nitro, or stub (dev mode)",
			Sources: cli.EnvVars("OBOL_TEE_TYPE"),
		},
		&cli.StringFlag{
			Name:    "model-hash",
			Usage:   "SHA-256 of model weights bound into TEE attestation (required with --tee)",
			Sources: cli.EnvVars("OBOL_MODEL_HASH"),
		},
	}
}

// applyFlags merges CLI flag values into an existing Deployment, leaving
// fields unchanged when the flag was not explicitly provided.
//
// For flags that have a non-empty default value (listen, upstream, price,
// chain, facilitator) we use cmd.IsSet so that an existing deployment's
// persisted value is not overwritten when the user omits the flag.
//
// For flags with no meaningful empty default (wallet, enclave-tag) we apply
// the flag whenever the string is non-empty, because IsSet can return false
// when the flag was resolved via env var lookup before the argument is parsed.
func applyFlags(cmd *cli.Command, d *inference.Deployment) {
	if v := cmd.String("enclave-tag"); v != "" {
		d.EnclaveTag = v
	}
	if cmd.IsSet("listen") {
		d.ListenAddr = cmd.String("listen")
	}
	if cmd.IsSet("upstream") {
		d.UpstreamURL = cmd.String("upstream")
	}
	if v := cmd.String("wallet"); v != "" {
		d.WalletAddress = v
	}
	if cmd.IsSet("price") {
		d.PricePerRequest = cmd.String("price")
	}
	if cmd.IsSet("chain") {
		d.Chain = cmd.String("chain")
	}
	if cmd.IsSet("facilitator") {
		d.FacilitatorURL = cmd.String("facilitator")
	}
	if cmd.IsSet("vm") {
		d.VMMode = cmd.Bool("vm")
	}
	if cmd.IsSet("vm-image") {
		d.VMImage = cmd.String("vm-image")
	}
	if cmd.IsSet("vm-cpus") {
		d.VMCPUs = int(cmd.Int("vm-cpus"))
	}
	if cmd.IsSet("vm-memory") {
		d.VMMemoryMB = int(cmd.Int("vm-memory"))
	}
	if cmd.IsSet("vm-host-port") {
		d.VMHostPort = int(cmd.Int("vm-host-port"))
	}
	if v := cmd.String("tee"); v != "" {
		d.TEEType = v
	}
	if v := cmd.String("model-hash"); v != "" {
		d.ModelHash = v
	}
}

// runGateway starts the service gateway for a Deployment and blocks until
// shutdown.
func runGateway(d *inference.Deployment) error {
	// Validate TEE type if set.
	if d.TEEType != "" {
		if _, err := tee.ParseTEEType(d.TEEType); err != nil {
			return err
		}
		if d.ModelHash == "" {
			return fmt.Errorf("--model-hash is required when --tee is set")
		}
	}

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
		VMMode:          d.VMMode,
		VMImage:         d.VMImage,
		VMCPUs:          d.VMCPUs,
		VMMemoryMB:      d.VMMemoryMB,
		VMHostPort:      d.VMHostPort,
		TEEType:         d.TEEType,
		ModelHash:       d.ModelHash,
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
