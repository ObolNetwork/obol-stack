# Paid Commerce And Flow Gotchas

Use this reference for x402 seller/buyer flows, paid LiteLLM routing, ERC-8004, token additions, and flow failures.

## Paid Routing

- All inference paths go through LiteLLM.
- `paid/*` routes to the `x402-buyer` sidecar in the LiteLLM pod.
- The sidecar spends one pre-signed authorization per paid request.
- Full QA uses an explicit OpenAI-compatible vLLM/llama.cpp endpoint via
  `OBOL_LLM_ENDPOINT`; the current default QA model is `qwen36-fast`.
- Agent natural-language output is informational. Assert structural state instead:
  - `PurchaseRequest.status.conditions[Ready]=True`
  - sidecar auth count changes
  - ERC-20 `Transfer` receipt is present and status is success
  - balance deltas match expected amount

## Agent LLM Path

- Never replace the Hermes buy prompt with a direct `kubectl exec buy.py` in QA.
- If Hermes refuses to execute `buy.py`, first confirm the skill files exist and the terminal tool is available, then inspect model routing.
- Do not accept a local Ollama `qwen3.5:9b` route as a full QA substitute.
  It has produced plain-chat refusals on tool-heavy prompts. Route Alice and Bob
  through `OBOL_LLM_ENDPOINT` and keep `OBOL_LLM_MODEL` aligned with the model
  used in the buy prompt and paid inference assertions.
- The real proof remains structural: Hermes must create the `PurchaseRequest`; the sidecar must hold auths; the paid LiteLLM call must return HTTP 200; settlement and balance deltas must match.

## Buyer Wallet Invariant

For `flow-11`, `flow-13`, and `flow-14`:

- Alice sells/registers with `.env` `REMOTE_SIGNER_PRIVATE_KEY`.
- Bob is the second deterministic derived key from that signer.
- Before Bob `stack up`, scaffold the default Hermes agent and import Bob's key:

```bash
obol agent new --runtime hermes --id obol-agent --no-sync
obol wallet import --instance obol-agent --private-key-file <file> --force
```

- After Bob starts, assert `bobSigner == BOB_WALLET`.
- Do not transfer funds to a generated signer to make the test pass.

## Token Support

Payment tokens live in `internal/x402/tokens.go`.

Add one registry entry per `(symbol, chain)` pair:

```go
"WETH": {
    "base": {
        Address: "0x4200000000000000000000000000000000000006",
        Symbol: "WETH",
        Decimals: 18,
        TransferMethod: "permit2",
        EIP712Name: "Wrapped Ether",
        EIP712Version: "1",
    },
},
```

Check:

- `eip3009` for native `transferWithAuthorization` tokens such as USDC/EURC.
- `permit2` for Uniswap Permit2.
- EIP-712 name/version match the on-chain contract.
- Facilitator advertises the chain in `/supported`.
- ExactPermit2Proxy exists on the chain for Permit2 tokens.
- Add `internal/x402/tokens_test.go` coverage.

## ERC-8004 Registration

The serviceoffer-controller does not sign on-chain. Registration is landed by CLI commands that use the agent remote-signer.

Valid paths:

1. `obol sell http` after remote-signer has the intended wallet.
2. Apply YAML with `registration.enabled: false` when testing payment only.
3. If YAML has `registration.enabled: true`, run `obol sell register`; otherwise the offer parks at `AwaitingExternalRegistration`.

`setMetadata` simulation reverts are usually read-side staleness after `register`. The correct fix is to wait for `ownerOf(agentID)` before `setMetadata`.

## Operational Gotchas

- Source `flows/lib.sh` first in every flow so PATH and helpers are loaded.
- Parse `.env` with anchored regexes; loose `grep REMOTE_SIGNER_PRIVATE_KEY` can read comments.
- eRPC, verifier, buyer, and Hermes API-server images are often distroless. Use transient probe pods instead of assuming `curl` or `wget` exists in those containers.
- Poll specific container readiness in multi-container pods. Do not trust only the pod summary status.
- `obol stack up` leaves cloudflared at zero replicas. Flows that apply ServiceOffer YAML directly must explicitly scale/restart cloudflared before reading tunnel status.
- `--namespace` on `obol sell http` sets both ServiceOffer namespace and upstream service namespace. Use the same namespace for follow-up `sell status|stop|delete`.
- For Anvil fork flows, bind Anvil to `0.0.0.0` and point each cluster eRPC at `host.k3d.internal:$ANVIL_PORT`.
- **Anvil must be nightly, not stable.** Stable lags ~5 months behind on Base Sepolia archive-lookup support. With stable, the facilitator's EIP-3009 `eth_getStorageAt` against USDC fails with `state at block #N is pruned` once the fork drifts past the upstream RPC's retention window. Install with `foundryup --install nightly`.
- **Anvil fork-RPC must be archive.** `publicnode.com` is non-archive and was the historic default in `flows/lib.sh::base_sepolia_rpc_candidates` — it is now removed. The archive-capable candidates currently in use: `drpc.org`, `sepolia.base.org`, `tenderly`, `onfinality`, `sentio`, `pocket`. Validate any new addition with a historical `eth_getStorageAt` before trusting it. Source list: chainlist.org/rpcs.json filtered to chainId 84532.
- **A long-lived Anvil drifts.** Fork base block stays fixed at startup; after a few hours of real-network advancement the facilitator's historical state lookups can land before the fork base and miss locally. For release smoke / repeat runs, recreate Anvil between sessions or accept that the fork window is bounded.
- Foundry nightly prints a stderr warning per invocation that contaminates `cast` stdout when stderr is merged (`2>&1`). `flows/lib.sh` exports `FOUNDRY_DISABLE_NIGHTLY_WARNING=1`; preserve this. Any per-flow `cast … 2>&1` pipeline that hits a `grep`/regex assertion will false-FAIL without it.
- `flows/lib.sh::poll_step_grep` and `run_step_grep` use `grep -E`. Patterns are ERE — `{N,}` etc. work as quantifiers. Without `-E` (the pre-fix behaviour), `^[1-9][0-9]{8,} ` and similar patterns silently never match and the step times out even when the output is correct.
- **Sidecar clean-up after PurchaseRequest deletion.** The buyer sidecar state is split across two ConfigMaps in the `llm` namespace (`x402-buyer-config`, `x402-buyer-auths`) plus the in-memory sidecar process. The controller's tombstone cleanup is the supported path; if you bypass it by stripping the finalizer, also remove the per-PR keys from both ConfigMaps and `kubectl rollout restart deployment/litellm -n llm` — otherwise `/status` will still report stale `remaining=` for the deleted purchase.
- `flow-08` step 16 (`x402-buyer auth pool decremented by 1`) is polled, not one-shot. The sidecar persists the spent-auth count asynchronously after the upstream returns; a one-shot read can still report the pre-call count for several seconds even when settlement, the on-chain Transfer, and the buyer/seller balance deltas have cleared.
- x402-rs Permit2 support is configured by `eip2612_gas_sponsoring=true` on `v2-eip155-exact`; there is no standalone `v2-eip155-permit2` scheme.
- On aarch64 GPU hosts, if cloudflared image pulls stall through the k3d mirror, use `flows/lib.sh::ensure_image_in_k3d cloudflare/cloudflared:2026.3.0 obol-stack-<stack-id>`.
