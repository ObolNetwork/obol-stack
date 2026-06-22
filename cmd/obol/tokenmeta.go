package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/stack"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Minimal ABI for best-effort token-metadata reads: ERC-20 decimals()/symbol()
// plus EIP-5267 eip712Domain() (authoritative signing name+version — the field
// the contract's name() can't be trusted for, e.g. Base Sepolia USDC).
const erc20MetaABI = `[
 {"name":"decimals","stateMutability":"view","type":"function","inputs":[],"outputs":[{"type":"uint8"}]},
 {"name":"symbol","stateMutability":"view","type":"function","inputs":[],"outputs":[{"type":"string"}]},
 {"name":"eip712Domain","stateMutability":"view","type":"function","inputs":[],"outputs":[
   {"name":"fields","type":"bytes1"},{"name":"name","type":"string"},{"name":"version","type":"string"},
   {"name":"chainId","type":"uint256"},{"name":"verifyingContract","type":"address"},
   {"name":"salt","type":"bytes32"},{"name":"extensions","type":"uint256[]"}]}
]`

// tokenMeta is the best-effort on-chain metadata for an ERC-20. Any field that
// couldn't be read is left at its zero value (independent per call).
type tokenMeta struct {
	Decimals      int
	DecimalsSet   bool
	Symbol        string
	EIP712Name    string
	EIP712Version string
}

// tokenMetaFetcher reads token metadata for an address on a chain. Injected so
// the autofill merge/error logic stays unit-testable offline.
type tokenMetaFetcher func(ctx context.Context, network, tokenAddr string) (tokenMeta, error)

// erpcAliasForChain maps a payment chain to its eRPC route alias. Known
// ERC-8004 chains use their configured alias (ethereum → "mainnet"); any other
// supported chain falls back to its canonical name, which is the alias
// `obol network add <chain>` registers.
func erpcAliasForChain(network string) string {
	if net, err := erc8004.ResolveNetwork(network); err == nil {
		return net.ERPCNetwork
	}
	return network
}

// fetchTokenMeta best-effort reads ERC-20 + EIP-5267 metadata via the stack's
// eRPC (host-reachable Traefik route, same path `obol sell register` uses).
// Per-field: a call that reverts/empties leaves that field zero. Returns an
// error only when the RPC endpoint itself is unreachable.
func fetchTokenMeta(ctx context.Context, cfg *config.Config, network, tokenAddr string) (tokenMeta, error) {
	rpcURL := strings.TrimRight(stack.LocalIngressURL(cfg), "/") + "/rpc/" + erpcAliasForChain(network)
	eth, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return tokenMeta{}, fmt.Errorf("connect eRPC at %s: %w", rpcURL, err)
	}
	defer eth.Close()

	parsed, err := abi.JSON(strings.NewReader(erc20MetaABI))
	if err != nil {
		return tokenMeta{}, err
	}
	c := bind.NewBoundContract(common.HexToAddress(tokenAddr), parsed, eth, eth, eth)
	opts := &bind.CallOpts{Context: ctx}

	var meta tokenMeta
	var out []any
	if err := c.Call(opts, &out, "decimals"); err == nil && len(out) == 1 {
		if d, ok := out[0].(uint8); ok {
			meta.Decimals = int(d)
			meta.DecimalsSet = true
		}
	}
	out = nil
	if err := c.Call(opts, &out, "symbol"); err == nil && len(out) == 1 {
		if s, ok := out[0].(string); ok {
			meta.Symbol = strings.TrimSpace(s)
		}
	}
	out = nil
	if err := c.Call(opts, &out, "eip712Domain"); err == nil && len(out) >= 3 {
		if n, ok := out[1].(string); ok {
			meta.EIP712Name = strings.TrimSpace(n)
		}
		if v, ok := out[2].(string); ok {
			meta.EIP712Version = strings.TrimSpace(v)
		}
	}
	return meta, nil
}

// assetComplete reports whether a raw-asset block has the signature-critical
// fields filled (decimals + EIP-712 domain). Symbol is cosmetic and excluded.
// Decimals uses -1 as the pre-autofill "not provided/read yet" sentinel so
// decimals=0 remains a valid, explicit ERC-20 precision.
func assetComplete(a schemas.AssetTerms) bool {
	return a.Decimals >= 0 && a.EIP712Name != "" && a.EIP712Version != ""
}

// autofillAcceptPayments fills missing token metadata on raw-asset payment
// options by reading the chain (best-effort, defaulting elsewhere to Permit2).
// Registry tokens and USDC chain-defaults are already complete and trigger NO
// RPC call. If the signature-critical fields still can't be resolved after the
// read, it errors telling the operator to specify them (or wire up eRPC) —
// never silently shipping a guess that would break settlement.
func autofillAcceptPayments(ctx context.Context, payments []map[string]any, fetch tokenMetaFetcher) error {
	for _, p := range payments {
		a, ok := p["asset"].(schemas.AssetTerms)
		if !ok || assetComplete(a) {
			continue
		}
		network, _ := p["network"].(string)
		meta, err := fetch(ctx, network, a.Address)
		if err != nil {
			return fmt.Errorf(
				"could not read token %s on %s from chain: %w\n  Fix: pass decimals=,eip712-name=,eip712-version= in --accept, or run `obol network add %s` so eRPC can reach the chain",
				a.Address, network, err, network)
		}
		if a.Decimals < 0 && meta.DecimalsSet {
			a.Decimals = meta.Decimals
		}
		if a.Symbol == "" {
			a.Symbol = meta.Symbol
		}
		if a.EIP712Name == "" {
			a.EIP712Name = meta.EIP712Name
		}
		if a.EIP712Version == "" {
			a.EIP712Version = meta.EIP712Version
		}
		if !assetComplete(a) {
			var missing []string
			if a.Decimals < 0 {
				missing = append(missing, "decimals")
			}
			if a.EIP712Name == "" {
				missing = append(missing, "eip712-name")
			}
			if a.EIP712Version == "" {
				missing = append(missing, "eip712-version")
			}
			return fmt.Errorf(
				"token %s on %s: could not read %s from the chain (token may not implement EIP-5267) — specify them in --accept",
				a.Address, network, strings.Join(missing, ", "))
		}
		if a.Symbol == "" {
			a.Symbol = "TOKEN" // cosmetic fallback; never affects signing
		}
		p["asset"] = a
	}
	return nil
}
