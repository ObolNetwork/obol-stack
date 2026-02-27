package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
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
			// CRD-based ServiceOffer commands
			monetizeOfferCommand(cfg),
			monetizeListOffersCommand(cfg),
			monetizeOfferStatusCommand(cfg),
			monetizeStopOfferCommand(cfg),
			monetizeDeleteOfferCommand(cfg),
			// Direct commands (backward compat)
			monetizeRegisterCommand(cfg),
			monetizePricingCommand(cfg),
			monetizeStatusCommand(cfg),
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ServiceOffer CRD commands
// ─────────────────────────────────────────────────────────────────────────────

func monetizeOfferCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "offer",
		Usage: "Create a ServiceOffer CR for payment-gated compute",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "type",
				Usage: "Workload type: inference or fine-tuning",
				Value: "inference",
			},
			&cli.StringFlag{
				Name:  "model",
				Usage: "Model name (e.g. qwen3.5:35b)",
			},
			&cli.StringFlag{
				Name:  "runtime",
				Usage: "Model runtime (ollama, vllm, tgi)",
				Value: "ollama",
			},
			&cli.StringFlag{
				Name:  "per-request",
				Usage: "Per-request price in USDC (e.g. 0.001)",
			},
			&cli.StringFlag{
				Name:  "per-mtok",
				Usage: "Per-million-tokens price in USDC (inference only)",
			},
			&cli.StringFlag{
				Name:  "per-hour",
				Usage: "Per-compute-hour price in USDC (fine-tuning only)",
			},
			&cli.StringFlag{
				Name:     "network",
				Usage:    "Payment chain (e.g. base-sepolia, base)",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "pay-to",
				Usage:    "USDC recipient wallet address (x402: payTo)",
				Sources:  cli.EnvVars("X402_WALLET"),
				Required: true,
			},
			&cli.IntFlag{
				Name:  "max-timeout",
				Usage: "Payment validity window in seconds",
				Value: 300,
			},
			&cli.StringFlag{
				Name:  "namespace",
				Usage: "Target namespace for the ServiceOffer",
				Value: "llm",
			},
			&cli.StringFlag{
				Name:  "upstream",
				Usage: "Upstream service name",
				Value: "ollama",
			},
			&cli.IntFlag{
				Name:  "port",
				Usage: "Upstream service port",
				Value: 11434,
			},
			&cli.StringFlag{
				Name:  "path",
				Usage: "URL path prefix (default: /services/<name>)",
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
				Usage: "Agent image URL for ERC-8004 registration (REQUIRED by spec)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() == 0 {
				return fmt.Errorf("name required: obol monetize offer <name> --network ... --pay-to ...")
			}
			name := cmd.Args().First()
			ns := cmd.String("namespace")

			// Validate at least one pricing field is set.
			perRequest := cmd.String("per-request")
			perMTok := cmd.String("per-mtok")
			perHour := cmd.String("per-hour")
			if perRequest == "" && perMTok == "" && perHour == "" {
				return fmt.Errorf("at least one price required: --per-request, --per-mtok, or --per-hour")
			}

			// Build price table.
			price := map[string]interface{}{}
			if perRequest != "" {
				price["perRequest"] = perRequest
			}
			if perMTok != "" {
				price["perMTok"] = perMTok
			}
			if perHour != "" {
				price["perHour"] = perHour
			}

			spec := map[string]interface{}{
				"type": cmd.String("type"),
				"upstream": map[string]interface{}{
					"service":   cmd.String("upstream"),
					"namespace": ns,
					"port":      cmd.Int("port"),
				},
				"payment": map[string]interface{}{
					"scheme":            "exact",
					"network":           cmd.String("network"),
					"payTo":             cmd.String("pay-to"),
					"maxTimeoutSeconds": cmd.Int("max-timeout"),
					"price":             price,
				},
			}

			if model := cmd.String("model"); model != "" {
				spec["model"] = map[string]interface{}{
					"name":    model,
					"runtime": cmd.String("runtime"),
				}
			}

			if path := cmd.String("path"); path != "" {
				spec["path"] = path
			}

			// Build registration section if any registration flags are set.
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

			return kubectlApply(cfg, manifest)
		},
	}
}

func monetizeListOffersCommand(cfg *config.Config) *cli.Command {
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

func monetizeOfferStatusCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "offer-status",
		Usage: "Show conditions for a ServiceOffer",
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
				return fmt.Errorf("name required: obol monetize offer-status <name> --namespace <ns>")
			}
			name := cmd.Args().First()
			ns := cmd.String("namespace")
			return kubectlRun(cfg, "get", "serviceoffers.obol.org", name, "-n", ns, "-o", "yaml")
		},
	}
}

func monetizeStopOfferCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "stop",
		Usage: "Stop serving a ServiceOffer (removes pricing route, keeps CR and registration)",
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
				return fmt.Errorf("name required: obol monetize stop <name> --namespace <ns>")
			}
			name := cmd.Args().First()
			ns := cmd.String("namespace")

			fmt.Printf("Stopping ServiceOffer %s/%s...\n", ns, name)

			// Remove pricing route from x402-verifier ConfigMap.
			// The CR and registration remain intact for restart.
			removePricingRoute(cfg, name)

			// Patch the Ready condition to False via kubectl.
			patchJSON := `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"Stopped","message":"Offer stopped by user"}]}}`
			err := kubectlRun(cfg, "patch", "serviceoffers.obol.org", name, "-n", ns,
				"--type=merge", "--subresource=status", "-p", patchJSON)
			if err != nil {
				return fmt.Errorf("failed to patch status: %w", err)
			}

			fmt.Printf("ServiceOffer %s/%s stopped. Use 'obol monetize offer' to restart.\n", ns, name)
			return nil
		},
	}
}

func monetizeDeleteOfferCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "delete",
		Usage: "Delete a ServiceOffer CR and deactivate ERC-8004 registration",
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
				return fmt.Errorf("name required: obol monetize delete <name> --namespace <ns>")
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

			// Remove x402 pricing route (prevents stale entries after CR deletion).
			removePricingRoute(cfg, name)

			// Deactivate ERC-8004 registration if agentId is set in CRD status.
			soOut, err := kubectlOutput(cfg, "get", "serviceoffers.obol.org", name, "-n", ns,
				"-o", "jsonpath={.status.agentId}")
			if err == nil && strings.TrimSpace(soOut) != "" {
				agentID := strings.TrimSpace(soOut)
				fmt.Printf("Deactivating ERC-8004 registration (agent %s)...\n", agentID)

				// Set active=false in the agent-managed registration ConfigMap.
				cmName := fmt.Sprintf("so-%s-registration", name)
				rawJSON, readErr := kubectlOutput(cfg, "get", "configmap", cmName, "-n", ns,
					"-o", `jsonpath={.data.agent-registration\.json}`)
				if readErr != nil || strings.TrimSpace(rawJSON) == "" {
					fmt.Printf("  No registration document found. Agent %s NFT persists on-chain.\n", agentID)
				} else {
					// Parse, set active=false, write back.
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

			fmt.Printf("  Registry:  eip155:%d:%s\n", erc8004.BaseSepoliaChainID, erc8004.IdentityRegistryBaseSepolia)

			return nil
		},
	}
}

// autoDetectEndpoint tries to discover the tunnel URL from the cluster.
func autoDetectEndpoint(cfg *config.Config) (string, error) {
	return tunnel.GetTunnelURL(cfg)
}

// ─────────────────────────────────────────────────────────────────────────────
// pricing
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
			&cli.StringFlag{
				Name:    "facilitator-url",
				Usage:   "x402 facilitator URL for payment verification (default: https://facilitator.x402.rs)",
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

// ─────────────────────────────────────────────────────────────────────────────
// status (cluster-level pricing + ERC-8004 registration)
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
					payTo := r.PayTo
					if payTo == "" {
						payTo = "(global)"
					}
					fmt.Printf("    %s → %s USDC  payTo=%s  %s\n", r.Pattern, r.Price, payTo, desc)
				}
			}

			fmt.Println()

			// Show ERC-8004 registration from ServiceOffer CRD status (single source of truth).
			fmt.Printf("ERC-8004 Registration:\n")
			kubectlRun(cfg, "get", "serviceoffers.obol.org", "-A",
				"-o", "custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,AGENT_ID:.status.agentId,TX:.status.registrationTxHash,REGISTERED:.status.conditions[?(@.type=='Registered')].status")

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

// ─────────────────────────────────────────────────────────────────────────────
// pricing route helpers
// ─────────────────────────────────────────────────────────────────────────────

// removePricingRoute removes the x402-verifier pricing route matching the
// given offer name. Used by both stop (keeps CR) and delete (removes CR).
func removePricingRoute(cfg *config.Config, name string) {
	urlPath := fmt.Sprintf("/services/%s", name)
	pricingCfg, err := x402verifier.GetPricingConfig(cfg)
	if err != nil {
		return // cluster pricing not available — nothing to clean up
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

// ─────────────────────────────────────────────────────────────────────────────
// kubectl helpers
// ─────────────────────────────────────────────────────────────────────────────

// kubectlApply applies a JSON manifest via kubectl apply -f -.
func kubectlApply(cfg *config.Config, manifest interface{}) error {
	raw, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}
	bin, kc := kubectl.Paths(cfg)
	return kubectl.Apply(bin, kc, raw)
}

// kubectlOutput executes kubectl and captures stdout.
func kubectlOutput(cfg *config.Config, args ...string) (string, error) {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return "", err
	}
	bin, kc := kubectl.Paths(cfg)
	return kubectl.Output(bin, kc, args...)
}

// kubectlRun executes kubectl with the given arguments and stack kubeconfig.
func kubectlRun(cfg *config.Config, args ...string) error {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return err
	}
	bin, kc := kubectl.Paths(cfg)
	return kubectl.Run(bin, kc, args...)
}

// mustMarshal JSON-encodes v, returning "{}" on error.
func mustMarshal(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
