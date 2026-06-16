# Flows

End-to-end validation scripts for the Obol Stack. Each `flow-NN-*.sh` is
idempotent and exits non-zero on failure.

## Scripts

- `flow-01-prerequisites.sh` — validate environment before any cluster work (Docker, Ollama, obol binary).
- `flow-02-stack-init-up.sh` — Stack Init + Up (getting-started.md §1-2).
- `flow-03-inference.sh` — LLM Inference: host Ollama, in-cluster, LiteLLM, tool-calls (§3a-3d).
- `flow-04-agent.sh` — Agent Init + Inference: agent init, hermes list, token, gateway (§4-5).
- `flow-05-network.sh` — Network management (§6). SKIPPED per autoresearch constraint 0.
- `flow-06-sell-setup.sh` — Sell Setup: pricing, sell http, controller reconcile (§1.1-1.4).
- `flow-07-sell-verify.sh` — Sell Verify: runs after flow-06, ServiceOffer must be Ready (§1.5-1.7).
- `flow-08-buy.sh` — Buy: requires flow-06 + flow-10 (§2.1-2.5).
- `flow-09-lifecycle.sh` — Lifecycle: list, status, stop, delete, cleanup (§4).
- `flow-10-anvil-facilitator.sh` — Anvil + Facilitator local test infra (§3). Run BEFORE flow-08.
- `flow-11-dual-stack.sh` — Dual-Stack: Alice sells, Bob discovers via ERC-8004 and buys.
- `flow-12-obol-payment.sh` — OBOL payment asset over the existing USDC commerce baseline.
- `flow-13-dual-stack-obol.sh` — Dual-Stack OBOL: Alice sells, Bob discovers and buys, but the payment asset is a fork-local OBOL ERC20Permit token and the facilitator is local (not the public Obol facilitator). Use this when you want to validate the OBOL Permit2 path end-to-end without depending on the public Obol facilitator or any USDC contract. Both obol stacks share ONE local Anvil fork of Base Sepolia via `host.k3d.internal:$ANVIL_PORT`. Requires `cast` + `anvil` + `forge`; the local facilitator runs as `ghcr.io/obolnetwork/x402-facilitator-prometheus-overlay:1.4.9`.
- `flow-14-live-obol-base-sepolia.sh` — Live Base Sepolia OBOL dual-stack: Alice registers/sells, Bob discovers/buys, and settlement is verified against the freshly deployed Base Sepolia OBOL ERC20Permit token and public Obol facilitator. Requires `REMOTE_SIGNER_PRIVATE_KEY` funded with Base Sepolia ETH and the second deterministic derived Bob key funded with OBOL; `OBOL_TOKEN_BASE_SEPOLIA` defaults to the fresh faucet-backed Base Sepolia OBOL token (`0x0a09371a8b011d5110656ceBCc70603e53FD2c78`; source of truth: ObolNetwork/obol-stack#447) and can be overridden.
- `flow-15-live-obol-faucet-alice-bob.sh` — Faucet-backed live OBOL smoke: claims official Base Sepolia faucet OBOL for Bob, checks Bob has buyer gas, then delegates to flow-14 for the Alice/Bob commerce loop.
- `flow-16-sell-agent.sh` — Agent ServiceOffer smoke: declare an Agent CRD, publish it via `obol sell agent`, and verify the x402 metadata surface for agent-backed paid routes.

`lib.sh` is shared baseline flow plumbing; `lib-dual-stack.sh` is shared
Alice/Bob orchestration for the dual-stack seller/buyer flows; `flows/tools/*`
holds small structured helpers used by the shell entrypoints. Keep new
`flow-NN-*.sh` scripts focused on the scenario, assertions, and environment
contract rather than duplicating stack, DNS, wallet, or config-mutation helpers.
`release-smoke.sh` is the release gate.

`hf-surface-smoke.sh` and `p2p-surface-smoke.sh` are out-of-band, host-side
"surface" smokes — no cluster required, and each check SKIPs on a missing
prerequisite rather than aborting. They cover the peer-to-peer / host-gateway
paths release-smoke (entirely the cluster path) does not:

- `hf-surface-smoke.sh` — dataset hub (anonymize → sign → publish → unpaid buy),
  fine-tune-on-spark provenance binding, router + ERC-8004 indexer discovery.
- `p2p-surface-smoke.sh` — standalone `obol sell inference` 402 emission +
  remote-model proxy (model served on `spark1`, reached via SSH forward); the
  paid dataset `/join/paid` x402 gate + `buy dataset --join` client guards
  (402 challenge, `--max-price` cap, fail-closed download); and the research
  membership → submit → payout E2E asserting **token-derived** worker identity
  (not the self-declared field) and best-per-worker payouts. When a local anvil
  base-sepolia fork + x402 facilitator are reachable (auto-detected; stand them
  up with flow-10), the **paid 200** (a signed EIP-3009 `X-PAYMENT` verified +
  settled, via the `flows/tools/x402-sign` host signer) and the **paid dataset
  mint + verified download** settle on chain for real; otherwise those two legs
  SKIP. The `--secure` transport gate (Surface 4) auto-activates when a genuinely
  non-secure origin exists: **4a** (ACCEPT over a NAMED cloudflared tunnel) when
  `SECURE_TUNNEL_NAME` + `SECURE_TUNNEL_HOSTNAME` are set (after `cloudflared
  tunnel login` + routing a hostname on your CF domain); **4b/4c** (REJECT/ACCEPT
  a CGNAT plaintext origin) when THIS host is on a tailnet (`tailscale up`) so a
  remote peer (`SECURE_ORIGIN_SSH`, default `spark1`) can reach a non-private mac
  IP — loopback/RFC1918 are always "secure". Each leg SKIPs precisely until its
  prereq is set; the gate is also unit-tested. Run e.g.
  `OBOL_BIN=.workspace/bin/obol SPARK=spark1 bash flows/p2p-surface-smoke.sh`.

## Running a flow detached over SSH

`nohup` and `setsid -f` get reaped when an SSH session ending closes the
controlling pty (observed at step 17 of `flow-11-dual-stack.sh` over an
ssh+cloudflared ProxyCommand session). Use `run-detached.sh`, which prefers
`tmux`, then `screen`, then `setsid -f`:

```sh
flows/run-detached.sh flow-11-dual-stack.sh
# prints: tmux session: flow-flow-11-dual-stack-<pid>
#         /path/to/repo/.tmp/flow-11-dual-stack-YYYYMMDD-HHMMSS.log
tail -F /path/to/.tmp/flow-11-dual-stack-*.log
```

Reattach with `tmux attach -t <session>` (or `screen -r <session>`). The log
file path is the second line of stdout — capture it for monitoring.
