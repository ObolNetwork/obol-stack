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
- x402-rs Permit2 support is configured by `eip2612_gas_sponsoring=true` on `v2-eip155-exact`; there is no standalone `v2-eip155-permit2` scheme.
- On aarch64 GPU hosts, if cloudflared image pulls stall through the k3d mirror, use `flows/lib.sh::ensure_image_in_k3d cloudflare/cloudflared:2026.3.0 obol-stack-<stack-id>`.
