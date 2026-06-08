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
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/validate"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/urfave/cli/v3"
)

// In-pod paths used by `obol buy inference`. Hermes always has python3 in
// the venv and skills mounted at $OBOL_SKILLS_DIR (see
// internal/hermes/hermes.go where the env is wired). We reference the
// literal paths so we don't depend on shell expansion through `kubectl exec`.
const (
	hermesPython    = "/opt/hermes/.venv/bin/python3"
	hermesBuyPyPath = "/data/.hermes/obol-skills/buy-x402/scripts/buy.py"
	defaultBuyName  = "default-paid"

	// defaultInteractiveCount is the count we propose when the user accepts
	// the interactive default. Small enough to feel like a try-before-trust
	// purchase on most providers, large enough to be useful for a quick
	// chat session.
	defaultInteractiveCount = 50

	// costCapMarkupBps is the default headroom over the seller's quoted
	// per-request price that we apply when --cost-cap is not set
	// explicitly. 5000 basis points = 50% above current, matching the
	// "default 50% more than current N" UX agreed in the design doc.
	costCapMarkupBps = 5000
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
		Usage:     "Buy paid inference from an x402-gated seller via the obol-agent",
		ArgsUsage: "[<seller-url>]",
		Description: `Pre-pays an x402-gated inference seller through an obol-agent's wallet.

Hand the command a seller URL — either a storefront base
("https://inference.v1337.org") or a specific offer
("https://inference.v1337.org/services/aeon") — and the CLI will walk
/api/services.json, pick the inference offer, and pre-sign authorizations
via the agent's remote signer.

With no URL, the public ` + x402verifier.DefaultBuySellerURL + ` storefront is used.

In a TTY, the CLI prompts for auto-refill, request count, and
confirmation. Pass --yes / -y for non-interactive runs (CI, scripts) —
--count is required in that mode.

Examples:
    obol buy inference
    obol buy inference https://inference.v1337.org/services/aeon
    obol buy inference https://seller.example/services/foo --yes --count 100
    obol buy inference https://seller.example/services/foo --auto-refill --refill-threshold 5 --refill-count 25`,
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
				Usage: "Agent instance whose wallet pays for the purchase (default: the master/stack-managed agent)",
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
				Usage: "How many requests (perRequest pricing) or million-tokens (perMTok pricing) of capacity to pre-pay. Required in non-interactive mode.",
			},
			&cli.IntFlag{
				Name:  "expected-agent-id",
				Usage: "Expected ERC-8004 agentId of the seller. Opt-in identity check; default skips verification.",
			},
			&cli.BoolFlag{
				Name:  "auto-refill",
				Usage: "Enable agent-managed refill of the auth pool",
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
				Usage: "After the buy lands, promote paid/<model> to the head of LiteLLM's model_list and sync every agent. Default: prompted in TTY, off otherwise unless --agent is set.",
			},
			&cli.BoolFlag{
				Name:  "replace",
				Usage: "Delete any existing PurchaseRequest with the same name before creating a fresh one (default: top up the existing auth pool)",
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

	autoRefill, err := promptAutoRefill(u, cmd)
	if err != nil {
		return err
	}

	count, err := promptCount(u, cmd, priceUnit, autoRefill.enabled, existing != "")
	if err != nil {
		return err
	}

	budgetAtomic, err := resolveBudget(cmd.String("budget"), token, count, priceAtomic)
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
	walletInfo := fetchWalletInfoBestEffort(cfg, target, token, paymentChainForDisplay(pricing), u)

	summary := buySummary{
		Agent:         target,
		PRName:        prName,
		OfferURL:      offerURL,
		Model:         chosenModel,
		Count:         count,
		PriceAtomic:   priceAtomic,
		PriceUnit:     priceUnit,
		Token:         token,
		BudgetAtomic:  budgetAtomic,
		AutoRefill:    autoRefill,
		CostCapAtomic: costCapAtomic,
		Wallet:        walletInfo,
		Mode:          mode,
	}
	printBuySummary(u, summary)
	if !confirmBuy(u, cmd) {
		return errors.New("buy cancelled")
	}

	setDefault := resolveSetDefault(u, cmd, chosenModel)

	argv := buildBuyPyArgv(buyPyOptions{
		Name:            prName,
		Seller:          offerURL,
		Model:           chosenModel,
		BudgetMicro:     budgetAtomic.String(),
		AutoRefill:      autoRefill.enabled,
		RefillThreshold: autoRefill.threshold,
		RefillCount:     autoRefill.count,
		CostCap:         costCapAtomic,
		Force:           cmd.Bool("force"),
		SetDefault:      false, // we do model prefer/agent sync from Go side
	})

	u.Infof("Dispatching to agent %s (instance=%s) …", target.Runtime, target.ID)
	if err := agentruntime.ExecInPod(cfg, target.Runtime, target.ID, argv); err != nil {
		return err
	}

	if setDefault {
		if err := promoteAndSyncModel(cfg, u, chosenModel); err != nil {
			u.Warnf("Set-default partially failed: %v", err)
			u.Dim("  The purchase landed; rerun `obol model prefer paid/" + chosenModel + " && obol agent sync` to retry.")
		}
	}
	return nil
}

// buyPyOptions captures everything needed to invoke `buy.py buy` inside the
// agent pod. Kept as a flat struct so buildBuyPyArgv stays trivially testable.
type buyPyOptions struct {
	Name            string
	Seller          string
	Model           string
	BudgetMicro     string
	AutoRefill      bool
	RefillThreshold int
	RefillCount     int
	CostCap         *big.Int // when non-nil, sets --cost-cap on the buy.py call
	Force           bool
	SetDefault      bool
}

// buildBuyPyArgv composes the argv for `python3 buy.py buy <name> ...`.
func buildBuyPyArgv(opts buyPyOptions) []string {
	argv := []string{
		hermesPython, hermesBuyPyPath, "buy", opts.Name,
		"--endpoint", opts.Seller,
		"--budget", opts.BudgetMicro,
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
	if opts.CostCap != nil && opts.CostCap.Sign() > 0 {
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
	Agent         agentTarget
	PRName        string
	OfferURL      string
	Model         string
	Count         int
	PriceAtomic   *big.Int
	PriceUnit     string // "perRequest" | "perMTok"
	Token         string
	BudgetAtomic  *big.Int
	AutoRefill    autoRefillPolicy
	CostCapAtomic *big.Int
	Wallet        *buy.WalletInfo
	Mode          buyMode
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
// When the user passes a storefront base, we splice the catalog endpoint;
// when they pass a specific service URL, that URL wins (we still validated
// it against the catalog before calling here).
func canonicalOfferURL(userURL string, entry *buy.CatalogEntry) (string, error) {
	if entry == nil {
		return "", errors.New("catalog entry is nil")
	}
	// If the user URL already has a /services/<name> segment, just return
	// it verbatim. This preserves any explicit base hostname they typed.
	if strings.Contains(userURL, "/services/") {
		return strings.TrimRight(userURL, "/"), nil
	}
	// Otherwise: storefront-only URL — splice the offer endpoint onto the
	// same scheme/host.
	base := strings.TrimRight(userURL, "/")
	endpoint := strings.TrimSpace(entry.Endpoint)
	if endpoint == "" {
		return "", fmt.Errorf("catalog entry for %q has empty endpoint", entry.Name)
	}
	// endpoint is path-only (e.g. "/services/aeon") per the controller's
	// render. Strip /v1/chat/completions if present so buy.py can re-add
	// the conventional suffix itself.
	for _, suffix := range []string{"/v1/chat/completions", "/chat/completions"} {
		endpoint = strings.TrimSuffix(endpoint, suffix)
	}
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

// pricingPerUnit returns the atomic per-request price plus the human unit
// label ("perRequest" or "perMTok") that the prompts/copy use.
func pricingPerUnit(entry *buy.CatalogEntry, pricing *buy.PricingResponse) (*big.Int, string, error) {
	unit := "perRequest"
	if entry != nil && strings.TrimSpace(entry.PriceUnit) != "" {
		unit = entry.PriceUnit
	}
	if pricing == nil || len(pricing.Accepts) == 0 {
		return nil, unit, errors.New("pricing response has no payment options")
	}
	accept := pricing.Accepts[0]
	raw := accept.Amount
	if strings.TrimSpace(raw) == "" {
		raw = accept.MaxAmountRequired
	}
	price, ok := new(big.Int).SetString(strings.TrimSpace(raw), 10)
	if !ok || price.Sign() <= 0 {
		return nil, unit, fmt.Errorf("invalid wire price %q", raw)
	}
	return price, unit, nil
}

// promptAutoRefill resolves --auto-refill / --refill-threshold /
// --refill-count from flags, then interactively fills in the missing pieces
// when in a TTY. In non-interactive mode the policy is exactly what the
// flags described.
func promptAutoRefill(u *ui.UI, cmd *cli.Command) (autoRefillPolicy, error) {
	enabled := cmd.Bool("auto-refill")
	threshold := cmd.Int("refill-threshold")
	count := cmd.Int("refill-count")

	if !u.IsTTY() || cmd.Bool("yes") {
		return autoRefillPolicy{enabled: enabled, threshold: threshold, count: count}, nil
	}

	if !enabled {
		enabled = u.Confirm("Enable auto-refill? Tops up the auth pool from the agent's wallet when it runs low.", false)
	}
	if !enabled {
		return autoRefillPolicy{}, nil
	}
	// buy.py picks sensible defaults when these are 0; only prompt if the
	// user wants to override.
	if threshold == 0 {
		v, err := promptPositiveInt(u, "Refill when remaining auths fall below", 5)
		if err != nil {
			return autoRefillPolicy{}, err
		}
		threshold = v
	}
	if count == 0 {
		v, err := promptPositiveInt(u, "Auths to sign per refill", 25)
		if err != nil {
			return autoRefillPolicy{}, err
		}
		count = v
	}
	return autoRefillPolicy{enabled: true, threshold: threshold, count: count}, nil
}

// promptCount drives the request-or-mtoks count prompt. In non-TTY mode
// --count is mandatory unless --yes was set (in which case we fall back to
// the interactive default — explicit consent suppresses the safety check).
func promptCount(u *ui.UI, cmd *cli.Command, priceUnit string, _ bool, existing bool) (int, error) {
	if v := cmd.Int("count"); v > 0 {
		return v, nil
	}
	if !u.IsTTY() {
		if cmd.Bool("yes") {
			return defaultInteractiveCount, nil
		}
		return 0, errors.New("--count is required in non-interactive mode (pass --count <N>, or run from a TTY)")
	}
	label := "How many requests to pre-pay?"
	if priceUnit == "perMTok" {
		label = "How many million tokens of inference to pre-pay?"
	}
	if existing {
		label = "How many more " + countUnit(priceUnit) + " to add to the existing pool?"
	}
	return promptPositiveInt(u, label, defaultInteractiveCount)
}

func countUnit(priceUnit string) string {
	if priceUnit == "perMTok" {
		return "million-token-batches"
	}
	return "requests"
}

func promptPositiveInt(u *ui.UI, label string, def int) (int, error) {
	for attempt := 0; attempt < 3; attempt++ {
		raw, err := u.Input(label, fmt.Sprintf("%d", def))
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

// resolveBudget computes a budget cap in atomic units. Explicit --budget
// wins; otherwise we compute count × price and add 0 headroom — buy.py
// rounds-up at the per-auth boundary, and a tight cap is exactly what users
// asked for when they didn't set one explicitly.
func resolveBudget(flag, token string, count int, price *big.Int) (*big.Int, error) {
	if flag = strings.TrimSpace(flag); flag != "" {
		s, err := budgetToBaseUnits(flag, token)
		if err != nil {
			return nil, err
		}
		v, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return nil, fmt.Errorf("internal: budgetToBaseUnits returned non-numeric %q", s)
		}
		return v, nil
	}
	if count <= 0 || price == nil || price.Sign() <= 0 {
		return nil, errors.New("internal: count or price is non-positive")
	}
	return new(big.Int).Mul(price, big.NewInt(int64(count))), nil
}

// resolveCostCap parses --cost-cap into atomic units, or computes a default
// of price × (1 + costCapMarkupBps/10000) when auto-refill is enabled.
func resolveCostCap(flag, token string, price *big.Int, autoRefill bool) (*big.Int, error) {
	if flag = strings.TrimSpace(flag); flag != "" {
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
	tokenInfo, _ := x402verifier.ResolveToken(s.Token, "")
	human := func(v *big.Int) string {
		if v == nil {
			return "?"
		}
		dec := tokenInfo.Decimals
		if dec == 0 {
			dec = 6
		}
		scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(dec)), nil)
		r := new(big.Rat).SetFrac(v, scale)
		return r.FloatString(6)
	}

	u.Print("")
	u.Print("─── Purchase summary ─────────────────────────────────────────")
	u.Print(fmt.Sprintf("  Offer:       %s", s.OfferURL))
	u.Print(fmt.Sprintf("  Model:       %s", s.Model))
	unit := "request"
	if s.PriceUnit == "perMTok" {
		unit = "million tokens"
	}
	u.Print(fmt.Sprintf("  Price:       %s %s per %s", human(s.PriceAtomic), s.Token, unit))
	u.Print(fmt.Sprintf("  Count:       %d (≈ %s %s total)", s.Count, human(s.BudgetAtomic), s.Token))
	if s.AutoRefill.enabled {
		u.Print(fmt.Sprintf("  Auto-refill: yes (top up when remaining<%d, sign %d more each time)", s.AutoRefill.threshold, s.AutoRefill.count))
	} else {
		u.Print("  Auto-refill: no")
	}
	if s.CostCapAtomic != nil && s.CostCapAtomic.Sign() > 0 {
		u.Print(fmt.Sprintf("  Cost cap:    refills skipped above %s %s/%s", human(s.CostCapAtomic), s.Token, unit))
	}
	if s.Wallet != nil {
		u.Print(fmt.Sprintf("  Paid from:   %s (balance %s %s on %s)", s.Wallet.Address, s.Wallet.HumanBalance(), s.Token, s.Wallet.Chain))
	} else {
		u.Print(fmt.Sprintf("  Paid from:   agent wallet (instance %s/%s)", s.Agent.Runtime, s.Agent.ID))
	}
	switch s.Mode {
	case buyModeTopUp:
		u.Print("  Existing PR: topping up the existing auth pool")
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

func resolveSetDefault(u *ui.UI, cmd *cli.Command, model string) bool {
	if cmd.IsSet("set-default") {
		return cmd.Bool("set-default")
	}
	if cmd.IsSet("agent") {
		return true
	}
	if !u.IsTTY() {
		return false
	}
	return u.Confirm(fmt.Sprintf("Set paid/%s as the default inference model for all agents (model prefer + agent sync)?", model), true)
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
	choices := []string{"Top up (sign more auths into the existing pool)", "Replace (delete and create fresh)", "Cancel"}
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
