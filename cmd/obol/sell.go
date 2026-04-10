package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/enclave"
	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/inference"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/stack"
	"github.com/ObolNetwork/obol-stack/internal/tee"
	"github.com/ObolNetwork/obol-stack/internal/tunnel"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/validate"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/ethereum/go-ethereum/crypto"
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
			sellTestCommand(cfg),
			sellStopCommand(cfg),
			sellDeleteCommand(cfg),
			sellPricingCommand(cfg),
			sellRegisterCommand(cfg),
			sellInfoCommand(cfg),
		},
	}
}

// ---------------------------------------------------------------------------
// sell inference — start a local x402 gateway for LLM inference
// ---------------------------------------------------------------------------

func sellInferenceCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "inference",
		Usage:     "Sell local model inference with x402 payments",
		ArgsUsage: "<name>",
		Description: `Starts an x402-gated reverse proxy in front of a local Ollama instance.
Buyers pay per-request in USDC to access inference endpoints.

Examples:
  obol sell inference my-qwen --model qwen3.5:4b --wallet 0x... --price 0.001
  obol sell inference my-llama --model llama3:8b --wallet 0x... --chain base`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "model",
				Usage: "Model name to serve (e.g. qwen3.5:4b)",
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
				Usage: "Payment chain (base-sepolia, base, ethereum)",
				Value: "base-sepolia",
			},
			&cli.BoolFlag{
				Name:  "obol-token",
				Usage: "Use Ethereum mainnet OBOL via Permit2 instead of the default chain asset",
			},
			&cli.StringFlag{
				Name:  "facilitator",
				Usage: "x402 facilitator URL",
				Value: x402verifier.DefaultFacilitatorURL,
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
			u := getUI(cmd)
			name := cmd.Args().First()
			if name == "" {
				if u.IsTTY() {
					var err error
					name, err = u.Input("Service name", "")
					if err != nil || name == "" {
						return fmt.Errorf("name required: obol sell inference <name> --wallet <addr>")
					}
				} else {
					return fmt.Errorf("name required: obol sell inference <name> --wallet <addr>")
				}
			}
			if err := validate.Name(name); err != nil {
				return err
			}

			wallet := cmd.String("wallet")
			if wallet == "" {
				if resolved, err := openclaw.ResolveWalletAddress(cfg); err == nil {
					wallet = resolved
					u.Infof("Using wallet from remote-signer: %s", wallet)
				} else if u.IsTTY() {
					var inputErr error
					wallet, inputErr = u.Input("Wallet address (USDC recipient)", "")
					if inputErr != nil || wallet == "" {
						return fmt.Errorf("wallet required: use --wallet <addr> or set X402_WALLET")
					}
				} else {
					return fmt.Errorf("wallet required: use --wallet <addr> or set X402_WALLET")
				}
			}

			if err := x402verifier.ValidateWallet(wallet); err != nil {
				return err
			}

			// Auto-detect model and upstream if --model not specified.
			modelFlag := cmd.String("model")
			upstreamFlag := cmd.String("upstream")
			if modelFlag == "" && u.IsTTY() {
				detected, scanErr := inference.ScanLocalEndpointsContext(ctx)
				if scanErr == nil && len(detected) > 0 {
					u.Blank()
					u.Info("Detected local inference servers:")
					u.Print(inference.FormatEndpointDisplay(detected))

					type pick struct {
						baseURL, modelID string
					}
					var picks []pick
					idx := 1
					for _, ep := range detected {
						for _, m := range ep.Models {
							u.Printf("  [%d] %s — %s (%s)", idx, m.ID, ep.BaseURL(), ep.ServerType)
							picks = append(picks, pick{ep.BaseURL(), m.ID})
							idx++
						}
					}

					if len(picks) == 1 {
						answer, _ := u.Input(fmt.Sprintf("Use %s on %s? [Y/n]", picks[0].modelID, picks[0].baseURL), "")
						answer = strings.TrimSpace(strings.ToLower(answer))
						if answer == "" || answer == "y" || answer == "yes" {
							modelFlag = picks[0].modelID
							upstreamFlag = picks[0].baseURL
						}
					} else if len(picks) > 1 {
						sel, _ := u.Input("Select [1]", "1")
						n, parseErr := strconv.Atoi(strings.TrimSpace(sel))
						if parseErr == nil && n >= 1 && n <= len(picks) {
							modelFlag = picks[n-1].modelID
							upstreamFlag = picks[n-1].baseURL
						}
					}
				}
			}
			if modelFlag == "" {
				return fmt.Errorf("--model is required (or run interactively to auto-detect)")
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

			chainName := cmd.String("chain")
			assetTerms, err := resolveAssetTerms(cmd, &chainName)
			if err != nil {
				return err
			}

			chain, err := x402verifier.ResolveChainInfo(chainName)
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
				UpstreamURL:     upstreamFlag,
				WalletAddress:   wallet,
				PricePerRequest: perRequest,
				PricePerMTok:    priceTable.PerMTok,
				Chain:           chainName,
				FacilitatorURL:  cmd.String("facilitator"),
				VMMode:          cmd.Bool("vm"),
				VMImage:         cmd.String("vm-image"),
				VMCPUs:          cmd.Int("vm-cpus"),
				VMMemoryMB:      cmd.Int("vm-memory"),
				VMHostPort:      cmd.Int("vm-host-port"),
				TEEType:         teeType,
				ModelHash:       modelHash,
			}

			if pf := cmd.String("provenance-file"); pf != "" {
				prov, err := loadProvenance(pf)
				if err != nil {
					return fmt.Errorf("load provenance: %w", err)
				}

				d.Provenance = prov
				u.Infof("Loaded provenance: %s (metric %s=%s, params %s)",
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
					u.Warnf("could not create cluster service: %v", err)
					u.Info("Falling back to standalone mode with built-in x402 payment gate.")
					d.NoPaymentGate = false
				} else {
					// Create a ServiceOffer CR pointing at the host service.
					soSpec, err := buildInferenceServiceOfferSpec(d, priceTable, svcNs, port, assetTerms)
					if err != nil {
						return err
					}

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
						u.Warnf("could not create ServiceOffer: %v", err)
						d.NoPaymentGate = false
					} else {
						u.Successf("ServiceOffer %s/%s created (type: inference, routed via cluster)", svcNs, name)

						// Ensure tunnel is active.
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

			return runInferenceGateway(u, d, chain)
		},
	}
}

// ---------------------------------------------------------------------------
// sell http — create a ServiceOffer CRD for any HTTP service
// ---------------------------------------------------------------------------

func sellHTTPCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "http",
		Usage:     "Sell any local HTTP service with x402 payments",
		ArgsUsage: "<name>",
		Description: `Publishes a payment gated HTTP API to any service within the stack, along with a SKILL.md detailing how to use it.
Include --register to have the service listed on EIP8004 onchain agent registry.

Example:
  obol sell http my-cool-api --upstream my-svc.my-namespace.svc.cluster.local --port 8080 --wallet 0x... --price 0.01 --chain base --register`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "wallet",
				Aliases: []string{"w"},
				Usage:   "USDC recipient wallet address (auto-detected from remote-signer)",
				Sources: cli.EnvVars("X402_WALLET"),
			},
			&cli.StringFlag{
				Name:  "chain",
				Usage: "Payment chain (base-sepolia, base, ethereum)",
				Value: "base-sepolia",
			},
			&cli.BoolFlag{
				Name:  "obol-token",
				Usage: "Use Ethereum mainnet OBOL via Permit2 instead of the default chain asset",
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
			&cli.StringFlag{
				Name:  "from-json",
				Usage: "Read ServiceOffer spec from JSON file (or - for stdin) instead of flags",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			// --from-json: read spec from file/stdin and apply directly.
			if jsonPath := cmd.String("from-json"); jsonPath != "" {
				data, err := readJSONInput(jsonPath)
				if err != nil {
					return err
				}
				var spec map[string]interface{}
				if err := json.Unmarshal(data, &spec); err != nil {
					return fmt.Errorf("parse JSON spec: %w", err)
				}

				name := cmd.Args().First()
				if name == "" {
					// Try metadata.name from the JSON if it looks like a full manifest.
					if md, ok := spec["metadata"].(map[string]interface{}); ok {
						if n, ok := md["name"].(string); ok {
							name = n
						}
					}
				}
				if name == "" {
					return fmt.Errorf("name required: provide as positional arg or metadata.name in JSON")
				}

				ns := cmd.String("namespace")

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
				u.Successf("ServiceOffer %s/%s created from JSON", ns, name)
				return nil
			}

			name := cmd.Args().First()
			if name == "" {
				if u.IsTTY() {
					var err error
					name, err = u.Input("Service name", "")
					if err != nil || name == "" {
						return fmt.Errorf("name required: obol sell http <name> --wallet <addr> --chain <chain>")
					}
				} else {
					return fmt.Errorf("name required: obol sell http <name> --wallet <addr> --chain <chain>")
				}
			}
			if err := validate.Name(name); err != nil {
				return err
			}

			// Auto-discover wallet from remote-signer if not set.
			wallet := cmd.String("wallet")
			if wallet == "" {
				if resolved, err := openclaw.ResolveWalletAddress(cfg); err == nil {
					wallet = resolved
					u.Infof("Using wallet from remote-signer: %s", wallet)
				} else if u.IsTTY() {
					var inputErr error
					wallet, inputErr = u.Input("Wallet address (USDC recipient)", "")
					if inputErr != nil || wallet == "" {
						return fmt.Errorf("wallet required: use --wallet <addr> or set X402_WALLET")
					}
				} else {
					return fmt.Errorf("wallet required: use --wallet <addr> or set X402_WALLET")
				}
			}
			if err := x402verifier.ValidateWallet(wallet); err != nil {
				return err
			}

			ns := cmd.String("namespace")

			if cmd.String("upstream") == "" {
				return fmt.Errorf("upstream service name required: use --upstream <service-name>\n\n  Example: obol sell http %s --upstream my-svc --port 8080 --wallet 0x... --chain base-sepolia --price 0.001", name)
			}
			if cmd.Int("port") == 0 {
				return fmt.Errorf("upstream port required: use --port <port-number>\n\n  Example: obol sell http %s --upstream my-svc --port 8080 --wallet 0x... --chain base-sepolia --price 0.001", name)
			}

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

			chainName := cmd.String("chain")
			assetTerms, err := resolveAssetTerms(cmd, &chainName)
			if err != nil {
				return err
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
					"network":           chainName,
					"payTo":             wallet,
					"maxTimeoutSeconds": cmd.Int("max-timeout"),
					"price":             price,
				},
			}
			if !assetTerms.IsZero() {
				spec["payment"].(map[string]any)["asset"] = assetTerms
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

				u.Infof("Loaded provenance: %s (metric %s=%s, params %s)",
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

			applyOut, err := kubectlApplyOutput(cfg, manifest)
			if err != nil {
				return err
			}
			action := "created"
			if strings.Contains(applyOut, "configured") || strings.Contains(applyOut, "unchanged") {
				action = "updated"
			}
			u.Successf("ServiceOffer %s/%s %s (type: http)", ns, name, action)
			if priceTable.PerMTok != "" {
				u.Infof("Requests will be charged at %s", formatPriceTableSummary(priceTable))
			}
			u.Infof("The agent will reconcile: health-check → payment gate → route")
			u.Infof("Check status: obol sell status %s -n %s", name, ns)

			// Ensure tunnel is active for public access.
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
		Usage: "List all services for sale",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "namespace",
				Aliases: []string{"n"},
				Usage:   "Filter by namespace (default: all namespaces)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			args := []string{"get", "serviceoffers.obol.org"}
			if ns := cmd.String("namespace"); ns != "" {
				args = append(args, "-n", ns)
			} else {
				args = append(args, "-A")
			}
			if u.IsJSON() {
				args = append(args, "-o", "json")
				out, err := kubectlOutput(cfg, args...)
				if err != nil {
					return err
				}
				u.Print(out)
				return nil
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
		Usage:     "Show the status of all services for sale or a specific service by name",
		ArgsUsage: "[name]",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "namespace",
				Aliases: []string{"n"},
				Usage:   "Namespace of the ServiceOffer",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			// If a name is provided, show per-offer conditions.
			if cmd.NArg() > 0 {
				name := cmd.Args().First()

				ns := cmd.String("namespace")
				if ns == "" {
					return errors.New("namespace required: obol sell status <name> -n <ns>")
				}
				outputFmt := "-o"
				outputVal := "yaml"
				if u.IsJSON() {
					outputVal = "json"
				}
				return kubectlRun(cfg, "get", "serviceoffers.obol.org", name, "-n", ns, outputFmt, outputVal)
			}

			// No name: show global pricing config + registrations.
			if u.IsJSON() {
				return sellStatusGlobalJSON(cfg, u)
			}

			pricingCfg, err := x402verifier.GetPricingConfig(cfg)
			if err != nil {
				u.Warnf("Payment configuration not available (%v)", err)
			} else {
				u.Printf("Payment Configuration:")
				u.Printf("  Wallet:      %s", valueOrNone(pricingCfg.Wallet))
				u.Printf("  Chain:       %s", valueOrNone(pricingCfg.Chain))
				u.Printf("  Facilitator: %s", valueOrNone(pricingCfg.FacilitatorURL))
				u.Printf("  Verify Only: %v", pricingCfg.VerifyOnly)
				u.Printf("  Routes:      %d", len(pricingCfg.Routes))
				for _, r := range pricingCfg.Routes {
					desc := r.Description
					if desc == "" {
						desc = "(no description)"
					}

					payTo := r.PayTo
					if payTo == "" {
						payTo = "(global)"
					}
					u.Printf("    %s → %s  payTo=%s  %s", r.Pattern, formatRoutePriceSummary(r), payTo, desc)
				}
			}

			u.Blank()

			u.Printf("ERC-8004 Agent Registration:")
			kubectlRun(cfg, "get", "serviceoffers.obol.org", "-A",
				"-o", "custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,AGENT_ID:.status.agentId,TX:.status.registrationTxHash,REGISTERED:.status.conditions[?(@.type=='Registered')].status")

			// Also show local inference gateway deployments.
			store := inference.NewStore(cfg.ConfigDir)

			deployments, _ := store.List()
			if len(deployments) > 0 {
				u.Blank()
				u.Printf("Local Inference Gateways:")
				for _, d := range deployments {
					u.Printf("  %-20s %s → %s  %s  chain=%s",
						d.Name, d.ListenAddr, d.UpstreamURL, formatInferencePriceSummary(d), d.Chain)
				}
			}

			return nil
		},
	}
}

// sellStatusGlobalJSON outputs the global sell status as JSON.
func sellStatusGlobalJSON(cfg *config.Config, u *ui.UI) error {
	type routeJSON struct {
		Pattern                string `json:"pattern"`
		Price                  string `json:"price"`
		Description            string `json:"description,omitempty"`
		PayTo                  string `json:"pay_to,omitempty"`
		PriceModel             string `json:"price_model,omitempty"`
		PerMTok                string `json:"per_mtok,omitempty"`
		ApproxTokensPerRequest int    `json:"approx_tokens_per_request,omitempty"`
	}
	type gatewayJSON struct {
		Name        string `json:"name"`
		ListenAddr  string `json:"listen_addr"`
		UpstreamURL string `json:"upstream_url"`
		Price       string `json:"price"`
		Chain       string `json:"chain"`
	}
	type statusGlobal struct {
		Payment *struct {
			Wallet         string      `json:"wallet"`
			Chain          string      `json:"chain"`
			FacilitatorURL string      `json:"facilitator_url"`
			VerifyOnly     bool        `json:"verify_only"`
			Routes         []routeJSON `json:"routes"`
		} `json:"payment,omitempty"`
		PaymentError  string          `json:"payment_error,omitempty"`
		Registrations json.RawMessage `json:"registrations,omitempty"`
		LocalGateways []gatewayJSON   `json:"local_gateways,omitempty"`
	}

	var result statusGlobal

	pricingCfg, err := x402verifier.GetPricingConfig(cfg)
	if err != nil {
		result.PaymentError = err.Error()
	} else {
		p := &struct {
			Wallet         string      `json:"wallet"`
			Chain          string      `json:"chain"`
			FacilitatorURL string      `json:"facilitator_url"`
			VerifyOnly     bool        `json:"verify_only"`
			Routes         []routeJSON `json:"routes"`
		}{
			Wallet:         pricingCfg.Wallet,
			Chain:          pricingCfg.Chain,
			FacilitatorURL: pricingCfg.FacilitatorURL,
			VerifyOnly:     pricingCfg.VerifyOnly,
		}
		for _, r := range pricingCfg.Routes {
			p.Routes = append(p.Routes, routeJSON{
				Pattern:                r.Pattern,
				Price:                  r.Price,
				Description:            r.Description,
				PayTo:                  r.PayTo,
				PriceModel:             r.PriceModel,
				PerMTok:                r.PerMTok,
				ApproxTokensPerRequest: r.ApproxTokensPerRequest,
			})
		}
		result.Payment = p
	}

	// Fetch registrations as raw JSON from kubectl.
	regOut, regErr := kubectlOutput(cfg, "get", "serviceoffers.obol.org", "-A", "-o", "json")
	if regErr == nil {
		result.Registrations = json.RawMessage(regOut)
	}

	// Local inference gateways.
	store := inference.NewStore(cfg.ConfigDir)
	deployments, _ := store.List()
	for _, d := range deployments {
		result.LocalGateways = append(result.LocalGateways, gatewayJSON{
			Name:        d.Name,
			ListenAddr:  d.ListenAddr,
			UpstreamURL: d.UpstreamURL,
			Price:       formatInferencePriceSummary(d),
			Chain:       d.Chain,
		})
	}

	return u.JSON(result)
}

// ---------------------------------------------------------------------------
// sell test — verify a service is live and payment-gated
// ---------------------------------------------------------------------------

func sellTestCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "test",
		Usage:     "Test that a service is live and requiring payment",
		ArgsUsage: "<name>",
		Description: `Checks that a published service is reachable and correctly gated behind
x402 payments. Returns the HTTP status and pricing details.

Examples:
  obol sell test flow-qwen -n llm
  obol sell test my-api -n default --path /health`,
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
			u := getUI(cmd)
			name := cmd.Args().First()
			if name == "" {
				return errors.New("name required: obol sell test <name> -n <ns>")
			}

			ns := cmd.String("namespace")
			if ns == "" {
				return errors.New("namespace required: obol sell test <name> -n <ns>")
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
			if !strings.HasPrefix(endpoint, "/") {
				return fmt.Errorf("invalid endpoint %q: must start with /", endpoint)
			}
			if strings.Contains(endpoint, "..") {
				return fmt.Errorf("invalid endpoint %q: path traversal not allowed", endpoint)
			}
			probeURL := "http://" + cmd.String("host") + endpoint + subpath
			u.Infof("Probing %s ...", probeURL)

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
			u.Infof("HTTP %d", resp.StatusCode)

			if resp.StatusCode == http.StatusPaymentRequired {
				var pretty bytes.Buffer
				if json.Indent(&pretty, body, "", "  ") == nil {
					u.Printf(pretty.String())
				} else {
					u.Printf(string(body))
				}

				u.Blank()
				u.Successf("Endpoint is live and payment-gated.")

				return nil
			}

			if len(body) > 0 {
				u.Printf(string(body))
			}

			if resp.StatusCode == http.StatusOK {
				u.Blank()
				u.Warnf("endpoint returned 200 (not payment-gated).")
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
		Usage:     "Pause a ServiceOffer without deleting it",
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
			u := getUI(cmd)
			if cmd.NArg() == 0 {
				return errors.New("name required: obol sell stop <name> -n <ns>")
			}

			name := cmd.Args().First()
			if err := validate.Name(name); err != nil {
				return err
			}
			ns := cmd.String("namespace")

			u.Infof("Stopping the service offering %s/%s...", ns, name)

			removePricingRoute(cfg, u, name)

			patchJSON := `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"Stopped","message":"Offer stopped by user"}]}}`
			err := kubectlRun(cfg, "patch", "serviceoffers.obol.org", name, "-n", ns,
				"--type=merge", "-p", patchJSON)
			if err != nil {
				return fmt.Errorf("failed to pause serviceoffer: %w", err)
			}

			u.Successf("Service offering %s/%s stopped.", ns, name)
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
		Usage:     "Delete the sale of a service entirely and deactivate its ERC-8004 agent registration",
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
			u := getUI(cmd)
			if cmd.NArg() == 0 {
				return errors.New("name required: obol sell delete <name> -n <ns>")
			}

			name := cmd.Args().First()
			if err := validate.Name(name); err != nil {
				return err
			}
			ns := cmd.String("namespace")

			if !cmd.Bool("force") {
				msg := fmt.Sprintf(
					"Delete ServiceOffer %s/%s? This will:\n  - Remove the associated Middleware and HTTPRoute\n  - Remove x402 enforcement for the service\n  - Deactivate the ERC-8004 registration (if registered)\n  - Let the serviceoffer-controller finalizer clean up published state",
					ns,
					name,
				)
				if !u.Confirm(msg, false) {
					u.Info("Aborted.")
					return nil
				}
			}

			removePricingRoute(cfg, u, name)

			soOut, err := kubectlOutput(cfg, "get", "serviceoffers.obol.org", name, "-n", ns,
				"-o", "jsonpath={.status.agentId}")
			if err == nil && strings.TrimSpace(soOut) != "" {
				agentID := strings.TrimSpace(soOut)
				u.Infof("Deactivating ERC-8004 registration (agent %s)...", agentID)

				cmName := fmt.Sprintf("so-%s-registration", name)
				rawJSON, readErr := kubectlOutput(cfg, "get", "configmap", cmName, "-n", ns,
					"-o", `jsonpath={.data.agent-registration\.json}`)
				if readErr != nil || strings.TrimSpace(rawJSON) == "" {
					u.Printf("  No registration document found. Agent %s NFT persists on-chain.", agentID)
				} else {
					var regDoc map[string]interface{}
					if jsonErr := json.Unmarshal([]byte(rawJSON), &regDoc); jsonErr != nil {
						u.Warnf("corrupt registration JSON, skipping deactivation: %v", jsonErr)
					} else {
						regDoc["active"] = false
						patchJSON, _ := json.Marshal(map[string]interface{}{
							"data": map[string]string{
								"agent-registration.json": mustMarshal(regDoc),
							},
						})
						if patchErr := kubectlRun(cfg, "patch", "configmap", cmName, "-n", ns,
							"-p", string(patchJSON), "--type=merge"); patchErr != nil {
							u.Warnf("could not deactivate agent registration: %v", patchErr)
						} else {
							u.Successf("Registration deactivated (active=false). On-chain NFT persists.")
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
		Usage: "Manage service pricing",
		Description: `Sets the wallet address and chain for x402 payment collection.
Reloads the payment verifier when configuration is changed.`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "wallet",
				Usage:   "USDC recipient wallet address (auto-detected from remote-signer)",
				Sources: cli.EnvVars("X402_WALLET"),
			},
			&cli.StringFlag{
				Name:  "chain",
				Usage: "Payment chain (base-sepolia, base, ethereum)",
				Value: "base-sepolia",
			},
			&cli.StringFlag{
				Name:    "facilitator-url",
				Usage:   "x402 facilitator URL",
				Sources: cli.EnvVars("X402_FACILITATOR_URL"),
			},
			&cli.StringFlag{
				Name:  "from-json",
				Usage: "Read pricing config from JSON file (or - for stdin) instead of flags",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			// --from-json: read pricing config from file/stdin.
			if jsonPath := cmd.String("from-json"); jsonPath != "" {
				data, err := readJSONInput(jsonPath)
				if err != nil {
					return err
				}
				var pricingCfg struct {
					Wallet         string `json:"wallet"`
					Chain          string `json:"chain"`
					FacilitatorURL string `json:"facilitatorUrl"`
				}
				if err := json.Unmarshal(data, &pricingCfg); err != nil {
					return fmt.Errorf("parse JSON pricing config: %w", err)
				}
				if pricingCfg.Wallet == "" {
					return fmt.Errorf("wallet is required in JSON input")
				}
				return x402verifier.Setup(cfg, pricingCfg.Wallet, pricingCfg.Chain, pricingCfg.FacilitatorURL)
			}

			wallet := cmd.String("wallet")
			if wallet == "" {
				if resolved, err := openclaw.ResolveWalletAddress(cfg); err == nil {
					wallet = resolved
					u.Infof("Using wallet from remote-signer: %s", wallet)
				} else {
					return fmt.Errorf("wallet required: use --wallet <addr> or set X402_WALLET")
				}
			}
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
		Usage: "Register a service on the ERC-8004 Agent Registry",
		Description: `Registers an agent on the ERC-8004 Agent Registry on one or more chains.
Uses the remote-signer wallet by default. Supports sponsored (zero-gas)
registration on networks that offer it (e.g. ethereum mainnet).

Examples:
  obol sell register                                    # interactive, defaults to base-sepolia
  obol sell register --chain base-sepolia               # register on base-sepolia
  obol sell register --chain mainnet,base               # register on multiple chains
  obol sell register --chain mainnet --sponsored        # zero-gas on ethereum mainnet`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "chain",
				Usage: "Registration chain(s), comma-separated (base-sepolia, base, mainnet)",
				Value: "base-sepolia",
			},
			&cli.BoolFlag{
				Name:  "sponsored",
				Usage: "Use sponsored (zero-gas) registration when available",
			},
			&cli.StringFlag{
				Name:  "endpoint",
				Usage: "Service endpoint URL (auto-detected from tunnel if not set)",
			},
			&cli.StringFlag{
				Name:  "name",
				Usage: "Agent name for registration",
				Value: "Obol Agent",
			},
			&cli.StringFlag{
				Name:  "description",
				Usage: "Agent description",
				Value: "Obol Stack AI agent with x402 payment-gated services",
			},
			&cli.StringFlag{
				Name:  "image",
				Usage: "Agent image URL for registration",
			},
			&cli.StringFlag{
				Name:    "private-key-file",
				Usage:   "Path to private key file (fallback if no remote-signer available)",
				Sources: cli.EnvVars("ERC8004_PRIVATE_KEY"),
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			// Resolve networks.
			chainCSV := cmd.String("chain")
			if u.IsTTY() && !cmd.IsSet("chain") {
				nets := erc8004.SupportedNetworks()
				options := make([]string, len(nets))
				for i, n := range nets {
					label := n.Name
					if n.HasSponsor() {
						label += " (sponsored, zero gas)"
					}
					options[i] = label
				}
				idx, err := u.Select("Registration network", options, 0)
				if err != nil {
					return err
				}
				chainCSV = nets[idx].Name
			}

			networks, err := erc8004.ResolveNetworks(chainCSV)
			if err != nil {
				return err
			}

			// Interactive confirmation of registration metadata.
			agentName := cmd.String("name")
			agentDesc := cmd.String("description")
			if u.IsTTY() {
				if !cmd.IsSet("name") {
					if val, err := u.Input("Agent name", agentName); err == nil && val != "" {
						agentName = val
					}
				}
				if !cmd.IsSet("description") {
					if val, err := u.Input("Agent description", agentDesc); err == nil && val != "" {
						agentDesc = val
					}
				}
			}

			// Resolve endpoint.
			endpoint := cmd.String("endpoint")
			if endpoint == "" {
				tunnelURL, err := tunnel.GetTunnelURL(cfg)
				if err != nil {
					if u.IsTTY() {
						endpoint, _ = u.Input("Service endpoint URL", "")
					}
					if endpoint == "" {
						return fmt.Errorf("--endpoint required (tunnel auto-detect failed: %v)", err)
					}
				} else {
					endpoint = tunnelURL
					u.Infof("Auto-detected endpoint from tunnel: %s", endpoint)
				}
			}
			agentURI := endpoint + "/.well-known/agent-registration.json"

			// Determine signing method: private key file (if explicitly provided)
			// or remote-signer (default when OpenClaw agent is deployed).
			useRemoteSigner := false
			var signerNS string

			// If --private-key-file is explicitly provided, honour user intent.
			if !cmd.IsSet("private-key-file") {
				if _, err := openclaw.ResolveWalletAddress(cfg); err == nil {
					ns, nsErr := openclaw.ResolveInstanceNamespace(cfg)
					if nsErr == nil {
						useRemoteSigner = true
						signerNS = ns
					}
				}
			}

			// Fallback to private key file if no remote-signer.
			var fallbackKey string
			if !useRemoteSigner {
				keyFile := cmd.String("private-key-file")
				if keyFile != "" {
					data, err := os.ReadFile(keyFile)
					if err != nil {
						return fmt.Errorf("read private key file: %w", err)
					}
					fallbackKey = strings.TrimSpace(string(data))
				}
				if fallbackKey == "" {
					return fmt.Errorf("no remote-signer wallet found and no --private-key-file provided.\nRun 'obol agent init' first, or use --private-key-file")
				}
			}

			// Register on each network (best-effort).
			u.Infof("Registering agent on ERC-8004 Agent Registry...")
			u.Printf("  Agent URI: %s", agentURI)
			u.Printf("  Networks:  %s", chainCSV)

			var successes int
			for _, net := range networks {
				u.Blank()
				u.Printf("  [%s] (chain ID %d)", net.Name, net.ChainID)
				u.Printf("    Registry: %s", net.RegistryAddress)

				sponsored := net.HasSponsor() && (cmd.Bool("sponsored") || !cmd.IsSet("sponsored"))

				if sponsored && useRemoteSigner {
					// Sponsored path via remote-signer.
					if err := registerSponsored(ctx, cfg, u, net, agentURI, signerNS); err != nil {
						u.Warnf("sponsored registration failed: %v", err)
						continue
					}
				} else if useRemoteSigner {
					// Direct on-chain via remote-signer (needs funded wallet).
					if err := registerDirectViaSigner(ctx, cfg, u, net, agentURI, signerNS); err != nil {
						u.Warnf("direct registration failed: %v", err)
						continue
					}
				} else {
					// Fallback: direct on-chain with private key file.
					if err := registerDirectWithKey(ctx, cfg, u, net, agentURI, fallbackKey); err != nil {
						u.Warnf("registration failed: %v", err)
						continue
					}
				}

				u.Printf("    CAIP-10:  %s", net.CAIP10Registry())
				successes++
			}

			if successes == 0 {
				return fmt.Errorf("registration failed on all networks")
			}

			u.Blank()
			u.Successf("Agent registered on %d/%d networks.", successes, len(networks))
			return nil
		},
	}
}

// registerSponsored performs a sponsored (zero-gas) registration via the remote-signer.
func registerSponsored(ctx context.Context, cfg *config.Config, u *ui.UI, net erc8004.NetworkConfig, agentURI, namespace string) error {
	u.Printf("    Using sponsored registration (zero gas)...")

	// Port-forward to remote-signer.
	pf, err := startSignerPortForward(cfg, namespace)
	if err != nil {
		return fmt.Errorf("port-forward to remote-signer: %w", err)
	}
	defer pf.Stop()

	signer := erc8004.NewRemoteSigner(fmt.Sprintf("http://localhost:%d", pf.localPort))

	agentID, txHash, err := erc8004.SponsoredRegister(ctx, signer, agentURI, net)
	if err != nil {
		return err
	}

	u.Printf("    Agent ID: %s", agentID.String())
	u.Printf("    Tx hash:  %s", txHash)
	return nil
}

// registerDirectViaSigner performs a direct on-chain registration via the remote-signer.
func registerDirectViaSigner(ctx context.Context, cfg *config.Config, u *ui.UI, net erc8004.NetworkConfig, agentURI, namespace string) error {
	u.Printf("    Using direct on-chain registration via remote-signer...")

	// Port-forward to remote-signer.
	pf, err := startSignerPortForward(cfg, namespace)
	if err != nil {
		return fmt.Errorf("port-forward to remote-signer: %w", err)
	}
	defer pf.Stop()

	signer := erc8004.NewRemoteSigner(fmt.Sprintf("http://localhost:%d", pf.localPort))

	addr, err := signer.GetAddress(ctx)
	if err != nil {
		return err
	}
	u.Printf("    Wallet:   %s", addr.Hex())

	// Connect to eRPC for this network.
	rpcBaseURL := stack.LocalIngressURL(cfg) + "/rpc"
	client, err := erc8004.NewClientForNetwork(ctx, rpcBaseURL, net)
	if err != nil {
		return fmt.Errorf("connect to %s via eRPC: %w", net.Name, err)
	}
	defer client.Close()

	// Create TransactOpts that delegates signing to the remote-signer.
	opts := signer.RemoteTransactOpts(ctx, addr, client.ChainID())

	agentID, err := client.RegisterWithOpts(ctx, opts, agentURI)
	if err != nil {
		return err
	}

	u.Printf("    Agent ID: %s", agentID.String())
	u.Printf("    Owner:    %s", addr.Hex())

	// Set x402 metadata.
	x402Meta := []byte(`{"x402":true}`)
	if err := client.SetMetadataWithOpts(ctx, opts, agentID, "x402", x402Meta); err != nil {
		u.Warnf("failed to set x402 metadata: %v", err)
	}
	return nil
}

// registerDirectWithKey performs a direct on-chain registration using a raw private key.
func registerDirectWithKey(ctx context.Context, cfg *config.Config, u *ui.UI, net erc8004.NetworkConfig, agentURI, keyHex string) error {
	u.Printf("    Using direct on-chain registration with private key...")

	keyHex = strings.TrimPrefix(keyHex, "0x")
	key, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		return fmt.Errorf("invalid private key: %w", err)
	}

	rpcBaseURL := stack.LocalIngressURL(cfg) + "/rpc"
	client, err := erc8004.NewClientForNetwork(ctx, rpcBaseURL, net)
	if err != nil {
		return fmt.Errorf("connect to %s via eRPC: %w", net.Name, err)
	}
	defer client.Close()

	agentID, err := client.Register(ctx, key, agentURI)
	if err != nil {
		return err
	}

	txAddr := crypto.PubkeyToAddress(key.PublicKey)
	u.Printf("    Agent ID: %s", agentID.String())
	u.Printf("    Owner:    %s", txAddr.Hex())

	x402Meta := []byte(`{"x402":true}`)
	if err := client.SetMetadata(ctx, key, agentID, "x402", x402Meta); err != nil {
		u.Warnf("failed to set x402 metadata: %v", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// inference gateway helpers (from service.go)
// ---------------------------------------------------------------------------

// runInferenceGateway starts the x402 inference gateway and blocks until shutdown.
func runInferenceGateway(u *ui.UI, d *inference.Deployment, chain x402verifier.ChainInfo) error {
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
		u.Blank()
		u.Info("Shutting down gateway...")
		if err := gw.Stop(); err != nil {
			u.Errorf("shutdown error: %v", err)
		}
	}()

	return gw.Start()
}

// startSignerPortForward launches a temporary port-forward to the remote-signer
// service in the given namespace. Caller must call pf.Stop() when done.
func startSignerPortForward(cfg *config.Config, namespace string) (*signerPortForwarder, error) {
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, kubectlBinary, "port-forward",
		"svc/remote-signer", ":9000", "-n", namespace)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start port-forward: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	parsedPort := make(chan int, 1)
	parseErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "Forwarding from") {
				parts := strings.Split(line, ":")
				if len(parts) >= 2 {
					portPart := strings.Fields(parts[len(parts)-1])[0]
					var p int
					if _, scanErr := fmt.Sscanf(portPart, "%d", &p); scanErr == nil {
						parsedPort <- p
						io.Copy(io.Discard, stdoutPipe)
						return
					}
				}
			}
		}
		parseErr <- fmt.Errorf("port-forward exited without reporting a local port")
	}()

	select {
	case p := <-parsedPort:
		return &signerPortForwarder{cmd: cmd, localPort: p, done: done, cancel: cancel}, nil
	case err := <-parseErr:
		cancel()
		return nil, err
	case err := <-done:
		cancel()
		if err != nil {
			return nil, fmt.Errorf("port-forward exited: %w", err)
		}
		return nil, fmt.Errorf("port-forward exited unexpectedly")
	case <-time.After(30 * time.Second):
		cancel()
		return nil, fmt.Errorf("timed out waiting for port-forward")
	}
}

// signerPortForwarder manages a background port-forward to the remote-signer.
type signerPortForwarder struct {
	cmd       *exec.Cmd
	localPort int
	done      chan error
	cancel    context.CancelFunc
}

// Stop terminates the port-forward process.
func (pf *signerPortForwarder) Stop() {
	pf.cancel()
	select {
	case <-pf.done:
	case <-time.After(5 * time.Second):
		if pf.cmd.Process != nil {
			pf.cmd.Process.Kill()
		}
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
			u := getUI(cmd)
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

			if u.IsJSON() || cmd.Bool("json") {
				out := map[string]any{
					"name":                      d.Name,
					"enclave_tag":               d.EnclaveTag,
					"listen_addr":               d.ListenAddr,
					"upstream_url":              d.UpstreamURL,
					"wallet_address":            d.WalletAddress,
					"price_per_request":         d.PricePerRequest,
					"price_per_mtok":            d.PricePerMTok,
					"approx_tokens_per_request": d.ApproxTokensPerRequest,
					"chain":                     d.Chain,
					"facilitator_url":           d.FacilitatorURL,
					"created_at":                d.CreatedAt,
					"updated_at":                d.UpdatedAt,
					"algorithm":                 "ECIES-P256-HKDF-SHA256-AES256GCM",
				}
				if keyErr == nil {
					out["pubkey"] = hex.EncodeToString(k.PublicKeyBytes())
					out["persistent"] = k.Persistent()
				} else {
					out["pubkey_error"] = keyErr.Error()
				}
				return u.JSON(out)
			}

			u.Printf("Name:         %s", d.Name)
			u.Printf("Enclave tag:  %s", d.EnclaveTag)
			u.Printf("Algorithm:    ECIES-P256-HKDF-SHA256-AES256GCM")
			if keyErr == nil {
				u.Printf("Pubkey:       %s", hex.EncodeToString(k.PublicKeyBytes()))
				u.Printf("Persistent:   %v", k.Persistent())
			} else {
				u.Printf("Pubkey:       (unavailable: %v)", keyErr)
			}
			u.Blank()
			u.Printf("Listen:       %s", d.ListenAddr)
			u.Printf("Upstream:     %s", d.UpstreamURL)
			u.Printf("Wallet:       %s", d.WalletAddress)
			u.Printf("Price:        %s", formatInferencePriceSummary(d))
			u.Printf("Chain:        %s", d.Chain)
			u.Printf("Facilitator:  %s", d.FacilitatorURL)
			u.Printf("Created:      %s", d.CreatedAt)
			if d.UpdatedAt != "" {
				u.Printf("Updated:      %s", d.UpdatedAt)
			}
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// kubectl helpers
// ---------------------------------------------------------------------------

func kubectlApply(cfg *config.Config, manifest interface{}) error {
	_, err := kubectlApplyOutput(cfg, manifest)
	return err
}

func kubectlApplyOutput(cfg *config.Config, manifest interface{}) (string, error) {
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("failed to marshal manifest: %w", err)
	}

	bin, kc := kubectl.Paths(cfg)
	return kubectl.ApplyOutput(bin, kc, raw)
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

func resolveAssetTerms(cmd *cli.Command, chainName *string) (schemas.AssetTerms, error) {
	if !cmd.Bool("obol-token") {
		return schemas.AssetTerms{}, nil
	}

	if chainName == nil {
		return schemas.AssetTerms{}, fmt.Errorf("internal error: chain name pointer is nil")
	}

	if !cmd.IsSet("chain") {
		*chainName = "ethereum"
	}

	switch strings.ToLower(strings.TrimSpace(*chainName)) {
	case "ethereum", "ethereum-mainnet", "mainnet":
	default:
		return schemas.AssetTerms{}, fmt.Errorf("--obol-token requires --chain ethereum")
	}

	return schemas.AssetTerms{
		Address:        "0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7",
		Symbol:         "OBOL",
		Decimals:       18,
		TransferMethod: schemas.AssetTransferMethodPermit2,
		EIP712Name:     "Obol Network",
		EIP712Version:  "1",
	}, nil
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

	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return fmt.Errorf("invalid port %q: must be a number between 1 and 65535", port)
	}

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
func buildInferenceServiceOfferSpec(d *inference.Deployment, pt schemas.PriceTable, ns, port string, asset schemas.AssetTerms) (map[string]any, error) {
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return nil, fmt.Errorf("invalid port %q: must be a number between 1 and 65535", port)
	}
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
	if !asset.IsZero() {
		spec["payment"].(map[string]any)["asset"] = asset
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

	return spec, nil
}

// removePricingRoute is a no-op retained for compatibility.
// The serviceoffer-controller now manages pricing routes via the ServiceOffer
// informer; static ConfigMap routes are no longer used.
func removePricingRoute(_ *config.Config, _ *ui.UI, _ string) {}
