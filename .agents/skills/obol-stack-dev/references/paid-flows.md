# Paid Flows (Live OBOL + Anvil Fork)

Use this for release-smoke OBOL gating, named flow regressions, and demo
validation. For ordinary QA, prefer CLI-first checks with `obol sell`, `obol
buy`, `obol model`, and `obol kubectl`. Use `flows/*.sh` only when the user asks
for release-smoke/full-flow validation or the regression is specifically inside
a named flow.

## Flow Selection

| Flow | Use for | Network | Token | Facilitator |
|---|---|---|---|---|
| `flows/flow-11-dual-stack.sh` | USDC seller/buyer baseline | Base Sepolia | USDC | configured x402 facilitator |
| `flows/flow-14-live-obol-base-sepolia.sh` | live OBOL smoke + demo gate | Base Sepolia | deployed OBOL (`0x0a09...c78`) | `https://x402.gcp.obol.tech` |
| `flows/flow-13-dual-stack-obol.sh` | OBOL Permit2 regression w/o live funds | Anvil fork | fork-local OBOL | local x402-rs (`host.k3d.internal:8404`) |

Default to live Base Sepolia for OBOL. Anvil only when explicitly testing the fork regression. `flow-14` is the demo gate; treat its failures as release-blocking.

## Release-Smoke Selectors

```bash
RELEASE_SMOKE_INCLUDE_OBOL=true        # adds live flow-14
RELEASE_SMOKE_INCLUDE_OBOL_FORK=true   # adds fork flow-13
```

Keep these explicit in the run command. Don't hide live/fork behind one selector.

## Wallet Invariant

Both Alice (seller/register) and Bob (buyer) derive from a single `.env REMOTE_SIGNER_PRIVATE_KEY`. Bob is the deterministic 2nd-derived key. The flow seeds Bob's remote-signer with this key through `obol wallet import` after Bob's stack and LLM route are up, then asserts `bobSigner == BOB_WALLET`.

**Do not** transfer funds to a generated signer to make the test pass. Keep live OBOL funding on the deterministic Bob address. Don't infer the canonical pair from balances on older duplicate token deployments.

Derive and check Bob:

```bash
SIGNER_KEY=$(grep -E '^[[:space:]]*REMOTE_SIGNER_PRIVATE_KEY=' .env | head -1 | cut -d= -f2-)
BOB_PRIVATE_KEY=$(env -u CHAIN cast keccak \
  "$(env -u CHAIN cast abi-encode 'f(bytes32,uint256)' "$SIGNER_KEY" 2)")
BOB_WALLET=$(env -u CHAIN cast wallet address --private-key "$BOB_PRIVATE_KEY")
cast call "$OBOL_TOKEN_BASE_SEPOLIA" "balanceOf(address)(uint256)" "$BOB_WALLET" \
  --rpc-url "${BASE_SEPOLIA_RPC:-https://sepolia.base.org}"
```

## Required Funding

- Alice (`cast wallet address --private-key "$REMOTE_SIGNER_PRIVATE_KEY"`): Base Sepolia ETH for gas + ServiceOffer registration.
- Bob (deterministic 2nd key above): Base Sepolia OBOL (`0x0a09371a8b011d5110656ceBCc70603e53FD2c78`).

`flows/lib.sh::fund_bob_from_alice_if_needed()` ERC-20 transfers `(required - balance)` from Alice to Bob when Bob's wallet drains across runs. The release-smoke runner calls it automatically before `flow-14`.

## Paid-RPC Setup

Free-tier Base Sepolia RPCs (`drpc.org`, `sepolia.base.org`) routinely return HTTP 408 under release-smoke load. Set one of:

```bash
# .env
BASE_SEPOLIA_RPC=https://lb.drpc.live/base-sepolia/<paid-token>
# or
ALCHEMY_BASE_SEPOLIA_API_KEY=<key>
```

The runner has a `warn_unpaid_base_sepolia_rpc` preflight. The CLI scrubs paid-RPC tokens from `obol network add` stdout (`cmd/obol/network.go::redactRPCURL`) and the runner pipes through `flows/lib.sh::scrub_secrets`. Both collapse paid URLs to `[REDACTED].<tld>/[REDACTED]` so logs only ever surface the provider, not the network selector or key.

## Success Criteria (flow-14 / flow-11 / flow-13)

- OBOL metadata reads as `Obol Network` / `OBOL` / 18 decimals and exposes `DOMAIN_SEPARATOR()` (only relevant for OBOL flows).
- Alice ServiceOffer reaches `Ready=True`.
- ERC-8004 registration tx published to Base Sepolia (`/.well-known/agent-registration.json` reachable via tunnel for live flows).
- Bob `PurchaseRequest` reaches `Ready=True`.
- LiteLLM exposes `paid/<OBOL_LLM_MODEL>` (default `qwen36-deep`).
- Paid inference returns HTTP 200 and **final-answer** content (not reasoning metadata or tool-catalogue text).
- On-chain `Transfer(Bob signer → Alice, <PAID_AMOUNT>)` receipt is archived.
- Alice balance increases and Bob signer balance decreases by exactly `PAID_AMOUNT` wei (USDC for flow-11, OBOL for flow-13/14).

## x402 Verifier vs Buyer Sidecar — When Each Is Used

| Path | Use case | What gates payment |
|---|---|---|
| **`obol sell http`** + `x402-buyer` sidecar | cluster-routed paid traffic (default, validated) | Traefik ForwardAuth → `x402-verifier` (verify-only) → upstream → buyer settles after `<400` |
| **`obol sell inference`** | direct raw `X-PAYMENT` buyers / standalone host gateway | gateway runs x402 middleware in-process |

Do **not** treat raw `X-PAYMENT` through Traefik ForwardAuth as a supported production path — `x402-verifier` runs `verifyOnly: true` and does not settle. If you must port-forward to the verifier and POST `/verify` directly, set `X-Forwarded-Uri` (and usually `X-Forwarded-Host`) like Traefik does or you'll get `403 forbidden: missing forwarded URI`.

## CA-Bundle Footgun

`x402-verifier` is distroless and ships **no CA store**. The `ca-certificates` ConfigMap in the `x402` namespace must be populated from the host's CA bundle, or all calls to `https://x402.gcp.obol.tech` fail with `x509: certificate signed by unknown authority` and surface as `Payment verification failed`.

Fixed automatically: `obol stack up` calls `x402verifier.PopulateCABundle` after infra deploy; `obol sell http` calls it before creating the ServiceOffer. Manual repopulate:

```bash
obol kubectl create configmap ca-certificates -n x402 \
  --from-file=ca-certificates.crt=/etc/ssl/cert.pem \
  --dry-run=client -o yaml | obol kubectl replace -f -
```

## Quick Full-Cycle Smoke

1. **Configure model**: `obol model setup custom --endpoint <url>/v1 --model <id>`; then `obol model prefer <id>` and `obol model sync`.
2. **Sell**: use `obol sell inference` or `obol sell demo <type>`; wait for `obol sell status <name> -n <ns>` to show `Ready=True`.
3. **Unpaid gate**: `obol sell test <name> -n <ns>`; expect HTTP 402 + accepts requirements.
4. **Buy**: use `obol buy inference <seller-url> --yes --count <N>` when testing buyer flow through the CLI. Confirm `PurchaseRequest Ready=True` with `obol kubectl get purchaserequest -A`.
5. **Paid call and spend proof**: call LiteLLM with `paid/<model>` and verify HTTP 200, sidecar `/status` spend counters, and on-chain balance deltas when the test is live-settlement gated.

Direct `buy.py` execution is reserved for existing release flow internals or
debugging a bug in that embedded skill itself.
