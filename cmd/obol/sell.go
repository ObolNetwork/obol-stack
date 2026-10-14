package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
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
	stackdefaults "github.com/ObolNetwork/obol-stack/internal/defaults"
	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/images"
	"github.com/ObolNetwork/obol-stack/internal/inference"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/replay"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/stack"
	"github.com/ObolNetwork/obol-stack/internal/tunnel"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/validate"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/ObolNetwork/obol-stack/internal/x402mcp"
	"github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"
)

func sellCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "sell",
		Usage: "Sell access to agents and services using digital payments",
		Description: `Sell access to agents and services using digital payments (x402).

Commands split into two scopes:

  The storefront (the whole shop)
    info        Preview the storefront and everything on sale, as buyers see it
    info set    Set the storefront's branding (name, tagline, logo)
    register    Publish the seller to a distribution channel (ERC-8004 on-chain,
                'register x402scan' for the x402scan discovery index)
    identity    Manage the durable on-chain identity record
    list        List every offer (raw)
    resume      Re-publish all offers (e.g. after a restart)

  A single service (one item on the shelf)
    inference | http | agent | mcp | demo   Put a service up for sale
    status      Operator health and conditions for an offer
    update      Change an offer's price or payout wallet in place
    stop        Stop selling an offer without deleting it
    delete      Stop selling an offer and remove it`,
		Commands: []*cli.Command{
			sellInferenceCommand(cfg),
			sellHTTPCommand(cfg),
			sellMCPCommand(cfg),
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
			sellIdentityCommand(cfg),
			sellInfoCommand(cfg),
			sellResumeCommand(cfg),
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
		Description: `Starts a standalone x402-gated reverse proxy in front of a host inference
server (Ollama, vLLM, ...), for hosts without an Obol Stack. Buyers pay
per-request in the selected x402 token. The gateway only serves the paid
OpenAI-compatible routes (POST /v1/chat/completions, /v1/completions,
/v1/embeddings, GET /v1/models) plus GET /health; everything else is 404.

On a host running the stack, sell the inference server through LiteLLM
instead (seller-pinned model, inference-only routes, settle on delivery):

  obol model setup custom --endpoint http://<host>:<port>/v1 --model <id>
  obol sell http <name> --upstream litellm --port 4000 --namespace llm \
      --model <id> --per-mtok <price> --pay-to 0x...

'obol sell inference' refuses to run when a stack kubeconfig exists unless
--standalone is passed.

Examples:
  obol sell inference my-qwen --model qwen3.5:4b --pay-to 0x... --price 0.001
  obol sell inference my-llama --model llama3:8b --pay-to 0x... --network base`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "model",
				Usage: "Model name to serve (e.g. qwen3.5:4b)",
			},
			payToFlag("Payment recipient address"),
			&cli.StringFlag{
				Name:  "price",
				Usage: "Per-request price (alias for --per-request)",
			},
			&cli.StringFlag{
				Name:  "per-request",
				Usage: "Per-request price (alias for --price)",
			},
			&cli.StringFlag{
				Name:  "per-mtok",
				Usage: "Per-million-tokens price in the selected payment token (charged as an approximation at 1000 tok/request)",
			},
			&cli.StringFlag{
				Name:    "network",
				Aliases: []string{"chain"},
				Usage:   "Payment network (base, base-sepolia, ethereum)",
				Value:   "base",
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
				Name:  "provenance-file",
				Usage: "Path to JSON file with provenance metadata (e.g. training or benchmark results)",
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
				Name:    "description",
				Aliases: []string{"register-description"},
				Usage:   "Human-readable description of the service. Surfaced on the 402 payment page, in the storefront catalog, and (when registration is enabled) on the ERC-8004 registration document.",
			},
			&cli.BoolFlag{
				Name:  "standalone",
				Usage: "Run the standalone gateway (built-in x402 gate) even when this host runs an Obol Stack",
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

			// With a stack on this host, the supported way to sell a host
			// inference server is LiteLLM + `obol sell http`. Refuse before
			// any prompt or persisted state, unless --standalone is set.
			if err := refuseSellInferenceWithStack(cfg, cmd, name); err != nil {
				return err
			}

			warnOnClockSkew(ctx, u, cmd.String("facilitator"))

			wallet := cmd.String("pay-to")
			if wallet == "" {
				if resolved, err := hermes.ResolveWalletAddress(cfg); err == nil {
					wallet = resolved
					u.Infof("Using wallet from remote-signer: %s", wallet)
				} else if u.IsTTY() {
					var inputErr error
					wallet, inputErr = u.Input("Wallet address (payment recipient)", "")
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
			// LiteLLM's `paid/*` wildcard route doesn't match model names
			// containing `/` — buyers signing against this seller would see
			// requests fall through to the buyer sidecar with a 404 (see
			// CLAUDE.md and the obol-agent's recent buy report). Reject
			// up-front and suggest a `--` separator that survives the route.
			if err := validateSellInferenceModelName(modelFlag); err != nil {
				return err
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

			// Resolve (and validate) the registration block once, here, and
			// persist it alongside the deployment descriptor.
			persistedRegistration, _, regErr := buildSellRegistrationConfig(name, sellRegistrationInput{
				NoRegister:    cmd.Bool("no-register"),
				Name:          cmd.String("register-name"),
				Description:   cmd.String("description"),
				Image:         cmd.String("register-image"),
				Skills:        cmd.StringSlice("register-skills"),
				Domains:       cmd.StringSlice("register-domains"),
				MetadataPairs: cmd.StringSlice("register-metadata"),
			})
			if regErr != nil {
				return regErr
			}

			d := &inference.Deployment{
				Name:            name,
				ListenAddr:      cmd.String("listen"),
				UpstreamURL:     upstreamFlag,
				WalletAddress:   wallet,
				PricePerRequest: perRequest,
				PricePerMTok:    priceTable.PerMTok,
				AssetSymbol:     assetSymbol,
				Chain:           chainName,
				FacilitatorURL:  cmd.String("facilitator"),
				ModelName:       modelFlag,
				Mode:            inference.ModeStandalone,
				Registration:    persistedRegistration,
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

			return runInferenceGateway(u, d, chain)
		},
	}
}

// ---------------------------------------------------------------------------
// sell http — create a ServiceOffer CRD for any HTTP service
// ---------------------------------------------------------------------------

func sellMCPCommand(cfg *config.Config) *cli.Command {
	_ = cfg
	return &cli.Command{
		Name:      "mcp",
		Usage:     "Sell a paid MCP tool over x402 (in-band _meta payment)",
		ArgsUsage: "[name]",
		Description: `Runs a local x402-paid MCP (Model Context Protocol) server in the
foreground. The paid tool forwards the buyer's JSON arguments to a backend HTTP
service, injecting the seller's own credential (an API key the buyer never
sees), so any credentialed real-world service can be resold to agents per call.
Buyers (e.g. hermes-agent's pay_mcp plugin) settle in-band via the MCP request
_meta["x402/payment"] field, per specs/transports-v2/mcp.md. The wrapper runs
verify -> execute -> settle inside the tool call, so a caller is never charged
for a failed tool. This is the application-layer counterpart to the HTTP-402
ForwardAuth gate used by 'obol sell inference' / 'obol sell http'.

Connect a buyer at http://localhost:<port>/mcp (streamable HTTP).

Examples:
  # Front a weather API as a paid MCP tool (the canonical x402 paid-MCP shape):
  obol sell mcp weather --pay-to 0x... --price 0.001 --network base-sepolia \
      --tool-name get_weather \
      --description 'Current weather for a city. Args: {city}' \
      --upstream https://your-weather-service/current

  # Any JSON HTTP service works the same way; pass the backend's auth header if
  # it needs one (set server-side, never sent to buyers):
  obol sell mcp my-tool --pay-to 0x... --price 0.005 --tool-name call \
      --upstream https://api.example.com/do --upstream-header 'X-Api-Key: <key>'`,
		Flags: []cli.Flag{
			payToFlag("Payment recipient address"),
			&cli.StringFlag{
				Name:    "network",
				Aliases: []string{"chain"},
				Usage:   "Payment network (base, base-sepolia, ethereum, polygon)",
				Value:   "base-sepolia",
			},
			&cli.StringFlag{
				Name:  "price",
				Usage: "Per-call price, USD-denominated (e.g. 0.001)",
				Value: "0.001",
			},
			&cli.StringFlag{
				Name:  "tool-name",
				Usage: "Name of the paid MCP tool",
				Value: "call",
			},
			&cli.StringFlag{
				Name:  "description",
				Usage: "Human-readable description of the paid tool",
			},
			&cli.IntFlag{
				Name:  "port",
				Usage: "Port to serve the MCP server on",
				Value: 4022,
			},
			&cli.StringFlag{
				Name:  "upstream",
				Usage: "Backend HTTP service URL the paid tool POSTs the buyer's JSON args to (e.g. a weather/data API)",
			},
			&cli.StringSliceFlag{
				Name:  "upstream-header",
				Usage: "Optional auth header for the backend, set server-side and never sent to buyers (repeatable, \"Key: Value\", e.g. \"X-Api-Key: <key>\")",
			},
			&cli.StringFlag{
				Name:  "facilitator",
				Usage: "x402 facilitator URL (verify/settle)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			payTo := cmd.String("pay-to")
			if payTo == "" {
				return errors.New("--pay-to is required")
			}
			name := cmd.Args().First()
			if name == "" {
				name = "obol-mcp"
			}
			facilitator := cmd.String("facilitator")
			if facilitator == "" {
				facilitator = x402verifier.DefaultFacilitatorURL
			}

			headers, err := parseUpstreamHeaders(cmd.StringSlice("upstream-header"))
			if err != nil {
				return err
			}

			u.Infof("Starting paid MCP server %q on port %d (Ctrl-C to stop)", name, cmd.Int("port"))
			return x402mcp.Serve(ctx, x402mcp.Options{
				Name:            name,
				ToolName:        cmd.String("tool-name"),
				Description:     cmd.String("description"),
				Port:            cmd.Int("port"),
				PayTo:           payTo,
				Price:           cmd.String("price"),
				Chain:           cmd.String("chain"),
				FacilitatorURL:  facilitator,
				Upstream:        cmd.String("upstream"),
				UpstreamHeaders: headers,
			})
		},
	}
}

// parseUpstreamHeaders turns repeatable "Key: Value" --upstream-header flags
// into a header map injected on upstream calls (the seller's credential).
func parseUpstreamHeaders(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	headers := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, ":")
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid --upstream-header %q: want \"Key: Value\"", p)
		}
		headers[k] = v
	}
	return headers, nil
}

func sellHTTPCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "http",
		Usage:     "Sell any local HTTP service with x402 payments",
		ArgsUsage: "<name>",
		// --accept carries a comma-separated key=value list per option
		// (token=USDC,network=base,price=1). cli/v3 slice flags split on ","
		// by default, which would shred one option into several fragments and
		// fail with "network is required". Disable the separator so each
		// --accept is one whole value; parseAcceptKV does its own "," split.
		// The repeatable --register-skills/-domains/-metadata flags are
		// unaffected in practice (each value is a single token / key=value).
		DisableSliceFlagSeparator: true,
		Description: `Publishes a payment gated HTTP API to any service within the stack.
By default it also registers the seller agent on ERC-8004 after the route is live.
Use --no-register to skip the on-chain registration step.

Examples:
  obol sell http my-cool-api --upstream my-svc.my-namespace.svc.cluster.local --port 8080 --pay-to 0x... --price 0.01 --network base
  obol sell http my-cool-api --upstream my-svc --port 8080 --pay-to 0x... --price 0.01 --network base --no-register
  obol sell http my-llm --upstream litellm --port 4000 --namespace llm --model qwen3.5:9b --pay-to 0x... --price 0.001

With --upstream litellm the seller picks the model: --model is required and the
gateway overwrites the model field of every buyer request with it.`,
		Flags: append([]cli.Flag{
			payToFlag("Payment recipient address"),
			&cli.StringFlag{
				Name:    "network",
				Aliases: []string{"chain"},
				Usage:   "Payment network (base, base-sepolia, ethereum)",
				Value:   "base",
			},
			&cli.StringFlag{
				Name:  "token",
				Usage: "Payment token (USDC, OBOL)",
				Value: "USDC",
			},
			&cli.StringFlag{
				Name:  "price",
				Usage: "Per-request price in the selected payment token (e.g. 0.001 USDC or 1 OBOL)",
			},
			&cli.StringFlag{
				Name:  "per-request",
				Usage: "Per-request price in the selected payment token (alias for --price)",
			},
			&cli.StringFlag{
				Name:  "per-mtok",
				Usage: "Per-million-tokens price in the selected payment token (charged as an approximation at 1000 tok/request)",
			},
			&cli.StringFlag{
				Name:  "per-hour",
				Usage: "Per-compute-hour price in the selected payment token",
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
			&cli.StringFlag{
				Name: "model",
				Usage: "LiteLLM model_name this offer serves (required with --upstream litellm). " +
					"The seller gateway overwrites the buyer's model field with it; change it later " +
					"with 'obol sell update <name> --model'",
			},
			&cli.IntFlag{
				Name:  "port",
				Usage: "Upstream service port",
				Value: 8080,
			},
			&cli.StringFlag{
				Name: "health-path",
				Usage: "Upstream health check path. When left at the default, --upstream litellm " +
					"defaults to /health/readiness and --upstream ollama defaults to / — " +
					"neither serves a usable /health.",
				Value: "/health",
			},
			&cli.StringFlag{
				Name:  "path",
				Usage: "URL path prefix (default: /services/<name>)",
			},
			&cli.StringFlag{
				Name: "hostname",
				Usage: "Dedicated public origin for this offer (e.g. audit.example.com). The offer's " +
					"routes answer at the hostname root with their own discovery bundle (/openapi.json, " +
					"/.well-known/x402, landing page) so per-origin crawlers (x402scan, agentcash) list " +
					"it as its own product. The /services/<name> path stays as an alias. Route the DNS " +
					"via 'obol tunnel hostname add <host> --offer <ns>/<name>'.",
			},
			&cli.StringSliceFlag{
				Name: "route",
				Usage: "Declare one route in the offer's route table (repeatable). " +
					"Format: path=/submit[,methods=POST|GET][,gate=paid|free|auth][,price=0.5][,summary=...]. " +
					"Paths are relative to the offer prefix; trailing /* covers sub-paths. " +
					"When any --route is set, only declared routes are served (undeclared paths 404) — " +
					"add path=/*,gate=paid for a catch-all. gate=free carves the route out of the " +
					"payment gate; gate=auth requires a SIWX wallet sign-in instead of payment; " +
					"price overrides the offer price for that route.",
			},
			&cli.IntFlag{
				Name:  "max-timeout",
				Usage: "Payment validity window in seconds",
				Value: 300,
			},
			&cli.BoolFlag{
				Name: "async",
				Usage: "Deliver paid requests as accepted jobs: payment settles at acceptance, the request " +
					"replays upstream with no client-facing deadline (survives tunnel timeouts), and the buyer " +
					"gets 202 + a free status page at <offer>/jobs/<id>. Results are gated to the paying " +
					"wallet (SIWX) or the jobToken from the 202 body. NOTE: no refunds — a failed run was " +
					"still paid for; set a contact via 'obol sell info set --contact-email'.",
			},
			&cli.StringFlag{
				Name:  "result-visibility",
				Usage: "Async result access: 'payer' (paying wallet or jobToken; default) or 'public' (the unguessable job id is the capability)",
			},
			&cli.StringFlag{
				Name:  "job-ttl",
				Usage: "Async job + result retention (Go duration, e.g. 72h); status pages return 410 afterwards",
			},
			&cli.IntFlag{
				Name:  "max-in-flight",
				Usage: "Cap concurrent in-flight requests to this offer (Traefik inFlightReq); 0 = uncapped",
			},
			&cli.IntFlag{
				Name:  "rps",
				Usage: "Cap average requests/second to this offer (Traefik rateLimit, burst 2x); 0 = uncapped",
			},
			// Registration flags
			&cli.BoolFlag{
				Name:   "register",
				Hidden: true, // deprecated no-op; registration is on by default
				Usage:  "Deprecated: registration is enabled by default",
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
				Name:    "description",
				Aliases: []string{"register-description"},
				Usage:   "Human-readable description of the service. Surfaced on the 402 payment page, in the storefront catalog, and (when registration is enabled) on the ERC-8004 registration document.",
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
				Usage: "Path to JSON file with provenance metadata (e.g. training or benchmark results)",
			},
			&cli.StringFlag{
				Name:  "from-json",
				Usage: "Read ServiceOffer spec from JSON file (or - for stdin) instead of flags",
			},
		}, acceptFlags()...),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			warnOnClockSkew(ctx, u, "")

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

				if err := checkFromJSONLiteLLMModel(spec); err != nil {
					return err
				}

				manifest := map[string]interface{}{
					"apiVersion": "obol.org/v1alpha1",
					"kind":       "ServiceOffer",
					"metadata": map[string]interface{}{
						"name":      name,
						"namespace": ns,
						"labels":    map[string]any{kubectl.ManagedByLabel: kubectl.ManagedByObol},
					},
					"spec": spec,
				}

				if err := preflightOfferPathCollision(cfg, manifest); err != nil {
					return err
				}
				if err := kubectlApply(cfg, manifest); err != nil {
					return err
				}
				if persistErr := persistServiceOffer(cfg, ns, name, manifest); persistErr != nil {
					u.Warnf("could not persist offer for stack-up resume: %v", persistErr)
				}
				u.Successf("ServiceOffer %s/%s created from JSON", ns, name)
				printOfferLinks(cfg, u, ns, name, true)
				return nil
			}

			name := cmd.Args().First()
			if name == "" {
				if u.IsTTY() {
					var err error
					name, err = u.Input("Service name", "")
					if err != nil || name == "" {
						return fmt.Errorf("name required: obol sell http <name> --pay-to <addr> --network <chain>")
					}
				} else {
					return fmt.Errorf("name required: obol sell http <name> --pay-to <addr> --network <chain>")
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
					wallet, inputErr = u.Input("Wallet address (payment recipient)", "")
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
				return fmt.Errorf("upstream service name required: use --upstream <service-name>\n\n  Example: obol sell http %s --upstream my-svc --port 8080 --pay-to 0x... --network base-sepolia --price 0.001", name)
			}
			if cmd.Int("port") == 0 {
				return fmt.Errorf("upstream port required: use --port <port-number>\n\n  Example: obol sell http %s --upstream my-svc --port 8080 --pay-to 0x... --network base-sepolia --price 0.001", name)
			}

			// LiteLLM-backed offers serve a seller-pinned model: the gateway
			// overwrites the buyer's "model", so the seller must name it.
			var configuredModels []string
			var modelListErr error
			if isLiteLLMUpstreamName(cmd.String("upstream")) && strings.TrimSpace(cmd.String("model")) != "" {
				configuredModels, modelListErr = configuredLiteLLMModels(cfg, u)
			}
			pinnedModel, err := checkSellHTTPModel(cmd.String("upstream"), cmd.String("model"), configuredModels, modelListErr)
			if err != nil {
				return err
			}

			// Build the payment block(s): multi-currency via --accept, else
			// the singular --chain/--token/--price flags. chainName/wallet are
			// re-pointed at the primary option so registration lands on it.
			paymentBlock, paymentsList, chainName, primaryPayTo, err := resolveOfferPayments(cmd, wallet, true)
			if err != nil {
				return err
			}
			if paymentsList != nil {
				// Best-effort fill of any raw-asset metadata from the chain
				// (no-op for registry/USDC options). Errors if a raw token's
				// decimals/EIP-712 domain can't be resolved.
				if err := autofillAcceptPayments(ctx, paymentsList, func(ctx context.Context, network, addr string) (tokenMeta, error) {
					return fetchTokenMeta(ctx, cfg, network, addr)
				}); err != nil {
					return err
				}
				paymentBlock = paymentsList[0]
			}
			wallet = primaryPayTo

			upstreamSvc := cmd.String("upstream")
			healthPath := cmd.String("health-path")
			if defaulted := defaultHealthPathForUpstream(upstreamSvc, healthPath); defaulted != healthPath {
				healthPath = defaulted
				u.Dim(fmt.Sprintf("  --upstream %s: no usable /health, defaulting --health-path to %q", upstreamSvc, healthPath))
			}
			spec := map[string]any{
				"type": "http",
				"upstream": map[string]any{
					"service":    upstreamSvc,
					"namespace":  ns,
					"port":       cmd.Int("port"),
					"healthPath": healthPath,
				},
				"payment": paymentBlock,
			}
			if pinnedModel != "" {
				spec["model"] = liteLLMModelSpec(pinnedModel)
			}
			if paymentsList != nil {
				spec["payments"] = paymentsList
			}
			applyListingFlags(cmd, spec)

			if path := cmd.String("path"); path != "" {
				spec["path"] = path
			}

			if hostname := strings.ToLower(strings.TrimSpace(cmd.String("hostname"))); hostname != "" {
				spec["hostname"] = hostname
			}

			if cmd.Bool("async") || cmd.String("result-visibility") != "" || cmd.String("job-ttl") != "" {
				asyncBlock := map[string]any{"enabled": true}
				if vis := cmd.String("result-visibility"); vis != "" {
					if vis != monetizeapi.ResultVisibilityPayer && vis != monetizeapi.ResultVisibilityPublic {
						return fmt.Errorf("--result-visibility must be payer or public (got %q)", vis)
					}
					asyncBlock["resultVisibility"] = vis
				}
				if ttl := cmd.String("job-ttl"); ttl != "" {
					if _, err := time.ParseDuration(ttl); err != nil {
						return fmt.Errorf("--job-ttl: %w", err)
					}
					asyncBlock["ttl"] = ttl
				}
				spec["async"] = asyncBlock
			}

			if cmd.Int("max-in-flight") > 0 || cmd.Int("rps") > 0 {
				limits := map[string]any{}
				if v := cmd.Int("max-in-flight"); v > 0 {
					limits["maxInFlight"] = v
				}
				if v := cmd.Int("rps"); v > 0 {
					limits["rps"] = v
				}
				spec["limits"] = limits
			}

			if routeVals := cmd.StringSlice("route"); len(routeVals) > 0 {
				routes, hasPaid, err := parseRouteFlags(routeVals)
				if err != nil {
					return err
				}
				spec["routes"] = routes
				if !hasPaid {
					u.Warn("Route table has no paid route — every declared path is free and undeclared paths are not served. Add a paid route (e.g. --route path=/*,gate=paid) if this offer should charge.")
				}
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
				Description:   cmd.String("description"),
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
					"labels":    map[string]any{kubectl.ManagedByLabel: kubectl.ManagedByObol},
				},
				"spec": spec,
			}

			if err := preflightOfferPathCollision(cfg, manifest); err != nil {
				return err
			}

			applyOut, err := kubectlApplyOutput(cfg, manifest)
			if err != nil {
				return err
			}
			if persistErr := persistServiceOffer(cfg, ns, name, manifest); persistErr != nil {
				u.Warnf("could not persist offer for stack-up resume: %v", persistErr)
			}
			action := "created"
			if strings.Contains(applyOut, "configured") || strings.Contains(applyOut, "unchanged") {
				action = "updated"
			}
			u.Successf("ServiceOffer %s/%s %s (type: http)", ns, name, action)
			if paymentsList != nil {
				u.Infof("Accepted payments: %s", acceptSummary(paymentsList))
			}
			if pm, ok := paymentBlock["price"].(map[string]any); ok {
				if _, isMTok := pm["perMTok"]; isMTok {
					u.Info("Per-MTok price is charged as a per-request approximation (~1000 tok/request) until exact metering ships.")
				}
			}
			u.Infof("The agent will reconcile: health-check → payment gate → route")
			u.Infof("Check status: obol sell status %s -n %s", name, ns)

			if strings.TrimSpace(cmd.String("hostname")) != "" {
				// The offer's spec.hostname binding just landed on the
				// cluster; narrow (or tear down) the storefront's catch-all
				// route for it now rather than waiting for some later,
				// unrelated tunnel/sell invocation to notice.
				if err := tunnel.RefreshStorefront(cfg); err != nil {
					u.Warnf("could not refresh storefront: %v", err)
				}
			}

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
				printOfferLinks(cfg, u, ns, name, action == "created")
				return nil
			}

			// Registration path: tunnelURL has already been awaited above.
			if reg, ok := spec["registration"].(map[string]any); ok {
				if enabled, _ := reg["enabled"].(bool); enabled {
					u.Blank()
					u.Info("Registering seller agent on ERC-8004...")
					if err := autoRegisterServiceOffer(ctx, cfg, u, autoRegisterOptions{
						ChainCSV:      chainName,
						Endpoint:      tunnelURL,
						AgentName:     registrationNameForPrompt(name, reg),
						AgentDesc:     registrationDescriptionForPrompt(name, reg),
						ExpectedOwner: wallet,
					}); err != nil {
						aw, _ := hermes.ResolveWalletAddress(cfg)
						printRegistrationNotice(u, registrationNotice{
							Mode:        regNoticeAutoFailed,
							Chain:       chainName,
							PayTo:       wallet,
							AgentWallet: aw,
							OfferName:   name,
							Namespace:   ns,
						})
						return fmt.Errorf("automatic sell registration failed: %w", err)
					}
				}
			}

			printOfferLinks(cfg, u, ns, name, action == "created")
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
		return nil, errors.New("nothing to update: pass at least one of --per-request / --per-mtok / --per-hour / --pay-to / --network")
	}

	return map[string]any{
		"spec": map[string]any{
			"payment": payment,
		},
	}, nil
}

// buildSellAcceptUpdatePatch returns a merge patch that REPLACES an offer's
// payment set with the given options. spec.payments is swapped wholesale; the
// singular spec.payment is reset to the new primary (payments[0]) so discovery
// surfaces that read it stay consistent. When the new primary is USDC (no
// explicit asset), the old asset block is nulled out so a previous non-USDC
// asset doesn't survive the merge.
func buildSellAcceptUpdatePatch(payments []map[string]any) map[string]any {
	primary := map[string]any{}
	for k, v := range payments[0] {
		primary[k] = v
	}
	if _, hasAsset := primary["asset"]; !hasAsset {
		primary["asset"] = nil
	}
	return map[string]any{
		"spec": map[string]any{
			"payments": payments,
			"payment":  primary,
		},
	}
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
	return registrationEnabledInSpec(spec)
}

// registrationEnabledInSpec reports whether a ServiceOffer spec map has
// ERC-8004 registration enabled.
func registrationEnabledInSpec(spec map[string]any) bool {
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

	origin, err := normalizeAgentOrigin(opts.Endpoint)
	if err != nil {
		return err
	}
	agentURI := origin + "/.well-known/agent-registration.json"
	u.Printf("  Agent URI: %s", agentURI)

	var successes, metadataFailures int
	for _, net := range networks {
		u.Blank()
		u.Printf("  [%s] (chain ID %d)", net.Name, net.ChainID)
		u.Printf("    Registry: %s", net.RegistryAddress)

		metadataOK, err := registerDirectViaSigner(ctx, cfg, u, net, agentURI, signerNS)
		if err != nil {
			u.Warnf("direct registration failed: %v", err)
			continue
		}
		if !metadataOK {
			metadataFailures++
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
	if metadataFailures > 0 {
		u.Warnf("x402 metadata missing on %d/%d network(s); mint + agentURI are registered — see warnings above, re-run 'obol sell register' to retry the metadata write.", metadataFailures, successes)
	}
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
	}
	// Show each accepted payment option. Single-payment offers keep the flat
	// Network/Asset/Price/Pay To lines; multi-currency offers list every option.
	payments := offer.EffectivePayments()
	if len(payments) <= 1 {
		p := effectivePaymentAsset(offer.Spec.Payment)
		lines = append(lines,
			fmt.Sprintf("Network:         %s", valueOrNone(p.Network)),
			fmt.Sprintf("Asset:           %s", formatOfferAsset(p.Asset)),
			fmt.Sprintf("Price:           %s", formatOfferPrice(p)),
			fmt.Sprintf("Pay To:          %s", valueOrNone(p.PayTo)),
		)
	} else {
		lines = append(lines, fmt.Sprintf("Payments:        %d accepted options (buyer picks one)", len(payments)))
		for i := range payments {
			p := effectivePaymentAsset(payments[i])
			lines = append(lines,
				fmt.Sprintf("  - %s on %s → %s",
					formatOfferPrice(p), valueOrNone(p.Network), valueOrNone(p.PayTo)),
				fmt.Sprintf("    asset: %s", formatOfferAsset(p.Asset)),
			)
		}
	}
	lines = append(lines,
		fmt.Sprintf("Agent ID:        %s", agentID),
		fmt.Sprintf("Registration Tx: %s", tx),
		"",
		"Conditions:",
	)
	for _, cond := range offer.Status.Conditions {
		lines = append(lines, formatConditionLine(cond))
	}
	if len(offer.Status.Conditions) > 0 {
		lines = append(lines, "",
			"Note: conditions are the controller's last reconciled snapshot — they update on",
			"state changes, not per paid request. The chain is the canonical revenue record.")
	}
	return lines
}

// effectivePaymentAsset fills a payment's Asset block from the chain's
// default settlement asset (USDC) when the offer left it implicit — i.e.
// the seller passed --token USDC (or nothing) and never wrote an explicit
// asset. This mirrors what the 402 wire and storefront catalog advertise,
// so `obol sell status` shows "USDC (0x833…)" / "3 USDC per request" instead
// of "(not set)" / "3 per request". Offers that already carry an explicit
// asset (e.g. OBOL) are returned unchanged.
func effectivePaymentAsset(p monetizeapi.ServiceOfferPayment) monetizeapi.ServiceOfferPayment {
	if p.Asset.Symbol != "" || p.Asset.Address != "" {
		return p
	}
	chain, err := x402verifier.ResolveChainInfo(p.Network)
	if err != nil {
		return p
	}
	def := chain.DefaultAsset()
	p.Asset = monetizeapi.ServiceOfferAsset{
		Address:        def.Address,
		Symbol:         def.Symbol,
		Decimals:       int64(def.Decimals),
		TransferMethod: def.TransferMethod,
		EIP712Name:     def.EIP712Name,
		EIP712Version:  def.EIP712Version,
	}
	return p
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
	if !cond.LastTransitionTime.IsZero() {
		header = header + fmt.Sprintf(" (changed %s)", humanAge(cond.LastTransitionTime.Time))
	}
	return fmt.Sprintf("  %s %s", icon, header)
}

// humanAge renders how long ago t was in the coarsest useful unit. Used
// on condition lines so sellers can tell a fresh reconcile from a status
// the controller last touched hours ago.
func humanAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
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
	DefaultChain string // default --network when not explicitly set
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
		Description:  "Pay this agent to research a question involving Ethereum or Base.",
		NeedsERPC:    true,
		DefaultChain: "ethereum",
		DefaultToken: "OBOL",
		Agent: &demoAgentSpec{
			Skills: []string{
				"ethereum-networks",
				"ethereum-local-wallet",
				"ethereum-reference",
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
  obol sell demo hello --token USDC --network base --price 0.001`,
		Flags: []cli.Flag{
			payToFlag("Token recipient address"),
			&cli.StringFlag{
				Name:    "network",
				Aliases: []string{"chain"},
				Usage:   "Payment network (defaults to demo type's default chain)",
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
				Usage: "Auto-register the demo on the ERC-8004 Agent Registry. Default: skip (avoid double-register reverts and ETH-for-gas requirement; run 'obol sell register' later if you want on-chain discovery).",
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
			// for non-USDC tokens when --network wasn't explicitly set.
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
			// to hold ETH for gas. Operators run `obol sell register --network ...`
			// when they actually want on-chain discovery.
			register := cmd.Bool("register")
			soManifest := buildDemoServiceOffer(name, demoNamespace, chain, wallet, price, register, spec, assetTerms)
			kubectl.SetManagedBy(soManifest)
			applyOut, err := kubectlApplyOutput(cfg, soManifest)
			if err != nil {
				return fmt.Errorf("apply ServiceOffer: %w", err)
			}
			// Persist the whole demo bundle (namespace + backend Deployment
			// + Service + ServiceOffer) as one v1 List so `obol sell
			// resume` restores a working demo, not an offer with a missing
			// upstream. kubectl applies List manifests natively; the List
			// metadata only keys the ledger file.
			items := make([]any, 0, 4)
			for _, res := range buildDemoResources(cfg, name, spec, chain) {
				items = append(items, res)
			}
			items = append(items, soManifest)
			bundle := map[string]any{
				"apiVersion": "v1",
				"kind":       "List",
				"metadata":   map[string]any{"name": name, "namespace": demoNamespace},
				"items":      items,
			}
			if persistErr := persistServiceOffer(cfg, demoNamespace, name, bundle); persistErr != nil {
				u.Warnf("could not persist demo offer for resume: %v", persistErr)
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
				u.Dim("  Run on-chain discovery later: obol sell register --network " + chain)
			}

			// 6. Print try-it instructions.
			u.Blank()
			printDemoTryIt(u, name, typeName, price, symbol, chain, tunnelURL, ready)
			printOfferLinks(cfg, u, demoNamespace, name, action == "created")

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
		u.Dim("  You can run it manually later: obol sell register --network " + chain)
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
	succeeded, metadataFailures := registerAgentOnNetworks(ctx, cfg, u, agentURI, signerNS, []erc8004.NetworkConfig{net})
	if len(succeeded) == 0 {
		u.Warn("Auto-register did not succeed.")
		u.Dim("  Retry with: obol sell register --network " + chain)
		return
	}
	u.Successf("Agent registered on %s.", net.Name)
	if metadataFailures > 0 {
		u.Warnf("x402 metadata missing; mint + agentURI are registered — re-run 'obol sell register --network %s' to retry the metadata write.", chain)
	}
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
	timeoutLabel := fmt.Sprintf("%ds", int(timeout.Round(time.Second).Seconds()))
	if timeout >= time.Minute && timeout%time.Minute == 0 {
		timeoutLabel = fmt.Sprintf("%dm", int(timeout.Minutes()))
	}
	_ = u.RunWithSpinner("Waiting for service to be Ready (up to "+timeoutLabel+")", func() error {
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
	resources := buildDemoResources(cfg, name, spec, paymentChain)

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
func buildDemoResources(cfg *config.Config, name string, spec demoSpec, paymentChain string) []map[string]any {
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
								"image":           stackdefaults.ResolveWorkloadImage(cfg, "ghcr.io/obolnetwork/demo-server"),
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
			// Demo services are an ordinary storefront category now, not a
			// special-cased flag — the catalog/storefront group on this.
			"listing": map[string]any{
				"category": "demo",
			},
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
				Usage: "Traefik host:port (default: this stack's local ingress, e.g. obol.stack or obol.stack:8080)",
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
			host := strings.TrimSpace(cmd.String("host"))
			if host == "" {
				host = strings.TrimPrefix(stack.LocalIngressURL(cfg), "http://")
			}
			probeURL := "http://" + host + endpoint + subpath
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
		Usage:     "Stop selling a service without deleting it",
		ArgsUsage: "<name>",
		Description: `Marks a ServiceOffer as draining. While draining:
  - The offer stays in /skill.md and /.well-known/agent-registration.json
    with available=false and a drainEndsAt timestamp, so external
    discovery (and ERC-8004 reputation scorers) can see the wind-down.
  - The HTTPRoute and x402 payment gate STAY UP for the grace period
    so buyers can complete in-flight payments.
  - When the grace period elapses, the controller tears down the route
    and marks PaymentGateReady/RoutePublished False with reason=Drained.

The ServiceOffer CR itself is preserved — use 'obol sell delete' to
remove it entirely (which also tombstones the ERC-8004 record).

Use --grace to shorten or extend the drain window, or --force to tear the
route down immediately (equivalent to --grace 0).`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "namespace",
				Aliases:  []string{"n"},
				Usage:    "Namespace of the ServiceOffer",
				Required: true,
			},
			&cli.DurationFlag{
				Name:  "grace",
				Usage: "Drain grace period (e.g. 30m, 2h). Defaults to 1h.",
				Value: monetizeapi.DefaultDrainGracePeriod,
			},
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"now"},
				Usage:   "Skip the drain window and tear the route down on the next reconcile (alias: --now)",
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
			grace := cmd.Duration("grace")
			if cmd.Bool("force") {
				grace = 0
			}
			if grace < 0 {
				return errors.New("--grace must be >= 0")
			}

			now := time.Now().UTC()
			drainEndsAt := now.Add(grace)

			// metav1.Duration JSON-marshals as the string form (e.g.
			// "1h0m0s"), and metav1.Time marshals as RFC3339. We can
			// emit a tiny strategic-merge patch directly without
			// importing the meta types into the CLI.
			patchJSON := fmt.Sprintf(
				`{"spec":{"drainAt":%q,"drainGracePeriod":%q}}`,
				now.Format(time.RFC3339),
				grace.String(),
			)
			if err := kubectlRun(cfg, "patch", "serviceoffers.obol.org", name, "-n", ns,
				"--type=merge", "-p", patchJSON); err != nil {
				return fmt.Errorf("failed to drain serviceoffer: %w", err)
			}

			// Refresh the resume ledger so an etcd-wiping stack-down/up
			// replays the offer in its drained state (an elapsed drainAt
			// reconciles straight to torn-down) instead of resurrecting a
			// deliberately stopped offer fully live. Reboot-resume was
			// already safe — client-side apply leaves drainAt alone because
			// it never owned the field — but the ledger replay IS the
			// last-applied state after a recreation.
			if err := refreshPersistedServiceOffer(cfg, ns, name); err != nil {
				u.Warnf("could not refresh persisted offer for resume: %v", err)
			}

			if grace == 0 {
				u.Successf("ServiceOffer %s/%s draining; route will be removed on the next reconcile (--force).", ns, name)
			} else {
				u.Successf("ServiceOffer %s/%s draining; route will be removed at %s.", ns, name, drainEndsAt.Format(time.RFC3339))
			}
			u.Infof("In-flight buyers can complete payments until then. Run `obol sell delete %s -n %s` to fully remove.", name, ns)
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
		Usage:     "Update pricing, wallet or served model on an existing ServiceOffer in place",
		ArgsUsage: "<name>",
		// See sell http: keep one --accept value intact (no cli/v3 "," split)
		// so multi-currency updates parse. parseAcceptKV splits internally.
		DisableSliceFlagSeparator: true,
		Description: `Patches a live ServiceOffer without deleting it. Only the fields you pass
are changed; everything else is preserved. The serviceoffer-controller will
reconcile the new payment config automatically.

Switching price models (e.g. per-request → per-mtok) nulls the previous keys
so the controller picks up the new model.

Examples:
  obol sell update my-api -n llm --per-request 0.002
  obol sell update my-api -n llm --per-mtok 5.0
  obol sell update my-api -n llm --wallet 0xNew... --network base
  obol sell update my-llm -n llm --model qwen3.5:27b   # LiteLLM-backed offers only
  obol sell update my-api -n llm --accept token=USDC,network=base,price=1 --accept token=OBOL,network=ethereum,price=10`,
		Flags: append([]cli.Flag{
			&cli.StringFlag{
				Name:     "namespace",
				Aliases:  []string{"n"},
				Usage:    "Namespace of the ServiceOffer",
				Required: true,
			},
			payToFlag("New payment recipient address"),
			&cli.StringFlag{
				Name:    "network",
				Aliases: []string{"chain"},
				Usage:   "New payment network (base, base-sepolia, ethereum)",
			},
			&cli.StringFlag{
				Name:  "price",
				Usage: "New per-request price in the selected payment token (alias for --per-request)",
			},
			&cli.StringFlag{
				Name:  "per-request",
				Usage: "New per-request price in the selected payment token",
			},
			&cli.StringFlag{
				Name:  "per-mtok",
				Usage: "New per-million-tokens price in the selected payment token",
			},
			&cli.StringFlag{
				Name:  "per-hour",
				Usage: "New per-compute-hour price in the selected payment token",
			},
			&cli.StringFlag{
				Name:  "model",
				Usage: "New LiteLLM model_name to serve (offers on the litellm upstream only); buyers' model field is overwritten with it",
			},
		}, acceptFlags()...),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if cmd.NArg() == 0 {
				return errors.New("name required: obol sell update <name> -n <ns> [--per-request N | --per-mtok N | --per-hour N | --accept ...] [--pay-to 0x...] [--network base]")
			}

			name := cmd.Args().First()
			if err := validate.Name(name); err != nil {
				return err
			}
			ns := cmd.String("namespace")

			offerJSON, err := kubectlOutput(cfg, "get", "serviceoffers.obol.org", name, "-n", ns, "-o", "json")
			if err != nil {
				return fmt.Errorf("ServiceOffer %s/%s not found: %w", ns, name, err)
			}

			var modelPatch map[string]any
			if cmd.IsSet("model") {
				var offer monetizeapi.ServiceOffer
				if err := json.Unmarshal([]byte(offerJSON), &offer); err != nil {
					return fmt.Errorf("parse ServiceOffer %s/%s: %w", ns, name, err)
				}
				configured, listErr := configuredLiteLLMModels(cfg, u)
				if modelPatch, err = sellUpdateModelPatch(&offer, cmd.String("model"), configured, listErr); err != nil {
					return err
				}
			}

			wallet := strings.TrimSpace(cmd.String("pay-to"))
			if wallet != "" {
				if err := x402verifier.ValidateWallet(wallet); err != nil {
					return err
				}
			}

			var patch map[string]any
			if accepts := cmd.StringSlice("accept"); len(accepts) > 0 {
				// --accept REPLACES the whole payment set (predictable: no
				// partial merge across options). spec.payments is a list, so a
				// merge patch swaps it wholesale; spec.payment is reset to the
				// new primary (asset cleared when the new primary is USDC).
				payments, err := buildAcceptPayments(accepts, wallet)
				if err != nil {
					return err
				}
				if err := autofillAcceptPayments(ctx, payments, func(ctx context.Context, network, addr string) (tokenMeta, error) {
					return fetchTokenMeta(ctx, cfg, network, addr)
				}); err != nil {
					return err
				}
				patch = buildSellAcceptUpdatePatch(payments)
			} else {
				var price schemas.PriceTable
				if cmd.String("price") != "" || cmd.String("per-request") != "" || cmd.String("per-mtok") != "" || cmd.String("per-hour") != "" {
					resolved, err := resolvePriceTable(cmd, true)
					if err != nil {
						return err
					}
					price = resolved
				}
				p, err := buildSellUpdatePatch(wallet, cmd.String("chain"), price)
				// Allow a listing-only update (weight/category) with no payment
				// change: swallow the "nothing to update" error when listing
				// flags are present, and start from an empty spec patch.
				if err != nil {
					if cmd.Int("weight") != 0 || strings.TrimSpace(cmd.String("category")) != "" || modelPatch != nil {
						p = map[string]any{"spec": map[string]any{}}
					} else {
						return fmt.Errorf("%w / --model", err)
					}
				}
				patch = p
			}

			// Listing flags (weight/category) and --model layer onto whichever patch we built.
			if spec, ok := patch["spec"].(map[string]any); ok {
				applyListingFlags(cmd, spec)
				if modelPatch != nil {
					spec["model"] = modelPatch
				}
			}
			patchBytes, err := json.Marshal(patch)
			if err != nil {
				return fmt.Errorf("marshal patch: %w", err)
			}

			if err := kubectlRun(cfg, "patch", "serviceoffers.obol.org", name, "-n", ns, "--type=merge", "-p", string(patchBytes)); err != nil {
				return fmt.Errorf("failed to patch serviceoffer: %w", err)
			}

			// Refresh the resume ledger from the live (post-patch) object.
			// Without this, the persisted manifest keeps the OLD payment
			// terms and the next `obol sell resume` / `obol stack up`
			// kubectl-applies them back — silently reverting a payTo or
			// price change the operator made on purpose.
			if err := refreshPersistedServiceOffer(cfg, ns, name); err != nil {
				u.Warnf("could not refresh persisted offer for resume: %v", err)
			}

			u.Successf("ServiceOffer %s/%s updated", ns, name)
			if modelPatch != nil {
				u.Infof("Buyers are now served model %s.", modelPatch["name"])
			}
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
		Usage:     "Stop selling a service and remove it (also deactivates any registrations)",
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
					"Delete ServiceOffer %s/%s? This will:\n  - Remove the associated Middleware and HTTPRoute\n  - Remove x402 enforcement for the service\n  - Let the serviceoffer-controller finalizer clean up offer-scoped state\n  - Leave the AgentIdentity record and on-chain NFT intact (the controller renders a tombstone if no active offers remain)",
					ns,
					name,
				)

				ok, err := u.ConfirmOrFlag(msg, "--force")
				if err != nil {
					return err
				}

				if !ok {
					u.Info("Aborted.")
					return nil
				}
			}

			// Identity-level registration ownership lives in the AgentIdentity
			// CR and is managed by the controller. The CLI no longer patches
			// the registration ConfigMap here; deleting the ServiceOffer is
			// enough; if this was the last active offer for the identity, the
			// controller renders an active:false / x402Support:false tombstone
			// document while keeping the agentId.

			// A legacy cluster-routed sell-inference descriptor may outlive
			// its ServiceOffer (e.g. after a cluster recreation). Deleting it
			// must still succeed so the descriptor gets tombstoned and resume
			// stops warning about it.
			legacy := legacyInferenceDescriptor(cfg, name, ns)
			deleteArgs := []string{"delete", "serviceoffers.obol.org", name, "-n", ns}
			if legacy != nil {
				deleteArgs = append(deleteArgs, "--ignore-not-found")
			}
			if err := kubectlRun(cfg, deleteArgs...); err != nil {
				return err
			}

			// Drop the offer's manifest from the resume ledger so the next
			// `obol stack up` / `obol sell resume` doesn't replay an offer
			// the operator just deleted. Covers every ledger-persisted type
			// (http, agent, demo bundles).
			if removeErr := removePersistedServiceOffer(cfg, ns, name); removeErr != nil {
				u.Warnf("could not remove persisted sell offer manifest at %s/%s: %v", ns, name, removeErr)
			}

			// Tombstone a legacy cluster-routed sell-inference descriptor:
			// it stays on disk for `sell status` history, and resume skips
			// it silently instead of warning on every stack up.
			if legacy != nil {
				legacy.DeletedAt = time.Now().UTC().Format(time.RFC3339)
				if updErr := inference.NewStore(cfg.ConfigDir).Update(legacy); updErr != nil {
					u.Warnf("could not tombstone inference descriptor for %s: %v", name, updErr)
				}
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
			payToFlag("Payment recipient address"),
			&cli.StringFlag{
				Name:    "network",
				Aliases: []string{"chain"},
				Usage:   "Payment network (base, base-sepolia, ethereum)",
				Value:   "base",
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
		Usage: "Publish the seller's identity to a distribution channel (ERC-8004, x402scan)",
		Description: `Publishes the seller's agent identity to a distribution channel so buyers can
discover it. The default (no subcommand) registers on the ERC-8004 Agent
Registry (on-chain); 'register x402scan' submits the storefront origin to the
x402scan.com discovery index instead.

Registers an AgentIdentity on the ERC-8004 Agent Registry for one chain.
The on-chain register tx is signed and broadcast by the Hermes remote-signer
and pays gas from the agent's wallet — make sure it has a small balance on
the target chain (~$0.20–$0.50 of native gas typically suffices).

Examples:
  obol sell register                                    # defaults to mainnet
  obol sell register --network base                       # register on base
  obol sell register --network base-sepolia               # add a Base Sepolia registration
  obol sell register x402scan                             # list in the x402scan index (no gas)`,
		Commands: []*cli.Command{
			sellRegisterX402scanCommand(cfg),
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "network",
				Aliases: []string{"chain"},
				Usage:   "Registration network (mainnet, base, base-sepolia)",
				Value:   "mainnet",
			},
			&cli.StringFlag{
				Name:    "origin",
				Aliases: []string{"endpoint"},
				Usage:   "Storefront origin (scheme://host[:port]) — agent-registration.json is always served at its root, never a service path (auto-detected from tunnel if not set)",
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
				Name:   "sponsored",
				Hidden: true, // disabled; kept so old invocations get an explanatory error
				Usage:  "(disabled) Sponsored zero-gas registration is currently unavailable. Re-run without --sponsored to register with the agent's own wallet (ETH for transaction fees required on the network).",
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

			// Resolve origin.
			origin := cmd.String("origin")
			if origin == "" {
				tunnelURL, err := tunnel.GetTunnelURL(cfg)
				if err != nil {
					if u.IsTTY() {
						origin, _ = u.Input("Storefront origin", "")
					}
					if origin == "" {
						return fmt.Errorf("--origin required (tunnel auto-detect failed: %v)", err)
					}
				} else {
					origin = tunnelURL
					u.Infof("Auto-detected origin from tunnel: %s", origin)
				}
			}
			origin, err = normalizeAgentOrigin(origin)
			if err != nil {
				return err
			}
			agentURI := origin + "/.well-known/agent-registration.json"

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

			succeeded, metadataFailures := registerAgentOnNetworks(ctx, cfg, u, agentURI, signerNS, networks)
			if len(succeeded) == 0 {
				return fmt.Errorf("registration failed on all networks")
			}

			// Offers created with --no-register stay registration.enabled=false
			// and never flip to Registered/Active even after a successful CLI
			// register. Enable them so the controller attaches them to the
			// shared AgentIdentity and discovery surfaces services. Scoped to
			// the networks that just succeeded so a register on one chain
			// doesn't flip on offers pinned to a different, unregistered chain.
			if n, err := enableRegistrationOnOffers(cfg, u, succeeded); err != nil {
				u.Warnf("could not enable registration on existing ServiceOffers: %v", err)
				u.Dim("  Manually set registration.enabled=true on offers created with --no-register.")
			} else if n > 0 {
				u.Infof("Enabled registration on %d ServiceOffer(s) previously created with --no-register", n)
			}

			u.Blank()
			u.Successf("Agent registered on %d/%d networks.", len(succeeded), len(networks))
			if metadataFailures > 0 {
				u.Warnf("x402 metadata missing on %d/%d network(s); mint + agentURI are registered — see warnings above, re-run 'obol sell register' to retry the metadata write.", metadataFailures, len(succeeded))
			}
			return nil
		},
	}
}

// defaultHealthPathForUpstream fills in a working health-check path for
// upstreams whose generic "/health" default (the --health-path flag's zero
// value) never returns 2xx, which would permanently fail UpstreamHealthy's
// 2xx-only probe gate. Returns healthPath unchanged for any explicit,
// non-default value or an upstream with no special case.
func defaultHealthPathForUpstream(upstreamSvc, healthPath string) string {
	if healthPath != "" && healthPath != "/health" {
		return healthPath
	}
	switch {
	case strings.EqualFold(upstreamSvc, "litellm"):
		// LiteLLM's /health requires the master key (401 unauthenticated).
		// Its readiness/liveliness probes are public and return 2xx.
		return "/health/readiness"
	case strings.EqualFold(upstreamSvc, "ollama"):
		// Ollama serves neither /health nor /health/readiness — only "/"
		// returns 2xx.
		return "/"
	default:
		return healthPath
	}
}

// normalizeAgentOrigin validates that raw is a bare storefront origin
// (scheme://host[:port], no path) and returns it with any trailing slash
// stripped. The controller only ever publishes agent-registration.json at
// the storefront origin root (never a per-offer service path — see
// serviceoffercontroller's PublishedURL/HTTPRoute construction), so a
// path-bearing value would mint an on-chain agentURI that resolves to a
// route the controller never serves. Reject rather than silently truncate:
// silent truncation is exactly what produces a broken, payment-gated
// metadata URI in the field.
func normalizeAgentOrigin(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("--origin must be a bare host with no path (got %q): expected scheme://host[:port]", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("--origin must use http or https (got %q): expected scheme://host[:port]", raw)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("--origin must be a bare host with no path (got %q); agent-registration.json is served at the storefront root, not a service path", raw)
	}
	return strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/"), nil
}

// autoRegisterOptOutAnnotation lets an offer opt out of
// enableRegistrationOnOffers' cluster-wide sweep permanently — e.g. an
// operator who deliberately created an offer with --no-register and wants it
// to stay unlisted even after a later `obol sell register` succeeds on its
// network. Not set by --no-register itself (that would defeat the sweep for
// every ordinary --no-register offer); it's an explicit escape hatch.
const autoRegisterOptOutAnnotation = "obol.stack/no-auto-register"

// offerEligibleForAutoEnable reports whether an offer should have
// registration.enabled flipped on: it must still be disabled, pinned to one
// of the networks the triggering `obol sell register` call actually
// succeeded on (registeredNetworks, keyed by erc8004.NetworkConfig.Name),
// and not carrying the autoRegisterOptOutAnnotation opt-out.
func offerEligibleForAutoEnable(o monetizeapi.ServiceOffer, registeredNetworks map[string]bool) bool {
	if o.Annotations[autoRegisterOptOutAnnotation] == "true" {
		return false
	}
	return !o.Spec.Registration.Enabled && registeredNetworks[o.Spec.Payment.Network]
}

// enableRegistrationOnOffers patches every ServiceOffer on one of the given
// networks that still has registration.enabled=false to true, so post-hoc
// `obol sell register` activates existing offers instead of leaving them
// Disabled while the AgentIdentity is already on-chain. Scoped to
// networks — a register call for one chain must not enable offers pinned to
// a different chain that was never registered.
func enableRegistrationOnOffers(cfg *config.Config, u *ui.UI, networks []erc8004.NetworkConfig) (int, error) {
	offers, err := listServiceOffers(cfg)
	if err != nil {
		return 0, err
	}
	registered := make(map[string]bool, len(networks))
	for _, n := range networks {
		registered[n.Name] = true
	}
	enabled := 0
	for _, o := range offers {
		if !offerEligibleForAutoEnable(o, registered) {
			continue
		}
		// Merge-patch only the enabled flag; leave name/description/skills alone.
		patch := `{"spec":{"registration":{"enabled":true}}}`
		if _, err := kubectlOutput(cfg, "patch", "serviceoffer.obol.org", o.Name,
			"-n", o.Namespace, "--type", "merge", "-p", patch); err != nil {
			u.Warnf("  %s/%s: %v", o.Namespace, o.Name, err)
			continue
		}
		u.Dim(fmt.Sprintf("  %s/%s: registration.enabled → true", o.Namespace, o.Name))
		enabled++
	}
	return enabled, nil
}

// registerAgentOnNetworks runs the per-network ERC-8004 registration loop used
// by both `obol sell register` and the auto-register step of `obol sell demo`.
// Each registration is signed by the Hermes remote-signer and pays gas from
// the agent's wallet. Returns the networks that registered successfully (so
// callers can scope follow-up actions to exactly those networks) and a count
// of networks where the mint/URI stage succeeded but the trailing x402
// metadata write failed.
func registerAgentOnNetworks(ctx context.Context, cfg *config.Config, u *ui.UI, agentURI, signerNS string, networks []erc8004.NetworkConfig) (succeeded []erc8004.NetworkConfig, metadataFailures int) {
	for _, net := range networks {
		u.Blank()
		u.Printf("  [%s] (chain ID %d)", net.Name, net.ChainID)
		u.Printf("    Registry: %s", net.RegistryAddress)

		metadataOK, err := registerDirectViaSigner(ctx, cfg, u, net, agentURI, signerNS)
		if err != nil {
			u.Warnf("registration failed: %v", err)
			continue
		}
		if !metadataOK {
			metadataFailures++
		}

		u.Printf("    CAIP-10:  %s", net.CAIP10Registry())
		succeeded = append(succeeded, net)
	}
	return succeeded, metadataFailures
}

// registerDirectViaSigner performs an idempotent ERC-8004 registration via
// the remote-signer. It reads the selected AgentIdentity first and branches:
//   - identity.status.registrations has this chain -> verify signer ownership,
//     then setAgentURI(uri) only when the on-chain tokenURI differs.
//   - no registration exists for this chain -> recover by (owner, agentURI)
//     on-chain; mint via register(agentURI) only if recovery returns no match.
//
// The AgentIdentity CR persists only durable ERC-8004 identity keys:
// status.registrations[] entries keyed by chain.
//
// Returns metadataOK=false (with a nil error) when the mint/URI-update stage
// succeeded but the trailing x402 metadata write failed — that's reported as
// a distinct, non-fatal stage by the caller rather than swallowed.
func registerDirectViaSigner(ctx context.Context, cfg *config.Config, u *ui.UI, net erc8004.NetworkConfig, agentURI, namespace string) (metadataOK bool, err error) {
	u.Printf("    Using direct on-chain registration via remote-signer...")

	pf, err := startSignerPortForward(cfg, namespace)
	if err != nil {
		return true, fmt.Errorf("port-forward to remote-signer: %w", err)
	}
	defer pf.Stop()

	signer := erc8004.NewRemoteSigner(fmt.Sprintf("http://localhost:%d", pf.localPort))
	addr, err := signer.GetAddress(ctx)
	if err != nil {
		return true, err
	}
	u.Printf("    Wallet:   %s", addr.Hex())

	rpcBaseURL := stack.LocalIngressURL(cfg) + "/rpc"
	client, err := erc8004.NewClientForNetwork(ctx, rpcBaseURL, net)
	if err != nil {
		return true, fmt.Errorf("connect to %s via eRPC: %w", net.Name, err)
	}
	defer client.Close()

	opts := signer.RemoteTransactOpts(ctx, addr, client.ChainID())

	// Pin the nonce locally for the whole call instead of letting each
	// Transact re-query PendingNonceAt: this function sends up to two
	// sequential txs (e.g. setAgentURI then setMetadata), and the eRPC
	// gateway fans requests out to independent upstreams per call, so a
	// nonce read immediately following a just-broadcast tx can hit a
	// lagging upstream and return a stale (too-low) nonce. bumpNonce below
	// advances the pinned value after each successful send.
	nonce, err := client.PendingNonceAt(ctx, addr)
	if err != nil {
		return true, fmt.Errorf("read pending nonce for %s on %s: %w", addr.Hex(), net.Name, err)
	}
	opts.Nonce = new(big.Int).SetUint64(nonce)
	bumpNonce := func() { opts.Nonce = new(big.Int).Add(opts.Nonce, big.NewInt(1)) }

	identity, err := ensureAgentIdentity(cfg, monetizeapi.AgentIdentityDefaultNamespace, monetizeapi.AgentIdentityDefaultName, monetizeapi.AgentIdentitySpec{})
	if err != nil {
		return true, fmt.Errorf("load AgentIdentity: %w", err)
	}
	existingAgentID := monetizeapi.AgentIdentityAgentIDForChain(identity.Status, net.Name)

	// Idempotent path: this chain already has an on-chain registration.
	if existingAgentID != "" {
		agentID, ok := new(big.Int).SetString(existingAgentID, 10)
		if !ok {
			return true, fmt.Errorf("AgentIdentity %s/%s has malformed agentId %q for chain %s",
				identity.Metadata.Namespace, identity.Metadata.Name, existingAgentID, net.Name)
		}
		owner, walletErr := client.AgentWallet(ctx, agentID)
		if walletErr != nil {
			return true, fmt.Errorf("verify agent %s on %s: %w", agentID, net.Name, walletErr)
		}
		if owner != addr {
			return true, fmt.Errorf("signer %s does not control AgentIdentity agent %s (on-chain owner: %s)", addr.Hex(), agentID, owner.Hex())
		}

		u.Printf("    Agent ID: %s (existing)", agentID.String())
		currentURI, err := client.TokenURI(ctx, agentID)
		if err != nil {
			return true, fmt.Errorf("read tokenURI(%s): %w", agentID, err)
		}
		if currentURI == agentURI {
			u.Printf("    URI:      unchanged, skipping setAgentURI")
		} else {
			u.Printf("    Updating agentURI via setAgentURI...")
			uriTx, err := client.SetAgentURIWithOpts(ctx, opts, agentID, agentURI)
			if err != nil {
				return true, fmt.Errorf("setAgentURI: %w", err)
			}
			u.Printf("    Tx:       %s", uriTx)
			bumpNonce()
			// No WaitForAgent here: the agent already existed before this
			// branch (AgentWallet above already confirmed the reader sees
			// it), so there's no token-visibility race to close — only the
			// nonce race, which the pinning above already handles.
		}
		// Refresh x402 metadata to keep parity with first-mint behavior.
		if err := client.SetMetadataWithOpts(ctx, opts, agentID, "x402", []byte(`{"x402":true}`)); err != nil {
			u.Warnf("failed to set x402 metadata: %v", err)
			return false, nil
		}
		return true, nil
	}

	// No recorded agentId: try owner+URI recovery from genesis before
	// minting so reruns don't double-mint a registration that already
	// exists on-chain.
	if recoveredID, _, ok := recoverRegistrationByOwnerAndURI(ctx, client, addr, agentURI, 0); ok {
		u.Printf("    Agent ID: %s (recovered via owner+URI)", recoveredID.String())
		identity.Status = monetizeapi.UpsertAgentIdentityRegistration(identity.Status, net.Name, recoveredID.String())
		if err := applyAgentIdentity(cfg, identity); err != nil {
			return true, fmt.Errorf("persist recovered AgentIdentity registration %s on %s: %w", recoveredID, net.Name, err)
		}
		_, _ = client.WaitForAgent(ctx, recoveredID, 30*time.Second)
		if err := client.SetMetadataWithOpts(ctx, opts, recoveredID, "x402", []byte(`{"x402":true}`)); err != nil {
			u.Warnf("failed to set x402 metadata: %v", err)
			return false, nil
		}
		return true, nil
	}

	// Fresh mint.
	startBlock := registrationRecoveryStartBlock(ctx, client, u)
	agentID, txHash, err := registerWithRecovery(ctx, u, client, agentURI, addr, startBlock, func() (*big.Int, string, error) {
		return client.RegisterWithOptsDetailed(ctx, opts, agentURI)
	})
	if err != nil {
		return true, err
	}
	bumpNonce()

	u.Printf("    Agent ID: %s", agentID.String())
	u.Printf("    Owner:    %s", addr.Hex())
	if txHash != "" {
		u.Printf("    Tx:       %s", txHash)
	}

	if _, err := client.WaitForAgent(ctx, agentID, 30*time.Second); err != nil {
		u.Warnf("agent not visible to reader after register: %v", err)
	}

	if err := client.SetMetadataWithOpts(ctx, opts, agentID, "x402", []byte(`{"x402":true}`)); err != nil {
		u.Warnf("failed to set x402 metadata: %v", err)
		metadataOK = false
	} else {
		metadataOK = true
	}

	identity.Status = monetizeapi.UpsertAgentIdentityRegistration(identity.Status, net.Name, agentID.String())
	if err := applyAgentIdentity(cfg, identity); err != nil {
		return metadataOK, fmt.Errorf("persist AgentIdentity registration %s on %s: %w\n\n  The on-chain registration succeeded; recover with `obol sell identity import --network %s --agent-id %s`.", agentID, net.Name, err, net.Name, agentID)
	}
	return metadataOK, nil
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
		// registerWithRecovery pins the nonce for the whole call and reuses
		// it across retries, so a broadcast-timeout retry can resubmit the
		// exact tx the node already has: these three responses mean no new
		// tx was accepted (no double-mint risk), and retrying gives
		// recoverRegistrationByOwnerAndURI more time to see the original
		// broadcast land instead of reporting a landed mint as failed.
		"already known",
		"nonce too low",
		"replacement transaction underpriced",
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

	kubectlBinary := cfg.ToolPath("kubectl")

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

// confirmOfferReplace guards against silently overwriting an existing
// ServiceOffer. `obol sell agent <name>` and `obol sell demo <name>` can
// resolve to the same offer name+namespace (one offer per agent path), so a
// second create would `kubectl apply` over the first with no warning. When an
// offer already exists and we're on an interactive terminal, prompt before
// replacing; the caller aborts on a "no". Non-interactive callers (resume,
// flows, JSON) keep the idempotent apply behaviour and never block.
//
// Returns true to proceed with the apply, false to abort.
func confirmOfferReplace(cfg *config.Config, u *ui.UI, namespace, name string) bool {
	if !u.IsTTY() || u.IsJSON() {
		return true
	}
	out, err := kubectlOutput(cfg, "get", "serviceoffer", name, "-n", namespace, "--ignore-not-found", "-o", "name")
	if err != nil || strings.TrimSpace(out) == "" {
		// Not found (or lookup failed) — nothing to replace; proceed.
		return true
	}
	u.Warnf("A ServiceOffer named %q already exists in namespace %s.", name, namespace)
	u.Dim("  Continuing will replace its payment terms, path, and registration with the new ones.")
	return u.Confirm(fmt.Sprintf("Replace ServiceOffer %s/%s?", namespace, name), false)
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
		if err := validate.Price(perRequest); err != nil {
			return schemas.PriceTable{}, fmt.Errorf("invalid --price/--per-request value %q: %w", perRequest, err)
		}

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

// ---------------------------------------------------------------------------
// sell resume — re-apply persisted offers
// ---------------------------------------------------------------------------

func sellResumeCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "resume",
		Usage: "Re-apply persisted sell offers (run after a host reboot)",
		Description: `Replays every locally-persisted sell offer against the cluster —
http (including LiteLLM-backed inference), agent, and demo-created agent
offers — so one command brings the whole catalog back.

'obol stack up' already runs this resume path; this command exposes it
directly so a reboot does not require a full stack-up to recover.

'obol sell inference' gateways are standalone foreground processes and are
not resumed. Offers created by older releases that routed 'sell inference'
through the cluster are skipped with a warning that shows how to re-create
them via LiteLLM ('obol model setup custom' + 'obol sell http --upstream litellm').

Idempotent: the kubectl applies re-assert existing objects. Runs the same ordered record
replay as 'obol stack up' (models, missing networks, RPC upstreams, x402
pricing, AgentIdentity, missing agent instances, Agent CRs, storefront,
apps, then offers), so agent-backed offers resolve their agent.ref even
after a full stack recreation.
'sell mcp' servers are foreground processes with no ServiceOffer and are
not resumed.

Examples:
  obol sell resume                      Resume all persisted offers now
  obol sell resume --install-boot-unit  Also run automatically at boot (Linux/systemd)`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "install-boot-unit",
				Usage: "Install + enable a systemd user unit that runs 'obol sell resume' at boot (Linux only)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			// At boot the k3d API server lags Docker by a minute or
			// more, and the per-offer kubectl applies warn-and-continue
			// on failure — without this wait a too-early resume silently
			// resumes nothing and nothing retries.
			if err := waitForClusterAPI(ctx, cfg, u, 3*time.Minute); err != nil {
				u.Warnf("cluster API not ready: %v (continuing — per-offer applies may fail)", err)
			}
			warnOnClockSkew(ctx, u, "")
			// Same ordered record replay as `obol stack up`
			// (internal/replay.Order): Agent CRs, apps, identity and
			// pricing all precede the offers that depend on them. Only
			// a failed offer replay fails the command (as before).
			sum := replay.ReplayRecorded(ctx, cfg, u, replayOptions())
			if err := sum.Errors[replay.StepSellOffers]; err != nil {
				return err
			}
			if cmd.Bool("install-boot-unit") {
				return installResumeBootUnit(cfg, u)
			}
			return nil
		},
	}
}

// replayOptions wires the package-main replay steps into internal/replay.
// Shared by `obol stack up` and `obol sell resume` so both run one list.
func replayOptions() replay.Options {
	return replay.Options{ResumeSellOffers: resumeSellOffers}
}

// waitForClusterAPI blocks until the cluster API server answers a
// /readyz probe, or the timeout elapses. A missing kubeconfig returns
// nil immediately — no cluster means nothing to wait for and the resume
// loop already degrades gracefully. Used by `obol sell resume` because
// its main caller is a boot unit racing the k3d node container's
// startup; `obol stack up` doesn't need it (cluster creation blocks).
func waitForClusterAPI(ctx context.Context, cfg *config.Config, u *ui.UI, timeout time.Duration) error {
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); err != nil {
		return nil
	}
	bin, kc := kubectl.Paths(cfg)
	deadline := time.Now().Add(timeout)
	waited := false
	for {
		if _, err := kubectl.Output(bin, kc, "get", "--raw=/readyz"); err == nil {
			if waited {
				u.Dim("  Cluster API ready.")
			}
			return nil
		} else if time.Now().After(deadline) {
			return fmt.Errorf("cluster API not ready within %s: %w", timeout, err)
		}
		if !waited {
			u.Dim("  Waiting for cluster API (boot race: k3d needs a moment after Docker starts)...")
			waited = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// resumeBootUnitName is the systemd user unit installed by
// `obol sell resume --install-boot-unit`.
const resumeBootUnitName = "obol-sell-resume.service"

// renderResumeBootUnit produces the systemd user unit that re-runs the
// sell resume path at boot. Pure so tests can pin the contents.
//
// The unit sleeps before resuming: Docker restarts the k3d node
// containers on boot, but the API server inside them needs a moment
// before the resume path's kubectl applies succeed. resumeSellOffers
// warns-and-continues per offer, so a still-starting cluster degrades
// gracefully rather than failing the unit.
func renderResumeBootUnit(obolBin, configDir string) string {
	return `[Unit]
Description=Obol sell offer resume (re-apply sell offers after boot)
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=oneshot
ExecStartPre=/bin/sleep 30
Environment=OBOL_CONFIG_DIR=` + configDir + `
ExecStart=` + obolBin + ` sell resume

[Install]
WantedBy=default.target
`
}

// installResumeBootUnit writes and enables a systemd user unit so the
// resume path runs automatically after a host reboot. Linux-only:
// headless sellers run Linux; macOS operators run `obol sell resume`
// manually (launchd support can follow if asked for).
//
// systemctl calls are best-effort — the unit file on disk is the
// durable artifact, and the printed hints cover the enable steps when
// systemctl is unavailable (e.g. containers, exotic inits).
func installResumeBootUnit(cfg *config.Config, u *ui.UI) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("--install-boot-unit requires Linux with systemd (detected %s); run `obol sell resume` manually after reboot instead", runtime.GOOS)
	}

	// Pin the binary running THIS command into ExecStart — the operator
	// just proved it has `sell resume`. The installed BinDir copy may be
	// an older release without the subcommand (rc11 on the live seller
	// box), which would make the unit fail on every boot.
	obolBin, err := runningObolBinary(cfg)
	if err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return fmt.Errorf("create systemd user dir: %w", err)
	}
	unitPath := filepath.Join(unitDir, resumeBootUnitName)
	if err := os.WriteFile(unitPath, []byte(renderResumeBootUnit(obolBin, cfg.ConfigDir)), 0o644); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}
	u.Successf("Installed boot unit %s", unitPath)
	u.Dim("  Unit runs: " + obolBin + " sell resume — keep this binary at this path, or re-run --install-boot-unit after moving/upgrading it.")

	for _, args := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", resumeBootUnitName},
	} {
		if out, cmdErr := exec.Command("systemctl", args...).CombinedOutput(); cmdErr != nil {
			u.Warnf("systemctl %s failed: %v (%s)", strings.Join(args, " "), cmdErr, strings.TrimSpace(string(out)))
			u.Dim("  Enable manually: systemctl --user daemon-reload && systemctl --user enable " + resumeBootUnitName)
		}
	}

	u.Dim("  User units only run at boot when lingering is on — run once: loginctl enable-linger $USER")
	return nil
}

// runningObolBinary picks the obol binary pinned into the boot unit: the
// binary running THIS process, which is known to have `sell resume`. The
// BinDir copy is only a fallback for the degenerate case where the
// running executable's path cannot be resolved.
func runningObolBinary(cfg *config.Config) (string, error) {
	if exe, err := os.Executable(); err == nil {
		return exe, nil
	}
	obolBin := filepath.Join(cfg.BinDir, "obol")
	if _, err := os.Stat(obolBin); err != nil {
		return "", fmt.Errorf("locate obol binary: %w", err)
	}
	return obolBin, nil
}

// warnOnClockSkew is a best-effort seller preflight. EIP-3009 payment
// authorizations anchor validAfter/validBefore to wall clocks, so a
// drifted seller host makes the facilitator reject every buyer payment
// ("authorization is not yet valid") while `sell status` still looks
// healthy — revenue silently stops. Probes the facilitator's HTTP Date
// header and warns above the threshold; probe failures stay silent
// because the check must never block or delay selling.
func warnOnClockSkew(ctx context.Context, u *ui.UI, facilitatorURL string) {
	if facilitatorURL == "" {
		facilitatorURL = x402verifier.DefaultFacilitatorURL
	}

	skew, err := x402verifier.MeasureClockSkew(ctx, facilitatorURL)
	if err != nil {
		return
	}
	if skew.Abs() <= x402verifier.ClockSkewWarnThreshold {
		return
	}

	direction := "ahead of"
	if skew < 0 {
		direction = "behind"
	}
	u.Warnf("This machine's clock is %s %s the payment facilitator (%s). Buyer payment authorizations may be rejected (\"authorization is not yet valid\") and revenue silently stops.",
		skew.Abs().Round(time.Second), direction, facilitatorURL)
	u.Dim("  Fix: enable NTP time sync (macOS: Date & Time → set automatically; Linux: timedatectl set-ntp true).")
}

// resumeSellOffers re-applies every locally-persisted ServiceOffer
// manifest (http, agent, demo-agent) so operator-created offers survive a
// cluster recreation (CRs live in etcd, which `obol stack down` destroys).
//
// Two entrypoints share this path:
//   - `obol stack up` (cmd/obol/main.go) — fresh-cluster case.
//   - `obol sell resume` — host-reboot case.
//
// `obol sell inference` descriptors are never replayed: the gateway is a
// standalone foreground process. Descriptors written by releases that
// routed it through the cluster are skipped with a one-line warning that
// shows the LiteLLM recipe to re-create the offer.
//
// Best-effort. Per-offer failures emit a warning and the loop continues,
// so one broken manifest cannot block stack-up.
func resumeSellOffers(ctx context.Context, cfg *config.Config, u *ui.UI) error {
	_ = ctx // reserved for cancellation support; current resume calls are synchronous and short

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, statErr := os.Stat(kubeconfigPath); statErr != nil {
		// No cluster — nothing to reattach.
		return nil
	}

	warnLegacyInferenceOffers(cfg, u)

	failed, err := resumePersistedServiceOffers(cfg, u)
	if err != nil {
		u.Warnf("resume persisted sell offers: %v", err)
	}

	// A seller who reboots and sees "stack up: OK" assumes their offers
	// are live. Per-offer warnings scroll past; this summary — and a
	// non-nil error that fails `obol sell resume` outright — makes a
	// partial resume impossible to miss.
	if len(failed) > 0 {
		u.Blank()
		u.Errorf("%d sell offer(s) failed to resume and are NOT live for buyers: %s", len(failed), strings.Join(failed, ", "))
		u.Dim("  Retry with `obol sell resume`; inspect with `obol sell status <name> -n <namespace>`.")
		return fmt.Errorf("%d sell offer(s) failed to resume: %s", len(failed), strings.Join(failed, ", "))
	}

	return nil
}

// warnLegacyInferenceOffers prints one warning per active legacy
// cluster-routed `sell inference` descriptor. Those offers are not
// resumed (cluster routing for host gateways has been removed); the
// warning names the commands that re-create the offer via LiteLLM.
// Tombstoned and standalone descriptors are skipped silently.
func warnLegacyInferenceOffers(cfg *config.Config, u *ui.UI) {
	all, err := inference.NewStore(cfg.ConfigDir).List()
	if err != nil {
		u.Warnf("list sell-inference descriptors: %v", err)
		return
	}
	for _, d := range activeInferenceDeployments(all) {
		if d.LegacyClusterRouted() {
			u.Warnf("%s", legacyInferenceResumeWarning(d))
		}
	}
}

// legacyInferenceResumeWarning is the one-line resume warning for a legacy
// cluster-routed `sell inference` descriptor.
func legacyInferenceResumeWarning(d *inference.Deployment) string {
	ns := legacyInferenceNamespace(d)
	recipe := liteLLMSellRecipe(inferenceRecipeFromDeployment(d))
	return fmt.Sprintf("not resuming sell-inference offer %q: cluster routing for `obol sell inference` was removed. "+
		"Remove the old offer (`obol sell delete %s -n %s`) and re-create it via LiteLLM: `%s` then `%s`",
		d.Name, d.Name, ns, recipe[0], recipe[1])
}

// legacyInferenceNamespace is the namespace a legacy cluster-routed
// descriptor's ServiceOffer was published in.
func legacyInferenceNamespace(d *inference.Deployment) string {
	if d.ServiceNamespace != "" {
		return d.ServiceNamespace
	}
	return "llm" // descriptors written before service_namespace was persisted
}

// legacyInferenceDescriptor returns the active legacy cluster-routed
// sell-inference descriptor for name whose offer lived in ns, or nil.
// Namespace-guarded so deleting an unrelated same-name offer elsewhere
// can't tombstone it.
func legacyInferenceDescriptor(cfg *config.Config, name, ns string) *inference.Deployment {
	d, err := inference.NewStore(cfg.ConfigDir).Get(name)
	if err != nil || d == nil || d.DeletedAt != "" || !d.LegacyClusterRouted() {
		return nil
	}
	if legacyInferenceNamespace(d) != ns {
		return nil
	}
	return d
}

// inferenceRecipe carries the operator's values for the LiteLLM recipe
// printed when `obol sell inference` is not the right tool.
type inferenceRecipe struct {
	Name       string
	Model      string
	Upstream   string
	PerMTok    string
	PerRequest string
	Chain      string
	Token      string
	PayTo      string
	NoRegister bool
}

func inferenceRecipeFromDeployment(d *inference.Deployment) inferenceRecipe {
	r := inferenceRecipe{
		Name:     d.Name,
		Model:    d.ModelName,
		Upstream: d.UpstreamURL,
		PerMTok:  d.PricePerMTok,
		Chain:    d.Chain,
		Token:    d.AssetSymbol,
		PayTo:    d.WalletAddress,
	}
	if r.PerMTok == "" {
		r.PerRequest = d.PricePerRequest
	}
	if enabled, ok := d.Registration["enabled"].(bool); ok && !enabled {
		r.NoRegister = true
	}
	return r
}

// liteLLMSellRecipe returns the two commands that sell a host inference
// server on a host running the stack: register the endpoint with LiteLLM,
// then publish a LiteLLM-backed `sell http` offer. Unknown values are
// rendered as <placeholders>.
func liteLLMSellRecipe(r inferenceRecipe) [2]string {
	model := r.Model
	if model == "" {
		model = "<model-id>"
	}
	upstream := strings.TrimRight(strings.TrimSpace(r.Upstream), "/")
	if upstream == "" {
		upstream = "http://localhost:11434"
	}
	if !strings.HasSuffix(upstream, "/v1") {
		upstream += "/v1"
	}

	setup := fmt.Sprintf("obol model setup custom --endpoint %s --model %s", upstream, model)

	parts := []string{"obol sell http", r.Name, "--upstream litellm --port 4000 --namespace llm", "--model " + model}
	switch {
	case r.PerMTok != "":
		parts = append(parts, "--per-mtok "+r.PerMTok)
	case r.PerRequest != "":
		parts = append(parts, "--per-request "+r.PerRequest)
	default:
		parts = append(parts, "--per-mtok <price>")
	}
	if r.Chain != "" {
		parts = append(parts, "--network "+r.Chain)
	}
	if r.Token != "" && !strings.EqualFold(r.Token, "USDC") {
		parts = append(parts, "--token "+r.Token)
	}
	if r.PayTo != "" {
		parts = append(parts, "--pay-to "+r.PayTo)
	}
	if r.NoRegister {
		parts = append(parts, "--no-register")
	}

	return [2]string{setup, strings.Join(parts, " ")}
}

// refuseSellInferenceWithStack returns the error `obol sell inference`
// fails with when this host runs an Obol Stack and --standalone is not
// set. The message carries the LiteLLM recipe filled with the operator's
// flags.
func refuseSellInferenceWithStack(cfg *config.Config, cmd *cli.Command, name string) error {
	if cmd.Bool("standalone") {
		return nil
	}
	if _, err := os.Stat(filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")); err != nil {
		return nil
	}

	perRequest := cmd.String("price")
	if perRequest == "" {
		perRequest = cmd.String("per-request")
	}
	r := inferenceRecipe{
		Name:       name,
		Model:      cmd.String("model"),
		Upstream:   cmd.String("upstream"),
		PerMTok:    cmd.String("per-mtok"),
		PerRequest: perRequest,
		Chain:      cmd.String("network"),
		Token:      cmd.String("token"),
		PayTo:      cmd.String("pay-to"),
		NoRegister: cmd.Bool("no-register"),
	}
	if r.PerMTok != "" && r.PerRequest != "" {
		// Same precedence as resolvePriceTable: a per-request price wins.
		r.PerMTok = ""
	}
	recipe := liteLLMSellRecipe(r)

	return fmt.Errorf("this host runs an Obol Stack, so `obol sell inference` will not start a host gateway here.\n"+
		"Sell the inference server through LiteLLM instead (seller-pinned model, inference-only routes, settle on delivery):\n\n"+
		"  %s\n"+
		"  %s\n\n"+
		"To run the standalone gateway with its own x402 gate on this host anyway, re-run with --standalone",
		recipe[0], recipe[1])
}

// activeInferenceDeployments filters out tombstoned descriptors —
// offers removed by `obol sell delete` whose on-disk state survives for
// `sell status`. Pure so the resume exclusion is testable on its own.
func activeInferenceDeployments(ds []*inference.Deployment) []*inference.Deployment {
	var active []*inference.Deployment
	for _, d := range ds {
		if d != nil && d.DeletedAt == "" {
			active = append(active, d)
		}
	}
	return active
}

// validateSellInferenceModelName enforces the no-slash model-name rule
// for `obol sell inference` offers.
func validateSellInferenceModelName(model string) error {
	if !strings.Contains(model, "/") {
		return nil
	}
	suggestion := strings.ReplaceAll(model, "/", "--")
	return fmt.Errorf(
		"--model %q contains '/', which breaks LiteLLM's `paid/*` wildcard on the buyer side; "+
			"use `--` (or another non-slash separator) — e.g. `%s` instead of `%s`",
		model, suggestion, model,
	)
}

// sellOfferStoreDir returns the on-disk ledger of persisted ServiceOffer
// manifests — one YAML file per offer at
// <ConfigDir>/sell-http/<namespace>__<name>.yaml so two offers with the
// same name in different namespaces never collide. (The dir name is
// historical: it predates the ledger covering more than `sell http`.
// Renaming it would orphan offers persisted by already-shipped CLIs.)
//
// EVERY ServiceOffer type persists here: `sell http`, `sell agent`, and
// the demo agent flow. The on-disk artifact is just the rendered
// ServiceOffer manifest; the resume path kubectl-applies it to bring the
// offer back identically. `sell inference` keeps its own store
// (<ConfigDir>/inference/) for its standalone host gateway, which is
// never replayed.
func sellOfferStoreDir(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "sell-http")
}

func sellOfferStorePath(cfg *config.Config, namespace, name string) string {
	return filepath.Join(sellOfferStoreDir(cfg), namespace+"__"+name+".yaml")
}

// parseRouteFlags converts repeatable --route values into spec.routes
// entries. Each value is a comma-separated key=value list; a segment
// without "=" is folded into the previous value so free-text summaries may
// contain commas (mirrors the --accept parsing approach). hasPaid reports
// whether at least one route charges — an all-free table is legal but
// almost certainly a mistake, so the caller warns.
func parseRouteFlags(vals []string) (routes []map[string]any, hasPaid bool, err error) {
	routes = make([]map[string]any, 0, len(vals))
	for _, val := range vals {
		route := map[string]any{}
		var segments []string
		for _, seg := range strings.Split(val, ",") {
			if !strings.Contains(seg, "=") && len(segments) > 0 {
				segments[len(segments)-1] += "," + seg
				continue
			}
			segments = append(segments, seg)
		}
		gate := monetizeapi.GatePaid
		for _, seg := range segments {
			key, v, _ := strings.Cut(seg, "=")
			key = strings.TrimSpace(key)
			v = strings.TrimSpace(v)
			switch key {
			case "path":
				if !strings.HasPrefix(v, "/") {
					return nil, false, fmt.Errorf("--route %q: path must start with / (got %q)", val, v)
				}
				route["path"] = v
			case "methods":
				methods := []any{}
				for _, m := range strings.Split(v, "|") {
					m = strings.ToUpper(strings.TrimSpace(m))
					switch m {
					case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
						methods = append(methods, m)
					default:
						return nil, false, fmt.Errorf("--route %q: unknown method %q", val, m)
					}
				}
				route["methods"] = methods
			case "gate":
				switch v {
				case monetizeapi.GatePaid, monetizeapi.GateFree, monetizeapi.GateAuth:
					gate = v
				default:
					return nil, false, fmt.Errorf("--route %q: gate must be paid, free, or auth (got %q)", val, v)
				}
			case "price":
				route["price"] = map[string]any{"perRequest": v}
			case "freeQuota", "free-quota":
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil || n < 0 {
					return nil, false, fmt.Errorf("--route %q: freeQuota must be a non-negative integer", val)
				}
				route["freeQuota"] = n
			case "summary":
				route["summary"] = v
			default:
				return nil, false, fmt.Errorf("--route %q: unknown key %q (want path, methods, gate, price, freeQuota, summary)", val, key)
			}
		}
		if route["path"] == nil {
			return nil, false, fmt.Errorf("--route %q: path is required", val)
		}
		route["gate"] = gate
		if gate == monetizeapi.GatePaid {
			hasPaid = true
		} else if route["price"] != nil || route["freeQuota"] != nil {
			return nil, false, fmt.Errorf("--route %q: price and freeQuota only apply to paid routes (gate=%s)", val, gate)
		}
		routes = append(routes, route)
	}
	return routes, hasPaid, nil
}

// preflightOfferPathCollision fails fast when another live ServiceOffer
// already claims the manifest's public path. The x402 verifier's route
// table is first-match-wins, so a colliding offer would silently shadow
// (or be shadowed by) the existing one. Best-effort: an unreachable
// cluster or missing CRD skips the check — the apply that follows will
// surface the real error, and the serviceoffer-controller backstops with
// RoutePublished=False/PathConflict either way. (A
// ValidatingAdmissionPolicy cannot do this check: VAPs cannot list other
// cluster objects.)
func preflightOfferPathCollision(cfg *config.Config, manifest map[string]any) error {
	meta, _ := manifest["metadata"].(map[string]any)
	name, _ := meta["name"].(string)
	ns, _ := meta["namespace"].(string)
	spec, _ := manifest["spec"].(map[string]any)
	path, _ := spec["path"].(string)
	if path == "" {
		path = "/services/" + name
	}
	hostname, _ := spec["hostname"].(string)
	// Static check first — reserved platform paths are rejected even when
	// the cluster is unreachable (the controller backstops with
	// RoutePublished=False/ReservedPath, but failing here is friendlier).
	if root := monetizeapi.ReservedPathConflict(path); root != "" {
		return fmt.Errorf("path %s collides with the reserved platform path %s (discovery/routing surface) — pass --path to pick a different public path", path, root)
	}
	// F8: same static check, but over each declared spec.routes[].path
	// entry rather than just the offer root. A route landing on "/auth" (or
	// nested under it) would shadow the verifier's SIWX sign-in endpoints.
	if routePath, root := reservedRoutePathCollision(spec); root != "" {
		return fmt.Errorf("route path %s collides with the reserved platform path %s (discovery/routing surface) — pick a different route path", routePath, root)
	}
	bin, kubeconfig := kubectl.Paths(cfg)
	out, err := kubectl.Output(bin, kubeconfig, "get", "serviceoffers.obol.org", "-A", "-o", "json")
	if err != nil {
		return nil //nolint:nilerr // best-effort preflight; the apply surfaces real errors
	}
	return offerPathCollisionInList([]byte(out), ns, name, path, hostname)
}

// reservedRoutePathCollision checks spec["routes"] against the route-level
// reserved-path denylist and returns the first colliding route's path and
// the reserved root it hit, or ("", "") when none collide. spec["routes"]
// takes two shapes depending on how the manifest was built: []map[string]any
// from parseRouteFlags (the --route flag path) or []any of
// map[string]interface{} from json.Unmarshal (the --from-json path) — any
// is an alias for interface{}, so both element types assert the same way.
func reservedRoutePathCollision(spec map[string]any) (routePath, root string) {
	var routes []any
	switch rs := spec["routes"].(type) {
	case []map[string]any:
		for _, r := range rs {
			routes = append(routes, r)
		}
	case []any:
		routes = rs
	}
	for _, item := range routes {
		rm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		p, _ := rm["path"].(string)
		if p == "" {
			continue
		}
		if r := monetizeapi.ReservedRoutePathConflict(p); r != "" {
			return p, r
		}
	}
	return "", ""
}

// offerPathCollisionInList is the pure core of preflightOfferPathCollision.
func offerPathCollisionInList(listJSON []byte, ns, name, path, hostname string) error {
	var list struct {
		Items []struct {
			Metadata struct {
				Name              string `json:"name"`
				Namespace         string `json:"namespace"`
				DeletionTimestamp string `json:"deletionTimestamp"`
			} `json:"metadata"`
			Spec struct {
				Path     string `json:"path"`
				Hostname string `json:"hostname"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listJSON, &list); err != nil {
		return nil //nolint:nilerr // unparseable listing: leave it to the controller backstop
	}
	if path == "" {
		path = "/services/" + name
	}
	want := strings.TrimSuffix(path, "/")
	for _, item := range list.Items {
		if item.Metadata.Namespace == ns && item.Metadata.Name == name {
			continue // re-applying the same offer is an update, not a collision
		}
		if item.Metadata.DeletionTimestamp != "" {
			continue
		}
		other := item.Spec.Path
		if other == "" {
			other = "/services/" + item.Metadata.Name
		}
		if strings.TrimSuffix(other, "/") == want {
			return fmt.Errorf("path %s is already used by offer %s/%s — pass --path to pick a different public path, or delete the existing offer first",
				path, item.Metadata.Namespace, item.Metadata.Name)
		}
		if hostname != "" && strings.EqualFold(item.Spec.Hostname, hostname) {
			return fmt.Errorf("hostname %s is already used by offer %s/%s — one offer per origin; pass a different --hostname or delete the existing offer first",
				hostname, item.Metadata.Namespace, item.Metadata.Name)
		}
	}
	return nil
}

// persistServiceOffer writes the rendered ServiceOffer manifest to disk
// so `obol stack up` can replay it after a stack-down/up cycle wipes
// etcd. Idempotent: subsequent calls overwrite atomically (write to a
// `.tmp` sibling, rename into place) so a crash mid-write doesn't leave
// a half-rendered file the resume path would choke on.
//
// Best-effort from the caller's perspective: a persistence failure
// should warn but not abort the sell command, because the cluster-side
// offer is already in place. Returning an error lets the caller pick
// the surface.
func persistServiceOffer(cfg *config.Config, namespace, name string, manifest map[string]any) error {
	if cfg == nil || namespace == "" || name == "" {
		return errors.New("persistServiceOffer: missing cfg / namespace / name")
	}
	if err := os.MkdirAll(sellOfferStoreDir(cfg), 0o755); err != nil {
		return fmt.Errorf("create store dir: %w", err)
	}
	kubectl.SetManagedBy(manifest)
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	final := sellOfferStorePath(cfg, namespace, name)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s → %s: %w", tmp, final, err)
	}
	return nil
}

// removePersistedServiceOffer deletes the on-disk manifest for a single offer
// when `obol sell delete` succeeds. Without this, the resume path on
// the next `obol stack up` would re-create an offer the operator
// intentionally deleted. Best-effort — a missing file is a no-op.
func removePersistedServiceOffer(cfg *config.Config, namespace, name string) error {
	if cfg == nil || namespace == "" || name == "" {
		return nil
	}
	path := sellOfferStorePath(cfg, namespace, name)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// refreshPersistedServiceOffer re-renders the ledger entry for one offer
// from the live cluster object, after an in-place mutation (`obol sell
// update`). Reads the CR, strips server-managed fields down to
// apiVersion/kind/metadata(name, namespace, labels)/spec, and overwrites
// the persisted manifest. When the existing ledger file is a demo v1
// List bundle, only the inner ServiceOffer item is replaced so the
// backend manifests survive.
//
// Also ADOPTS offers that were never persisted (e.g. created by an
// agent via raw kubectl): an explicit `sell update` is operator intent,
// so the offer earns resume coverage from then on.
func refreshPersistedServiceOffer(cfg *config.Config, ns, name string) error {
	out, err := kubectlOutput(cfg, "get", "serviceoffers.obol.org", name, "-n", ns, "-o", "yaml")
	if err != nil {
		return fmt.Errorf("read live offer: %w", err)
	}
	var live map[string]any
	if err := yaml.Unmarshal([]byte(out), &live); err != nil {
		return fmt.Errorf("parse live offer: %w", err)
	}
	spec, ok := live["spec"].(map[string]any)
	if !ok {
		return errors.New("live offer has no spec")
	}
	metadata := map[string]any{"name": name, "namespace": ns}
	if md, ok := live["metadata"].(map[string]any); ok {
		if labels, ok := md["labels"].(map[string]any); ok && len(labels) > 0 {
			metadata["labels"] = labels
		}
	}
	fresh := map[string]any{
		"apiVersion": "obol.org/v1alpha1",
		"kind":       "ServiceOffer",
		"metadata":   metadata,
		"spec":       spec,
	}

	// Demo bundles persist as a v1 List; swap the inner offer in place.
	if data, err := os.ReadFile(sellOfferStorePath(cfg, ns, name)); err == nil {
		var existing map[string]any
		if yaml.Unmarshal(data, &existing) == nil && existing["kind"] == "List" {
			items, _ := existing["items"].([]any)
			for i, it := range items {
				m, ok := it.(map[string]any)
				if ok && m["kind"] == "ServiceOffer" {
					items[i] = fresh
				}
			}
			existing["items"] = items
			return persistServiceOffer(cfg, ns, name, existing)
		}
	}

	return persistServiceOffer(cfg, ns, name, fresh)
}

// removePersistedServiceOffersInNamespace drops every ledger manifest
// whose metadata.namespace matches ns. Used by `obol agent delete`
// alongside its in-cluster ServiceOffer deletion (the agent finalizer
// tears down the agent's children but leaves the namespace and offers):
// once the CRs are deleted there, the ledger entries must go too or
// resume replays ghosts. Returns the number of files removed.
func removePersistedServiceOffersInNamespace(cfg *config.Config, ns string) (int, error) {
	if cfg == nil || ns == "" {
		return 0, nil
	}
	manifests, err := loadPersistedServiceOffers(sellOfferStoreDir(cfg), ui.New(false))
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, m := range manifests {
		if m.Namespace != ns {
			continue
		}
		if err := os.Remove(m.Path); err != nil && !os.IsNotExist(err) {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// resumePersistedServiceOffers re-applies every ServiceOffer manifest in
// the ledger (http, agent, demo-agent — every type without a host-side
// process) after a reboot or a stack-down/up cycle. Mirror of the
// inference resume loop: walk the store dir, kubectl apply each file,
// warn-and-continue on per-offer failures so one corrupt YAML can't
// block the rest.
//
// A replayed type=agent offer needs its Agent CR to exist before it can
// reach Ready. After a reboot the CR is still in etcd; after a full
// stack recreation the replayed bundle re-creates the agent NAMESPACE
// (see agentOfferBundle) so the offer lands and the controller reports
// the missing agent on its conditions until the operator recreates it
// (`obol agent new`). The Agent CR itself is never replayed — that
// would mint a fresh wallet.
//
// Skipped silently when the store dir is missing (no offers ever
// persisted) or when no kubeconfig is present (stack up hasn't reached
// the cluster yet). Counts and announces what was reattached so the
// operator sees the same "Resumed N offers" feedback they get for
// inference.
func resumePersistedServiceOffers(cfg *config.Config, u *ui.UI) (failed []string, err error) {
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, statErr := os.Stat(kubeconfigPath); statErr != nil {
		return nil, nil // no cluster yet
	}

	manifests, err := loadPersistedServiceOffers(sellOfferStoreDir(cfg), u)
	if err != nil {
		return nil, err
	}
	if len(manifests) == 0 {
		return nil, nil
	}

	u.Blank()
	u.Infof("Resuming %d locally-persisted sell offer(s)...", len(manifests))
	// Recorded manifests pin stack-owned images (e.g. demo-server) at the tag
	// current when the offer was created. Re-resolve them to this CLI's
	// policy so replay upgrades with the stack and, in dev mode, never asks
	// for a dev-<sha> tag that a recreated cluster no longer has imported.
	replacers := images.BuildReplacers(func(repo string) string {
		return stackdefaults.ResolveWorkloadImage(cfg, repo)
	})
	for _, m := range manifests {
		// Legacy ledger entries predate the managed-by label.
		kubectl.SetManagedBy(m.Manifest)
		if refreshed, err := refreshManagedImages(m.Manifest, replacers); err != nil {
			u.Warnf("resume %s %s/%s: re-resolving images: %v (applying as recorded)", m.label(), m.Namespace, m.Name, err)
		} else {
			m.Manifest = refreshed
		}
		if err := kubectlApply(cfg, m.Manifest); err != nil {
			u.Warnf("resume %s %s/%s: %v", m.label(), m.Namespace, m.Name, err)
			failed = append(failed, fmt.Sprintf("%s/%s", m.Namespace, m.Name))
			continue
		}
		u.Successf("Resumed %s offer %s/%s", m.label(), m.Namespace, m.Name)
	}
	return failed, nil
}

// loadPersistedServiceOffers walks a ledger dir and parses every
// *.yaml manifest in it. Unreadable or malformed files warn and are
// skipped — the resume loop must survive one corrupt entry. A missing
// dir means no offers were ever persisted and returns an empty slice.
func loadPersistedServiceOffers(dir string, u *ui.UI) ([]persistedServiceOffer, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read store dir: %w", err)
	}

	var manifests []persistedServiceOffer
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			u.Warnf("read %s: %v", path, err)
			continue
		}
		var manifest map[string]any
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			u.Warnf("parse %s: %v", path, err)
			continue
		}
		ns, name := manifestNSName(manifest)
		if ns == "" || name == "" {
			u.Warnf("skip %s: manifest has no metadata.namespace/name", path)
			continue
		}
		manifests = append(manifests, persistedServiceOffer{
			Path:      path,
			Manifest:  manifest,
			Namespace: ns,
			Name:      name,
			Type:      manifestOfferType(manifest),
		})
	}
	return manifests, nil
}

// refreshManagedImages rewrites every stack-owned image reference in a
// recorded manifest using replacers (see images.BuildReplacers).
func refreshManagedImages(manifest map[string]any, replacers []images.Replacer) (map[string]any, error) {
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	out := images.RewriteBytes(raw, replacers)
	if bytes.Equal(out, raw) {
		return manifest, nil
	}
	var refreshed map[string]any
	if err := json.Unmarshal(out, &refreshed); err != nil {
		return nil, err
	}
	return refreshed, nil
}

type persistedServiceOffer struct {
	Path      string
	Manifest  map[string]any
	Namespace string
	Name      string
	Type      string // spec.type (http, agent, inference, ...); empty when absent
}

// label renders the offer for resume messaging: "sell-agent" /
// "sell-http" / plain "sell" when the manifest carries no spec.type
// (legacy files persisted before the type was recorded).
func (m persistedServiceOffer) label() string {
	if m.Type == "" {
		return "sell"
	}
	return "sell-" + m.Type
}

// manifestNSName pulls metadata.namespace + metadata.name out of an
// unmarshaled ServiceOffer manifest. Returns empty strings when the
// shape is malformed; the resume loop reports the error per-file.
func manifestNSName(manifest map[string]any) (string, string) {
	md, ok := manifest["metadata"].(map[string]any)
	if !ok {
		return "", ""
	}
	ns, _ := md["namespace"].(string)
	name, _ := md["name"].(string)
	return ns, name
}

// manifestOfferType pulls spec.type out of an unmarshaled ServiceOffer
// manifest; empty string when absent or malformed. For v1 List bundles
// (the legacy demo persists backend + offer together) it reports the
// inner ServiceOffer's type.
func manifestOfferType(manifest map[string]any) string {
	if manifest["kind"] == "List" {
		items, _ := manifest["items"].([]any)
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok || m["kind"] != "ServiceOffer" {
				continue
			}
			return manifestOfferType(m)
		}
		return ""
	}
	spec, ok := manifest["spec"].(map[string]any)
	if !ok {
		return ""
	}
	t, _ := spec["type"].(string)
	return t
}
