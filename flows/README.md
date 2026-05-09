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
- `flow-13-dual-stack-obol.sh` — Dual-Stack OBOL: Alice sells, Bob discovers and buys, but the payment asset is a fork-local OBOL ERC20Permit token and the facilitator is local (not the public Obol facilitator). Use this when you want to validate the OBOL Permit2 path end-to-end without depending on the public Obol facilitator or any USDC contract. Both obol stacks share ONE local Anvil fork of Base Sepolia via `host.k3d.internal:$ANVIL_PORT`. Requires `cast` + `anvil` + `forge`; the local facilitator runs as `ghcr.io/x402-rs/x402-facilitator:1.4.7`.
- `flow-14-live-obol-base-sepolia.sh` — Live Base Sepolia OBOL dual-stack: Alice registers/sells, Bob discovers/buys, and settlement is verified against the freshly deployed Base Sepolia OBOL ERC20Permit token and public Obol facilitator. Requires `REMOTE_SIGNER_PRIVATE_KEY` funded with Base Sepolia ETH and the second deterministic derived Bob key funded with OBOL; `OBOL_TOKEN_BASE_SEPOLIA` defaults to the fresh faucet-backed Base Sepolia OBOL token (`0x0a09371a8b011d5110656ceBCc70603e53FD2c78`; source of truth: ObolNetwork/obol-stack#447) and can be overridden.
- `flow-15-live-obol-faucet-alice-bob.sh` — Faucet-backed live OBOL smoke: claims official Base Sepolia faucet OBOL for Bob, checks Bob has buyer gas, then delegates to flow-14 for the Alice/Bob commerce loop.
- `flow-16-sell-agent.sh` — Agent ServiceOffer smoke: declare an Agent CRD, publish it via `obol sell agent`, and verify the x402 metadata surface for agent-backed paid routes.

`lib.sh` is shared baseline flow plumbing; `lib-dual-stack.sh` is shared
Alice/Bob orchestration for the dual-stack seller/buyer flows; `flows/tools/*`
holds small structured helpers used by the shell entrypoints. Keep new
`flow-NN-*.sh` scripts focused on the scenario, assertions, and environment
contract rather than duplicating stack, DNS, wallet, or config-mutation helpers.
`release-smoke.sh` is the release gate.

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
