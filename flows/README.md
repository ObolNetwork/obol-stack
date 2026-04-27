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

`lib.sh` is shared helpers; `release-smoke.sh` is the release gate.

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
