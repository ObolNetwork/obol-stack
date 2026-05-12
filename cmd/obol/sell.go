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
	"math/big"
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
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/images"
	"github.com/ObolNetwork/obol-stack/internal/inference"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/stack"
	"github.com/ObolNetwork/obol-stack/internal/tee"
	"github.com/ObolNetwork/obol-stack/internal/tunnel"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/validate"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli/v3"
)

func sellCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "sell",
		Usage: "Sell access to services via x402 micropayments",
		Commands: []*cli.Command{
			sellInferenceCommand(cfg),
			sellHTTPCommand(cfg),
			sellAgentCommand(cfg),
			sellDemoCommand(cfg),
			sellListCommand(cfg),
			sellStatusCommand(cfg),
			sellTestCommand(cfg),
			sellStopCommand(cfg),
			sellUpdateCommand(cfg),
			sellDeleteCommand(cfg),
			sellPricingCommand(cfg),
			sellRegisterCommand(cfg),
			sellInfoCommand(cfg),
		},
	}
}

// payToFlag returns the standard "where do payments go" flag used across
// all sell commands. The primary name is --pay-to; --wallet and -w are
// kept as deprecated aliases for one minor release. Usage strings are
// wired so help text consistently advertises --pay-to as the canonical
// form. Callers read the value via cmd.String("pay-to").
func payToFlag(usage string) *cli.StringFlag {
	if usage == "" {
		usage = "Token recipient address (auto-detected from remote-signer)"
	}
	return &cli.StringFlag{
		Name:    "pay-to",
		Aliases: []string{"wallet", "recipient", "w"},
		Usage:   usage + " [aliases: --wallet (deprecated), -w]",
		Sources: cli.EnvVars("X402_WALLET"),
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
  obol sell inference my-qwen --model qwen3.5:4b --pay-to 0x... --price 0.001
  obol sell inference my-llama --model llama3:8b --pay-to 0x... --chain base`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "model",
				Usage: "Model name to serve (e.g. qwen3.5:4b)",
			},
			payToFlag("USDC recipient address"),
			&cli.StringFlag{
				Name:  "price",
				Usage: "Per-request price (alias for --per-request; default 0.001 when no price flag is set)",
			},
			&cli.StringFlag{
				Name:  "per-request",
				Usage: "Per-request price (alias for --price)",
			},
			&cli.StringFlag{
				Name:  "per-mtok",
				Usage: "Per-million-tokens price in USDC (charged as an approximation at 1000 tok/request)",
			},
			&cli.StringFlag{
				Name:  "chain",
				Usage: "Payment chain (base, base-sepolia, ethereum)",
				Value: "base",
			},
			&cli.StringFlag{
				Name:  "token",
				Usage: "Payment token (USDC, OBOL)",
				Value: "USDC",
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
			&cli.BoolFlag{
				Name:  "no-register",
				Usage: "Skip ERC-8004 registration (no /.well-known/agent-registration.json HTTPRoute is published)",
			},
			&cli.StringFlag{
				Name:  "register-name",
				Usage: "Agent name for ERC-8004 registration (defaults to the offer name)",
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
				Usage: "OASF skill tags for ERC-8004 registration (repeatable)",
			},
			&cli.StringSliceFlag{
				Name:  "register-domains",
				Usage: "OASF domain tags for ERC-8004 registration (repeatable)",
			},
			&cli.StringSliceFlag{
				Name:  "register-metadata",
				Usage: "Additional registration metadata as key=value pairs (repeatable, e.g. gpu=A100-80GB)",
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
						return fmt.Errorf("name required: obol sell inference <name> --pay-to <addr>")
					}
				} else {
					return fmt.Errorf("name required: obol sell inference <name> --pay-to <addr>")
				}
			}
			if err := validate.Name(name); err != nil {
				return err
			}

			wallet := cmd.String("pay-to")
			if wallet == "" {
				if resolved, err := hermes.ResolveWalletAddress(cfg); err == nil {
					wallet = resolved
					u.Infof("Using wallet from remote-signer: %s", wallet)
				} else if u.IsTTY() {
					var inputErr error
					wallet, inputErr = u.Input("Wallet address (USDC recipient)", "")
					if inputErr != nil || wallet == "" {
						return fmt.Errorf("recipient required: use --pay-to <addr> or set X402_WALLET")
					}
				} else {
					return fmt.Errorf("recipient required: use --pay-to <addr> or set X402_WALLET")
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

			assetSymbol := strings.ToUpper(strings.TrimSpace(assetTerms.Symbol))
			if assetSymbol == "" {
				assetSymbol = "USDC"
			}

			// Resolve the registration block once, here, so we can persist it
			// alongside the deployment descriptor. The resume path
			// (`obol stack up` after a stack-down) rebuilds the ServiceOffer
			// from the on-disk descriptor; without the registration block
			// persisted, replays would lose the operator's --register-*
			// customizations.
			persistedRegistration, _, regErr := buildSellRegistrationConfig(name, sellRegistrationInput{
				NoRegister:    cmd.Bool("no-register"),
				Name:          cmd.String("register-name"),
				Description:   cmd.String("register-description"),
				Image:         cmd.String("register-image"),
				Skills:        cmd.StringSlice("register-skills"),
				Domains:       cmd.StringSlice("register-domains"),
				MetadataPairs: cmd.StringSlice("register-metadata"),
			})
			if regErr != nil {
				return regErr
			}

			d := &inference.Deployment{
				Name:             name,
				EnclaveTag:       cmd.String("enclave-tag"),
				ListenAddr:       cmd.String("listen"),
				UpstreamURL:      upstreamFlag,
				WalletAddress:    wallet,
				PricePerRequest:  perRequest,
				PricePerMTok:     priceTable.PerMTok,
				AssetSymbol:      assetSymbol,
				Chain:            chainName,
				FacilitatorURL:   cmd.String("facilitator"),
				VMMode:           cmd.Bool("vm"),
				VMImage:          cmd.String("vm-image"),
				VMCPUs:           cmd.Int("vm-cpus"),
				VMMemoryMB:       cmd.Int("vm-memory"),
				VMHostPort:       cmd.Int("vm-host-port"),
				TEEType:          teeType,
				ModelHash:        modelHash,
				ModelName:        modelFlag,
				ServiceNamespace: "llm",
				Registration:     persistedRegistration,
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

				// Bind on all interfaces — the in-cluster Endpoints object
				// points at the host's docker-bridge IP (e.g. 172.17.0.1 on
				// Linux), which the kernel only delivers to listeners bound
				// to 0.0.0.0 or the bridge IP itself, not to 127.0.0.1.
				// Loopback-only binding made the cluster see "connection
				// refused" on the host-routed Service.
				d.ListenAddr = "0.0.0.0:" + port

				// Create a K8s Service + Endpoints pointing to the host.
				svcNs := "llm" // co-locate with LiteLLM for simplicity
				if err := createHostService(cfg, name, svcNs, port); err != nil {
					u.Warnf("could not create cluster service: %v", err)
					u.Info("Falling back to standalone mode with built-in x402 payment gate.")
					d.NoPaymentGate = false
				} else {
					// Create a ServiceOffer CR pointing at the host service.
					// Reuse the persistedRegistration resolved above; both this
					// in-process create AND the on-disk descriptor must agree.
					soSpec, err := buildInferenceServiceOfferSpec(d, priceTable, svcNs, port, assetTerms, modelFlag, persistedRegistration)
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

						tunnelURL := ""
						if url, tErr := tunnel.EnsureTunnelForSell(cfg, u); tErr != nil {
							u.Warnf("Tunnel not started: %v", tErr)
							u.Dim("  Start manually with: obol tunnel restart")
						} else {
							tunnelURL = url
							u.Successf("Tunnel active: %s", tunnelURL)
						}

						// Auto-register the seller on ERC-8004, mirroring the
						// `obol sell http` path. Without this step the offer
						// stays in Registered=False AwaitingExternalRegistration
						// forever, which makes Ready=False and excludes the
						// offer from /api/services.json (the storefront feed
						// only includes Ready=True offers).
						if shouldAutoRegisterSell(soSpec, tunnelURL) {
							reg, _ := soSpec["registration"].(map[string]any)
							u.Blank()
							u.Info("Registering seller agent on ERC-8004...")
							if err := autoRegisterServiceOffer(ctx, cfg, u, autoRegisterOptions{
								ChainCSV:      cmd.String("chain"),
								Endpoint:      tunnelURL,
								AgentName:     registrationNameForPrompt(name, reg),
								AgentDesc:     registrationDescriptionForPrompt(name, reg),
								ExpectedOwner: wallet,
							}); err != nil {
								u.Warnf("automatic sell registration failed: %v", err)
								u.Dim("  Re-run later with: obol sell register " + name + " -n " + svcNs)
							}
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
		Description: `Publishes a payment gated HTTP API to any service within the stack.
By default it also registers the seller agent on ERC-8004 after the route is live.
Use --no-register to skip the on-chain registration step.

Examples:
  obol sell http my-cool-api --upstream my-svc.my-namespace.svc.cluster.local --port 8080 --pay-to 0x... --price 0.01 --chain base
  obol sell http my-cool-api --upstream my-svc --port 8080 --pay-to 0x... --price 0.01 --chain base --no-register`,
		Flags: []cli.Flag{
			payToFlag("USDC recipient address"),
			&cli.StringFlag{
				Name:  "chain",
				Usage: "Payment chain (base, base-sepolia, ethereum)",
				Value: "base",
			},
			&cli.StringFlag{
				Name:  "token",
				Usage: "Payment token (USDC, OBOL)",
				Value: "USDC",
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
				Usage: "Deprecated: registration is enabled by default",
			},
			&cli.BoolFlag{
				Name:  "no-register",
				Usage: "Skip the automatic ERC-8004 registration step",
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
						return fmt.Errorf("name required: obol sell http <name> --pay-to <addr> --chain <chain>")
					}
				} else {
					return fmt.Errorf("name required: obol sell http <name> --pay-to <addr> --chain <chain>")
				}
			}
			if err := validate.Name(name); err != nil {
				return err
			}

			// Auto-discover wallet from remote-signer if not set.
			wallet := cmd.String("pay-to")
			if wallet == "" {
				if resolved, err := hermes.ResolveWalletAddress(cfg); err == nil {
					wallet = resolved
					u.Infof("Using wallet from remote-signer: %s", wallet)
				} else if u.IsTTY() {
					var inputErr error
					wallet, inputErr = u.Input("Wallet address (USDC recipient)", "")
					if inputErr != nil || wallet == "" {
						return fmt.Errorf("recipient required: use --pay-to <addr> or set X402_WALLET")
					}
				} else {
					return fmt.Errorf("recipient required: use --pay-to <addr> or set X402_WALLET")
				}
			}
			if err := x402verifier.ValidateWallet(wallet); err != nil {
				return err
			}

			// Ensure the x402-verifier CA bundle is populated so TLS verification of
			// the facilitator works. This is a no-op if already populated. Non-fatal.
			x402verifier.PopulateCABundle(cfg)

			ns := cmd.String("namespace")

			if cmd.String("upstream") == "" {
				return fmt.Errorf("upstream service name required: use --upstream <service-name>\n\n  Example: obol sell http %s --upstream my-svc --port 8080 --pay-to 0x... --chain base-sepolia --price 0.001", name)
			}
			if cmd.Int("port") == 0 {
				return fmt.Errorf("upstream port required: use --port <port-number>\n\n  Example: obol sell http %s --upstream my-svc --port 8080 --pay-to 0x... --chain base-sepolia --price 0.001", name)
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

			reg, registerEnabled, err := buildSellRegistrationConfig(name, sellRegistrationInput{
				NoRegister:    cmd.Bool("no-register"),
				Register:      cmd.Bool("register"),
				Name:          cmd.String("register-name"),
				Description:   cmd.String("register-description"),
				Image:         cmd.String("register-image"),
				Skills:        cmd.StringSlice("register-skills"),
				Domains:       cmd.StringSlice("register-domains"),
				MetadataPairs: cmd.StringSlice("register-metadata"),
			})
			if err != nil {
				return err
			}
			if registerEnabled {
				spec["registration"] = reg
			}

			// When registration is enabled, the serviceoffer-controller reads the
			// public tunnel URL from the obol-frontend ConfigMap to populate
			// .well-known/agent-registration.json. If the tunnel isn't fully ready
			// (deployment rolled out AND a *.trycloudflare.com URL captured) by the
			// time we create the ServiceOffer, the registration reconcile parks in
			// AwaitingExternalRegistration. Block here so that, on success, the
			// controller's first reconcile already has a usable base URL.
			//
			// For --no-register flows the tunnel is best-effort: we still try to
			// bring it up afterwards, but a tunnel failure must not fail the sell.
			var tunnelURL string
			if registerEnabled {
				u.Info("Waiting for Cloudflare tunnel before creating ServiceOffer...")
				url, terr := tunnel.EnsureTunnelForSell(cfg, u)
				if terr != nil {
					return fmt.Errorf("tunnel not ready for registered sell: %w\n\n  Tip: run with --no-register to publish without on-chain registration, or restart the tunnel with 'obol tunnel restart'", terr)
				}
				tunnelURL = url
				u.Successf("Tunnel active: %s", tunnelURL)
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
				u.Infof("Requests will be charged at %s", formatPriceTableSummary(priceTable, assetTerms.Symbol))
			}
			u.Infof("The agent will reconcile: health-check → payment gate → route")
			u.Infof("Check status: obol sell status %s -n %s", name, ns)

			if !registerEnabled {
				// Best-effort tunnel for --no-register: not fatal if it doesn't come up.
				u.Blank()
				u.Info("Ensuring tunnel is active for public access...")
				if url, terr := tunnel.EnsureTunnelForSell(cfg, u); terr != nil {
					u.Warnf("Tunnel not started: %v", terr)
					u.Dim("  Start manually with: obol tunnel restart")
				} else {
					u.Successf("Tunnel active: %s", url)
				}
				return nil
			}

			// Registration path: tunnelURL has already been awaited above.
			if reg, ok := spec["registration"].(map[string]any); ok {
				if enabled, _ := reg["enabled"].(bool); enabled {
					u.Blank()
					u.Info("Registering seller agent on ERC-8004...")
					if err := autoRegisterServiceOffer(ctx, cfg, u, autoRegisterOptions{
						ChainCSV:      cmd.String("chain"),
						Endpoint:      tunnelURL,
						AgentName:     registrationNameForPrompt(name, reg),
						AgentDesc:     registrationDescriptionForPrompt(name, reg),
						ExpectedOwner: wallet,
					}); err != nil {
						return fmt.Errorf("automatic sell registration failed: %w", err)
					}
				}
			}

			return nil
		},
	}
}

// signerPayeeDelegationNote returns a human-readable note explaining the
// ownership delegation when the agent's on-chain registration signer differs
// from the offer's payment payTo wallet, and "" when they match (or either
// is empty). Used by the auto-register flow to surface the split as an
// informational message rather than blocking the registration outright —
// ERC-8004 explicitly supports this separation via setAgentWallet, and x402
// settlement uses the offer's payTo regardless of the registry's wallet.
//
// The previous behavior (returning fmt.Errorf("registration signer ... does
// not match the payment wallet ...")) made it look like an ERC-8004 spec
// constraint when it was purely an obol-CLI policy choice. See PR notes for
// the full rationale.
func signerPayeeDelegationNote(signer, payTo string) string {
	s := strings.TrimSpace(signer)
	p := strings.TrimSpace(payTo)
	if s == "" || p == "" || strings.EqualFold(s, p) {
		return ""
	}
	return fmt.Sprintf(
		"Agent owner (registration signer) %s differs from offer payTo %s. "+
			"ERC-8004 allows this via setAgentWallet; x402 settlement honors payTo regardless. "+
			"Re-point payments later with: obol sell update <name> --pay-to <addr>",
		s, p,
	)
}

// buildSellUpdatePatch assembles the JSON-merge patch body for
// `obol sell update`. Extracted from the Action so the patch shape — the
// thing that actually hits the cluster — is testable without a live ServiceOffer.
//
// Returns the patch map and an error when nothing was provided (the Action
// surfaces this as "nothing to update: pass at least one of ...").
//
// When --pay-to is set, the wallet must already have been validated by
// x402verifier.ValidateWallet at the call site; this helper does no further
// validation so it stays a pure data shape.
func buildSellUpdatePatch(payTo, chain string, price schemas.PriceTable) (map[string]any, error) {
	payment := map[string]any{}

	if payTo = strings.TrimSpace(payTo); payTo != "" {
		payment["payTo"] = payTo
	}
	if chain = strings.TrimSpace(chain); chain != "" {
		payment["network"] = chain
	}

	if price.PerRequest != "" || price.PerMTok != "" || price.PerHour != "" {
		// Null out the unused price keys explicitly so a switch from, e.g.,
		// perRequest→perMTok doesn't leave the previous key stranded and
		// fighting the new one through merge semantics.
		p := map[string]any{
			"perRequest": nil,
			"perMTok":    nil,
			"perHour":    nil,
		}
		switch {
		case price.PerRequest != "":
			p["perRequest"] = price.PerRequest
		case price.PerMTok != "":
			p["perMTok"] = price.PerMTok
		case price.PerHour != "":
			p["perHour"] = price.PerHour
		}
		payment["price"] = p
	}

	if len(payment) == 0 {
		return nil, errors.New("nothing to update: pass at least one of --per-request / --per-mtok / --per-hour / --pay-to / --chain")
	}

	return map[string]any{
		"spec": map[string]any{
			"payment": payment,
		},
	}, nil
}

// shouldAutoRegisterSell reports whether the post-create auto-register step
// must run for a freshly-applied ServiceOffer spec. Both `obol sell http` and
// `obol sell inference` need the same gate: registration must be enabled AND
// a tunnel URL must be available to write into the on-chain registration
// document. The decision is intentionally shared so the inference path
// cannot silently regress to "create the offer, never register" (the bug
// behind https://github.com/ObolNetwork/obol-stack/issues — Registered=False
// AwaitingExternalRegistration hiding the offer from /api/services.json).
func shouldAutoRegisterSell(spec map[string]any, tunnelURL string) bool {
	if tunnelURL == "" {
		return false
	}
	reg, ok := spec["registration"].(map[string]any)
	if !ok {
		return false
	}
	enabled, _ := reg["enabled"].(bool)
	return enabled
}

func registrationNameForPrompt(fallback string, reg map[string]any) string {
	if reg == nil {
		return fallback
	}
	if name, ok := reg["name"].(string); ok && strings.TrimSpace(name) != "" {
		return name
	}
	return fallback
}

func registrationDescriptionForPrompt(fallback string, reg map[string]any) string {
	if reg == nil {
		return fmt.Sprintf("Obol Stack service %s", fallback)
	}
	if desc, ok := reg["description"].(string); ok && strings.TrimSpace(desc) != "" {
		return desc
	}
	return fmt.Sprintf("Obol Stack service %s", fallback)
}

type autoRegisterOptions struct {
	ChainCSV      string
	Endpoint      string
	AgentName     string
	AgentDesc     string
	ExpectedOwner string
}

type sellRegistrationInput struct {
	NoRegister    bool
	Register      bool
	Name          string
	Description   string
	Image         string
	Skills        []string
	Domains       []string
	MetadataPairs []string
}

// autoRegisterServiceOffer performs ERC-8004 registration via the agent's
// remote-signer. The remote-signer holds the only copy of the agent's
// signing key — the CLI never sees raw key material. If no remote-signer is
// configured (no Hermes default agent), the operator must run
// `obol agent init` first (or `obol wallet import` to seed a known key).
func autoRegisterServiceOffer(ctx context.Context, cfg *config.Config, u *ui.UI, opts autoRegisterOptions) error {
	if opts.Endpoint == "" {
		return errors.New("endpoint is required for automatic registration")
	}

	networks, err := erc8004.ResolveNetworks(opts.ChainCSV)
	if err != nil {
		return err
	}

	if _, err := hermes.ResolveWalletAddress(cfg); err != nil {
		return fmt.Errorf("no Hermes remote-signer wallet found: %w\n\n  Run 'obol agent init' first, or 'obol wallet import --private-key-file <file>' to seed a specific key", err)
	}
	signerNS, err := hermes.ResolveInstanceNamespace(cfg)
	if err != nil {
		return fmt.Errorf("resolve Hermes instance namespace: %w", err)
	}

	pf, err := startSignerPortForward(cfg, signerNS)
	if err != nil {
		return fmt.Errorf("port-forward to remote-signer: %w", err)
	}
	defer pf.Stop()

	signer := erc8004.NewRemoteSigner(fmt.Sprintf("http://localhost:%d", pf.localPort))
	addr, err := signer.GetAddress(ctx)
	if err != nil {
		return err
	}
	signerAddr := addr.Hex()

	// ERC-8004 treats the agent OWNER (msg.sender at register time) and the
	// agent WALLET (settable post-mint via setAgentWallet) as independent
	// addresses. x402 settlement honors the offer's spec.payment.payTo
	// directly — buyers pay that address regardless of what the registry's
	// getAgentWallet returns. So a different signer and payTo is legitimate;
	// it is the canonical pattern for "hot signer, cold/multisig payee".
	//
	// We surface the split as an informational note (not an error) so the
	// operator can confirm the delegation was intentional, and so the obvious
	// follow-up — `obol sell update <name> --pay-to <new>` for the payee — is
	// discoverable.
	if note := signerPayeeDelegationNote(signerAddr, opts.ExpectedOwner); note != "" {
		u.Info(note)
	}

	agentURI := strings.TrimRight(opts.Endpoint, "/") + "/.well-known/agent-registration.json"
	u.Printf("  Agent URI: %s", agentURI)

	var successes int
	for _, net := range networks {
		u.Blank()
		u.Printf("  [%s] (chain ID %d)", net.Name, net.ChainID)
		u.Printf("    Registry: %s", net.RegistryAddress)

		if err := registerDirectViaSigner(ctx, cfg, u, net, agentURI, signerNS); err != nil {
			u.Warnf("direct registration failed: %v", err)
			continue
		}

		u.Printf("    Name:     %s", opts.AgentName)
		u.Printf("    Summary:  %s", opts.AgentDesc)
		u.Printf("    CAIP-10:  %s", net.CAIP10Registry())
		successes++
	}

	if successes == 0 {
		return fmt.Errorf("registration failed on all networks")
	}

	u.Blank()
	u.Successf("Seller agent registered on %d/%d networks.", successes, len(networks))
	return nil
}

func buildSellRegistrationConfig(serviceName string, in sellRegistrationInput) (map[string]any, bool, error) {
	registerEnabled := !in.NoRegister
	if !registerEnabled && (in.Register || in.Name != "" || in.Description != "" || in.Image != "" ||
		len(in.Skills) > 0 || len(in.Domains) > 0 || len(in.MetadataPairs) > 0) {
		return nil, false, errors.New("--no-register cannot be combined with registration-specific flags")
	}
	if !registerEnabled {
		return nil, false, nil
	}

	reg := map[string]any{
		"enabled":     true,
		"name":        registrationNameForPrompt(serviceName, nil),
		"description": registrationDescriptionForPrompt(serviceName, nil),
	}
	if in.Name != "" {
		reg["name"] = in.Name
	}
	if in.Description != "" {
		reg["description"] = in.Description
	}
	if in.Image != "" {
		reg["image"] = in.Image
	}
	if len(in.Skills) > 0 {
		reg["skills"] = in.Skills
	}
	if len(in.Domains) > 0 {
		reg["domains"] = in.Domains
	}
	if len(in.MetadataPairs) > 0 {
		meta, err := parseMetadataPairs(in.MetadataPairs)
		if err != nil {
			return nil, false, err
		}
		reg["metadata"] = meta
	}
	return reg, true, nil
}

func serviceOfferStatusLines(namespace, name string, offer monetizeapi.ServiceOffer, baseURL string) []string {
	endpoint := valueOrNone(offer.Status.Endpoint)
	if baseURL != "" && offer.Status.Endpoint != "" {
		endpoint = strings.TrimRight(baseURL, "/") + offer.Status.Endpoint
	}

	tx := valueOrNone(offer.Status.RegistrationTxHash)
	if url := explorerTxURL(offer.Spec.Payment.Network, offer.Status.RegistrationTxHash); url != "" {
		tx = url
	}

	agentID := valueOrNone(offer.Status.AgentID)
	if url := agentRegistryNFTURL(offer.Spec.Payment.Network, offer.Status.AgentID); url != "" {
		agentID = fmt.Sprintf("%s (%s)", offer.Status.AgentID, url)
	}

	lines := []string{
		fmt.Sprintf("ServiceOffer:    %s/%s", namespace, name),
		fmt.Sprintf("Endpoint:        %s", endpoint),
		fmt.Sprintf("Network:         %s", valueOrNone(offer.Spec.Payment.Network)),
		fmt.Sprintf("Asset:           %s", formatOfferAsset(offer.Spec.Payment.Asset)),
		fmt.Sprintf("Price:           %s", formatOfferPrice(offer.Spec.Payment)),
		fmt.Sprintf("Pay To:          %s", valueOrNone(offer.Spec.Payment.PayTo)),
		fmt.Sprintf("Agent ID:        %s", agentID),
		fmt.Sprintf("Registration Tx: %s", tx),
		"",
		"Conditions:",
	}
	for _, cond := range offer.Status.Conditions {
		lines = append(lines, formatConditionLine(cond))
	}
	return lines
}

// formatOfferAsset renders the payment asset as "SYMBOL" or
// "SYMBOL (0xaddr)" when the contract address is known.
func formatOfferAsset(asset monetizeapi.ServiceOfferAsset) string {
	symbol := strings.TrimSpace(asset.Symbol)
	addr := strings.TrimSpace(asset.Address)
	switch {
	case symbol == "" && addr == "":
		return "(not set)"
	case symbol == "":
		return addr
	case addr == "":
		return symbol
	default:
		return fmt.Sprintf("%s (%s)", symbol, addr)
	}
}

// formatOfferPrice renders the price line for a ServiceOffer payment block,
// preferring per-request, then per-MTok, then per-hour. Asset symbol is
// included when available.
func formatOfferPrice(p monetizeapi.ServiceOfferPayment) string {
	symbol := strings.TrimSpace(p.Asset.Symbol)
	suffix := ""
	if symbol != "" {
		suffix = " " + symbol
	}
	switch {
	case p.Price.PerRequest != "":
		return fmt.Sprintf("%s%s per request", p.Price.PerRequest, suffix)
	case p.Price.PerMTok != "":
		return fmt.Sprintf("%s%s per MTok", p.Price.PerMTok, suffix)
	case p.Price.PerHour != "":
		return fmt.Sprintf("%s%s per hour", p.Price.PerHour, suffix)
	default:
		return "(not set)"
	}
}

// explorerTxURL returns the block-explorer URL for a transaction hash on the
// given network. Returns "" when the network is unknown or hash is empty.
func explorerTxURL(network, txHash string) string {
	hash := strings.TrimSpace(txHash)
	if hash == "" {
		return ""
	}
	net, err := erc8004.ResolveNetwork(network)
	if err != nil {
		return ""
	}
	base := explorerBaseURL(net.Name)
	if base == "" {
		return ""
	}
	return fmt.Sprintf("%s/tx/%s", base, hash)
}

// formatConditionLine renders a single condition with a status icon followed
// by the type, reason, and message. Icons:
//
//	✓  Status=True (success)              — green check
//	ℹ  Status=True with Reason "Skipped"  — informational
//	    or "Disabled", non-failure paths
//	⚠  Status=False                       — failure / blocked
//	⏳  Status=Unknown / empty             — pending
func formatConditionLine(cond monetizeapi.Condition) string {
	icon := conditionIcon(cond)
	parts := []string{cond.Type}
	if cond.Reason != "" {
		parts = append(parts, cond.Reason)
	}
	header := strings.Join(parts, ": ")
	if cond.Message != "" {
		header = header + " — " + cond.Message
	}
	return fmt.Sprintf("  %s %s", icon, header)
}

// conditionIcon picks a glyph based on the condition's status + reason. The
// glyphs are plain unicode (no lipgloss) so the function is safe to call from
// pure unit tests; coloring is applied at print time via the ui package.
func conditionIcon(cond monetizeapi.Condition) string {
	switch cond.Status {
	case "True":
		switch cond.Reason {
		case "Skipped", "Disabled":
			return "ℹ"
		}
		return "✓"
	case "False":
		return "⚠"
	default:
		return "⏳"
	}
}

// ---------------------------------------------------------------------------
// sell demo — deploy a demo service behind x402 payment gate
// ---------------------------------------------------------------------------

// demoSpec describes a built-in demo type with default pricing and config.
type demoSpec struct {
	Type         string // DEMO_TYPE env value
	Price        string // default per-request price (in DefaultToken units)
	Description  string // human-readable one-liner
	NeedsERPC    bool   // whether the demo queries eRPC
	DefaultChain string // default --chain when not explicitly set
	DefaultToken string // default --token when not explicitly set

	// Agent is set on demo types that resolve to an agent-backed offer
	// (Agent CRD + ServiceOffer of type=agent) rather than a pure-Go
	// demo-server Deployment. Empty for legacy hello/blocks demos.
	Agent *demoAgentSpec
}

// demoAgentSpec captures the per-demo Agent shape used by quant-style
// demos. The values land on the Agent CR via `obol agent new` semantics.
type demoAgentSpec struct {
	Skills    []string
	Objective string
}

const defaultDemoType = "hello"

var demoTypes = map[string]demoSpec{
	"hello": {
		Type:         "hello",
		Price:        "1",
		Description:  "Proof-of-payment echo service — confirms you got through the x402 gate",
		DefaultChain: "ethereum",
		DefaultToken: "OBOL",
	},
	"blocks": {
		Type:         "blocks",
		Price:        "0.0001",
		Description:  "Live blockchain data from a local full node (block, gas, chain ID)",
		NeedsERPC:    true,
		DefaultChain: "base-sepolia",
		DefaultToken: "USDC",
	},
	"quant": {
		Type:         "quant",
		Price:        "10",
		Description:  "Agent-backed chain analyst (Agent CRD + ServiceOffer of type=agent)",
		NeedsERPC:    true,
		DefaultChain: "ethereum",
		DefaultToken: "OBOL",
		Agent: &demoAgentSpec{
			Skills: []string{
				"ethereum-networks",
				"ethereum-local-wallet",
				"addresses",
				"gas",
			},
			Objective: "You are a focused EVM chain analyst. Answer the user's question using only the RPC tools you have. Be concise. If a question is outside chain analysis, refuse politely.",
		},
	},
}

const demoNamespace = "demo"

func sellDemoCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "demo",
		Usage:     "Deploy a demo service behind x402 payment gate",
		ArgsUsage: "[type]",
		Description: `Deploys a demo HTTP server and creates a ServiceOffer to payment-gate it.
The demo proves the full sell→discover→pay→receive flow works end-to-end.

Types:
  hello    Proof-of-payment echo                    (default: 1 OBOL on ethereum)
  blocks   Live blockchain data                     (default: 0.0001 USDC on base-sepolia)
  quant    Agent-backed chain analyst (Agent CRD)   (default: 10 OBOL on ethereum)

Run with no arguments to deploy the canonical hello demo on mainnet.

Example:
  obol sell demo                                # hello @ 1 OBOL on ethereum
  obol sell demo blocks                         # blocks @ 0.0001 USDC on base-sepolia
  obol sell demo quant --price 5                # quant @ 5 OBOL on ethereum
  obol sell demo hello --token USDC --chain base --price 0.001`,
		Flags: []cli.Flag{
			payToFlag("Token recipient address"),
			&cli.StringFlag{
				Name:  "chain",
				Usage: "Payment chain (defaults to demo type's default chain)",
			},
			&cli.StringFlag{
				Name:  "token",
				Usage: "Payment token (defaults to demo type's default token)",
			},
			&cli.StringFlag{
				Name:  "price",
				Usage: "Override default per-request price (in token units)",
			},
			&cli.StringFlag{
				Name:  "name",
				Usage: "Override service name (default: demo-<type>)",
			},
			&cli.BoolFlag{
				Name:  "register",
				Usage: "Auto-register the demo on the ERC-8004 Agent Registry. Default: skip (avoid double-register reverts and ETH-for-gas requirement; run `obol sell register` later if you want on-chain discovery).",
			},
			&cli.BoolFlag{
				Name:   "no-register",
				Hidden: true, // back-compat: no-op now that skipping is the default
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			// Stack must be running to deploy a demo (we apply K8s resources).
			if err := kubectl.EnsureCluster(cfg); err != nil {
				return fmt.Errorf("Obol Stack is not running. Start it with `obol stack up` first")
			}

			// Default to canonical hello demo when no type is given.
			typeName := defaultDemoType
			if cmd.NArg() >= 1 {
				typeName = cmd.Args().First()
			}
			spec, ok := demoTypes[typeName]
			if !ok {
				return fmt.Errorf("unknown demo type %q — choose: hello, blocks, quant", typeName)
			}

			name := cmd.String("name")
			if name == "" {
				name = "demo-" + typeName
			}

			// Apply per-type defaults when the user didn't explicitly set chain/token.
			// This lets bare `obol sell demo` pick OBOL/ethereum (hello),
			// `obol sell demo quant` pick USDC/base-sepolia, etc.
			chain := cmd.String("chain")
			chainExplicit := cmd.IsSet("chain")
			if chain == "" {
				chain = spec.DefaultChain
			}
			tokenName := cmd.String("token")
			if tokenName == "" {
				tokenName = spec.DefaultToken
			}

			// Resolve wallet.
			wallet := cmd.String("pay-to")
			if wallet == "" {
				if resolved, err := hermes.ResolveWalletAddress(cfg); err == nil {
					wallet = resolved
					u.Infof("Using wallet from remote-signer: %s", wallet)
				} else if u.IsTTY() {
					var inputErr error
					wallet, inputErr = u.Input("Wallet address (token recipient)", "")
					if inputErr != nil || wallet == "" {
						return fmt.Errorf("recipient required: use --pay-to <addr> or set X402_WALLET")
					}
				} else {
					return fmt.Errorf("recipient required: use --pay-to <addr> or set X402_WALLET")
				}
			}
			if err := x402verifier.ValidateWallet(wallet); err != nil {
				return err
			}

			price := cmd.String("price")
			if price == "" {
				price = spec.Price
			}

			// Resolve token metadata. resolveAssetTermsFor may flip chain to ethereum
			// for non-USDC tokens when --chain wasn't explicitly set.
			assetTerms, err := resolveAssetTermsFor(tokenName, &chain, chainExplicit)
			if err != nil {
				return err
			}
			symbol := assetTerms.Symbol
			if symbol == "" {
				symbol = "USDC"
			}

			u.Infof("Deploying demo %q (%s)", typeName, spec.Description)

			// Agent-backed demos take a separate path: declare an Agent CR
			// + ServiceOffer of type=agent rather than rolling out the
			// pure-Go demo-server. We branch here so legacy hello/blocks
			// flow stays untouched.
			if spec.Agent != nil {
				return runAgentBackedDemo(ctx, cfg, u, cmd, name, typeName, price, symbol, chain, wallet, spec, assetTerms)
			}

			// 1. Deploy demo backend (namespace + Deployment + Service).
			if err := deployDemoBackend(cfg, u, name, spec, chain); err != nil {
				return fmt.Errorf("deploy demo backend: %w", err)
			}

			// 2. Create ServiceOffer. Auto-registration is opt-in for demos —
			// the previous default (auto-register on every demo) caused
			// repeated `setMetadata` calls to revert at the contract once the
			// agent already had x402 metadata, and required the demo wallet
			// to hold ETH for gas. Operators run `obol sell register --chain ...`
			// when they actually want on-chain discovery.
			register := cmd.Bool("register")
			soManifest := buildDemoServiceOffer(name, demoNamespace, chain, wallet, price, register, spec, assetTerms)
			applyOut, err := kubectlApplyOutput(cfg, soManifest)
			if err != nil {
				return fmt.Errorf("apply ServiceOffer: %w", err)
			}
			action := "created"
			if strings.Contains(applyOut, "configured") || strings.Contains(applyOut, "unchanged") {
				action = "updated"
			}
			u.Successf("ServiceOffer %s/%s %s (type: http, price: %s %s/req)", demoNamespace, name, action, price, symbol)
			u.Infof("The controller will reconcile: health-check → payment gate → route")
			u.Infof("Check status: obol sell status %s -n %s", name, demoNamespace)

			// 3. Ensure tunnel is active.
			u.Blank()
			u.Info("Ensuring tunnel is active for public access...")

			tunnelURL := ""
			if tURL, err := tunnel.EnsureTunnelForSell(cfg, u); err != nil {
				u.Warnf("Tunnel not started: %v", err)
				u.Dim("  Start manually with: obol tunnel restart")
			} else {
				tunnelURL = tURL
				u.Successf("Tunnel active: %s", tunnelURL)
			}

			// 4. Wait up to 60s for the ServiceOffer to reach Ready=True so the
			// /skill.md catalog and storefront pick up the offer before we tell
			// the user to try it. If it times out we still print the try-it
			// block with a propagation note.
			ready := waitForOfferReady(cfg, u, name, demoNamespace, 60*time.Second)

			// 5. Auto-register on ERC-8004. With registration enabled the
			// controller publishes the .well-known doc and waits for the
			// on-chain Registered event before flipping the offer Ready;
			// auto-submitting the tx here closes that gap so `sell demo` is
			// a one-shot path to a discoverable service. With --no-register
			// we skip both the spec flag (so Ready unblocks via Disabled) and
			// the on-chain submit — useful when the user doesn't want the
			// wallet to need any ETH balance.
			if register {
				autoRegisterDemo(ctx, cfg, u, chain, tunnelURL)
			} else {
				u.Info("Registration skipped (default for demos). The offer will still reach Ready.")
				u.Dim("  Run on-chain discovery later: obol sell register --chain " + chain)
			}

			// 6. Print try-it instructions.
			u.Blank()
			printDemoTryIt(u, name, typeName, price, symbol, chain, tunnelURL, ready)

			return nil
		},
	}
}

// autoRegisterDemo submits the ERC-8004 registration on the demo's chain using
// the Hermes remote-signer. Pays gas from the agent's wallet — pass
// --no-register on `sell demo` to skip this entirely. Best-effort: every
// failure path prints a "run `obol sell register` later" hint and returns
// without aborting the demo.
func autoRegisterDemo(ctx context.Context, cfg *config.Config, u *ui.UI, chain, tunnelURL string) {
	u.Blank()
	u.Info("Registering agent on ERC-8004 (auto)...")

	skipHint := func(reason string) {
		u.Warnf("Skipping auto-register: %s", reason)
		u.Dim("  You can run it manually later: obol sell register --chain " + chain)
	}

	if tunnelURL == "" {
		skipHint("no tunnel URL — service must be publicly reachable for the registration document")
		return
	}
	net, err := erc8004.ResolveNetwork(chain)
	if err != nil {
		skipHint(fmt.Sprintf("chain %q is not an ERC-8004 registration target", chain))
		return
	}
	if _, err := hermes.ResolveWalletAddress(cfg); err != nil {
		skipHint("no Hermes remote-signer wallet found (run `obol agent init` first)")
		return
	}
	signerNS, err := hermes.ResolveInstanceNamespace(cfg)
	if err != nil {
		skipHint(fmt.Sprintf("resolve Hermes namespace: %v", err))
		return
	}

	agentURI := strings.TrimRight(tunnelURL, "/") + "/.well-known/agent-registration.json"
	if registerAgentOnNetworks(ctx, cfg, u, agentURI, signerNS, []erc8004.NetworkConfig{net}) == 0 {
		u.Warn("Auto-register did not succeed.")
		u.Dim("  Retry with: obol sell register --chain " + chain)
		return
	}
	u.Successf("Agent registered on %s.", net.Name)
}

// waitForOfferReady polls a ServiceOffer's Ready condition for up to `timeout`.
// Returns true if Ready=True observed, false otherwise. Uses a spinner.
func waitForOfferReady(cfg *config.Config, u *ui.UI, name, ns string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	bin, kc := kubectl.Paths(cfg)

	check := func() bool {
		out, err := kubectl.Output(bin, kc, "get", "serviceoffers.obol.org", name, "-n", ns,
			"-o", `jsonpath={.status.conditions[?(@.type=="Ready")].status}`)
		return err == nil && strings.TrimSpace(out) == "True"
	}

	if check() {
		return true
	}

	var ready bool
	_ = u.RunWithSpinner("Waiting for service to be Ready (up to 60s)", func() error {
		for time.Now().Before(deadline) {
			if check() {
				ready = true
				return nil
			}
			time.Sleep(2 * time.Second)
		}
		return nil
	})
	return ready
}

// deployDemoBackend creates the demo namespace, Deployment, and Service.
func deployDemoBackend(cfg *config.Config, u *ui.UI, name string, spec demoSpec, paymentChain string) error {
	resources := buildDemoResources(name, spec, paymentChain)

	for _, res := range resources {
		data, err := json.Marshal(res)
		if err != nil {
			return fmt.Errorf("marshal resource: %w", err)
		}

		bin, kc := kubectl.Paths(cfg)
		if _, err := kubectl.ApplyOutput(bin, kc, data); err != nil {
			return fmt.Errorf("apply %s/%s: %w", res["kind"], name, err)
		}
	}

	u.Successf("Demo backend %s deployed in namespace %s", name, demoNamespace)

	// Wait for rollout.
	return u.RunWithSpinner("Waiting for demo pod to be ready", func() error {
		bin, kc := kubectl.Paths(cfg)
		return kubectl.RunSilent(bin, kc,
			"rollout", "status", "deployment/"+name, "-n", demoNamespace, "--timeout=60s")
	})
}

// buildDemoResources returns the K8s manifests for a demo backend.
func buildDemoResources(name string, spec demoSpec, paymentChain string) []map[string]any {
	env := []map[string]string{
		{"name": "DEMO_TYPE", "value": spec.Type},
		{"name": "PORT", "value": "8080"},
	}
	if spec.NeedsERPC {
		env = append(env, map[string]string{
			"name": "ERPC_URL", "value": demoERPCURL(paymentChain),
		})
	}

	labels := map[string]string{
		"app":                    name,
		"app.kubernetes.io/name": name,
		"obol.org/demo":          "true",
	}

	return []map[string]any{
		// Namespace
		{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]any{
				"name": demoNamespace,
			},
		},
		// Deployment
		{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name":      name,
				"namespace": demoNamespace,
				"labels":    labels,
			},
			"spec": map[string]any{
				"replicas": 1,
				"selector": map[string]any{
					"matchLabels": map[string]string{"app": name},
				},
				"template": map[string]any{
					"metadata": map[string]any{
						"labels": labels,
					},
					"spec": map[string]any{
						"containers": []map[string]any{
							{
								"name":            "demo",
								"image":           images.Resolve("ghcr.io/obolnetwork/demo-server"),
								"imagePullPolicy": "IfNotPresent",
								"env":             env,
								"ports": []map[string]any{
									{"containerPort": 8080, "name": "http"},
								},
								"livenessProbe": map[string]any{
									"httpGet": map[string]any{
										"path": "/health",
										"port": "http",
									},
									"initialDelaySeconds": 2,
									"periodSeconds":       10,
								},
								"readinessProbe": map[string]any{
									"httpGet": map[string]any{
										"path": "/health",
										"port": "http",
									},
									"initialDelaySeconds": 1,
									"periodSeconds":       5,
								},
								"resources": map[string]any{
									"requests": map[string]string{"cpu": "10m", "memory": "16Mi"},
									"limits":   map[string]string{"cpu": "100m", "memory": "64Mi"},
								},
							},
						},
					},
				},
			},
		},
		// Service
		{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]any{
				"name":      name,
				"namespace": demoNamespace,
				"labels":    labels,
			},
			"spec": map[string]any{
				"selector": map[string]string{"app": name},
				"ports": []map[string]any{
					{"port": 8080, "targetPort": 8080, "name": "http"},
				},
			},
		},
	}
}

func demoERPCURL(paymentChain string) string {
	return fmt.Sprintf("http://erpc.erpc.svc.cluster.local/rpc/%s", demoRPCNetwork(paymentChain))
}

func demoRPCNetwork(paymentChain string) string {
	switch paymentChain {
	case "base", "base-mainnet":
		return "base"
	case "base-sepolia":
		return "base-sepolia"
	case "ethereum", "ethereum-mainnet", "mainnet":
		return "mainnet"
	default:
		if chain, err := x402verifier.ResolveChainInfo(paymentChain); err == nil {
			switch chain.Name {
			case "ethereum":
				return "mainnet"
			default:
				return chain.Name
			}
		}
		return paymentChain
	}
}

// buildDemoServiceOffer returns a ServiceOffer manifest for a demo service.
// When register is false, registration.enabled is false on the offer; the
// controller short-circuits the Registered condition to True/Disabled so the
// offer can still reach Ready without a wallet ETH balance for the on-chain
// registration tx.
func buildDemoServiceOffer(name, ns, chain, wallet, price string, register bool, spec demoSpec, asset schemas.AssetTerms) map[string]any {
	payment := map[string]any{
		"scheme":            "exact",
		"network":           chain,
		"payTo":             wallet,
		"maxTimeoutSeconds": 300,
		"price": map[string]any{
			"perRequest": price,
		},
	}
	if !asset.IsZero() {
		payment["asset"] = asset
	}

	return map[string]any{
		"apiVersion": "obol.org/v1alpha1",
		"kind":       "ServiceOffer",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
		},
		"spec": map[string]any{
			"type": "http",
			"upstream": map[string]any{
				"service":    name,
				"namespace":  ns,
				"port":       8080,
				"healthPath": "/health",
			},
			"payment": payment,
			"path":    "/services/" + name,
			"registration": map[string]any{
				"enabled":     register,
				"name":        name,
				"description": spec.Description,
				"skills":      []string{"x402-demo", spec.Type},
			},
		},
	}
}

// printDemoTryIt prints copy-paste instructions for calling the demo service.
// `ready` indicates whether the ServiceOffer reached Ready=True before the
// caller's poll deadline; when false, we add a "may take a moment" note.
func printDemoTryIt(u *ui.UI, name, typeName, price, symbol, chain, tunnelURL string, ready bool) {
	endpoint := "<tunnel-url>/services/" + name
	if tunnelURL != "" {
		endpoint = tunnelURL + "/services/" + name
	}

	u.Bold("── Try it ──────────────────────────────────────────────")
	u.Blank()

	u.Printf("  Demo %q is live at: %s", typeName, endpoint)
	u.Printf("  Price: %s %s/request on %s", price, symbol, chain)
	if !ready {
		u.Dim("  (still propagating — service may take a moment to appear in /skill.md)")
	}
	u.Blank()

	// 1. Ask your agent — primary, recommended path.
	u.Printf("  Ask your agent")
	u.Blank()
	u.Dim(fmt.Sprintf(`     "Use the buy-x402 skill to call the paid service at %s.`, endpoint))
	u.Dim(fmt.Sprintf(`      It costs %s %s per request on %s. Report what it returns."`, price, symbol, chain))
	u.Blank()

	// 2. Or check it out manually — pricing probe + paid request.
	u.Printf("  Or check it out manually")
	u.Blank()
	u.Dim("     Check the API pricing — terminal:")
	u.Dim(fmt.Sprintf("       curl -s %s | jq .", endpoint))
	u.Dim("     ...or browser:")
	u.Dim(fmt.Sprintf("       %s", endpoint))
	u.Blank()
	u.Dim("     Pay for the API call (Python — pip install x402 httpx):")
	u.Dim("       import httpx")
	u.Dim("       from x402.client import x402_client")
	u.Dim("")
	u.Dim(fmt.Sprintf(`       client = x402_client(httpx.Client(), private_key="<%s holder on %s>")`, symbol, chain))
	u.Dim(fmt.Sprintf(`       resp = client.get("%s")`, endpoint))
	u.Dim("       print(resp.json())")
	u.Blank()

	// 3. How x402 works — short prose paragraph (not numbered).
	u.Printf("  How x402 works")
	u.Blank()
	u.Dim("     A request without payment returns HTTP 402 with the price and a")
	u.Dim("     payment recipe. The buyer (or library) signs an off-chain")
	u.Dim("     authorization, retries the request with an X-PAYMENT header, and")
	u.Dim("     the seller's facilitator settles on-chain. No gas needed — the")
	u.Dim("     seller covers settlement. Useful for: data feeds, AI inference,")
	u.Dim("     subscriptions, digital purchases, and agent-to-agent commerce.")
	u.Dim("     Spec: https://www.x402.org")
	u.Blank()

	u.Bold("─────────────────────────────────────────────────────────")
}

// cleanupDemoBackend removes the Deployment and Service for a demo backend.
// Best-effort: logs warnings but does not fail the overall delete.
func cleanupDemoBackend(cfg *config.Config, u *ui.UI, name string) {
	bin, kc := kubectl.Paths(cfg)
	for _, kind := range []string{"deployment", "service"} {
		if err := kubectl.RunSilent(bin, kc, "delete", kind, name, "-n", demoNamespace, "--ignore-not-found"); err != nil {
			u.Warnf("Failed to delete %s/%s: %v", kind, name, err)
		}
	}
	u.Successf("Demo backend resources cleaned up")
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
				if u.IsJSON() {
					return kubectlRun(cfg, "get", "serviceoffers.obol.org", name, "-n", ns, "-o", "json")
				}

				raw, err := kubectlOutput(cfg, "get", "serviceoffers.obol.org", name, "-n", ns, "-o", "json")
				if err != nil {
					return err
				}

				var offer monetizeapi.ServiceOffer
				if err := json.Unmarshal([]byte(raw), &offer); err != nil {
					return fmt.Errorf("parse ServiceOffer: %w", err)
				}

				// Best-effort: pull the public tunnel URL so the Endpoint line
				// shows the full URL buyers should hit, not just the path.
				baseURL, _ := tunnel.GetTunnelURL(cfg)

				for _, line := range serviceOfferStatusLines(ns, name, offer, baseURL) {
					if line == "" {
						u.Blank()
						continue
					}
					u.Print(line)
				}
				return nil
			}

			// No name: show global pricing config + registrations.
			if u.IsJSON() {
				return sellStatusGlobalJSON(cfg, u)
			}

			tunnelURL, _ := tunnel.GetTunnelURL(cfg)
			defaultWallet, _ := hermes.ResolveWalletAddress(cfg)

			pricingCfg, err := x402verifier.GetPricingConfig(cfg)
			if err != nil {
				u.Warnf("Payment configuration not available (%v)", err)
			} else {
				u.Printf("Payment Configuration:")
				if tunnelURL != "" {
					u.Printf("  Tunnel URL:     %s", tunnelURL)
				}
				u.Printf("  Default Wallet: %s", valueOrNone(defaultWallet))
				u.Printf("  Chain:          %s", valueOrNone(pricingCfg.Chain))
				u.Printf("  Facilitator:    %s", valueOrNone(pricingCfg.FacilitatorURL))
				u.Printf("  Verify Only:    %v", pricingCfg.VerifyOnly)
				u.Printf("  Routes:         %d", len(pricingCfg.Routes))
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

			offers, offersErr := listServiceOffers(cfg)
			if offersErr != nil {
				u.Blank()
				u.Warnf("Could not list services (%v)", offersErr)
			} else {
				u.Blank()
				u.Printf("Agent Registrations:")
				printed := 0
				for _, o := range offers {
					if o.Status.AgentID == "" && o.Status.RegistrationTxHash == "" {
						continue
					}
					tx := valueOrNone(o.Status.RegistrationTxHash)
					if url := explorerTxURL(o.Spec.Payment.Network, o.Status.RegistrationTxHash); url != "" {
						tx = url
					}
					line := fmt.Sprintf("  %s/%s  agent=%s  tx=%s",
						o.Namespace, o.Name,
						valueOrNone(o.Status.AgentID),
						tx)
					if url := agentRegistryNFTURL(o.Spec.Payment.Network, o.Status.AgentID); url != "" {
						line = line + "  " + url
					}
					u.Printf(line)
					printed++
				}
				if printed == 0 {
					u.Printf("  (no agents registered)")
				}

				u.Blank()
				u.Printf("Services:")
				if len(offers) == 0 {
					u.Printf("  (no services published)")
				} else {
					for _, o := range offers {
						registered := isConditionTrue(o.Status.Conditions, "Registered")
						mark := "✗"
						if registered {
							mark = "✓"
						}
						tx := valueOrNone(o.Status.RegistrationTxHash)
						if url := explorerTxURL(o.Spec.Payment.Network, o.Status.RegistrationTxHash); url != "" {
							tx = url
						}
						u.Printf("  %s/%s  registered=%s  tx=%s",
							o.Namespace, o.Name, mark, tx)
					}
					u.Blank()
					u.Dim(fmt.Sprintf("See detailed service information with e.g. `obol sell status %s -n %s`",
						offers[0].Name, offers[0].Namespace))
				}
			}

			// Also show local inference gateway deployments.
			store := inference.NewStore(cfg.ConfigDir)

			deployments, _ := store.List()
			if len(deployments) > 0 {
				u.Blank()
				u.Printf("Local Inference Gateways:")
				for _, d := range deployments {
					u.Printf("  %-20s %s → %s  %s  chain=%s",
						d.Name, d.ListenAddr, d.UpstreamURL, formatInferencePriceSummary(d, ""), d.Chain)
				}
			}

			return nil
		},
	}
}

// agentRegistryNFTURL returns the block-explorer URL for an ERC-8004 agent
// registration NFT — `<explorer>/nft/<registry>/<agentId>` — on the given
// network. The registry address is sourced from erc8004.ResolveNetwork: Base
// mainnet and Ethereum mainnet share one address (CREATE2), Base Sepolia is
// a separate deployment. Returns "" when the network is unknown or the
// agent ID is empty.
func agentRegistryNFTURL(network, agentID string) string {
	if strings.TrimSpace(agentID) == "" {
		return ""
	}
	net, err := erc8004.ResolveNetwork(network)
	if err != nil {
		return ""
	}
	base := explorerBaseURL(net.Name)
	if base == "" {
		return ""
	}
	return fmt.Sprintf("%s/nft/%s/%s", base, net.RegistryAddress, agentID)
}

// explorerBaseURL maps a canonical network name to its block explorer base.
// Returns "" for networks without a public explorer (e.g. local Anvil).
func explorerBaseURL(canonicalName string) string {
	switch canonicalName {
	case "ethereum":
		return "https://etherscan.io"
	case "base":
		return "https://basescan.org"
	case "base-sepolia":
		return "https://sepolia.basescan.org"
	}
	return ""
}

// isConditionTrue reports whether the named condition exists with Status=True.
func isConditionTrue(conds []monetizeapi.Condition, name string) bool {
	for _, c := range conds {
		if c.Type == name {
			return c.Status == "True"
		}
	}
	return false
}

// listServiceOffers fetches all ServiceOffers across all namespaces and parses
// them into typed structs. Returns a nil slice when the cluster is unreachable
// or the CRD is not installed.
func listServiceOffers(cfg *config.Config) ([]monetizeapi.ServiceOffer, error) {
	raw, err := kubectlOutput(cfg, "get", "serviceoffers.obol.org", "-A", "-o", "json")
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []monetizeapi.ServiceOffer `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, fmt.Errorf("parse ServiceOffer list: %w", err)
	}
	return list.Items, nil
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
			Price:       formatInferencePriceSummary(d, ""),
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
// sell update
// ---------------------------------------------------------------------------

func sellUpdateCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "update",
		Usage:     "Update pricing or wallet on an existing ServiceOffer in place",
		ArgsUsage: "<name>",
		Description: `Patches a live ServiceOffer without deleting it. Only the fields you pass
are changed; everything else is preserved. The serviceoffer-controller will
reconcile the new payment config automatically.

Switching price models (e.g. per-request → per-mtok) nulls the previous keys
so the controller picks up the new model.

Examples:
  obol sell update my-api -n llm --per-request 0.002
  obol sell update my-api -n llm --per-mtok 5.0
  obol sell update my-api -n llm --wallet 0xNew... --chain base`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "namespace",
				Aliases:  []string{"n"},
				Usage:    "Namespace of the ServiceOffer",
				Required: true,
			},
			payToFlag("New USDC recipient address"),
			&cli.StringFlag{
				Name:  "chain",
				Usage: "New payment chain (base, base-sepolia, ethereum)",
			},
			&cli.StringFlag{
				Name:  "price",
				Usage: "New per-request price in USDC (alias for --per-request)",
			},
			&cli.StringFlag{
				Name:  "per-request",
				Usage: "New per-request price in USDC",
			},
			&cli.StringFlag{
				Name:  "per-mtok",
				Usage: "New per-million-tokens price in USDC",
			},
			&cli.StringFlag{
				Name:  "per-hour",
				Usage: "New per-compute-hour price in USDC",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if cmd.NArg() == 0 {
				return errors.New("name required: obol sell update <name> -n <ns> [--per-request N | --per-mtok N | --per-hour N] [--pay-to 0x...] [--chain base]")
			}

			name := cmd.Args().First()
			if err := validate.Name(name); err != nil {
				return err
			}
			ns := cmd.String("namespace")

			if _, err := kubectlOutput(cfg, "get", "serviceoffers.obol.org", name, "-n", ns, "-o", "name"); err != nil {
				return fmt.Errorf("ServiceOffer %s/%s not found: %w", ns, name, err)
			}

			wallet := strings.TrimSpace(cmd.String("pay-to"))
			if wallet != "" {
				if err := x402verifier.ValidateWallet(wallet); err != nil {
					return err
				}
			}

			var price schemas.PriceTable
			if cmd.String("price") != "" || cmd.String("per-request") != "" || cmd.String("per-mtok") != "" || cmd.String("per-hour") != "" {
				resolved, err := resolvePriceTable(cmd, true)
				if err != nil {
					return err
				}
				price = resolved
			}

			patch, err := buildSellUpdatePatch(wallet, cmd.String("chain"), price)
			if err != nil {
				return err
			}
			patchBytes, err := json.Marshal(patch)
			if err != nil {
				return fmt.Errorf("marshal patch: %w", err)
			}

			if err := kubectlRun(cfg, "patch", "serviceoffers.obol.org", name, "-n", ns, "--type=merge", "-p", string(patchBytes)); err != nil {
				return fmt.Errorf("failed to patch serviceoffer: %w", err)
			}

			u.Successf("ServiceOffer %s/%s updated", ns, name)
			u.Info("The controller will reconcile the new payment config.")
			u.Infof("Check status: obol sell status %s -n %s", name, ns)
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
				Aliases: []string{"f", "y", "yes"},
				Usage:   "Skip confirmation (aliases: -f, -y, --yes)",
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

			// Clean up demo backend resources if this is a demo service.
			if ns == demoNamespace {
				cleanupDemoBackend(cfg, u, name)
			}

			// Auto-stop quick tunnel when no ServiceOffers remain.
			remaining, listErr := kubectlOutput(cfg, "get", "serviceoffers.obol.org", "-A",
				"-o", "jsonpath={.items}")
			if listErr == nil && (remaining == "[]" || strings.TrimSpace(remaining) == "") {
				st, _ := tunnel.LoadTunnelState(cfg)
				if st == nil || !st.IsPersistent() {
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
		Description: `Sets the recipient address and chain for x402 payment collection.
Reloads the payment verifier when configuration is changed.`,
		Flags: []cli.Flag{
			payToFlag("USDC recipient address"),
			&cli.StringFlag{
				Name:  "chain",
				Usage: "Payment chain (base, base-sepolia, ethereum)",
				Value: "base",
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

			wallet := cmd.String("pay-to")
			if wallet == "" {
				if resolved, err := hermes.ResolveWalletAddress(cfg); err == nil {
					wallet = resolved
					u.Infof("Using wallet from remote-signer: %s", wallet)
				} else {
					return fmt.Errorf("recipient required: use --pay-to <addr> or set X402_WALLET")
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
The on-chain register tx is signed and broadcast by the Hermes remote-signer
and pays gas from the agent's wallet — make sure it has a small balance on
each target chain (~$0.20–$0.50 of native gas typically suffices).

Examples:
  obol sell register                                    # defaults to mainnet
  obol sell register --chain base                       # register on base
  obol sell register --chain mainnet,base               # register on multiple chains`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "chain",
				Usage: "Registration chain(s), comma-separated (mainnet, base, base-sepolia)",
				Value: "mainnet",
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
			&cli.BoolFlag{
				Name:  "sponsored",
				Usage: "(disabled) Sponsored zero-gas registration is currently unavailable. Re-run without --sponsored to register with the agent's own wallet (ETH for transaction fees required on the network).",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			// Sponsored gasless registration was removed in a6cd2c2 because the
			// EIP-7702 sponsor was returning silent zero-event txs (rc7/rc8 had
			// `--sponsored` quietly produce Agent ID 0). Surface that loudly to
			// anyone with the old muscle memory rather than ignoring the flag.
			if cmd.Bool("sponsored") {
				u.Warn("Sponsored zero-gas registration is currently disabled.")
				u.Dim("  Re-run without --sponsored to register with the agent's own wallet.")
				u.Dim("  (ETH for transaction fees required on the network)")
				return errors.New("--sponsored is disabled; re-run without the flag to register with the agent's own wallet (ETH for transaction fees required on the network)")
			}

			// Resolve networks.
			chainCSV := cmd.String("chain")
			if u.IsTTY() && !cmd.IsSet("chain") {
				nets := erc8004.SupportedNetworks()
				options := make([]string, len(nets))
				for i, n := range nets {
					options[i] = n.Name
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

			// All signing happens via the agent's remote-signer; the CLI never
			// sees raw key material. If no Hermes default agent is configured,
			// the operator must run `obol agent init` (or `obol wallet import`
			// to seed a known key) first.
			if _, err := hermes.ResolveWalletAddress(cfg); err != nil {
				return fmt.Errorf("no Hermes remote-signer wallet found: %w\n\n  Run 'obol agent init' first, or 'obol wallet import --private-key-file <file>' to seed a specific key", err)
			}
			signerNS, err := hermes.ResolveInstanceNamespace(cfg)
			if err != nil {
				return fmt.Errorf("resolve Hermes instance namespace: %w", err)
			}

			// Register on each network (best-effort).
			u.Infof("Registering agent on ERC-8004 Agent Registry...")
			u.Printf("  Agent URI: %s", agentURI)
			u.Printf("  Networks:  %s", chainCSV)

			successes := registerAgentOnNetworks(ctx, cfg, u, agentURI, signerNS, networks)
			if successes == 0 {
				return fmt.Errorf("registration failed on all networks")
			}

			u.Blank()
			u.Successf("Agent registered on %d/%d networks.", successes, len(networks))
			return nil
		},
	}
}

// registerAgentOnNetworks runs the per-network ERC-8004 registration loop used
// by both `obol sell register` and the auto-register step of `obol sell demo`.
// Each registration is signed by the Hermes remote-signer and pays gas from
// the agent's wallet. Returns the number of networks that registered
// successfully.
func registerAgentOnNetworks(ctx context.Context, cfg *config.Config, u *ui.UI, agentURI, signerNS string, networks []erc8004.NetworkConfig) int {
	var successes int
	for _, net := range networks {
		u.Blank()
		u.Printf("  [%s] (chain ID %d)", net.Name, net.ChainID)
		u.Printf("    Registry: %s", net.RegistryAddress)

		if err := registerDirectViaSigner(ctx, cfg, u, net, agentURI, signerNS); err != nil {
			u.Warnf("registration failed: %v", err)
			continue
		}

		u.Printf("    CAIP-10:  %s", net.CAIP10Registry())
		successes++
	}
	return successes
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

	startBlock := registrationRecoveryStartBlock(ctx, client, u)
	agentID, txHash, err := registerWithRecovery(ctx, u, client, agentURI, addr, startBlock, func() (*big.Int, string, error) {
		return client.RegisterWithOptsDetailed(ctx, opts, agentURI)
	})
	if err != nil {
		return err
	}

	u.Printf("    Agent ID: %s", agentID.String())
	u.Printf("    Owner:    %s", addr.Hex())
	if txHash != "" {
		u.Printf("    Tx:       %s", txHash)
	}

	// The Register tx is mined on the WRITE upstream, but a follow-up
	// setMetadata estimateGas goes through the READ upstream which can lag
	// (we observed ERC721NonexistentToken reverts when a stale eRPC route was
	// pinned to a parallel Anvil fork). Block until the reader sees the token.
	if _, err := client.WaitForAgent(ctx, agentID, 30*time.Second); err != nil {
		u.Warnf("agent not visible to reader after register: %v", err)
	}

	// Set x402 metadata.
	x402Meta := []byte(`{"x402":true}`)
	if err := client.SetMetadataWithOpts(ctx, opts, agentID, "x402", x402Meta); err != nil {
		u.Warnf("failed to set x402 metadata: %v", err)
	}
	return nil
}

func registrationRecoveryStartBlock(ctx context.Context, client *erc8004.Client, u *ui.UI) uint64 {
	startBlock, err := client.CurrentBlockNumber(ctx)
	if err != nil {
		u.Warnf("could not read registration recovery start block: %v", err)
		return 0
	}
	return startBlock
}

func registerWithRecovery(
	ctx context.Context,
	u *ui.UI,
	client *erc8004.Client,
	agentURI string,
	owner common.Address,
	startBlock uint64,
	submit func() (*big.Int, string, error),
) (*big.Int, string, error) {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		agentID, txHash, err := submit()
		if err == nil {
			return agentID, txHash, nil
		}
		lastErr = err

		if agentID, txHash, ok := recoverRegistrationByOwnerAndURI(ctx, client, owner, agentURI, startBlock); ok {
			u.Warnf("registration submit returned an error but the on-chain event was recovered: %v", err)
			return agentID, txHash, nil
		}

		if attempt == 3 || !isTransientRegistrationError(err) {
			return nil, "", err
		}

		u.Warnf("registration attempt %d/3 failed: %v; retrying", attempt, err)
		if !sleepWithContext(ctx, time.Duration(attempt*4)*time.Second) {
			return nil, "", ctx.Err()
		}
	}
	return nil, "", lastErr
}

func recoverRegistrationByOwnerAndURI(ctx context.Context, client *erc8004.Client, owner common.Address, agentURI string, startBlock uint64) (*big.Int, string, bool) {
	for attempt := 1; attempt <= 4; attempt++ {
		agentID, txHash, found, err := client.FindRegistrationByOwnerAndURI(ctx, owner, agentURI, startBlock)
		if err == nil && found {
			return agentID, txHash, true
		}
		if !sleepWithContext(ctx, 2*time.Second) {
			return nil, "", false
		}
	}
	return nil, "", false
}

func isTransientRegistrationError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"500 internal server error",
		"502 bad gateway",
		"503 service unavailable",
		"504 gateway timeout",
		"timeout",
		"deadline exceeded",
		"temporarily unavailable",
		"connection reset",
		"eof",
		"too many requests",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
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
		AssetSymbol:     d.AssetSymbol,
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
			u.Printf("Price:        %s", formatInferencePriceSummary(d, ""))
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
	return resolveAssetTermsFor(strings.TrimSpace(cmd.String("token")), chainName, cmd.IsSet("chain"))
}

// resolveAssetTermsFor is the cmd-free core of resolveAssetTerms — takes the
// already-resolved token and chain explicitly. Used by sell demo where chain
// and token come from per-type defaults rather than CLI flags.
func resolveAssetTermsFor(tokenName string, chainName *string, chainExplicit bool) (schemas.AssetTerms, error) {
	tokenName = strings.ToUpper(tokenName)

	// USDC = chain default — no asset override needed.
	if tokenName == "USDC" {
		return schemas.AssetTerms{}, nil
	}

	if chainName == nil {
		return schemas.AssetTerms{}, fmt.Errorf("internal error: chain name pointer is nil")
	}

	// For non-default tokens, default to ethereum when chain is not explicit.
	if !chainExplicit {
		if envChain := strings.TrimSpace(os.Getenv("OBOL_TOKEN_CHAIN")); envChain != "" {
			*chainName = envChain
		} else if *chainName == "" {
			*chainName = "ethereum"
		}
	}

	// Env var overrides bypass the registry — used for test deployments on
	// chains where the token isn't officially deployed (e.g., fork-local OBOL
	// on Base Sepolia via OBOL_TOKEN_ADDRESS).
	if addr := strings.TrimSpace(os.Getenv("OBOL_TOKEN_ADDRESS")); addr != "" {
		return schemas.AssetTerms{
			Address:        addr,
			Symbol:         envOrDefault("OBOL_TOKEN_SYMBOL", "OBOL"),
			Decimals:       18,
			TransferMethod: schemas.AssetTransferMethodPermit2,
			EIP712Name:     envOrDefault("OBOL_TOKEN_NAME", "Obol Network"),
			EIP712Version:  envOrDefault("OBOL_TOKEN_VERSION", "1"),
		}, nil
	}

	// Registry lookup.
	entry, ok := x402verifier.ResolveToken(tokenName, *chainName)
	if !ok {
		onChain := x402verifier.TokensOnChain(*chainName)
		forToken := x402verifier.ChainsForToken(tokenName)
		var hint string
		switch {
		case len(forToken) > 0 && len(onChain) > 0:
			hint = fmt.Sprintf(
				"; tokens on %s: %s; %s is registered on: %s",
				*chainName, strings.Join(onChain, ", "),
				tokenName, strings.Join(forToken, ", "),
			)
		case len(forToken) > 0:
			hint = fmt.Sprintf(
				"; no tokens registered on %s; %s is registered on: %s",
				*chainName, tokenName, strings.Join(forToken, ", "),
			)
		case len(onChain) > 0:
			hint = fmt.Sprintf(
				"; tokens on %s: %s; %s is not registered on any chain",
				*chainName, strings.Join(onChain, ", "), tokenName,
			)
		default:
			hint = fmt.Sprintf(
				"; no tokens registered on %s and %s is not registered on any chain",
				*chainName, tokenName,
			)
		}
		return schemas.AssetTerms{}, fmt.Errorf(
			"--token %s is not available on chain %s%s",
			tokenName, *chainName, hint,
		)
	}

	return schemas.AssetTerms{
		Address:        entry.Address,
		Symbol:         entry.Symbol,
		Decimals:       entry.Decimals,
		TransferMethod: entry.TransferMethod,
		EIP712Name:     entry.EIP712Name,
		EIP712Version:  entry.EIP712Version,
	}, nil
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func formatPriceTableSummary(priceTable schemas.PriceTable, symbol string) string {
	if symbol == "" {
		symbol = "USDC"
	}
	switch {
	case priceTable.PerRequest != "":
		return fmt.Sprintf("%s %s/request", priceTable.PerRequest, symbol)
	case priceTable.PerMTok != "":
		return fmt.Sprintf("%s %s/request (approx from %s %s/MTok @ %d tok/request)",
			priceTable.EffectiveRequestPrice(), symbol,
			priceTable.PerMTok, symbol,
			schemas.ApproxTokensPerRequest,
		)
	case priceTable.PerHour != "":
		return fmt.Sprintf("%s %s/request (approx from %s %s/hour @ %d min/request)",
			priceTable.EffectiveRequestPrice(), symbol,
			priceTable.PerHour, symbol,
			schemas.ApproxMinutesPerRequest,
		)
	default:
		return fmt.Sprintf("0 %s/request", symbol)
	}
}

func formatRoutePriceSummary(route x402verifier.RouteRule) string {
	symbol := route.AssetSymbol
	if symbol == "" {
		symbol = "USDC"
	}
	if route.PriceModel == "perMTok" && route.PerMTok != "" && route.ApproxTokensPerRequest > 0 {
		return fmt.Sprintf("%s %s/request (approx from %s %s/MTok @ %d tok/request)",
			route.Price, symbol, route.PerMTok, symbol, route.ApproxTokensPerRequest)
	}

	if route.Price != "" {
		return fmt.Sprintf("%s %s/request", route.Price, symbol)
	}

	return fmt.Sprintf("0 %s/request", symbol)
}

func formatInferencePriceSummary(d *inference.Deployment, symbol string) string {
	if symbol == "" {
		symbol = "USDC"
	}
	if d.PricePerMTok != "" && d.ApproxTokensPerRequest > 0 {
		return fmt.Sprintf("%s %s/request (approx from %s %s/MTok @ %d tok/request)",
			d.PricePerRequest, symbol, d.PricePerMTok, symbol, d.ApproxTokensPerRequest)
	}

	return fmt.Sprintf("%s %s/request", d.PricePerRequest, symbol)
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
// resumeSellOffers re-applies the cluster-side artifacts (Service +
// Endpoints + ServiceOffer) for every locally-persisted `obol sell
// inference` deployment after `obol stack up` brings a fresh cluster
// online. Without this step the cluster has no record of operator-created
// offers (CRs live in etcd, which is destroyed by `obol stack down`),
// even though the host-side descriptors at
// `<ConfigDir>/inference/<name>/` still exist.
//
// The foreground gateway is NOT restarted here — `obol sell inference`
// is an interactive operator action and we don't want stack-up to launch
// long-running processes. The operator re-runs `obol sell inference
// <name>` after stack-up to bring the gateway back; this step ensures
// the cluster side is already in place so the gateway hits a "service
// healthy" reconcile instead of "create from scratch".
//
// Best-effort. Per-offer failures emit a warning and the loop continues,
// so one broken descriptor cannot block stack-up.
func resumeSellOffers(ctx context.Context, cfg *config.Config, u *ui.UI) error {
	store := inference.NewStore(cfg.ConfigDir)
	deployments, err := store.List()
	if err != nil {
		return fmt.Errorf("list inference deployments: %w", err)
	}
	if len(deployments) == 0 {
		return nil
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, statErr := os.Stat(kubeconfigPath); statErr != nil {
		// No cluster — nothing to reattach.
		return nil
	}

	u.Blank()
	u.Infof("Resuming %d locally-persisted sell-inference offer(s)...", len(deployments))

	var resumed int
	for _, d := range deployments {
		if err := resumeOneInferenceOffer(cfg, u, d); err != nil {
			u.Warnf("resume %s: %v", d.Name, err)
			continue
		}
		resumed++
		u.Successf("Resumed sell-inference offer %q (run `obol sell inference %s` to restart the host gateway)", d.Name, d.Name)
	}

	if resumed > 0 {
		u.Dim("  Host gateways are not auto-started — re-run `obol sell inference <name>` in a terminal you can keep open.")
	}
	_ = ctx // reserved for cancellation support; current resume calls are synchronous and short
	return nil
}

// resumeOneInferenceOffer re-creates the cluster-side artifacts that
// `obol sell inference` would have produced for a single Deployment. Pure
// in the sense that it only consumes the on-disk descriptor; it never
// re-prompts the operator. Returns an error when the descriptor is
// incomplete (no model name, no namespace, no listen port) so the resume
// loop can surface a clear message per-offer.
func resumeOneInferenceOffer(cfg *config.Config, u *ui.UI, d *inference.Deployment) error {
	if d == nil || d.Name == "" {
		return errors.New("nil or unnamed deployment descriptor")
	}
	if d.ModelName == "" {
		return fmt.Errorf("deployment %q is missing model_name on disk — recreate the offer with `obol sell inference %s --model <id> ...`", d.Name, d.Name)
	}
	ns := d.ServiceNamespace
	if ns == "" {
		ns = "llm" // legacy descriptors written before service_namespace was persisted
	}

	port := "8402"
	if idx := strings.LastIndex(d.ListenAddr, ":"); idx >= 0 && idx+1 < len(d.ListenAddr) {
		port = d.ListenAddr[idx+1:]
	}

	if err := createHostService(cfg, d.Name, ns, port); err != nil {
		return fmt.Errorf("create cluster Service/Endpoints: %w", err)
	}

	chainName := d.Chain
	assetTerms, err := resolveAssetTermsFor(d.AssetSymbol, &chainName, true)
	if err != nil {
		return fmt.Errorf("resolve asset terms: %w", err)
	}

	pt := schemas.PriceTable{PerRequest: d.PricePerRequest, PerMTok: d.PricePerMTok}
	soSpec, err := buildInferenceServiceOfferSpec(d, pt, ns, port, assetTerms, d.ModelName, d.Registration)
	if err != nil {
		return fmt.Errorf("rebuild ServiceOffer spec: %w", err)
	}

	manifest := map[string]any{
		"apiVersion": "obol.org/v1alpha1",
		"kind":       "ServiceOffer",
		"metadata": map[string]any{
			"name":      d.Name,
			"namespace": ns,
		},
		"spec": soSpec,
	}
	if err := kubectlApply(cfg, manifest); err != nil {
		return fmt.Errorf("apply ServiceOffer: %w", err)
	}

	// Auto-start the foreground x402 gateway as a detached background
	// subprocess so the offer reaches UpstreamHealthy=True (and therefore
	// shows up in /api/services.json) without the operator running an
	// extra `obol sell inference` command after every stack-up.
	//
	// This is the pragmatic shape of the resume feature today. The proper
	// long-term path is C from the design doc: build the inference gateway
	// as an in-cluster Deployment image, helmfile-managed, so stack-up
	// brings it back the same way it brings back Traefik or LiteLLM. That
	// removes the host-side subprocess + PID-file plumbing entirely and
	// lets the controller observe the gateway as a normal Pod. Tracked as
	// a follow-up because the gateway code (internal/inference/gateway.go)
	// currently runs in-process with the CLI, not as a packaged image.
	if err := startDetachedInferenceGateway(cfg, u, d); err != nil {
		u.Warnf("could not auto-start host gateway for %q: %v", d.Name, err)
		u.Dim("  Run `obol sell inference " + d.Name + "` manually in a terminal you can keep open.")
	}
	return nil
}

// startDetachedInferenceGateway forks `obol sell inference <name>` as a
// detached background subprocess. The CLI rebuilds the full flag set from
// the persisted Deployment, redirects stdout/stderr to a log file under
// the workspace state dir, and stores the PID alongside so subsequent
// stack-ups can detect a still-running gateway and skip the relaunch.
//
// Skipped silently when a healthy PID is already on disk. Returns an
// error only when launch itself fails (e.g. missing obol binary, log
// file unwritable). Callers should warn-and-continue on failure — a
// missing gateway leaves UpstreamHealthy=False which the controller will
// re-emit clearly, while a hard error here would block the rest of the
// resume loop.
func startDetachedInferenceGateway(cfg *config.Config, u *ui.UI, d *inference.Deployment) error {
	if d == nil || d.Name == "" {
		return errors.New("nil or unnamed deployment")
	}

	stateDir := filepath.Join(cfg.StateDir, "sell-inference", d.Name)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	pidFile := filepath.Join(stateDir, "gateway.pid")
	logFile := filepath.Join(stateDir, "gateway.log")

	if pid, ok := readGatewayPID(pidFile); ok && processAlive(pid) {
		u.Dim("  Gateway already running for " + d.Name + " (pid " + strconv.Itoa(pid) + ")")
		return nil
	}

	obolBin := filepath.Join(cfg.BinDir, "obol")
	if _, statErr := os.Stat(obolBin); statErr != nil {
		// Fall back to whatever obol the parent process is running as.
		exe, exeErr := os.Executable()
		if exeErr != nil {
			return fmt.Errorf("locate obol binary: %w", exeErr)
		}
		obolBin = exe
	}

	args := buildResumeGatewayArgs(d)

	logF, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	cmd := exec.Command(obolBin, args...)
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.SysProcAttr = detachedSysProcAttr()
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		_ = logF.Close()
		return fmt.Errorf("start gateway subprocess: %w", err)
	}
	// We don't wait on the child, but releasing the handle ensures the
	// parent process can exit cleanly without keeping a zombie reference.
	if err := cmd.Process.Release(); err != nil {
		u.Dim("  (could not release gateway process handle: " + err.Error() + ")")
	}
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		u.Warnf("gateway started (pid %d) but could not write %s: %v", cmd.Process.Pid, pidFile, err)
	}
	u.Successf("Gateway started in background (pid %d, log %s)", cmd.Process.Pid, logFile)
	return nil
}

// buildResumeGatewayArgs reconstructs the `obol sell inference` flag set
// that originally created the offer, from the persisted Deployment. The
// order matches obol sell inference's flag list so a future surface
// reduction of the CLI doesn't silently drop a piece of operator intent
// (CI's source-level guard tests catch that case).
//
// Exposed for testing: callers can compare the reproduced flag set
// against the original `obol sell inference` invocation that wrote the
// descriptor.
func buildResumeGatewayArgs(d *inference.Deployment) []string {
	args := []string{"sell", "inference", d.Name}
	if d.ModelName != "" {
		args = append(args, "--model", d.ModelName)
	}
	if d.UpstreamURL != "" {
		args = append(args, "--upstream", d.UpstreamURL)
	}
	if d.WalletAddress != "" {
		args = append(args, "--pay-to", d.WalletAddress)
	}
	if d.ListenAddr != "" {
		args = append(args, "--listen", d.ListenAddr)
	}
	if d.Chain != "" {
		args = append(args, "--chain", d.Chain)
	}
	if d.AssetSymbol != "" {
		args = append(args, "--token", d.AssetSymbol)
	}
	if d.PricePerMTok != "" {
		args = append(args, "--per-mtok", d.PricePerMTok)
	} else if d.PricePerRequest != "" {
		args = append(args, "--price", d.PricePerRequest)
	}
	if d.FacilitatorURL != "" {
		args = append(args, "--facilitator", d.FacilitatorURL)
	}
	// Registration block — rebuild --register-* flags from the persisted map.
	if reg, ok := d.Registration["enabled"].(bool); ok && !reg {
		args = append(args, "--no-register")
	} else if d.Registration != nil {
		if v, _ := d.Registration["name"].(string); v != "" {
			args = append(args, "--register-name", v)
		}
		if v, _ := d.Registration["description"].(string); v != "" {
			args = append(args, "--register-description", v)
		}
		if v, _ := d.Registration["image"].(string); v != "" {
			args = append(args, "--register-image", v)
		}
		for _, s := range registrationStringSlice(d.Registration, "skills") {
			args = append(args, "--register-skills", s)
		}
		for _, s := range registrationStringSlice(d.Registration, "domains") {
			args = append(args, "--register-domains", s)
		}
	}
	return args
}

func registrationStringSlice(reg map[string]any, key string) []string {
	raw, ok := reg[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func readGatewayPID(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 doesn't deliver — just probes existence/permission on Unix.
	return p.Signal(syscall.Signal(0)) == nil
}

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
//
// modelName is the upstream model identifier the operator passed via --model
// (e.g. "aeon-ultimate"). It is written into spec.model.name so the
// serviceoffer-controller's registration document carries the real model id
// rather than the historical hardcoded "ollama" string.
//
// registration is the operator's ERC-8004 registration block as produced by
// buildSellRegistrationConfig — pass nil (or an empty map) to skip
// registration. When non-nil it is merged verbatim into spec.registration.
func buildInferenceServiceOfferSpec(d *inference.Deployment, pt schemas.PriceTable, ns, port string, asset schemas.AssetTerms, modelName string, registration map[string]any) (map[string]any, error) {
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
		model := strings.TrimSpace(modelName)
		if model == "" {
			model = "ollama" // pre-fix fallback; the Action enforces --model
		}
		spec["model"] = map[string]any{
			"name":    model,
			"runtime": "ollama",
		}
	}

	if len(registration) > 0 {
		spec["registration"] = registration
	}

	return spec, nil
}

// removePricingRoute is a no-op retained for compatibility.
// The serviceoffer-controller now manages pricing routes via the ServiceOffer
// informer; static ConfigMap routes are no longer used.
func removePricingRoute(_ *config.Config, _ *ui.UI, _ string) {}
