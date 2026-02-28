package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/enclave"
	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/inference"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/tee"
	"github.com/ObolNetwork/obol-stack/internal/tunnel"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/mark3labs/x402-go"
	"github.com/urfave/cli/v3"
)

func sellCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "sell",
		Usage: "Sell access to services via x402 micropayments",
		Commands: []*cli.Command{
			sellInferenceCommand(cfg),
			sellHTTPCommand(cfg),
			sellListCommand(cfg),
			sellStatusCommand(cfg),
			sellStopCommand(cfg),
			sellDeleteCommand(cfg),
			sellPricingCommand(cfg),
			sellRegisterCommand(cfg),
		},
	}
}

// ---------------------------------------------------------------------------
// sell inference — start a local x402 gateway for LLM inference
// ---------------------------------------------------------------------------

func sellInferenceCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "inference",
		Usage:     "Sell LLM inference via a local x402 payment gateway",
		ArgsUsage: "<name>",
		Description: `Starts an x402-gated reverse proxy in front of a local Ollama instance.
Buyers pay per-request in USDC to access inference endpoints.

Examples:
  obol sell inference my-qwen --model qwen3:0.6b --wallet 0x... --price 0.001
  obol sell inference my-llama --model llama3:8b --wallet 0x... --chain base`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "model",
				Usage: "Model name to serve (e.g. qwen3:0.6b)",
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
				Name:    "listen",
				Aliases: []string{"l"},
				Usage:   "Gateway listen address",
				Value:   ":8402",
			},
			&cli.StringFlag{
				Name:    "upstream",
				Aliases: []string{"u"},
				Usage:   "Upstream Ollama URL",
				Value:   "http://localhost:11434",
			},
			&cli.StringFlag{
				Name:    "enclave-tag",
				Aliases: []string{"e"},
				Usage:   "Keychain Secure Enclave tag (default: com.obol.inference.<name>)",
				Sources: cli.EnvVars("OBOL_ENCLAVE_TAG"),
			},
			&cli.BoolFlag{
				Name:  "vm",
				Usage: "Run Ollama inside an Apple Containerization Linux micro-VM",
			},
			&cli.StringFlag{
				Name:  "vm-image",
				Usage: "OCI image for the VM container",
				Value: "ollama/ollama:latest",
			},
			&cli.IntFlag{
				Name:  "vm-cpus",
				Usage: "vCPUs for the VM",
				Value: 4,
			},
			&cli.IntFlag{
				Name:  "vm-memory",
				Usage: "RAM for the VM in MiB",
				Value: 8192,
			},
			&cli.IntFlag{
				Name:  "vm-host-port",
				Usage: "Host port mapped from the VM's Ollama port 11434",
				Value: 11435,
			},
			&cli.StringFlag{
				Name:    "tee",
				Usage:   "Linux TEE backend: tdx, snp, nitro, or stub",
				Sources: cli.EnvVars("OBOL_TEE_TYPE"),
			},
			&cli.StringFlag{
				Name:    "model-hash",
				Usage:   "SHA-256 of model weights for TEE attestation (required with --tee)",
				Sources: cli.EnvVars("OBOL_MODEL_HASH"),
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			name := cmd.Args().First()
			if name == "" {
				return fmt.Errorf("name required: obol sell inference <name> --wallet <addr>")
			}

			wallet := cmd.String("wallet")
			if wallet == "" {
				return fmt.Errorf("wallet required: use --wallet <addr> or set X402_WALLET")
			}
			if err := x402verifier.ValidateWallet(wallet); err != nil {
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

			chain, err := resolveX402Chain(cmd.String("chain"))
			if err != nil {
				return err
			}

			d := &inference.Deployment{
				Name:            name,
				EnclaveTag:      cmd.String("enclave-tag"),
				ListenAddr:      cmd.String("listen"),
				UpstreamURL:     cmd.String("upstream"),
				WalletAddress:   wallet,
				PricePerRequest: cmd.String("price"),
				Chain:           cmd.String("chain"),
				FacilitatorURL:  cmd.String("facilitator"),
				VMMode:          cmd.Bool("vm"),
				VMImage:         cmd.String("vm-image"),
				VMCPUs:          int(cmd.Int("vm-cpus")),
				VMMemoryMB:      int(cmd.Int("vm-memory")),
				VMHostPort:      int(cmd.Int("vm-host-port")),
				TEEType:         teeType,
				ModelHash:       modelHash,
			}

			// Persist the deployment config for later reference.
			store := inference.NewStore(cfg.ConfigDir)
			if err := store.Create(d, true); err != nil {
				return err
			}

			return runInferenceGateway(d, chain)
		},
	}
}

// ---------------------------------------------------------------------------
// sell http — create a ServiceOffer CRD for any HTTP service
// ---------------------------------------------------------------------------

func sellHTTPCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "http",
		Usage:     "Sell access to any HTTP service via x402 (cluster-based)",
		ArgsUsage: "<name>",
		Description: `Creates a ServiceOffer in the cluster. The agent reconciles it through:
health-check → payment gate → route publishing → optional ERC-8004 registration.

Examples:
  obol sell http my-api --upstream my-svc --port 8080 --wallet 0x... --price 0.01
  obol sell http my-db-proxy --upstream pgbouncer --port 5432 --wallet 0x... --chain base`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "wallet",
				Aliases:  []string{"w"},
				Usage:    "USDC recipient wallet address",
				Sources:  cli.EnvVars("X402_WALLET"),
				Required: true,
			},
			&cli.StringFlag{
				Name:     "chain",
				Usage:    "Payment chain (e.g. base-sepolia, base)",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "price",
				Usage: "Per-request price in USDC (e.g. 0.001)",
			},
			&cli.StringFlag{
				Name:  "per-request",
				Usage: "Per-request price in USDC (alias for --price)",
			},
			&cli.StringFlag{
				Name:  "per-hour",
				Usage: "Per-compute-hour price in USDC",
			},
			&cli.StringFlag{
				Name:    "namespace",
				Aliases: []string{"n"},
				Usage:   "Target namespace for the ServiceOffer",
				Value:   "default",
			},
			&cli.StringFlag{
				Name:  "upstream",
				Usage: "Upstream service name",
			},
			&cli.IntFlag{
				Name:  "port",
				Usage: "Upstream service port",
				Value: 8080,
			},
			&cli.StringFlag{
				Name:  "health-path",
				Usage: "Upstream health check path",
				Value: "/health",
			},
			&cli.StringFlag{
				Name:  "path",
				Usage: "URL path prefix (default: /services/<name>)",
			},
			&cli.IntFlag{
				Name:  "max-timeout",
				Usage: "Payment validity window in seconds",
				Value: 300,
			},
			// Registration flags
			&cli.BoolFlag{
				Name:  "register",
				Usage: "Register on ERC-8004 after routing is live",
			},
			&cli.StringFlag{
				Name:  "register-name",
				Usage: "Agent name for ERC-8004 registration",
			},
			&cli.StringFlag{
				Name:  "register-description",
				Usage: "Agent description for ERC-8004 registration",
			},
			&cli.StringFlag{
				Name:  "register-image",
				Usage: "Agent image URL for ERC-8004 registration",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() == 0 {
				return fmt.Errorf("name required: obol sell http <name> --wallet <addr> --chain <chain>")
			}
			name := cmd.Args().First()
			ns := cmd.String("namespace")

			// Resolve price: --price takes precedence, then --per-request.
			perRequest := cmd.String("price")
			if perRequest == "" {
				perRequest = cmd.String("per-request")
			}
			perHour := cmd.String("per-hour")
			if perRequest == "" && perHour == "" {
				return fmt.Errorf("price required: use --price or --per-hour")
			}

			price := map[string]interface{}{}
			if perRequest != "" {
				price["perRequest"] = perRequest
			}
			if perHour != "" {
				price["perHour"] = perHour
			}

			spec := map[string]interface{}{
				"type": "http",
				"upstream": map[string]interface{}{
					"service":    cmd.String("upstream"),
					"namespace":  ns,
					"port":       cmd.Int("port"),
					"healthPath": cmd.String("health-path"),
				},
				"payment": map[string]interface{}{
					"scheme":            "exact",
					"network":           cmd.String("chain"),
					"payTo":             cmd.String("wallet"),
					"maxTimeoutSeconds": cmd.Int("max-timeout"),
					"price":             price,
				},
			}

			if path := cmd.String("path"); path != "" {
				spec["path"] = path
			}

			if cmd.Bool("register") || cmd.String("register-name") != "" {
				reg := map[string]interface{}{
					"enabled": cmd.Bool("register"),
				}
				if n := cmd.String("register-name"); n != "" {
					reg["name"] = n
				}
				if d := cmd.String("register-description"); d != "" {
					reg["description"] = d
				}
				if img := cmd.String("register-image"); img != "" {
					reg["image"] = img
				}
				spec["registration"] = reg
			}

			manifest := map[string]interface{}{
				"apiVersion": "obol.org/v1alpha1",
				"kind":       "ServiceOffer",
				"metadata": map[string]interface{}{
					"name":      name,
					"namespace": ns,
				},
				"spec": spec,
			}

			if err := kubectlApply(cfg, manifest); err != nil {
				return err
			}
			fmt.Printf("ServiceOffer %s/%s created (type: http)\n", ns, name)
			fmt.Printf("The agent will reconcile: health-check → payment gate → route\n")
			fmt.Printf("Check status: obol sell status %s -n %s\n", name, ns)
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// sell list
// ---------------------------------------------------------------------------

func sellListCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List all ServiceOffer CRs",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "namespace",
				Aliases: []string{"n"},
				Usage:   "Filter by namespace (default: all namespaces)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := []string{"get", "serviceoffers.obol.org"}
			if ns := cmd.String("namespace"); ns != "" {
				args = append(args, "-n", ns)
			} else {
				args = append(args, "-A")
			}
			args = append(args, "-o", "wide")
			return kubectlRun(cfg, args...)
		},
	}
}

// ---------------------------------------------------------------------------
// sell status — merged offer-status + global status
// ---------------------------------------------------------------------------

func sellStatusCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "status",
		Usage:     "Show offer status (with name) or global pricing config (without name)",
		ArgsUsage: "[name]",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "namespace",
				Aliases: []string{"n"},
				Usage:   "Namespace of the ServiceOffer",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// If a name is provided, show per-offer conditions.
			if cmd.NArg() > 0 {
				name := cmd.Args().First()
				ns := cmd.String("namespace")
				if ns == "" {
					return fmt.Errorf("namespace required: obol sell status <name> -n <ns>")
				}
				return kubectlRun(cfg, "get", "serviceoffers.obol.org", name, "-n", ns, "-o", "yaml")
			}

			// No name: show global pricing config + registrations.
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
					payTo := r.PayTo
					if payTo == "" {
						payTo = "(global)"
					}
					fmt.Printf("    %s → %s USDC  payTo=%s  %s\n", r.Pattern, r.Price, payTo, desc)
				}
			}

			fmt.Println()

			fmt.Printf("ERC-8004 Registration:\n")
			kubectlRun(cfg, "get", "serviceoffers.obol.org", "-A",
				"-o", "custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,AGENT_ID:.status.agentId,TX:.status.registrationTxHash,REGISTERED:.status.conditions[?(@.type=='Registered')].status")

			// Also show local inference gateway deployments.
			store := inference.NewStore(cfg.ConfigDir)
			deployments, _ := store.List()
			if len(deployments) > 0 {
				fmt.Printf("\nLocal Inference Gateways:\n")
				for _, d := range deployments {
					fmt.Printf("  %-20s %s → %s  %s USDC/req  chain=%s\n",
						d.Name, d.ListenAddr, d.UpstreamURL, d.PricePerRequest, d.Chain)
				}
			}

			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// sell stop
// ---------------------------------------------------------------------------

func sellStopCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "stop",
		Usage:     "Stop serving a ServiceOffer (removes pricing route, keeps CR)",
		ArgsUsage: "<name>",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "namespace",
				Aliases:  []string{"n"},
				Usage:    "Namespace of the ServiceOffer",
				Required: true,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() == 0 {
				return fmt.Errorf("name required: obol sell stop <name> -n <ns>")
			}
			name := cmd.Args().First()
			ns := cmd.String("namespace")

			fmt.Printf("Stopping ServiceOffer %s/%s...\n", ns, name)

			removePricingRoute(cfg, name)

			patchJSON := `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"Stopped","message":"Offer stopped by user"}]}}`
			err := kubectlRun(cfg, "patch", "serviceoffers.obol.org", name, "-n", ns,
				"--type=merge", "--subresource=status", "-p", patchJSON)
			if err != nil {
				return fmt.Errorf("failed to patch status: %w", err)
			}

			fmt.Printf("ServiceOffer %s/%s stopped.\n", ns, name)
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// sell delete
// ---------------------------------------------------------------------------

func sellDeleteCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "delete",
		Usage:     "Delete a ServiceOffer CR and deactivate ERC-8004 registration",
		ArgsUsage: "<name>",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "namespace",
				Aliases:  []string{"n"},
				Usage:    "Namespace of the ServiceOffer",
				Required: true,
			},
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Usage:   "Skip confirmation",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() == 0 {
				return fmt.Errorf("name required: obol sell delete <name> -n <ns>")
			}
			name := cmd.Args().First()
			ns := cmd.String("namespace")

			if !cmd.Bool("force") {
				fmt.Printf("Delete ServiceOffer %s/%s? This will:\n", ns, name)
				fmt.Println("  - Remove the associated Middleware and HTTPRoute")
				fmt.Println("  - Remove the pricing route from the x402 verifier")
				fmt.Println("  - Deactivate the ERC-8004 registration (if registered)")
				fmt.Print("[y/N] ")
				var response string
				fmt.Scanln(&response)
				if !strings.EqualFold(response, "y") && !strings.EqualFold(response, "yes") {
					fmt.Println("Aborted.")
					return nil
				}
			}

			removePricingRoute(cfg, name)

			soOut, err := kubectlOutput(cfg, "get", "serviceoffers.obol.org", name, "-n", ns,
				"-o", "jsonpath={.status.agentId}")
			if err == nil && strings.TrimSpace(soOut) != "" {
				agentID := strings.TrimSpace(soOut)
				fmt.Printf("Deactivating ERC-8004 registration (agent %s)...\n", agentID)

				cmName := fmt.Sprintf("so-%s-registration", name)
				rawJSON, readErr := kubectlOutput(cfg, "get", "configmap", cmName, "-n", ns,
					"-o", `jsonpath={.data.agent-registration\.json}`)
				if readErr != nil || strings.TrimSpace(rawJSON) == "" {
					fmt.Printf("  No registration document found. Agent %s NFT persists on-chain.\n", agentID)
				} else {
					var regDoc map[string]interface{}
					if jsonErr := json.Unmarshal([]byte(rawJSON), &regDoc); jsonErr != nil {
						fmt.Printf("  Warning: corrupt registration JSON, skipping deactivation: %v\n", jsonErr)
					} else {
						regDoc["active"] = false
						patchJSON, _ := json.Marshal(map[string]interface{}{
							"data": map[string]string{
								"agent-registration.json": mustMarshal(regDoc),
							},
						})
						if patchErr := kubectlRun(cfg, "patch", "configmap", cmName, "-n", ns,
							"-p", string(patchJSON), "--type=merge"); patchErr != nil {
							fmt.Printf("  Warning: could not deactivate registration: %v\n", patchErr)
						} else {
							fmt.Printf("  Registration deactivated (active=false). On-chain NFT persists.\n")
						}
					}
				}
			}

			return kubectlRun(cfg, "delete", "serviceoffers.obol.org", name, "-n", ns)
		},
	}
}

// ---------------------------------------------------------------------------
// sell pricing
// ---------------------------------------------------------------------------

func sellPricingCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "pricing",
		Usage: "Configure x402 pricing in the cluster",
		Description: `Sets the wallet address and chain for x402 payment collection.
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
			&cli.StringFlag{
				Name:    "facilitator-url",
				Usage:   "x402 facilitator URL",
				Sources: cli.EnvVars("X402_FACILITATOR_URL"),
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			wallet := cmd.String("wallet")
			if err := x402verifier.ValidateWallet(wallet); err != nil {
				return err
			}
			return x402verifier.Setup(cfg, wallet, cmd.String("chain"), cmd.String("facilitator-url"))
		},
	}
}

// ---------------------------------------------------------------------------
// sell register
// ---------------------------------------------------------------------------

func sellRegisterCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "register",
		Usage: "Register service on ERC-8004 Identity Registry (Base Sepolia)",
		Description: `Mints an agent NFT on the ERC-8004 Identity Registry.
Requires a funded Base Sepolia wallet (private key).`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "private-key",
				Usage:   "DEPRECATED: use --private-key-file or ERC8004_PRIVATE_KEY env var",
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
				tunnelURL, err := tunnel.GetTunnelURL(cfg)
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

			x402Meta := []byte(`{"x402":true}`)
			if err := client.SetMetadata(ctx, key, agentID, "x402", x402Meta); err != nil {
				fmt.Printf("  Warning: failed to set x402 metadata: %v\n", err)
			}

			fmt.Printf("  Registry:  eip155:%d:%s\n", erc8004.BaseSepoliaChainID, erc8004.IdentityRegistryBaseSepolia)

			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// inference gateway helpers (from service.go)
// ---------------------------------------------------------------------------

// runInferenceGateway starts the x402 inference gateway and blocks until shutdown.
func runInferenceGateway(d *inference.Deployment, chain x402.ChainConfig) error {
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

// resolveX402Chain maps a chain name to an x402 ChainConfig.
func resolveX402Chain(name string) (x402.ChainConfig, error) {
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

// sellInfoCommand returns info about a local inference gateway deployment.
// Kept for the enclave pubkey functionality.
func sellInfoCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "info",
		Usage:     "Show inference gateway deployment details and encryption key",
		ArgsUsage: "<name>",
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
				return fmt.Errorf("usage: obol sell info <name>")
			}

			store := inference.NewStore(cfg.ConfigDir)
			d, err := store.Get(name)
			if err != nil {
				return err
			}

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
// kubectl helpers
// ---------------------------------------------------------------------------

func kubectlApply(cfg *config.Config, manifest interface{}) error {
	raw, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}
	bin, kc := kubectl.Paths(cfg)
	return kubectl.Apply(bin, kc, raw)
}

func kubectlOutput(cfg *config.Config, args ...string) (string, error) {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return "", err
	}
	bin, kc := kubectl.Paths(cfg)
	return kubectl.Output(bin, kc, args...)
}

func kubectlRun(cfg *config.Config, args ...string) error {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return err
	}
	bin, kc := kubectl.Paths(cfg)
	return kubectl.Run(bin, kc, args...)
}

func mustMarshal(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func valueOrNone(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}

// removePricingRoute removes the x402-verifier pricing route for the given offer.
func removePricingRoute(cfg *config.Config, name string) {
	urlPath := fmt.Sprintf("/services/%s", name)
	pricingCfg, err := x402verifier.GetPricingConfig(cfg)
	if err != nil {
		return
	}
	updatedRoutes := make([]x402verifier.RouteRule, 0, len(pricingCfg.Routes))
	for _, r := range pricingCfg.Routes {
		if !strings.Contains(r.Pattern, urlPath) {
			updatedRoutes = append(updatedRoutes, r)
		}
	}
	if len(updatedRoutes) < len(pricingCfg.Routes) {
		pricingCfg.Routes = updatedRoutes
		if err := x402verifier.WritePricingConfig(cfg, pricingCfg); err != nil {
			fmt.Printf("Warning: failed to remove pricing route: %v\n", err)
		} else {
			fmt.Printf("Removed pricing route for %s\n", urlPath)
		}
	}
}
