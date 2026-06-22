package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/schemas"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/shopspring/decimal"
	"github.com/urfave/cli/v3"
)

// --accept lets a seller advertise multiple payment options (currencies /
// networks) for one offer. Each --accept is a comma-separated key=value list:
//
//	--accept token=USDC,network=base,price=1,pay-to=0x...
//	--accept token=OBOL,network=ethereum,price=10
//	--accept asset=0x...,decimals=18,transfer=permit2,eip712-name=Foo,eip712-version=1,symbol=FOO,network=base,price=0.5
//
// `token=<symbol>` resolves the full asset block from the built-in registry
// (the curated, best-in-class path). `asset=0x...` is the escape hatch for any
// ERC-20 on a supported chain: the seller supplies decimals + transfer +
// eip712-name/version themselves. The two are mutually exclusive.
//
// Arbitrary (unsupported) chains are intentionally NOT accepted yet — every
// option's network must resolve via ResolveChainInfo. Raw asset on an
// arbitrary eip155:<id> chain is a planned follow-up (needs facilitator-support
// + buy.py chain-mapping verification).

var evmAddressRe = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// acceptOption is one parsed, validated --accept entry. Network is normalized
// to the canonical chain name; Asset is zero for the USDC chain-default (which
// the verifier fills in), set otherwise.
type acceptOption struct {
	Network  string
	PayTo    string
	PriceKey string // perRequest | perMTok | perHour | perEpoch
	PriceVal string
	Asset    schemas.AssetTerms
	// AssetDecimalsSet distinguishes an explicitly supplied decimals=0 from an
	// omitted decimals field while raw-asset metadata is still being autofilled.
	AssetDecimalsSet bool
	// dedupKey identifies the (chain, token) pair for duplicate detection.
	dedupKey string
}

var acceptPriceKeys = map[string]string{
	"price":       "perRequest",
	"per-request": "perRequest",
	"per-mtok":    "perMTok",
	"per-hour":    "perHour",
	"per-epoch":   "perEpoch",
}

var acceptKnownKeys = map[string]bool{
	"token": true, "network": true, "chain": true, "pay-to": true,
	"price": true, "per-request": true, "per-mtok": true, "per-hour": true, "per-epoch": true,
	"asset": true, "decimals": true, "transfer": true, "symbol": true,
	"eip712-name": true, "eip712-version": true, "max-timeout": true,
}

// parseAcceptKV splits a single --accept value into its key/value pairs.
func parseAcceptKV(raw string) (map[string]string, int64, error) {
	out := map[string]string{}
	var maxTimeout int64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		if !ok || k == "" || v == "" {
			return nil, 0, fmt.Errorf("malformed --accept segment %q (want key=value)", part)
		}
		if !acceptKnownKeys[k] {
			return nil, 0, fmt.Errorf("unknown --accept key %q", k)
		}
		if k == "max-timeout" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n <= 0 {
				return nil, 0, fmt.Errorf("--accept max-timeout %q must be a positive integer", v)
			}
			maxTimeout = n
			continue
		}
		if _, dup := out[k]; dup {
			return nil, 0, fmt.Errorf("--accept key %q given twice", k)
		}
		out[k] = v
	}
	return out, maxTimeout, nil
}

// parseAcceptOption parses and validates one --accept value. defaultPayTo is
// the command-level --pay-to, used when the option omits pay-to.
func parseAcceptOption(raw, defaultPayTo string) (acceptOption, int64, error) {
	kv, maxTimeout, err := parseAcceptKV(raw)
	if err != nil {
		return acceptOption{}, 0, err
	}

	// Network (required) — must resolve to a supported chain.
	network := kv["network"]
	if network == "" {
		network = kv["chain"]
	}
	if network == "" {
		return acceptOption{}, 0, fmt.Errorf("--accept %q: network is required", raw)
	}
	chainInfo, err := x402verifier.ResolveChainInfo(network)
	if err != nil {
		return acceptOption{}, 0, fmt.Errorf("--accept %q: %w", raw, err)
	}
	canonicalChain := chainInfo.Name

	// payTo (required) — option-level wins, else the command default.
	payTo := kv["pay-to"]
	if payTo == "" {
		payTo = strings.TrimSpace(defaultPayTo)
	}
	if !evmAddressRe.MatchString(payTo) {
		return acceptOption{}, 0, fmt.Errorf("--accept %q: pay-to must be a 0x EVM address (got %q)", raw, payTo)
	}

	// Exactly one price slot.
	priceKey, priceVal := "", ""
	for flag, slot := range acceptPriceKeys {
		if v := kv[flag]; v != "" {
			if priceKey != "" {
				return acceptOption{}, 0, fmt.Errorf("--accept %q: set only one of price/per-request/per-mtok/per-hour/per-epoch", raw)
			}
			priceKey, priceVal = slot, v
		}
	}
	if priceKey == "" {
		return acceptOption{}, 0, fmt.Errorf("--accept %q: a price is required (price=, per-mtok=, …)", raw)
	}
	if d, derr := decimal.NewFromString(priceVal); derr != nil || d.IsNegative() {
		return acceptOption{}, 0, fmt.Errorf("--accept %q: price %q must be a non-negative decimal", raw, priceVal)
	}

	// Asset: token=<symbol> (registry) XOR asset=0x... (raw). Default USDC
	// when neither is given (matches the singular-flag default).
	tokenSym := strings.TrimSpace(kv["token"])
	rawAddr := strings.TrimSpace(kv["asset"])
	opt := acceptOption{Network: canonicalChain, PayTo: payTo, PriceKey: priceKey, PriceVal: priceVal}

	switch {
	case tokenSym != "" && rawAddr != "":
		return acceptOption{}, 0, fmt.Errorf("--accept %q: set either token=<symbol> or asset=0x..., not both", raw)

	case rawAddr != "":
		if !evmAddressRe.MatchString(rawAddr) {
			return acceptOption{}, 0, fmt.Errorf("--accept %q: asset must be a 0x ERC-20 address (got %q)", raw, rawAddr)
		}
		// transfer defaults to permit2 — the near-universal flow (EIP-3009 is
		// effectively USDC-only). decimals/symbol/eip712-* are optional here:
		// any not supplied are filled best-effort from the chain by
		// autofillAcceptPayments, which errors if they still can't be resolved.
		transfer := strings.ToLower(strings.TrimSpace(kv["transfer"]))
		if transfer == "" {
			transfer = schemas.AssetTransferMethodPermit2
		}
		if transfer != schemas.AssetTransferMethodEIP3009 && transfer != schemas.AssetTransferMethodPermit2 {
			return acceptOption{}, 0, fmt.Errorf("--accept %q: transfer must be eip3009 or permit2", raw)
		}
		dec := -1
		decimalsSet := false
		if d := strings.TrimSpace(kv["decimals"]); d != "" {
			n, derr := strconv.Atoi(d)
			if derr != nil || n < 0 || n > 255 {
				return acceptOption{}, 0, fmt.Errorf("--accept %q: decimals must be 0-255", raw)
			}
			dec = n
			decimalsSet = true
		}
		opt.Asset = schemas.AssetTerms{
			Address: rawAddr, Symbol: strings.TrimSpace(kv["symbol"]), Decimals: dec,
			TransferMethod: transfer,
			EIP712Name:     strings.TrimSpace(kv["eip712-name"]),
			EIP712Version:  strings.TrimSpace(kv["eip712-version"]),
		}
		opt.AssetDecimalsSet = decimalsSet
		opt.dedupKey = canonicalChain + "\x00" + strings.ToLower(rawAddr)

	default:
		// Registry shorthand. USDC is the chain default (empty asset block).
		if tokenSym == "" {
			tokenSym = "USDC"
		}
		if strings.EqualFold(tokenSym, "USDC") {
			opt.dedupKey = canonicalChain + "\x00usdc"
			break
		}
		entry, ok := x402verifier.ResolveToken(tokenSym, canonicalChain)
		if !ok {
			return acceptOption{}, 0, fmt.Errorf(
				"--accept %q: token %s is not in the registry for %s (use asset=0x... with decimals/transfer/eip712 for an unlisted token)",
				raw, tokenSym, canonicalChain)
		}
		opt.Asset = schemas.AssetTerms{
			Address: entry.Address, Symbol: entry.Symbol, Decimals: entry.Decimals,
			TransferMethod: entry.TransferMethod, EIP712Name: entry.EIP712Name, EIP712Version: entry.EIP712Version,
		}
		opt.dedupKey = canonicalChain + "\x00" + strings.ToLower(entry.Address)
	}

	return opt, maxTimeout, nil
}

// paymentMap renders the option as a ServiceOffer spec.payment(s) entry.
func (o acceptOption) paymentMap(maxTimeout int64) map[string]any {
	if maxTimeout <= 0 {
		maxTimeout = 300
	}
	m := map[string]any{
		"scheme":            "exact",
		"network":           o.Network,
		"payTo":             o.PayTo,
		"maxTimeoutSeconds": maxTimeout,
		"price":             map[string]any{o.PriceKey: o.PriceVal},
	}
	if !o.Asset.IsZero() {
		m["asset"] = o.Asset
	}
	return m
}

// buildAcceptPayments parses every --accept value into ServiceOffer payment
// maps, rejecting duplicate (chain, token) pairs. The returned slice is the
// spec.payments[] list; payments[0] is the primary option and callers also
// write it to spec.payment. Returns (nil, nil) when no --accept was given so
// callers can fall back to the singular --chain/--token/--price flags.
func buildAcceptPayments(accepts []string, defaultPayTo string) ([]map[string]any, error) {
	if len(accepts) == 0 {
		return nil, nil
	}
	payments := make([]map[string]any, 0, len(accepts))
	seen := map[string]string{}
	for _, raw := range accepts {
		opt, maxTimeout, err := parseAcceptOption(raw, defaultPayTo)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[opt.dedupKey]; dup {
			return nil, fmt.Errorf("--accept duplicate payment option for the same (chain, token): %q and %q", prev, raw)
		}
		seen[opt.dedupKey] = raw
		payments = append(payments, opt.paymentMap(maxTimeout))
	}
	return payments, nil
}

// priceTableToMap renders a resolved PriceTable as the CRD price block,
// emitting whichever single slot is populated.
func priceTableToMap(pt schemas.PriceTable) map[string]any {
	price := map[string]any{}
	switch {
	case pt.PerRequest != "":
		price["perRequest"] = pt.PerRequest
	case pt.PerMTok != "":
		price["perMTok"] = pt.PerMTok
	case pt.PerHour != "":
		price["perHour"] = pt.PerHour
	case pt.PerEpoch != "":
		price["perEpoch"] = pt.PerEpoch
	}
	return price
}

// acceptFlags returns the shared --accept / --weight / --category flags so the
// three sell creation commands stay in lockstep. allowPerHour mirrors the
// command's own price flags but does not affect --accept (which carries its
// own price keys).
func acceptFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringSliceFlag{
			Name: "accept",
			Usage: "Accepted payment option (repeatable) for multi-currency offers, e.g. " +
				"--accept token=OBOL,network=ethereum,price=10 --accept token=USDC,network=base,price=1. " +
				"Unlisted tokens: asset=0x..,network=..,price=.. — decimals/symbol/eip712-name/eip712-version are read " +
				"from the chain (EIP-5267) when omitted and transfer defaults to permit2; pass them explicitly to override " +
				"or if the chain can't be reached. When set, --chain/--token/--price are ignored.",
		},
		&cli.IntFlag{
			Name:  "weight",
			Usage: "Storefront ordering weight; higher sorts earlier within its category",
		},
		&cli.StringFlag{
			Name:  "category",
			Usage: "Storefront grouping section (e.g. \"demo\")",
		},
	}
}

// resolveOfferPayments builds the ServiceOffer payment block(s) for a creation
// command. When --accept is present it returns the multi-payment list (and
// payments[0] as the primary); otherwise it falls back to the singular
// --chain/--token/--price flags and returns a nil payments list. wallet is the
// already-resolved default recipient. The returned network/payTo reflect the
// PRIMARY option so callers can drive ERC-8004 registration off it (register
// on the first payment's network — the locked decision).
func resolveOfferPayments(cmd *cli.Command, wallet string, allowPerHour bool) (payment map[string]any, payments []map[string]any, network, payTo string, err error) {
	if accepts := cmd.StringSlice("accept"); len(accepts) > 0 {
		payments, err = buildAcceptPayments(accepts, wallet)
		if err != nil {
			return nil, nil, "", "", err
		}
		primary := payments[0]
		net, _ := primary["network"].(string)
		to, _ := primary["payTo"].(string)
		return primary, payments, net, to, nil
	}

	priceTable, perr := resolvePriceTable(cmd, allowPerHour)
	if perr != nil {
		return nil, nil, "", "", perr
	}
	chainName := cmd.String("chain")
	assetTerms, aerr := resolveAssetTerms(cmd, &chainName)
	if aerr != nil {
		return nil, nil, "", "", aerr
	}
	maxTimeout := cmd.Int("max-timeout")
	if maxTimeout <= 0 {
		maxTimeout = 300
	}
	payment = map[string]any{
		"scheme":            "exact",
		"network":           chainName,
		"payTo":             wallet,
		"maxTimeoutSeconds": maxTimeout,
		"price":             priceTableToMap(priceTable),
	}
	if !assetTerms.IsZero() {
		payment["asset"] = assetTerms
	}
	return payment, nil, chainName, wallet, nil
}

// paymentSymbol returns the display token symbol for a payment block: the
// explicit asset symbol, or "USDC" (the chain default) when none is set.
func paymentSymbol(payment map[string]any) string {
	if a, ok := payment["asset"].(schemas.AssetTerms); ok && a.Symbol != "" {
		return a.Symbol
	}
	return "USDC"
}

// paymentPriceValue returns the first populated price-slot value of a payment
// block (perRequest / perMTok / perHour / perEpoch).
func paymentPriceValue(payment map[string]any) string {
	pt, ok := payment["price"].(map[string]any)
	if !ok {
		return ""
	}
	for _, k := range []string{"perRequest", "perMTok", "perHour", "perEpoch"} {
		if v, ok := pt[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// applyListingFlags writes spec.listing from --weight/--category when either is
// set. No-op otherwise so offers without listing hints serialize unchanged.
func applyListingFlags(cmd *cli.Command, spec map[string]any) {
	listing := map[string]any{}
	if w := cmd.Int("weight"); w != 0 {
		listing["weight"] = w
	}
	if c := strings.TrimSpace(cmd.String("category")); c != "" {
		listing["category"] = c
	}
	if len(listing) > 0 {
		spec["listing"] = listing
	}
}

// acceptSummary renders a short "TOKEN on chain @ price" line per option for
// CLI confirmation output. Deterministic order (as given).
func acceptSummary(payments []map[string]any) string {
	parts := make([]string, 0, len(payments))
	for _, p := range payments {
		network, _ := p["network"].(string)
		sym := "USDC"
		if a, ok := p["asset"].(schemas.AssetTerms); ok && a.Symbol != "" {
			sym = a.Symbol
		}
		price := ""
		if pt, ok := p["price"].(map[string]any); ok {
			keys := make([]string, 0, len(pt))
			for k := range pt {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if len(keys) > 0 {
				if v, ok := pt[keys[0]].(string); ok {
					price = v
				}
			}
		}
		parts = append(parts, fmt.Sprintf("%s on %s @ %s", sym, network, price))
	}
	return strings.Join(parts, "; ")
}
