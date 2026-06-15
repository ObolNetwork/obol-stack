package main

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/buy"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/model"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/validate"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/urfave/cli/v3"
)

const (
	defaultBuyName = "default-paid"

	// defaultInteractivePerMTokCount is the default for perMTok offers:
	// 5 million tokens is enough for a meaningful chat session without
	// committing a large initial spend.
	defaultInteractivePerMTokCount = 5

	// defaultInteractivePerRequestCount is the default for perRequest
	// offers — small but useful first-purchase footprint.
	defaultInteractivePerRequestCount = 50

	// costCapMarkupBps is the default headroom over the seller's quoted
	// per-request price that we apply when --cost-cap is not set
	// explicitly. 5000 basis points = 50% above current, matching the
	// "default 50% more than current N" UX agreed in the design doc.
	costCapMarkupBps = 5000

	// maxBuyPyAuthCount mirrors buy.py's MAX_AUTH_COUNT. The host converts
	// user-facing counts (requests or million-tokens) into the exact auths
	// buy.py signs, so it must apply the same cap before presenting the
	// confirmation summary.
	maxBuyPyAuthCount = 1000

	// permit2SafeAuthCount mirrors buy.py's PERMIT2_SAFE_AUTH_COUNT: the
	// ConfigMap-backed exact-payment path can store ~537 auths before
	// hitting Kubernetes' 1MiB size limit, so permit2 buys are capped at
	// 500. Surfaced here so the host can warn users BEFORE confirmation
	// (the in-pod cap message arrives after the spend is committed).
	permit2SafeAuthCount = 500
)

func buyCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "buy",
		Usage: "Buy access to remote services via x402 micropayments",
		Commands: []*cli.Command{
			buyInferenceCommand(cfg),
		},
	}
}

func buyInferenceCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "inference",
		Usage:     "Buy inference for your agents from an x402-gated seller",
		ArgsUsage: "[<seller-url>]",
		Description: `Buy x402-gated inference from a seller.

Hand the command a seller URL (a storefront base like
"https://inference.v1337.org" or a specific offer ".../services/aeon") and
the CLI walks /api/services.json, picks the inference offer, and pre-signs
payment authorizations via the agent's remote signer. With no argument the
public ` + x402verifier.DefaultBuySellerURL + ` storefront is used.

In a TTY the flow prompts for auto-refill, request count, and confirmation.
Pass --yes / -y for non-interactive runs (--count required).

For hosted BYOK providers (Venice, OpenRouter, …) use ` + "`obol model setup`" + `
instead — that path takes the API key and wires LiteLLM directly, no x402.

Examples:
    obol buy inference
    obol buy inference https://inference.v1337.org/services/aeon
    obol buy inference https://seller.example/services/foo --yes --count 100`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "seller",
				Usage: "Seller URL (alternative to positional). When neither is set the default storefront is used.",
			},
			&cli.StringFlag{
				Name:  "name",
				Usage: "PurchaseRequest name (defaults to the seller offer name, then to \"" + defaultBuyName + "\")",
			},
			&cli.StringFlag{
				Name:  "agent",
				Usage: "Agent instance whose wallet pays for the purchase AND whose hermes config is switched to use paid/<model>. Default: the master/stack-managed agent (asked interactively in TTY).",
			},
			&cli.StringFlag{
				Name:  "model",
				Usage: "Remote model id to buy (optional; required only when the seller offers multiple inference models on this URL)",
			},
			&cli.StringFlag{
				Name:  "budget",
				Usage: "Spending cap in the payment token (e.g. \"1.5\" for 1.5 USDC). When unset the budget is derived from --count × seller price.",
			},
			&cli.StringFlag{
				Name:  "token",
				Usage: fmt.Sprintf("Payment token override. Supported: %s. By default the token is derived from the seller's offer.", strings.Join(x402verifier.SupportedTokens(), ", ")),
			},
			&cli.IntFlag{
				Name:  "count",
				Usage: "How many requests (perRequest pricing) or million-tokens (perMTok pricing) of capacity to pre-authorize. Required in non-interactive mode.",
			},
			&cli.IntFlag{
				Name:  "expected-agent-id",
				Usage: "Expected ERC-8004 agentId of the seller. Opt-in identity check; default skips verification.",
			},
			&cli.BoolFlag{
				Name:  "auto-refill",
				Usage: "Enable agent-managed top-up of the pre-authorized budget when it runs low",
			},
			&cli.IntFlag{
				Name:  "refill-threshold",
				Usage: "Sign more auths when remaining drops below this (requires --auto-refill)",
			},
			&cli.IntFlag{
				Name:  "refill-count",
				Usage: "How many auths to sign each refill (requires --auto-refill)",
			},
			&cli.StringFlag{
				Name:  "cost-cap",
				Usage: "Maximum per-request price (in payment-token base units) to accept on a refill. Skips refills that would re-sign above this ceiling.",
			},
			&cli.BoolFlag{
				Name:    "yes",
				Aliases: []string{"y"},
				Usage:   "Skip interactive confirmation prompts (required for non-TTY runs unless --count is also set)",
			},
			&cli.BoolFlag{
				Name:  "set-default",
				Usage: "Promote paid/<model> to LiteLLM's head-of-list AND sync every agent in the stack to read it. Default: prompted in TTY, off in non-interactive runs. Independent of --agent: --agent only switches the paying agent's own config.",
			},
			&cli.BoolFlag{
				Name:  "replace",
				Usage: "Delete any existing PurchaseRequest with the same name before creating a fresh one (default: top up the existing pre-authorized budget)",
			},
			&cli.BoolFlag{
				Name:  "force",
				Usage: "Pass-through to buy.py: proceed even when wallet balance is below total cost (some auths may fail on-chain)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runBuyInference(ctx, cfg, cmd)
		},
	}
}

// runBuyInference is the orchestrator for the new flow. Kept separate from
// the cli.Command literal so the steps stay scannable: resolve agent →
// resolve seller URL → pick catalog entry → resolve token+count+budget →
// confirm → exec buy.py → optional model prefer + agent sync.
func runBuyInference(ctx context.Context, cfg *config.Config, cmd *cli.Command) error {
	u := getUI(cmd)

	// If the argument names a hosted provider in the registry (venice,
	// openrouter, …) rather than a seller URL, the user wants BYOK setup,
	// not an x402 purchase. Redirect to `obol model setup`. The command
	// name stays reserved for future credit top-up flows against the same
	// remote providers.
	arg := strings.TrimSpace(cmd.String("seller"))
	if arg == "" {
		arg = strings.TrimSpace(cmd.Args().First())
	}
	if prof, ok := model.ProviderByID(arg); ok && prof.ID != model.ProviderOllama {
		return fmt.Errorf("BYOK provider setup moved — run: obol model setup --provider %s", prof.ID)
	}

	u.Info("Purchasing remote inference for running Obol Agents")

	target, err := resolveBuyAgent(cfg, cmd)
	if err != nil {
		return err
	}

	sellerURL, err := resolveSellerURL(cmd)
	if err != nil {
		return err
	}

	u.Infof("Fetching service catalog from %s …", sellerURL)
	entries, err := buy.FetchServiceCatalog(ctx, sellerURL)
	if err != nil {
		return fmt.Errorf("seller catalog: %w (pass --seller to override, or contact the operator)", err)
	}
	entry, err := buy.PickCatalogEntry(entries, sellerURL)
	if err != nil {
		return err
	}

	// Build the canonical offer URL we hand buy.py. If the caller passed a
	// storefront base, lift /services/<name> from the catalog entry.
	offerURL, err := canonicalOfferURL(sellerURL, entry)
	if err != nil {
		return err
	}

	// Resolve model: flag wins, else use the catalog entry's. Mismatch
	// errors loudly so users see the typo instead of a controller-side
	// "no such model" later.
	chosenModel, err := resolveBuyModel(cmd.String("model"), entry)
	if err != nil {
		return err
	}

	// Probe the live 402 to confirm the seller is gating this offer and to
	// recover the on-the-wire amount + asset for ValidateTokenAgainstPricing.
	u.Infof("Probing seller pricing …")
	pricing, err := buy.FetchSellerPricing(ctx, offerURL, chosenModel)
	if err != nil {
		return fmt.Errorf("pricing pre-flight: %w", err)
	}

	token, err := resolveBuyToken(cmd.String("token"), entry, pricing)
	if err != nil {
		return err
	}
	if err := buy.ValidateTokenAgainstPricing(token, pricing); err != nil {
		return err
	}

	// Optional ERC-8004 identity check — opt-in via --expected-agent-id.
	if expected := cmd.Int("expected-agent-id"); expected != 0 {
		u.Infof("Verifying seller identity (expected agentId=%d) …", expected)
		reg, err := buy.FetchSellerRegistration(ctx, offerURL)
		if err != nil {
			return fmt.Errorf("identity pre-flight: %w", err)
		}
		if err := buy.VerifySellerEndpoint(reg, offerURL); err != nil {
			return err
		}
		if err := buy.VerifyAgentIDForPricing(reg, int64(expected), pricing); err != nil {
			return err
		}
		u.Infof("Identity OK: agentId=%d", expected)
	}

	// Inspect any existing PurchaseRequest for this name in the agent's
	// namespace — we either top up (default) or replace (--replace).
	prName := strings.TrimSpace(cmd.String("name"))
	if prName == "" {
		prName = strings.TrimSpace(entry.Name)
	}
	if prName == "" {
		prName = defaultBuyName
	}
	if err := validate.Name(prName); err != nil {
		return err
	}

	existing, err := getPurchaseRequest(cfg, target, prName)
	if err != nil {
		return err
	}
	mode, err := resolveBuyMode(u, cmd, existing)
	if err != nil {
		return err
	}
	if mode == buyModeReplace && existing != "" {
		u.Infof("Deleting existing PurchaseRequest %s/%s before creating a fresh one …", prName, agentruntime.Namespace(target.Runtime, target.ID))
		if err := deletePurchaseRequest(cfg, target, prName); err != nil {
			return err
		}
		existing = "" // top-up logic below shouldn't think one is still there
	}

	priceAtomic, priceUnit, err := pricingPerUnit(entry, pricing)
	if err != nil {
		return err
	}

	autoRefill, err := promptAutoRefill(u, cmd, priceUnit)
	if err != nil {
		return err
	}

	tokenDecimalsForPrompt := resolveTokenDecimals(entry, token, paymentChainForDisplay(pricing))
	count, err := promptCount(u, cmd, priceUnit, existing != "", token, priceAtomic, tokenDecimalsForPrompt)
	if err != nil {
		return err
	}

	multiplier := authMultiplier(priceUnit)
	requestedAuths := count * multiplier
	auths, capped, capReason := applyAuthCap(entry, requestedAuths)
	pricePerAuthAtomic, err := pricePerAuth(priceAtomic, multiplier)
	if err != nil {
		return err
	}

	budgetAtomic, err := resolveBudget(cmd.String("budget"), token, auths, pricePerAuthAtomic)
	if err != nil {
		return err
	}

	costCapAtomic, err := resolveCostCap(cmd.String("cost-cap"), token, priceAtomic, autoRefill.enabled)
	if err != nil {
		return err
	}

	// Wallet preflight: surface the paying address + balance before the
	// final confirmation. The agent pod walks the signer + eRPC for us, so
	// failures here are non-fatal (we still let the buy proceed if e.g.
	// the chain alias isn't installed — buy.py re-checks balance itself).
	chainForDisplay := paymentChainForDisplay(pricing)
	walletInfo := fetchWalletInfoBestEffort(cfg, target, token, chainForDisplay, u)
	decimals := resolveTokenDecimals(entry, token, chainForDisplay)

	autoRefillThresholdAuths, autoRefillCountAuths, autoRefillCapped, autoRefillCapReason := effectiveAutoRefillAuths(autoRefill, entry, multiplier)

	summary := buySummary{
		Agent:               target,
		PRName:              prName,
		OfferURL:            offerURL,
		Model:               chosenModel,
		RequestedAuths:      requestedAuths,
		Auths:               auths,
		Multiplier:          multiplier,
		Capped:              capped,
		CapReason:           capReason,
		PriceAtomic:         priceAtomic,
		PricePerAuthAtomic:  pricePerAuthAtomic,
		PriceUnit:           priceUnit,
		Token:               token,
		TokenDecimals:       decimals,
		BudgetAtomic:        budgetAtomic,
		AutoRefill:          autoRefill,
		AutoRefillThreshold: autoRefillThresholdAuths,
		AutoRefillCount:     autoRefillCountAuths,
		AutoRefillCapped:    autoRefillCapped,
		AutoRefillCapReason: autoRefillCapReason,
		CostCapAtomic:       costCapAtomic,
		Wallet:              walletInfo,
		Mode:                mode,
	}
	printBuySummary(u, summary)
	if !confirmBuy(u, cmd) {
		return errors.New("buy inference cancelled")
	}

	perAgent, global := resolveDefaultScopes(u, cmd, chosenModel, target)

	// buy.py operates in per-auth wire units (each auth pays the per-request
	// wire amount). multiplier was set above when we computed the permit2
	// cap. Refill threshold/count stay in natural units and get multiplied
	// the same way.
	costCapForBuyPy := costCapAtomic
	if costCapForBuyPy != nil && costCapForBuyPy.Sign() > 0 && priceUnit == "perMTok" {
		// Cost cap is stored as per-MTok atomic; buy.py compares against
		// per-request wire amounts, so divide before passing.
		costCapForBuyPy = new(big.Int).Quo(costCapForBuyPy, big.NewInt(int64(schemas.ApproxTokensPerRequest)))
	}
	argv := buildBuyPyArgv(buyPyOptions{
		Runtime:         target.Runtime,
		Name:            prName,
		Seller:          offerURL,
		Model:           chosenModel,
		BudgetMicro:     budgetAtomic.String(),
		Count:           auths,
		AutoRefill:      autoRefill.enabled,
		RefillThreshold: autoRefillThresholdAuths,
		RefillCount:     autoRefillCountAuths,
		CostCap:         costCapForBuyPy,
		Force:           cmd.Bool("force"),
		// Per-agent default lives entirely in-pod: buy.py edits the agent's
		// hermes-config to set model.default = paid/<model> for the agent
		// whose wallet just paid. No global LiteLLM reorder, no other
		// agents touched. Global default is handled below via host-side
		// model prefer + agent sync.
		SetDefault: perAgent,
	})

	u.Infof("Dispatching to agent %s (instance=%s) …", target.Runtime, target.ID)
	if err := agentruntime.ExecInPod(cfg, target.Runtime, target.ID, argv); err != nil {
		return err
	}

	if global {
		if err := promoteAndSyncModel(cfg, u, chosenModel); err != nil {
			u.Warnf("Set-default (global) partially failed: %v", err)
			u.Dim("  The purchase landed; rerun `obol model prefer paid/" + chosenModel + " && obol agent sync` to retry.")
		}
	} else if perAgent {
		// Per-agent set-default lands inside the agent pod (buy.py edits
		// hermes-config). It does NOT touch the global LiteLLM model_list
		// ranking, so `obol model list` still shows the previous head —
		// surface that explicitly so users aren't confused when the
		// "ranking" doesn't budge.
		u.Blank()
		u.Infof("paid/%s is now the default model for %s/%s.", chosenModel, target.Runtime, target.ID)
		u.Dim("  Other agents are unchanged. To make this the default for every agent in the stack:")
		u.Dim(fmt.Sprintf("    obol model prefer paid/%s", chosenModel))
	}
	return nil
}

// buyPyOptions captures everything needed to invoke `buy.py buy` inside the
// agent pod. Kept as a flat struct so buildBuyPyArgv stays trivially testable.
//
// All count fields (Count, RefillThreshold, RefillCount) are in buy.py's
// native per-auth wire units. The host CLI converts from natural units
// (requests | million-tokens) before populating these fields.
type buyPyOptions struct {
	Runtime         agentruntime.Runtime
	Name            string
	Seller          string
	Model           string
	BudgetMicro     string
	Count           int // explicit auth count; 0 lets buy.py derive from budget
	AutoRefill      bool
	RefillThreshold int
	RefillCount     int
	CostCap         *big.Int // emitted only with AutoRefill; caps future top-up prices
	Force           bool
	SetDefault      bool
}

// buildBuyPyArgv composes the argv for `python3 buy.py buy <name> ...`.
func buildBuyPyArgv(opts buyPyOptions) []string {
	argv := buy.BuyPyCommand(opts.Runtime, "buy", opts.Name,
		"--endpoint", opts.Seller,
		"--budget", opts.BudgetMicro,
	)
	if opts.Count > 0 {
		argv = append(argv, "--count", fmt.Sprintf("%d", opts.Count))
	}
	if m := strings.TrimSpace(opts.Model); m != "" {
		argv = append(argv, "--model", m)
	}
	if opts.AutoRefill {
		argv = append(argv, "--auto-refill")
		if opts.RefillThreshold > 0 {
			argv = append(argv, "--refill-threshold", fmt.Sprintf("%d", opts.RefillThreshold))
		}
		if opts.RefillCount > 0 {
			argv = append(argv, "--refill-count", fmt.Sprintf("%d", opts.RefillCount))
		}
	}
	if opts.AutoRefill && opts.CostCap != nil && opts.CostCap.Sign() > 0 {
		argv = append(argv, "--cost-cap", opts.CostCap.String())
	}
	if opts.SetDefault {
		argv = append(argv, "--set-default")
	}
	if opts.Force {
		argv = append(argv, "--force")
	}
	return argv
}

// budgetToBaseUnits parses a human-readable token amount (e.g. "10" for 10
// USDC, "0.023" for 0.023 OBOL) and returns the equivalent in atomic base
// units as a base-10 integer string.
func budgetToBaseUnits(amount, token string) (string, error) {
	s := strings.TrimSpace(amount)
	if s == "" {
		return "", errors.New("--budget is empty")
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return "", fmt.Errorf("--budget %q is not a valid number", amount)
	}
	if r.Sign() <= 0 {
		return "", fmt.Errorf("--budget %q must be positive", amount)
	}

	tok := strings.ToUpper(strings.TrimSpace(token))
	if tok == "" {
		tok = "USDC"
	}

	chains := x402verifier.ChainsForToken(tok)
	if len(chains) == 0 {
		supported := strings.Join(x402verifier.SupportedTokens(), ", ")
		return "", fmt.Errorf("--token %q is not a known payment token; supported tokens: %s", token, supported)
	}
	entry, _ := x402verifier.ResolveToken(tok, chains[0])
	decimals := entry.Decimals

	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	base := new(big.Rat).Mul(r, scale)
	if !base.IsInt() {
		return "", fmt.Errorf("--budget %q has more precision than %s supports (%d decimals)", amount, tok, decimals)
	}
	return base.Num().String(), nil
}

// ── Helpers ─────────────────────────────────────────────────────────────────

type buyMode int

const (
	buyModeFresh   buyMode = iota // no existing PurchaseRequest
	buyModeTopUp                  // append auths to existing PR
	buyModeReplace                // delete then recreate
)

type autoRefillPolicy struct {
	enabled   bool
	threshold int
	count     int
}

type buySummary struct {
	Agent               agentTarget
	PRName              string
	OfferURL            string
	Model               string
	RequestedAuths      int      // pre-cap auths implied by the user's natural-unit count
	Auths               int      // post-cap auths buy.py will actually sign
	Multiplier          int      // auths per natural unit: 1 for perRequest, ~1000 for perMTok
	Capped              bool     // true when the auth cap reduced the request
	CapReason           string   // e.g. "Permit2 storage limit" or "buy.py signing limit"
	PriceAtomic         *big.Int // per natural unit
	PricePerAuthAtomic  *big.Int // per buy.py auth / settled request
	PriceUnit           string   // "perRequest" | "perMTok"
	Token               string
	TokenDecimals       int // for human formatting; catalog wins, ResolveToken fallback
	BudgetAtomic        *big.Int
	AutoRefill          autoRefillPolicy
	AutoRefillThreshold int
	AutoRefillCount     int
	AutoRefillCapped    bool
	AutoRefillCapReason string
	CostCapAtomic       *big.Int
	Wallet              *buy.WalletInfo
	Mode                buyMode
}

// resolveBuyAgent picks the agent instance whose wallet pays the bill. When
// --agent is set we treat it as authoritative; otherwise we use the same
// resolution logic as `obol agent sync` (prefer the master Hermes instance,
// fall back to the only instance when there's exactly one).
func resolveBuyAgent(cfg *config.Config, cmd *cli.Command) (agentTarget, error) {
	if name := strings.TrimSpace(cmd.String("agent")); name != "" {
		return resolveAnyAgentTarget(cfg, []string{name})
	}
	return resolveAnyAgentTarget(cfg, nil)
}

func resolveSellerURL(cmd *cli.Command) (string, error) {
	if v := strings.TrimSpace(cmd.Args().First()); v != "" {
		if !looksLikeURL(v) {
			return "", fmt.Errorf("positional argument %q does not look like a URL — pass --name for a custom PurchaseRequest name and a URL as the positional argument", v)
		}
		return v, nil
	}
	if v := strings.TrimSpace(cmd.String("seller")); v != "" {
		return v, nil
	}
	return x402verifier.DefaultBuySellerURL, nil
}

func looksLikeURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// canonicalOfferURL produces the /services/<name> URL we hand to buy.py.
// Real-world catalogs (inference.v1337.org) store `endpoint` as an
// absolute URL ("https://host/services/<name>"); older/local controllers
// emit a path-only form ("/services/<name>"). Handle both so we don't end
// up concatenating the storefront base onto a full URL.
func canonicalOfferURL(userURL string, entry *buy.CatalogEntry) (string, error) {
	if entry == nil {
		return "", errors.New("catalog entry is nil")
	}
	// If the user URL already has a /services/<name> segment, trust it.
	// Preserves any explicit hostname they typed (useful for behind-tunnel
	// dev hosts where the catalog still publishes the public hostname).
	if strings.Contains(userURL, "/services/") {
		return strings.TrimRight(userURL, "/"), nil
	}

	endpoint := strings.TrimSpace(entry.Endpoint)
	if endpoint == "" {
		return "", fmt.Errorf("catalog entry for %q has empty endpoint", entry.Name)
	}
	// Strip /v1/chat/completions so buy.py / FetchSellerPricing can re-add
	// the conventional suffix themselves.
	for _, suffix := range []string{"/v1/chat/completions", "/chat/completions"} {
		endpoint = strings.TrimSuffix(endpoint, suffix)
	}
	endpoint = strings.TrimRight(endpoint, "/")

	// Absolute URL — use verbatim. Don't splice onto the user's base; the
	// catalog's hostname is the seller's source of truth.
	if looksLikeURL(endpoint) {
		return endpoint, nil
	}

	// Path-only — splice onto the user's scheme+host.
	base := strings.TrimRight(userURL, "/")
	if !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}
	return base + endpoint, nil
}

func resolveBuyModel(flag string, entry *buy.CatalogEntry) (string, error) {
	flag = strings.TrimSpace(flag)
	entryModel := strings.TrimSpace(entry.Model)

	if flag != "" {
		if entryModel != "" && !strings.EqualFold(flag, entryModel) {
			return "", fmt.Errorf("--model %q does not match seller offer model %q for %s", flag, entryModel, entry.Name)
		}
		return flag, nil
	}
	if entryModel == "" {
		return "", fmt.Errorf("seller offer %q advertises no model id — pass --model explicitly", entry.Name)
	}
	return entryModel, nil
}

func resolveBuyToken(flag string, entry *buy.CatalogEntry, pricing *buy.PricingResponse) (string, error) {
	flag = strings.ToUpper(strings.TrimSpace(flag))
	if flag != "" {
		return flag, nil
	}
	// Prefer the catalog-advertised asset symbol over the 402-derived one
	// — the controller publishes a normalized symbol while the 402 only
	// carries a contract address.
	if entry != nil && entry.Asset != nil && strings.TrimSpace(entry.Asset.Symbol) != "" {
		return strings.ToUpper(strings.TrimSpace(entry.Asset.Symbol)), nil
	}
	// Last-ditch: walk the supported token registry against the 402's
	// asset/network so non-Obol storefronts that omit Asset still work.
	if pricing != nil && len(pricing.Accepts) > 0 {
		accept := pricing.Accepts[0]
		for _, sym := range x402verifier.SupportedTokens() {
			chains := x402verifier.ChainsForToken(sym)
			for _, chain := range chains {
				if entry, ok := x402verifier.ResolveToken(sym, chain); ok {
					if strings.EqualFold(entry.Address, accept.Asset) {
						return sym, nil
					}
				}
			}
		}
	}
	return "USDC", nil
}

// pricingPerUnit returns the atomic per-NATURAL-unit price and the unit
// label. Natural unit means: per-request for perRequest offers, per-MTok
// for perMTok offers. The catalog's priceAtomicUnits is the authoritative
// value (controller derives it from priceRaw × 10^decimals); we fall back
// to deriving from the 402 wire amount when the catalog field is empty.
//
// Conversion to buy.py's per-auth wire units happens at argv-build time,
// not here, so the rest of the flow can reason in natural units.
func pricingPerUnit(entry *buy.CatalogEntry, pricing *buy.PricingResponse) (*big.Int, string, error) {
	unit := "perRequest"
	if entry != nil && strings.TrimSpace(entry.PriceUnit) != "" {
		unit = entry.PriceUnit
	}
	// Catalog-derived atomic is the natural-unit price the seller renders
	// at /api/services.json. Use it when present.
	if entry != nil && strings.TrimSpace(entry.PriceAtomicUnits) != "" {
		if p, ok := new(big.Int).SetString(strings.TrimSpace(entry.PriceAtomicUnits), 10); ok && p.Sign() > 0 {
			return p, unit, nil
		}
	}
	if pricing == nil || len(pricing.Accepts) == 0 {
		return nil, unit, errors.New("pricing response has no payment options")
	}
	accept := pricing.Accepts[0]
	raw := accept.Amount
	if strings.TrimSpace(raw) == "" {
		raw = accept.MaxAmountRequired
	}
	wire, ok := new(big.Int).SetString(strings.TrimSpace(raw), 10)
	if !ok || wire.Sign() <= 0 {
		return nil, unit, fmt.Errorf("invalid wire price %q", raw)
	}
	// Wire is always per-request. For perMTok offers, multiply by ~1000
	// tokens-per-request to recover the per-MTok ceiling.
	if unit == "perMTok" {
		wire = new(big.Int).Mul(wire, big.NewInt(int64(schemas.ApproxTokensPerRequest)))
	}
	return wire, unit, nil
}

// promptAutoRefill resolves --auto-refill / --refill-threshold /
// --refill-count from flags, then interactively fills in the missing pieces
// when in a TTY. In non-interactive mode the policy is exactly what the
// flags described. Threshold and count are in NATURAL units (requests for
// perRequest offers, million-tokens for perMTok); the buy.py argv builder
// converts to per-auth wire units when sending to the agent.
func promptAutoRefill(u *ui.UI, cmd *cli.Command, priceUnit string) (autoRefillPolicy, error) {
	enabled := cmd.Bool("auto-refill")
	threshold := cmd.Int("refill-threshold")
	count := cmd.Int("refill-count")

	if !u.IsTTY() || cmd.Bool("yes") {
		return autoRefillPolicy{enabled: enabled, threshold: threshold, count: count}, nil
	}

	if !enabled {
		enabled = u.Confirm("Enable auto-top-up? Adds more pre-authorized inference from the agent's wallet when it runs low.", false)
	}
	if !enabled {
		return autoRefillPolicy{}, nil
	}
	unit := summaryUnit(priceUnit)
	if threshold == 0 {
		v, err := promptPositiveInt(u, fmt.Sprintf("Top up when remaining %s drop below", unit), 5)
		if err != nil {
			return autoRefillPolicy{}, err
		}
		threshold = v
	}
	if count == 0 {
		v, err := promptPositiveInt(u, fmt.Sprintf("%s to add per top-up", capitalize(unit)), 25)
		if err != nil {
			return autoRefillPolicy{}, err
		}
		count = v
	}
	return autoRefillPolicy{enabled: true, threshold: threshold, count: count}, nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// promptCount drives the request-or-mtoks count prompt. In non-TTY mode
// --count is mandatory unless --yes was set (in which case we fall back to
// the interactive default — explicit consent suppresses the safety check).
//
// The interactive default is unit-aware (5 million tokens for perMTok, 50
// requests for perRequest) and the prompt surfaces the ceiling cost inline
// after the default chip: "... [5] (5 OBOL):".
func promptCount(u *ui.UI, cmd *cli.Command, priceUnit string, existing bool, token string, price *big.Int, decimals int) (int, error) {
	if v := cmd.Int("count"); v > 0 {
		return v, nil
	}
	def := defaultCountForUnit(priceUnit)
	if !u.IsTTY() {
		if cmd.Bool("yes") {
			return def, nil
		}
		return 0, errors.New("--count is required in non-interactive mode (pass --count <N>, or run from a TTY)")
	}
	label := "How many requests to pre-authorize?"
	if priceUnit == "perMTok" {
		label = "How many million tokens of inference to pre-authorize?"
	}
	if existing {
		label = "How many more " + summaryUnit(priceUnit) + " to add to the existing pool?"
	}
	suffix := ""
	if cost := previewCost(def, price, decimals); cost != "" {
		suffix = fmt.Sprintf("(%s %s)", cost, token)
	}
	return promptPositiveIntWithSuffix(u, label, suffix, def)
}

func defaultCountForUnit(priceUnit string) int {
	if priceUnit == "perMTok" {
		return defaultInteractivePerMTokCount
	}
	return defaultInteractivePerRequestCount
}

// summaryUnit returns the user-facing label for the natural unit ("requests"
// or "million tokens"). Singular/plural is intentionally collapsed —
// summary copy reads the same whether the count is 1 or 50.
func summaryUnit(priceUnit string) string {
	if priceUnit == "perMTok" {
		return "million tokens"
	}
	return "requests"
}

// previewCost returns the "≈ X TOKEN" string for `count` units at `price`
// each, formatted with `decimals`. Empty string when price is unavailable.
func previewCost(count int, price *big.Int, decimals int) string {
	if count <= 0 || price == nil || price.Sign() <= 0 {
		return ""
	}
	total := new(big.Int).Mul(price, big.NewInt(int64(count)))
	return formatTokenAmount(total, decimals)
}

func authMultiplier(priceUnit string) int {
	if priceUnit == "perMTok" {
		return schemas.ApproxTokensPerRequest
	}
	return 1
}

// perRequestEstimate divides a per-MTok atomic price by the temporary
// tokens-per-request constant the controller uses today. Until the
// facilitator implements usage-based settlement, this is the actual
// flat per-call charge a buyer pays — surface it so users can compare
// against perRequest offers without converting in their head.
func perRequestEstimate(perMTokAtomic *big.Int) *big.Int {
	if perMTokAtomic == nil {
		return nil
	}
	return new(big.Int).Quo(perMTokAtomic, big.NewInt(int64(schemas.ApproxTokensPerRequest)))
}

func pricePerAuth(price *big.Int, multiplier int) (*big.Int, error) {
	if price == nil || price.Sign() <= 0 {
		return nil, errors.New("internal: price is non-positive")
	}
	if multiplier <= 1 {
		return new(big.Int).Set(price), nil
	}
	perAuth := new(big.Int).Quo(price, big.NewInt(int64(multiplier)))
	if perAuth.Sign() <= 0 {
		return nil, fmt.Errorf("price %s divided by auth multiplier %d rounds to zero", price, multiplier)
	}
	return perAuth, nil
}

// formatTokenAmount renders an atomic-units value as a human decimal with
// the minimum number of fractional digits needed: whole values show as
// "1" / "5", dust keeps enough digits to stay non-zero ("0.001"), and
// fractional balances stop at the last significant digit ("0.1", "12.34").
func formatTokenAmount(v *big.Int, decimals int) string {
	if v == nil {
		return "?"
	}
	if decimals <= 0 {
		decimals = 6
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	r := new(big.Rat).SetFrac(v, scale)
	s := r.FloatString(decimals)
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	return s
}

func capacityLabel(auths, multiplier int, priceUnit string) string {
	if auths < 0 {
		auths = 0
	}
	if multiplier <= 1 || priceUnit != "perMTok" {
		return fmt.Sprintf("%d requests", auths)
	}
	r := new(big.Rat).SetFrac(big.NewInt(int64(auths)), big.NewInt(int64(multiplier)))
	s := r.FloatString(6)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "" {
		s = "0"
	}
	return s + " million tokens"
}

func promptPositiveInt(u *ui.UI, label string, def int) (int, error) {
	return promptPositiveIntWithSuffix(u, label, "", def)
}

func promptPositiveIntWithSuffix(u *ui.UI, label, suffix string, def int) (int, error) {
	for attempt := 0; attempt < 3; attempt++ {
		raw, err := u.InputWithSuffix(label, fmt.Sprintf("%d", def), suffix)
		if err != nil {
			return 0, err
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return def, nil
		}
		v, err := parsePositiveInt(raw)
		if err == nil {
			return v, nil
		}
		u.Warnf("%v", err)
	}
	return 0, fmt.Errorf("invalid input for %q after 3 attempts", label)
}

func parsePositiveInt(s string) (int, error) {
	v := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a positive integer: %q", s)
		}
		v = v*10 + int(r-'0')
	}
	if v <= 0 {
		return 0, fmt.Errorf("must be > 0: %q", s)
	}
	return v, nil
}

// resolveBudget computes the actual atomic spend represented by authCount ×
// per-auth price. Explicit --budget remains a user cap: it must cover the
// auths we are about to ask buy.py to sign, but it does not inflate the
// displayed or passed budget above the exact signed total.
func resolveBudget(flag, token string, authCount int, perAuthPrice *big.Int) (*big.Int, error) {
	if authCount <= 0 || perAuthPrice == nil || perAuthPrice.Sign() <= 0 {
		return nil, errors.New("internal: auth count or price is non-positive")
	}
	required := new(big.Int).Mul(perAuthPrice, big.NewInt(int64(authCount)))
	if flag = strings.TrimSpace(flag); flag != "" {
		s, err := budgetToBaseUnits(flag, token)
		if err != nil {
			return nil, err
		}
		v, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return nil, fmt.Errorf("internal: budgetToBaseUnits returned non-numeric %q", s)
		}
		if v.Cmp(required) < 0 {
			return nil, fmt.Errorf("--budget %s is below the requested pre-authorization cost %s base units; reduce --count or raise --budget", v.String(), required.String())
		}
	}
	return required, nil
}

// resolveCostCap parses --cost-cap into atomic units, or computes a default
// of price × (1 + costCapMarkupBps/10000) when auto-refill is enabled.
func resolveCostCap(flag, token string, price *big.Int, autoRefill bool) (*big.Int, error) {
	if flag = strings.TrimSpace(flag); flag != "" {
		if !autoRefill {
			return nil, errors.New("--cost-cap requires --auto-refill because it only applies to future auto-top-ups")
		}
		// --cost-cap is in atomic units (matches buy.py convention).
		v, ok := new(big.Int).SetString(flag, 10)
		if !ok || v.Sign() <= 0 {
			// Try human-readable parsing as a fallback so users who
			// type "0.05" still get the right thing.
			s, err := budgetToBaseUnits(flag, token)
			if err != nil {
				return nil, fmt.Errorf("--cost-cap %q is neither base units nor a valid decimal amount: %w", flag, err)
			}
			parsed, ok := new(big.Int).SetString(s, 10)
			if !ok {
				return nil, fmt.Errorf("--cost-cap %q failed to parse", flag)
			}
			return parsed, nil
		}
		return v, nil
	}
	if !autoRefill || price == nil || price.Sign() <= 0 {
		return nil, nil
	}
	cap := new(big.Int).Mul(price, big.NewInt(int64(10000+costCapMarkupBps)))
	cap.Quo(cap, big.NewInt(10000))
	return cap, nil
}

func authCapForEntry(entry *buy.CatalogEntry) (int, string) {
	if entry == nil || entry.Asset == nil {
		return maxBuyPyAuthCount, "buy.py signing limit"
	}
	if strings.EqualFold(strings.TrimSpace(entry.Asset.TransferMethod), "permit2") {
		return permit2SafeAuthCount, "Permit2 storage limit"
	}
	return maxBuyPyAuthCount, "buy.py signing limit"
}

func applyAuthCap(entry *buy.CatalogEntry, requestedAuths int) (int, bool, string) {
	limit, reason := authCapForEntry(entry)
	if requestedAuths <= limit {
		return requestedAuths, false, ""
	}
	if limit < 1 {
		limit = 1
	}
	return limit, true, reason
}

func effectiveAutoRefillAuths(policy autoRefillPolicy, entry *buy.CatalogEntry, multiplier int) (thresholdAuths, countAuths int, capped bool, reason string) {
	if !policy.enabled {
		return 0, 0, false, ""
	}
	if multiplier <= 0 {
		multiplier = 1
	}
	thresholdAuths = policy.threshold * multiplier
	countAuths = policy.count * multiplier
	limit, reason := authCapForEntry(entry)
	if thresholdAuths > limit {
		thresholdAuths = limit
		capped = true
	}
	if countAuths > limit {
		countAuths = limit
		capped = true
	}
	if !capped {
		reason = ""
	}
	return thresholdAuths, countAuths, capped, reason
}

// resolveTokenDecimals picks the most reliable decimals source for the
// chosen payment token. The catalog asset is authoritative when present;
// otherwise we fall through to the in-binary token registry with the
// payment chain explicit (avoids the empty-chain ResolveToken bug that
// silently fell back to 6-decimal scaling).
func resolveTokenDecimals(entry *buy.CatalogEntry, token, chain string) int {
	if entry != nil && entry.Asset != nil && entry.Asset.Decimals > 0 {
		return int(entry.Asset.Decimals)
	}
	if t, ok := x402verifier.ResolveToken(token, chain); ok && t.Decimals > 0 {
		return t.Decimals
	}
	// Last resort: USDC-style 6 decimals. Logging the fallback at the call
	// site is the caller's job — here we just return a deterministic value
	// so the summary doesn't crash with a divide-by-zero scale.
	return 6
}

func paymentChainForDisplay(pricing *buy.PricingResponse) string {
	if pricing == nil || len(pricing.Accepts) == 0 {
		return x402verifier.DefaultBuySellerChain
	}
	net := strings.TrimSpace(pricing.Accepts[0].Network)
	if net == "" {
		return x402verifier.DefaultBuySellerChain
	}
	switch net {
	case "eip155:1":
		return "mainnet"
	case "eip155:8453":
		return "base"
	case "eip155:84532":
		return "base-sepolia"
	}
	return net
}

func fetchWalletInfoBestEffort(cfg *config.Config, target agentTarget, token, chain string, u *ui.UI) *buy.WalletInfo {
	info, err := buy.FetchWalletInfo(cfg, target.Runtime, target.ID, token, chain)
	if err != nil {
		u.Dim(fmt.Sprintf("  (wallet balance preflight skipped: %v)", err))
		return nil
	}
	return info
}

// printBuySummary mirrors the design-doc summary line so the user sees
// exactly what they're signing before the confirmation.
func printBuySummary(u *ui.UI, s buySummary) {
	human := func(v *big.Int) string {
		return formatTokenAmount(v, s.TokenDecimals)
	}

	u.Print("")
	u.Print("─── Purchase summary ─────────────────────────────────────────")
	u.Print(fmt.Sprintf("  Offer:       %s", s.OfferURL))
	u.Print(fmt.Sprintf("  Model:       %s", s.Model))
	switch s.PriceUnit {
	case "perMTok":
		// Today both pricing units settle at a flat per-request charge
		// (the perMTok value is divided by schemas.ApproxTokensPerRequest
		// to derive a fixed wire amount). Token-metered settlement is
		// part of the x402 spec but not implemented on the Obol
		// facilitator yet — don't claim "unused capacity isn't charged"
		// until it actually isn't.
		u.Print(fmt.Sprintf("  Price:           %s %s per million tokens (≈ %s/request at ~1000 tokens/request)", human(s.PriceAtomic), s.Token, human(s.PricePerAuthAtomic)))
		u.Print(fmt.Sprintf("  Pre-authorizing: up to %s (≈ %s %s ceiling)", capacityLabel(s.Auths, s.Multiplier, s.PriceUnit), human(s.BudgetAtomic), s.Token))
		u.Print("                   Settled per-request at the seller's quoted estimate; token-metered settlement is on the roadmap.")
	default:
		u.Print(fmt.Sprintf("  Price:           %s %s per request", human(s.PriceAtomic), s.Token))
		u.Print(fmt.Sprintf("  Pre-authorizing: %s (≈ %s %s ceiling)", capacityLabel(s.Auths, s.Multiplier, s.PriceUnit), human(s.BudgetAtomic), s.Token))
		u.Print("                   Settled per-request after each successful response.")
	}
	if s.Capped {
		// Tell the user upfront when the exact auth count was trimmed before
		// dispatch, so the summary cannot overstate the spend cap.
		u.Warnf("  Pre-authorization capped: %s requested → %s allowed per buy (%s).",
			capacityLabel(s.RequestedAuths, s.Multiplier, s.PriceUnit),
			capacityLabel(s.Auths, s.Multiplier, s.PriceUnit),
			s.CapReason)
		u.Dim("  To get the full ask: enable --auto-top-up and re-run with the original count, or run `obol buy inference` again after this buy lands.")
	}
	if s.AutoRefill.enabled {
		if s.AutoRefillThreshold > 0 || s.AutoRefillCount > 0 {
			u.Print(fmt.Sprintf("  Auto-top-up: yes (top up when remaining < %s, add %s each time)",
				capacityLabel(s.AutoRefillThreshold, s.Multiplier, s.PriceUnit),
				capacityLabel(s.AutoRefillCount, s.Multiplier, s.PriceUnit)))
		} else {
			u.Print("  Auto-top-up: yes (agent defaults)")
		}
		if s.AutoRefillCapped {
			u.Warnf("  Auto-top-up policy capped to fit %s.", s.AutoRefillCapReason)
		}
	} else {
		u.Print("  Auto-top-up: no")
	}
	if s.CostCapAtomic != nil && s.CostCapAtomic.Sign() > 0 {
		capUnit := "request"
		if s.PriceUnit == "perMTok" {
			capUnit = "million tokens"
		}
		u.Print(fmt.Sprintf("  Cost cap:    auto-top-ups skipped above %s %s per %s", human(s.CostCapAtomic), s.Token, capUnit))
	}
	if s.Wallet != nil {
		u.Print(fmt.Sprintf("  Paid from:   %s (balance %s %s on %s)", s.Wallet.Address, s.Wallet.HumanBalance(), s.Token, s.Wallet.Chain))
	} else {
		u.Print(fmt.Sprintf("  Paid from:   agent wallet (instance %s/%s)", s.Agent.Runtime, s.Agent.ID))
		u.Print("  (wallet balance preflight skipped — buy.py will re-check inside the pod)")
	}
	if s.Wallet != nil && s.BudgetAtomic != nil && s.Wallet.AtomicUnits != nil && s.Wallet.AtomicUnits.Cmp(s.BudgetAtomic) < 0 {
		u.Warnf("  Wallet balance is below the requested budget — some auths will fail to settle on-chain.")
		u.Print(fmt.Sprintf("  Top up the wallet (%s, %s on %s) before proceeding, or rerun with --force to sign anyway.", s.Wallet.Address, s.Token, s.Wallet.Chain))
	}
	switch s.Mode {
	case buyModeTopUp:
		u.Print("  Existing PR: topping up the existing pre-authorized budget")
	case buyModeReplace:
		u.Print("  Existing PR: replacing the existing PurchaseRequest")
	}
	u.Print("──────────────────────────────────────────────────────────────")
}

func confirmBuy(u *ui.UI, cmd *cli.Command) bool {
	if cmd.Bool("yes") {
		return true
	}
	if !u.IsTTY() {
		// Non-TTY without --yes — never silently proceed. Caller will see
		// the printed summary then the error.
		return false
	}
	return u.Confirm("Confirm purchase?", true)
}

// resolveDefaultScopes returns (perAgent, global) bools controlling the
// post-buy "use this model" behavior:
//
//   - perAgent → invoke buy.py `--set-default`, which atomically edits the
//     paying agent's hermes-config inside the pod. Other agents and the
//     LiteLLM model_list head are untouched.
//   - global → run `obol model prefer paid/<model>` + `obol agent sync`
//     from the host. Reorders LiteLLM's model_list and rolls every agent
//     so head-of-list pickers see the new primary.
//
// They're independent. --agent X implies perAgent (you asked for X to use
// this) but never global. --set-default explicitly toggles global.
// Interactive mode asks only the per-agent question (default Y) because
// the global toggle has wider blast radius and surprised users in early
// testing.
func resolveDefaultScopes(u *ui.UI, cmd *cli.Command, model string, target agentTarget) (perAgent, global bool) {
	if cmd.IsSet("set-default") {
		global = cmd.Bool("set-default")
	}
	if cmd.IsSet("agent") {
		perAgent = true
		return
	}
	if cmd.Bool("yes") || !u.IsTTY() {
		// Non-interactive default: do not touch any agent's config unless
		// the operator was explicit (via --agent or --set-default).
		return
	}
	perAgent = u.Confirm(
		fmt.Sprintf("Switch %s/%s to use paid/%s as its inference model?", target.Runtime, target.ID, model),
		true,
	)
	return
}

func promoteAndSyncModel(cfg *config.Config, u *ui.UI, modelID string) error {
	paidName := "paid/" + modelID
	if err := model.PreferModels(cfg, u, []string{paidName}); err != nil {
		return fmt.Errorf("model prefer %s: %w", paidName, err)
	}
	hermesIDs, err := agentruntime.ListInstanceIDs(cfg, agentruntime.Hermes)
	if err != nil {
		return fmt.Errorf("list hermes instances: %w", err)
	}
	openclawIDs, err := agentruntime.ListInstanceIDs(cfg, agentruntime.OpenClaw)
	if err != nil {
		return fmt.Errorf("list openclaw instances: %w", err)
	}
	for _, id := range hermesIDs {
		if err := syncAgentTarget(cfg, agentTarget{Runtime: agentruntime.Hermes, ID: id}, u); err != nil {
			return fmt.Errorf("sync hermes/%s: %w", id, err)
		}
	}
	for _, id := range openclawIDs {
		if err := syncAgentTarget(cfg, agentTarget{Runtime: agentruntime.OpenClaw, ID: id}, u); err != nil {
			return fmt.Errorf("sync openclaw/%s: %w", id, err)
		}
	}
	return nil
}

// getPurchaseRequest returns the PurchaseRequest name when one exists in
// the agent's namespace, "" otherwise. We use kubectl --ignore-not-found
// instead of a dynamic client to stay consistent with how the rest of the
// CLI talks to the API.
func getPurchaseRequest(cfg *config.Config, target agentTarget, name string) (string, error) {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return "", err
	}
	bin, kc := kubectl.Paths(cfg)
	ns := agentruntime.Namespace(target.Runtime, target.ID)
	out, err := kubectl.Output(bin, kc, "get", monetizeapi.PurchaseRequestResource, name, "-n", ns, "-o", "name", "--ignore-not-found")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func deletePurchaseRequest(cfg *config.Config, target agentTarget, name string) error {
	bin := filepath.Join(cfg.BinDir, "kubectl")
	kc := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	ns := agentruntime.Namespace(target.Runtime, target.ID)
	return kubectl.Run(bin, kc, "delete", monetizeapi.PurchaseRequestResource, name, "-n", ns, "--ignore-not-found", "--wait=true")
}

// resolveBuyMode decides between fresh / top-up / replace given the
// existing PR state and user intent.
func resolveBuyMode(u *ui.UI, cmd *cli.Command, existing string) (buyMode, error) {
	if existing == "" {
		return buyModeFresh, nil
	}
	if cmd.Bool("replace") {
		return buyModeReplace, nil
	}
	if cmd.Bool("yes") || !u.IsTTY() {
		// Non-interactive default: top up. Safer than replace.
		return buyModeTopUp, nil
	}
	u.Infof("Found existing PurchaseRequest %s in agent namespace.", existing)
	choices := []string{"Top up (add more pre-authorized capacity)", "Replace (delete and create fresh)", "Cancel"}
	idx, err := u.Select("How would you like to proceed?", choices, 0)
	if err != nil {
		return buyModeFresh, err
	}
	switch idx {
	case 0:
		return buyModeTopUp, nil
	case 1:
		return buyModeReplace, nil
	default:
		return buyModeFresh, errors.New("buy cancelled")
	}
}
