package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/inference"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/stack"
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
			sellProbeCommand(cfg),
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
				Name:  "per-request",
				Usage: "Per-request price in USDC (alias for --price)",
			},
			&cli.StringFlag{
				Name:  "per-mtok",
				Usage: "Per-million-tokens price in USDC (charged as an approximation at 1000 tok/request)",
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
			&cli.StringFlag{
				Name:  "provenance-file",
				Usage: "Path to JSON file with provenance metadata (e.g. autoresearch experiment results)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			name := cmd.Args().First()
			if name == "" {
				return errors.New("name required: obol sell inference <name> --wallet <addr>")
			}

			wallet := cmd.String("wallet")
			if wallet == "" {
				return errors.New("wallet required: use --wallet <addr> or set X402_WALLET")
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
					return errors.New("--model-hash is required when --tee is set")
				}
			}

			chain, err := resolveX402Chain(cmd.String("chain"))
			if err != nil {
				return err
			}

			priceTable, err := resolvePriceTable(cmd, false)
			if err != nil {
				return err
			}

			perRequest, err := priceTable.EffectiveRequestPriceE()
			if err != nil {
				return fmt.Errorf("invalid pricing: %w", err)
			}

			d := &inference.Deployment{
				Name:            name,
				EnclaveTag:      cmd.String("enclave-tag"),
				ListenAddr:      cmd.String("listen"),
				UpstreamURL:     cmd.String("upstream"),
				WalletAddress:   wallet,
				PricePerRequest: perRequest,
				PricePerMTok:    priceTable.PerMTok,
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

			if pf := cmd.String("provenance-file"); pf != "" {
				prov, err := loadProvenance(pf)
				if err != nil {
					return fmt.Errorf("load provenance: %w", err)
				}

				d.Provenance = prov
				fmt.Printf("Loaded provenance: %s (metric %s=%s, params %s)\n",
					prov.Framework, prov.MetricName, prov.MetricValue, prov.ParamCount)
			}

			if priceTable.PerMTok != "" {
				d.ApproxTokensPerRequest = schemas.ApproxTokensPerRequest
			}

			// Persist the deployment config for later reference.
			store := inference.NewStore(cfg.ConfigDir)
			if err := store.Create(d, true); err != nil {
				return err
			}

			// If a cluster is available, route through the cluster's x402 flow
			// (tunnel → Traefik → x402-verifier → host gateway → Ollama).
			// The gateway's built-in x402 is disabled to avoid double-gating.
			kubeconfigPath := cfg.ConfigDir + "/kubeconfig.yaml"

			clusterAvailable := false
			if _, statErr := os.Stat(kubeconfigPath); statErr == nil {
				clusterAvailable = true
			}

			if clusterAvailable {
				d.NoPaymentGate = true

				// Resolve the gateway port from the listen address.
				listenAddr := d.ListenAddr

				port := "8402"
				if idx := strings.LastIndex(listenAddr, ":"); idx >= 0 {
					port = listenAddr[idx+1:]
				}

				// Bind to loopback only — the cluster reaches us via the
				// K8s Service+Endpoints bridge; there is no reason to expose
				// the unpaid gateway on all interfaces.
				d.ListenAddr = "127.0.0.1:" + port

				// Create a K8s Service + Endpoints pointing to the host.
				svcNs := "llm" // co-locate with LiteLLM for simplicity
				if err := createHostService(cfg, name, svcNs, port); err != nil {
					fmt.Printf("Warning: could not create cluster service: %v\n", err)
					fmt.Println("Falling back to standalone mode with built-in x402 payment gate.")

					d.NoPaymentGate = false
				} else {
					// Create a ServiceOffer CR pointing at the host service.
					soSpec := buildInferenceServiceOfferSpec(d, priceTable, svcNs, port)

					soManifest := map[string]any{
						"apiVersion": "obol.org/v1alpha1",
						"kind":       "ServiceOffer",
						"metadata": map[string]any{
							"name":      name,
							"namespace": svcNs,
						},
						"spec": soSpec,
					}
					if err := kubectlApply(cfg, soManifest); err != nil {
						fmt.Printf("Warning: could not create ServiceOffer: %v\n", err)

						d.NoPaymentGate = false
					} else {
						fmt.Printf("ServiceOffer %s/%s created (type: inference, routed via cluster)\n", svcNs, name)

						// Ensure tunnel is active.
						u := getUI(cmd)
						u.Blank()
						u.Info("Ensuring tunnel is active for public access...")

						if tunnelURL, tErr := tunnel.EnsureTunnelForSell(cfg, u); tErr != nil {
							u.Warnf("Tunnel not started: %v", tErr)
							u.Dim("  Start manually with: obol tunnel restart")
						} else {
							u.Successf("Tunnel active: %s", tunnelURL)
						}
					}
				}
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
				Name:  "per-mtok",
				Usage: "Per-million-tokens price in USDC (charged as an approximation at 1000 tok/request)",
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
			&cli.StringSliceFlag{
				Name:  "register-skills",
				Usage: "OASF skills for discovery (e.g. natural_language_processing/text_generation)",
			},
			&cli.StringSliceFlag{
				Name:  "register-domains",
				Usage: "OASF domains for discovery (e.g. technology/artificial_intelligence)",
			},
			&cli.StringSliceFlag{
				Name:  "register-metadata",
				Usage: "Additional registration metadata as key=value pairs (repeatable, e.g. gpu=A100-80GB)",
			},
			&cli.StringFlag{
				Name:  "provenance-file",
				Usage: "Path to JSON file with provenance metadata (e.g. autoresearch experiment results)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() == 0 {
				return errors.New("name required: obol sell http <name> --wallet <addr> --chain <chain>")
			}

			name := cmd.Args().First()
			ns := cmd.String("namespace")

			priceTable, err := resolvePriceTable(cmd, true)
			if err != nil {
				return err
			}

			price := map[string]any{}

			switch {
			case priceTable.PerRequest != "":
				price["perRequest"] = priceTable.PerRequest
			case priceTable.PerMTok != "":
				price["perMTok"] = priceTable.PerMTok
			case priceTable.PerHour != "":
				price["perHour"] = priceTable.PerHour
			}

			spec := map[string]any{
				"type": "http",
				"upstream": map[string]any{
					"service":    cmd.String("upstream"),
					"namespace":  ns,
					"port":       cmd.Int("port"),
					"healthPath": cmd.String("health-path"),
				},
				"payment": map[string]any{
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

			if pf := cmd.String("provenance-file"); pf != "" {
				prov, err := loadProvenance(pf)
				if err != nil {
					return fmt.Errorf("load provenance: %w", err)
				}
				// Round-trip through JSON to build the map, respecting omitempty tags.
				provBytes, err := json.Marshal(prov)
				if err != nil {
					return fmt.Errorf("marshal provenance: %w", err)
				}

				var provMap map[string]any
				if err := json.Unmarshal(provBytes, &provMap); err != nil {
					return fmt.Errorf("unmarshal provenance: %w", err)
				}

				spec["provenance"] = provMap

				fmt.Printf("Loaded provenance: %s (metric %s=%s, params %s)\n",
					prov.Framework, prov.MetricName, prov.MetricValue, prov.ParamCount)
			}

			if cmd.Bool("register") || cmd.String("register-name") != "" {
				reg := map[string]any{
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

				if skills := cmd.StringSlice("register-skills"); len(skills) > 0 {
					reg["skills"] = skills
				}

				if domains := cmd.StringSlice("register-domains"); len(domains) > 0 {
					reg["domains"] = domains
				}

				if metaPairs := cmd.StringSlice("register-metadata"); len(metaPairs) > 0 {
					meta, err := parseMetadataPairs(metaPairs)
					if err != nil {
						return err
					}

					reg["metadata"] = meta
				}

				spec["registration"] = reg
			}

			manifest := map[string]any{
				"apiVersion": "obol.org/v1alpha1",
				"kind":       "ServiceOffer",
				"metadata": map[string]any{
					"name":      name,
					"namespace": ns,
				},
				"spec": spec,
			}

			if err := kubectlApply(cfg, manifest); err != nil {
				return err
			}

			fmt.Printf("ServiceOffer %s/%s created (type: http)\n", ns, name)

			if priceTable.PerMTok != "" {
				fmt.Printf("Requests will be charged at %s\n", formatPriceTableSummary(priceTable))
			}

			fmt.Printf("The agent will reconcile: health-check → payment gate → route\n")
			fmt.Printf("Check status: obol sell status %s -n %s\n", name, ns)

			// Ensure tunnel is active for public access.
			u := getUI(cmd)
			u.Blank()
			u.Info("Ensuring tunnel is active for public access...")

			if tunnelURL, err := tunnel.EnsureTunnelForSell(cfg, u); err != nil {
				u.Warnf("Tunnel not started: %v", err)
				u.Dim("  Start manually with: obol tunnel restart")
			} else {
				u.Successf("Tunnel active: %s", tunnelURL)
			}

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
					return errors.New("namespace required: obol sell status <name> -n <ns>")
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

					fmt.Printf("    %s → %s  payTo=%s  %s\n", r.Pattern, formatRoutePriceSummary(r), payTo, desc)
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
					fmt.Printf("  %-20s %s → %s  %s  chain=%s\n",
						d.Name, d.ListenAddr, d.UpstreamURL, formatInferencePriceSummary(d), d.Chain)
				}
			}

			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// sell probe — send an unauthenticated request to verify 402 payment gate
// ---------------------------------------------------------------------------

func sellProbeCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "probe",
		Usage:     "Probe a ServiceOffer endpoint to verify it returns 402 pricing",
		ArgsUsage: "<name>",
		Description: `Sends an unauthenticated request through Traefik to the ServiceOffer's
endpoint and displays the HTTP status code and x402 pricing response.

A 402 response with x402Version=1 confirms the endpoint is live and payment-gated.

Examples:
  obol sell probe flow-qwen -n llm
  obol sell probe my-api -n default --path /health`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "namespace",
				Aliases: []string{"n"},
				Usage:   "Namespace of the ServiceOffer",
			},
			&cli.StringFlag{
				Name:  "path",
				Usage: "Subpath to probe (appended to the offer's endpoint)",
				Value: "/health",
			},
			&cli.StringFlag{
				Name:  "host",
				Usage: "Traefik host:port",
				Value: "obol.stack:8080",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			name := cmd.Args().First()
			if name == "" {
				return errors.New("name required: obol sell probe <name> -n <ns>")
			}

			ns := cmd.String("namespace")
			if ns == "" {
				return errors.New("namespace required: obol sell probe <name> -n <ns>")
			}

			// Get the ServiceOffer's endpoint from the CR status.
			endpoint, err := kubectlOutput(cfg, "get", "serviceoffers.obol.org", name,
				"-n", ns, "-o", "jsonpath={.status.endpoint}")
			if err != nil {
				return fmt.Errorf("get ServiceOffer %s/%s: %w", ns, name, err)
			}

			endpoint = strings.TrimSpace(endpoint)
			if endpoint == "" {
				return fmt.Errorf("ServiceOffer %s/%s has no endpoint (not yet reconciled?)", ns, name)
			}

			subpath := cmd.String("path")
			probeURL := "http://" + cmd.String("host") + endpoint + subpath
			fmt.Printf("Probing %s ...\n", probeURL)

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
			if err != nil {
				return fmt.Errorf("create request: %w", err)
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("probe failed: %w", err)
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			fmt.Printf("HTTP %d\n", resp.StatusCode)

			if resp.StatusCode == http.StatusPaymentRequired {
				var pretty bytes.Buffer
				if json.Indent(&pretty, body, "", "  ") == nil {
					fmt.Println(pretty.String())
				} else {
					fmt.Println(string(body))
				}

				fmt.Println("\nEndpoint is live and payment-gated.")

				return nil
			}

			if len(body) > 0 {
				fmt.Println(string(body))
			}

			if resp.StatusCode == http.StatusOK {
				fmt.Println("\nWarning: endpoint returned 200 (not payment-gated).")
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
				return errors.New("name required: obol sell stop <name> -n <ns>")
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
				return errors.New("name required: obol sell delete <name> -n <ns>")
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
					var regDoc map[string]any
					if jsonErr := json.Unmarshal([]byte(rawJSON), &regDoc); jsonErr != nil {
						fmt.Printf("  Warning: corrupt registration JSON, skipping deactivation: %v\n", jsonErr)
					} else {
						regDoc["active"] = false

						patchJSON, _ := json.Marshal(map[string]any{
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

			if err := kubectlRun(cfg, "delete", "serviceoffers.obol.org", name, "-n", ns); err != nil {
				return err
			}

			// Auto-stop quick tunnel when no ServiceOffers remain.
			remaining, listErr := kubectlOutput(cfg, "get", "serviceoffers.obol.org", "-A",
				"-o", "jsonpath={.items}")
			if listErr == nil && (remaining == "[]" || strings.TrimSpace(remaining) == "") {
				st, _ := tunnel.LoadTunnelState(cfg)
				if st == nil || st.Mode != "dns" {
					u := getUI(cmd)
					u.Blank()
					u.Info("No ServiceOffers remaining. Stopping quick tunnel.")
					_ = tunnel.Stop(cfg, u)
					_ = tunnel.DeleteStorefront(cfg)
				}
			}

			return nil
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
				return errors.New("private key required: use --private-key-file <path> or set ERC8004_PRIVATE_KEY")
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
					return fmt.Errorf("--endpoint required (tunnel auto-detect failed: %w)", err)
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
		NoPaymentGate:   d.NoPaymentGate,
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

// ---------------------------------------------------------------------------
// kubectl helpers
// ---------------------------------------------------------------------------

func kubectlApply(cfg *config.Config, manifest any) error {
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

func mustMarshal(v any) string {
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

func parseMetadataPairs(values []string) (map[string]string, error) {
	meta := make(map[string]string, len(values))
	for _, raw := range values {
		key, value, ok := strings.Cut(raw, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("invalid --register-metadata value %q: expected key=value", raw)
		}

		meta[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}

	return meta, nil
}

func resolvePriceTable(cmd *cli.Command, allowPerHour bool) (schemas.PriceTable, error) {
	perRequest := cmd.String("price")
	if perRequest == "" {
		perRequest = cmd.String("per-request")
	}

	perMTok := cmd.String("per-mtok")

	var perHour string
	if allowPerHour {
		perHour = cmd.String("per-hour")
	}

	switch {
	case perRequest != "":
		return schemas.PriceTable{PerRequest: perRequest}, nil
	case perMTok != "":
		if _, err := schemas.ApproximateRequestPriceFromPerMTok(perMTok); err != nil {
			return schemas.PriceTable{}, fmt.Errorf("invalid --per-mtok value %q: %w", perMTok, err)
		}

		return schemas.PriceTable{PerMTok: perMTok}, nil
	case perHour != "":
		if _, err := schemas.ApproximateRequestPriceFromPerHour(perHour); err != nil {
			return schemas.PriceTable{}, fmt.Errorf("invalid --per-hour value %q: %w", perHour, err)
		}

		return schemas.PriceTable{PerHour: perHour}, nil
	default:
		if allowPerHour {
			return schemas.PriceTable{}, errors.New("price required: use --price, --per-request, --per-mtok, or --per-hour")
		}

		return schemas.PriceTable{}, errors.New("price required: use --price, --per-request, or --per-mtok")
	}
}

func formatPriceTableSummary(priceTable schemas.PriceTable) string {
	switch {
	case priceTable.PerRequest != "":
		return priceTable.PerRequest + " USDC/request"
	case priceTable.PerMTok != "":
		return fmt.Sprintf("%s USDC/request (approx from %s USDC/MTok @ %d tok/request)",
			priceTable.EffectiveRequestPrice(),
			priceTable.PerMTok,
			schemas.ApproxTokensPerRequest,
		)
	case priceTable.PerHour != "":
		return fmt.Sprintf("%s USDC/request (approx from %s USDC/hour @ %d min/request)",
			priceTable.EffectiveRequestPrice(),
			priceTable.PerHour,
			schemas.ApproxMinutesPerRequest,
		)
	default:
		return "0 USDC/request"
	}
}

func formatRoutePriceSummary(route x402verifier.RouteRule) string {
	if route.PriceModel == "perMTok" && route.PerMTok != "" && route.ApproxTokensPerRequest > 0 {
		return fmt.Sprintf("%s USDC/request (approx from %s USDC/MTok @ %d tok/request)",
			route.Price, route.PerMTok, route.ApproxTokensPerRequest)
	}

	if route.Price != "" {
		return route.Price + " USDC/request"
	}

	return "0 USDC/request"
}

func formatInferencePriceSummary(d *inference.Deployment) string {
	if d.PricePerMTok != "" && d.ApproxTokensPerRequest > 0 {
		return fmt.Sprintf("%s USDC/request (approx from %s USDC/MTok @ %d tok/request)",
			d.PricePerRequest, d.PricePerMTok, d.ApproxTokensPerRequest)
	}

	return d.PricePerRequest + " USDC/request"
}

// loadProvenance reads a provenance JSON file and returns the parsed struct.
func loadProvenance(path string) (*inference.Provenance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var prov inference.Provenance

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&prov); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	return &prov, nil
}

// createHostService creates a headless Service + Endpoints in the cluster
// pointing to the Docker host IP on the given port, so that the cluster can
// route traffic to a host-side inference gateway.
//
// Kubernetes Endpoints require an IP address, not a hostname. We resolve the
// host IP using the same strategy as ollamaHostIPForBackend in internal/stack.
func createHostService(cfg *config.Config, name, ns, port string) error {
	hostIP, err := resolveHostIP(cfg)
	if err != nil {
		return fmt.Errorf("cannot resolve host IP for cluster routing: %w", err)
	}

	portNum, _ := strconv.Atoi(port)

	svc := map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
		},
		"spec": map[string]any{
			"ports": []map[string]any{
				{"port": portNum, "targetPort": portNum, "protocol": "TCP"},
			},
		},
	}
	ep := map[string]any{
		"apiVersion": "v1",
		"kind":       "Endpoints",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
		},
		"subsets": []map[string]any{
			{
				"addresses": []map[string]any{
					{"ip": hostIP},
				},
				"ports": []map[string]any{
					{"port": portNum, "protocol": "TCP"},
				},
			},
		},
	}

	if err := kubectlApply(cfg, svc); err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}

	if err := kubectlApply(cfg, ep); err != nil {
		return fmt.Errorf("failed to create endpoints: %w", err)
	}

	return nil
}

// resolveHostIP returns the host IP reachable from cluster containers.
// For k3s (bare-metal) the host is localhost; for k3d the host is
// reachable via Docker networking.
func resolveHostIP(cfg *config.Config) (string, error) {
	// Check if this is a k3s (bare-metal) backend — host is localhost.
	if backend := stack.DetectExistingBackend(cfg); backend == stack.BackendK3s {
		return "127.0.0.1", nil
	}

	// k3d / Docker: try DNS resolution of host.docker.internal or host.k3d.internal.
	for _, host := range []string{"host.docker.internal", "host.k3d.internal"} {
		if addrs, err := net.LookupHost(host); err == nil && len(addrs) > 0 {
			return addrs[0], nil
		}
	}
	// macOS Docker Desktop fallback: well-known VM gateway.
	if runtime.GOOS == "darwin" {
		return "192.168.65.254", nil
	}
	// Linux fallback: docker0 bridge IP.
	if iface, err := net.InterfaceByName("docker0"); err == nil {
		if addrs, err := iface.Addrs(); err == nil {
			for _, addr := range addrs {
				if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil {
					return ipNet.IP.String(), nil
				}
			}
		}
	}

	return "", errors.New("cannot determine host IP; ensure Docker is running or using k3s backend")
}

// buildInferenceServiceOfferSpec builds a ServiceOffer spec for a host-side
// inference gateway routed through the cluster's x402 flow.
func buildInferenceServiceOfferSpec(d *inference.Deployment, pt schemas.PriceTable, ns, port string) map[string]any {
	portNum, _ := strconv.Atoi(port)
	spec := map[string]any{
		"type": "inference",
		"upstream": map[string]any{
			"service":    d.Name,
			"namespace":  ns,
			"port":       portNum,
			"healthPath": "/health",
		},
		"payment": map[string]any{
			"scheme":  "exact",
			"network": d.Chain,
			"payTo":   d.WalletAddress,
			"price":   map[string]any{},
		},
		"path": "/services/" + d.Name,
	}

	price := spec["payment"].(map[string]any)["price"].(map[string]any)
	if pt.PerMTok != "" {
		price["perMTok"] = pt.PerMTok
	} else {
		price["perRequest"] = d.PricePerRequest
	}

	if d.UpstreamURL != "" {
		spec["model"] = map[string]any{
			"name":    "ollama",
			"runtime": "ollama",
		}
	}

	return spec
}

// removePricingRoute removes the x402-verifier pricing route for the given offer.
func removePricingRoute(cfg *config.Config, name string) {
	urlPath := "/services/" + name

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
