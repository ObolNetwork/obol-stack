package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/agentcrd"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/model"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/tunnel"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/urfave/cli/v3"
)

func sellAgentCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "agent",
		Usage:     "Gate an existing Obol Stack agent with x402 payments",
		ArgsUsage: "<name>",
		Description: `Wraps an existing Agent (created with ` + "`obol agent new <name>`" + `) with a
ServiceOffer of type=agent. The serviceoffer-controller resolves
spec.agent.ref into the agent's cluster endpoint, surfaces the agent's
pinned model + skill list in the 402 response's extra block, and
publishes the route through Traefik.

Run ` + "`obol agent new <name> --skills ... --model ... --create-wallet`" + ` first
to declare the agent, then ` + "`obol sell agent <name>`" + ` to make it sellable.

Examples:
  obol sell agent quant --price 0.01 --token USDC --chain base-sepolia
  obol sell agent quant --price 10 --token OBOL --chain ethereum --pay-to 0xColdVault`,
		Flags: []cli.Flag{
			payToFlag("Recipient for sale revenue (defaults to the agent's own wallet when one was provisioned)"),
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
				Usage: "Per-request price (e.g. 0.01 for USDC, 10 for OBOL)",
			},
			&cli.StringFlag{
				Name:  "per-request",
				Usage: "Per-request price (alias for --price)",
			},
			&cli.StringFlag{
				Name:  "path",
				Usage: "URL path prefix (default: /services/<name>)",
			},
			&cli.IntFlag{
				Name:  "max-timeout",
				Usage: "Payment validity window in seconds. Agent tasks can be slower than raw inference, so the default is generous.",
				Value: 300,
			},
			&cli.BoolFlag{
				Name:  "no-register",
				Usage: "Skip ERC-8004 registration. Useful for local dev or when the recipient wallet has no ETH for gas.",
			},
			&cli.StringFlag{
				Name:  "register-name",
				Usage: "Agent name for ERC-8004 registration (defaults to the offer name)",
			},
			&cli.StringFlag{
				Name:    "description",
				Aliases: []string{"register-description"},
				Usage:   "Human-readable description of the service. Surfaced on the 402 payment page, in the storefront catalog, and (when registration is enabled) on the ERC-8004 registration document. Defaults to the agent's objective.",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if cmd.NArg() != 1 {
				return fmt.Errorf("agent name required: obol sell agent <name>")
			}
			name := strings.TrimSpace(cmd.Args().First())
			if err := agentcrd.ValidateName(name); err != nil {
				return err
			}

			if err := kubectl.EnsureCluster(cfg); err != nil {
				return fmt.Errorf("Obol Stack is not running. Start it with `obol stack up` first")
			}

			// Look the agent up. On a TTY, missing agents trigger an
			// inline create-and-sell flow so users don't have to context-
			// switch between two commands. Non-TTY callers get the
			// terse "agent not found" error so scripts fail fast.
			agent, err := getAgentRefForSale(cfg, name)
			if err != nil {
				if !u.IsTTY() || u.IsJSON() {
					return err
				}
				u.Warnf("Agent %q not found in namespace %s.", name, agentcrd.Namespace(name))
				ans, _ := u.Input("Create it now? [Y/n]", "Y")
				if !strings.EqualFold(strings.TrimSpace(ans), "n") && !strings.EqualFold(strings.TrimSpace(ans), "no") {
					if createErr := createCRDAgent(cfg, u, createCRDAgentOptions{
						Name:        name,
						Interactive: true,
					}); createErr != nil {
						return createErr
					}
					// Wait briefly for the controller to populate
					// status.walletAddress + endpoint before reading
					// the agent again. Best-effort: the offer reconciler
					// will surface "WaitingForAgent" if we move ahead
					// before the agent is Ready.
					u.Info("Waiting up to 30s for the controller to materialise the agent...")
					agent, err = waitForAgentRefForSale(cfg, name, 30*time.Second)
					if err != nil {
						u.Warnf("Agent not yet ready: %v — proceeding; offer will reconcile when the agent catches up.", err)
						agent = &agentRefForSale{Name: name, Namespace: agentcrd.Namespace(name)}
					}
				} else {
					return err
				}
			}

			price := strings.TrimSpace(cmd.String("price"))
			if price == "" {
				price = strings.TrimSpace(cmd.String("per-request"))
			}
			if price == "" {
				return fmt.Errorf("price required: use --price or --per-request")
			}

			chain := cmd.String("chain")
			tokenName := cmd.String("token")

			// Resolve token metadata. resolveAssetTermsFor may flip chain
			// when the token isn't supported on the requested chain.
			chainExplicit := cmd.IsSet("chain")
			assetTerms, err := resolveAssetTermsFor(tokenName, &chain, chainExplicit)
			if err != nil {
				return err
			}

			payTo := strings.TrimSpace(cmd.String("pay-to"))
			if payTo == "" {
				// Default order: agent's own wallet → host remote-signer.
				if agent.WalletAddress != "" {
					payTo = agent.WalletAddress
					u.Infof("Routing revenue to agent's own wallet: %s", payTo)
				} else if resolved, err := hermes.ResolveWalletAddress(cfg); err == nil {
					payTo = resolved
					u.Infof("Routing revenue to host remote-signer wallet: %s", payTo)
				} else {
					return fmt.Errorf("recipient required: use --pay-to <addr> or provision a wallet at agent creation time")
				}
			}
			if err := x402verifier.ValidateWallet(payTo); err != nil {
				return err
			}

			path := strings.TrimSpace(cmd.String("path"))
			if path == "" {
				path = "/services/" + name
			}

			register := !cmd.Bool("no-register")
			regName := strings.TrimSpace(cmd.String("register-name"))
			if regName == "" {
				regName = name
			}
			regDesc := strings.TrimSpace(cmd.String("description"))
			if regDesc == "" {
				regDesc = agent.Objective
			}

			// Build the ServiceOffer manifest. type=agent + agent.ref tells
			// the controller to resolve upstream from the Agent CR; we
			// don't supply spec.upstream here.
			offerNs := agent.Namespace
			payment := map[string]any{
				"scheme":            "exact",
				"network":           chain,
				"payTo":             payTo,
				"maxTimeoutSeconds": cmd.Int("max-timeout"),
				"price": map[string]any{
					"perRequest": price,
				},
			}
			if !assetTerms.IsZero() {
				payment["asset"] = assetTerms
			}
			spec := map[string]any{
				"type": "agent",
				"agent": map[string]any{
					"ref": map[string]any{
						"name":      agent.Name,
						"namespace": agent.Namespace,
					},
				},
				"payment": payment,
				"path":    path,
			}
			if register {
				skills := make([]any, len(agent.Skills))
				for i, s := range agent.Skills {
					skills[i] = s
				}
				symbol := assetTerms.Symbol
				if symbol == "" {
					symbol = strings.ToUpper(tokenName)
				}
				spec["registration"] = map[string]any{
					"enabled":     true,
					"name":        regName,
					"description": regDesc,
					"skills":      skills,
					"metadata":    agentOfferRegistrationMetadata(agent, price, symbol, chain),
				}
			}

			manifest := map[string]any{
				"apiVersion": "obol.org/v1alpha1",
				"kind":       "ServiceOffer",
				"metadata": map[string]any{
					"name":      name,
					"namespace": offerNs,
				},
				"spec": spec,
			}

			out, err := kubectlApplyOutput(cfg, manifest)
			if err != nil {
				return fmt.Errorf("apply ServiceOffer: %w", err)
			}
			action := "created"
			if strings.Contains(out, "configured") || strings.Contains(out, "unchanged") {
				action = "updated"
			}
			u.Successf("ServiceOffer %s/%s %s (type: agent, %s %s/req → %s)", offerNs, name, action, price, assetTerms.Symbol, payTo)
			u.Infof("Reconciler will resolve agent.ref → derive upstream → publish payment gate + route")
			u.Infof("Check status: obol sell status %s -n %s", name, offerNs)

			// Best-effort tunnel hint, mirroring `sell http`.
			if tURL, terr := tunnel.EnsureTunnelForSell(cfg, u); terr != nil {
				u.Warnf("Tunnel not started: %v", terr)
				u.Dim("  Start manually with: obol tunnel restart")
			} else {
				u.Successf("Tunnel: %s%s", tURL, path)
			}

			if !register {
				u.Dim("Registration skipped (--no-register). Run `obol sell register --chain " + chain + "` later for on-chain discovery.")
			} else {
				// sell agent is declare-only: it sets spec.registration and
				// relies on the controller + a manual `obol sell register`. Make
				// the Ready=False consequence, gas need, and signer/payee split
				// legible instead of leaving the operator to discover them.
				aw := strings.TrimSpace(agent.WalletAddress)
				if aw == "" {
					if resolved, werr := hermes.ResolveWalletAddress(cfg); werr == nil {
						aw = resolved
					}
				}
				printRegistrationNotice(u, registrationNotice{
					Mode:        regNoticeDeclareOnly,
					Chain:       chain,
					PayTo:       payTo,
					AgentWallet: aw,
					OfferName:   name,
					Namespace:   offerNs,
				})
			}
			return nil
		},
	}
}

// runAgentBackedDemo is the agent-backed code path for `obol sell demo`.
// It declares an Agent CR (skills, objective, create-wallet) then a
// ServiceOffer of type=agent referencing it. The legacy demo flow
// (deploy demo-server + type=http offer) still handles hello/blocks.
func runAgentBackedDemo(
	ctx context.Context,
	cfg *config.Config,
	u *ui.UI,
	cmd *cli.Command,
	name, typeName, price, symbol, chain, payTo string,
	spec demoSpec,
	assetTerms schemas.AssetTerms,
) error {
	if spec.Agent == nil {
		return fmt.Errorf("runAgentBackedDemo called with non-agent demo spec %q", typeName)
	}

	// Extract agent name from offer name. Demo offers are named
	// "demo-quant" by default; the agent we provision is named the same
	// so kubectl get/delete works without a separate lookup table.
	agentName := name
	if err := agentcrd.ValidateName(agentName); err != nil {
		return fmt.Errorf("derived agent name %q is invalid: %w", agentName, err)
	}

	// 1. Seed host-side files (skills + SOUL.md) and apply the Agent CR.
	//    Idempotent — re-running `obol sell demo quant` after a previous
	//    run is a no-op for the agent if it already exists. A CR that is
	//    mid-deletion (DeletionTimestamp set, finalizer still draining)
	//    is NOT treated as "already exists": short-circuiting on it
	//    means a follow-up `sell demo quant` after a hung `agent delete`
	//    silently does nothing, which is what motivated this check.
	state, stateErr := getAgentCRState(cfg, agentName)
	switch {
	case stateErr == nil && state.Exists && state.Terminating:
		return fmt.Errorf("Agent %s is still being deleted (DeletionTimestamp set, finalizer draining).\n\n"+
			"Wait for the controller to finish, or run `obol agent delete %s --force` to strip the finalizer "+
			"and retry.", agentName, agentName)
	case stateErr == nil && state.Exists:
		u.Dim(fmt.Sprintf("Agent %s already exists, leaving as-is", agentName))
	default:
		soulWritten, seedErr := agentcrd.SeedHostFiles(cfg, agentName,
			spec.Agent.Skills, spec.Agent.Objective, agentcrd.SeedOptions{})
		if seedErr != nil {
			return fmt.Errorf("seed agent host files: %w", seedErr)
		}
		if soulWritten {
			u.Successf("SOUL.md seeded at %s", agentcrd.HostSoulPath(cfg, agentName))
		}
		// Namespace must exist before the Agent CR can land; controller-
		// side namespace creation is part of provisioning, which doesn't
		// run until the CR exists. Apply both, namespace first.
		nsManifest := map[string]any{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]any{
				"name": agentcrd.Namespace(agentName),
				"labels": map[string]any{
					"obol.org/agent-namespace":     "true",
					"app.kubernetes.io/managed-by": "obol-cli",
				},
			},
		}
		if _, err := kubectlApplyOutput(cfg, nsManifest); err != nil {
			return fmt.Errorf("apply namespace: %w", err)
		}
		// Pin a model up front so the controller doesn't park at
		// ModelUnpinned. The demo is meant to be one-shot; surfacing
		// LiteLLM-empty as a clear error here is better than letting the
		// agent silently never reach Ready.
		demoModel, modelErr := resolveDefaultAgentModel(cfg)
		if modelErr != nil {
			return fmt.Errorf("resolve a default model for the demo agent: %w", modelErr)
		}
		u.Infof("Pinning demo agent to model %q (cluster top-of-rank)", demoModel)

		manifest := agentcrd.BuildAgent(agentName, agentcrd.AgentOptions{
			Model:        demoModel,
			Skills:       spec.Agent.Skills,
			Objective:    spec.Agent.Objective,
			CreateWallet: true,
		})
		if _, err := kubectlApplyOutput(cfg, manifest); err != nil {
			return fmt.Errorf("apply Agent: %w", err)
		}
		u.Successf("Agent %s/%s created (skills: %s)", agentcrd.Namespace(agentName), agentName,
			strings.Join(spec.Agent.Skills, ", "))
	}

	// 2. Build and apply the agent-typed ServiceOffer.
	register := cmd.Bool("register")
	offerNs := agentcrd.Namespace(agentName)
	agentForMetadata, _ := getAgentRefForSale(cfg, agentName)
	if agentForMetadata == nil {
		agentForMetadata = &agentRefForSale{Name: agentName, Namespace: offerNs, Runtime: monetizeapi.AgentRuntimeHermes}
	}

	payment := map[string]any{
		"scheme":            "exact",
		"network":           chain,
		"payTo":             payTo,
		"maxTimeoutSeconds": 300,
		"price":             map[string]any{"perRequest": price},
	}
	if !assetTerms.IsZero() {
		payment["asset"] = assetTerms
	}
	specMap := map[string]any{
		"type": "agent",
		"agent": map[string]any{
			"ref": map[string]any{
				"name":      agentName,
				"namespace": offerNs,
			},
		},
		"payment": payment,
		"path":    "/services/" + name,
	}
	if register {
		skillsAny := make([]any, len(spec.Agent.Skills))
		for i, s := range spec.Agent.Skills {
			skillsAny[i] = s
		}
		specMap["registration"] = map[string]any{
			"enabled":     true,
			"name":        name,
			"description": spec.Description,
			"skills":      skillsAny,
			"metadata":    agentOfferRegistrationMetadata(agentForMetadata, price, symbol, chain),
		}
	}

	soManifest := map[string]any{
		"apiVersion": "obol.org/v1alpha1",
		"kind":       "ServiceOffer",
		"metadata": map[string]any{
			"name":      name,
			"namespace": offerNs,
			// Agent-backed demos can't live in the legacy "demo"
			// namespace today (the controller's confused-deputy guard at
			// agent_resolver.go forces spec.agent.ref.namespace ==
			// offer.namespace), so the catalog renderer can't infer
			// "demo" from offer.namespace alone. The obol.org/demo
			// label is the explicit signal — keep it set here so quant
			// and friends show up under "Demo services" on the
			// storefront. Drop this once the catalog renderer's
			// cross-namespace guard is relaxed to infer demo offers
			// from their namespace.
			"labels": map[string]any{"obol.org/demo": "true"},
		},
		"spec": specMap,
	}
	applyOut, err := kubectlApplyOutput(cfg, soManifest)
	if err != nil {
		return fmt.Errorf("apply ServiceOffer: %w", err)
	}
	action := "created"
	if strings.Contains(applyOut, "configured") || strings.Contains(applyOut, "unchanged") {
		action = "updated"
	}
	u.Successf("ServiceOffer %s/%s %s (type: agent, price: %s %s/req)", offerNs, name, action, price, symbol)

	// 3. Tunnel + try-it. Re-uses the legacy demo's printDemoTryIt so the
	//    output stays consistent across hello/blocks/quant.
	tunnelURL := ""
	if tURL, terr := tunnel.EnsureTunnelForSell(cfg, u); terr != nil {
		u.Warnf("Tunnel not started: %v", terr)
		u.Dim("  Start manually with: obol tunnel restart")
	} else {
		tunnelURL = tURL
		u.Successf("Tunnel active: %s", tunnelURL)
	}

	if register {
		autoRegisterDemo(ctx, cfg, u, chain, tunnelURL)
	} else {
		u.Info("Registration skipped. The offer will still reach Ready when the agent is provisioned.")
		u.Dim("  Run on-chain discovery later: obol sell register --chain " + chain)
	}

	ready := waitForOfferReady(cfg, u, name, offerNs, 2*time.Minute)

	u.Blank()
	printDemoTryIt(u, name, typeName, price, symbol, chain, tunnelURL, ready)

	return nil
}

// resolveDefaultAgentModel picks a model to pin onto a fresh Agent CR.
// Walks the cluster's LiteLLM model_list (the same source `obol model
// list` reads), drops the wildcard `paid/*` meta route, and returns the
// top entry. The list is already in the operator's preferred order via
// `obol model prefer`, so "first usable" is a meaningful default.
//
// Concrete entries like `paid/aeon` ARE valid: they route through the
// x402-buyer sidecar to a purchased remote model. Only the literal
// `paid/*` wildcard is skipped — that is a LiteLLM routing namespace
// entry, not a model the agent can call by name.
//
// Returns an error if the cluster has no usable models — the caller
// turns this into a clear "configure a model first" message rather than
// silently picking nothing.
func resolveDefaultAgentModel(cfg *config.Config) (string, error) {
	configured, err := model.GetConfiguredModels(cfg)
	if err != nil {
		return "", err
	}
	return pickAgentDefault(configured)
}

// pickAgentDefault is the pure filter-and-pick logic for resolveDefaultAgentModel,
// extracted so it can be unit-tested without a live cluster.
func pickAgentDefault(configured []string) (string, error) {
	for _, name := range configured {
		// Skip only the literal `paid/*` wildcard meta route — that entry is
		// a LiteLLM routing namespace, not a concrete model the agent can call.
		// Concrete purchased entries like `paid/aeon` are valid and should be
		// returned when they're at the head of the rank (e.g. after
		// `obol model prefer paid/aeon`).
		if name == "paid/*" {
			continue
		}
		return name, nil
	}
	return "", fmt.Errorf("no usable LiteLLM model configured; run `obol model setup` or pull an Ollama model first")
}

// agentRefForSale is what we need to know about the referenced Agent CR
// to assemble a coherent ServiceOffer: namespace (where the offer goes
// alongside the agent), wallet address (default revenue recipient),
// objective (default registration description), and skills (registration
// metadata).
type agentRefForSale struct {
	Name          string
	Namespace     string
	WalletAddress string
	Runtime       string
	Model         string
	Objective     string
	Skills        []string
}

func getAgentRefForSale(cfg *config.Config, name string) (*agentRefForSale, error) {
	bin, kc := kubectl.Paths(cfg)
	ns := agentcrd.Namespace(name)
	out, err := kubectl.Output(bin, kc, "get", "agent", name, "-n", ns, "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("agent %q not found in namespace %s — declare it first with `obol agent new %s`", name, ns, name)
	}
	parsed, err := decodeAgentJSON(out)
	if err != nil {
		return nil, fmt.Errorf("decode agent %s: %w", name, err)
	}
	if parsed.Name == "" {
		parsed.Name = name
	}
	if parsed.Namespace == "" {
		parsed.Namespace = ns
	}
	return parsed, nil
}

// waitForAgentRefForSale polls the agent until status.walletAddress is
// set (when wallet was requested) or the timeout expires. Used by
// `obol sell agent`'s inline-create flow so the offer it builds carries
// a usable payTo when the user opted into a wallet.
func waitForAgentRefForSale(cfg *config.Config, name string, timeout time.Duration) (*agentRefForSale, error) {
	deadline := time.Now().Add(timeout)
	var last *agentRefForSale
	for time.Now().Before(deadline) {
		ref, err := getAgentRefForSale(cfg, name)
		if err == nil {
			last = ref
			if ref.WalletAddress != "" {
				return ref, nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	if last != nil {
		return last, nil
	}
	return nil, fmt.Errorf("agent %q did not appear within %s", name, timeout)
}

// decodeAgentJSON pulls the fields we care about for a sale out of a
// kubectl-rendered Agent JSON document. We only read the subset used by
// `obol sell agent`; full Agent decoding lives in the controller.
func decodeAgentJSON(raw string) (*agentRefForSale, error) {
	var doc struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec struct {
			Runtime   string   `json:"runtime"`
			Model     string   `json:"model"`
			Skills    []string `json:"skills"`
			Objective string   `json:"objective"`
		} `json:"spec"`
		Status struct {
			WalletAddress string `json:"walletAddress"`
			PinnedModel   string `json:"pinnedModel"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, err
	}
	model := strings.TrimSpace(doc.Spec.Model)
	if model == "" {
		model = strings.TrimSpace(doc.Status.PinnedModel)
	}
	runtime := strings.TrimSpace(doc.Spec.Runtime)
	if runtime == "" {
		runtime = monetizeapi.AgentRuntimeHermes
	}
	return &agentRefForSale{
		Name:          doc.Metadata.Name,
		Namespace:     doc.Metadata.Namespace,
		WalletAddress: doc.Status.WalletAddress,
		Runtime:       runtime,
		Model:         model,
		Objective:     doc.Spec.Objective,
		Skills:        append([]string(nil), doc.Spec.Skills...),
	}, nil
}

func agentOfferRegistrationMetadata(agent *agentRefForSale, price, symbol, chain string) map[string]string {
	metadata := map[string]string{
		"pricingUnit": "agent-turn",
	}
	if price = strings.TrimSpace(price); price != "" {
		metadata["x402Price"] = price
	}
	if symbol = strings.TrimSpace(symbol); symbol != "" {
		metadata["x402Asset"] = strings.ToUpper(symbol)
	}
	if chain = strings.TrimSpace(chain); chain != "" {
		metadata["x402Network"] = chain
	}
	runtime := monetizeapi.AgentRuntimeHermes
	if agent != nil && strings.TrimSpace(agent.Runtime) != "" {
		runtime = strings.TrimSpace(agent.Runtime)
	}
	metadata["runtime"] = runtime
	if agent != nil && strings.TrimSpace(agent.Model) != "" {
		metadata["model"] = strings.TrimSpace(agent.Model)
	}
	return metadata
}
