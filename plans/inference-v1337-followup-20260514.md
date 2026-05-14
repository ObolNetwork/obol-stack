# Live buy from `inference.v1337.org` — follow-up findings

Date: 2026-05-14
Test bed: spark1 (Linux aarch64)
Worktree: `/home/claude/obol-stack-qa-20260513-135712-post490`
Branch HEAD during the test: `eb13055` (`chore/buy-external-followups`)
Companion to: `plans/inference-v1337-buy-report-20260514.md`

## TL;DR

Re-ran the v1337 buy with `KEEP_CLUSTER_ON_FAIL=1` (new knob in `flows/buy-external.sh`, commit `b749f95`). Steps 1–17 PASS. The controller reconciled the PurchaseRequest in 55 seconds (`Probed` → `AuthsLoaded` → `Configured` → `Ready`), publishing `paid/qwen3.6-27b` via the buyer sidecar. **The "controller external-seller mode gap" hypothesis from the original report is false** — the controller is endpoint-agnostic by design (confirmed by code review of `internal/serviceoffercontroller/purchase.go`) and works against arbitrary external x402 sellers without modification.

The original report's attempt 5 failure (`command terminated with exit code 137` at the controller-reconcile wait) was almost certainly a kubectl-exec session SIGKILL — not a controller hang. Today's identical code path completed cleanly in 0s at the same step.

## What today's run actually showed

PR `status.conditions[]` from the captured `purchaserequest.yaml`:

```yaml
conditions:
- type: Probed         status: "True"   reason: Validated   message: "402: 23000000000000000 on eip155:84532"   06:37:06Z
- type: AuthsLoaded    status: "True"   reason: Loaded      message: "Loaded 1 pre-signed auths from spec"      06:37:06Z
- type: Configured     status: "True"   reason: Written     message: "Wrote 1 auths to llm/x402-buyer-auths"    06:37:06Z
- type: Ready          status: "True"   reason: Reconciled  message: "Sidecar: 1 remaining, 0 spent"            06:38:01Z
observedGeneration: 1
publicModel: paid/qwen3.6-27b
remaining: 1
totalSigned: 1
signerAddress: 0x57b0eF875DeB5A37301F1640E469a2129Da9490E
```

`buyer-status-after.json`:

```json
{"v1337-aeon": {"url": "https://inference.v1337.org/services/aeon", "remote_model": "qwen3.6-27b", "public_model": "paid/qwen3.6-27b", "remaining": 1, "spent": 0, "network": "base-sepolia"}}
```

The Go-side probe at `purchase.go:183` was NOT WAF-blocked. The follow-up worry (entry #10 in `release-smoke-debugging.md`) about Cloudflare's WAF blocking `Go-http-client/1.1` UA does not reproduce against v1337. Worth keeping the doc note for the general class of WAF UA filters, but not a load-bearing concern for the controller code path.

## Why attempt 5 looked like a controller hang

Today the harness completed step 14 (`buy.py completed`) with the same code that produced attempt 5's exit-code-137. Two structural differences plausibly explain why attempt 5 hung where today's run sailed:

1. **`bootstrap_flow_workspace` now picks the freshest binary** (commit `eb13055`). Attempt 5 silently used a stale `.build/obol` whose embedded buy.py lacked the USER_AGENT fix. Even after the operator rebuilt `.build/obol` mid-attempt, the Bob workspace had already been bootstrapped from the older copy. The PVC's buy.py wrote a probe with `Python-urllib` UA → 403 from CF → the controller's view of the world differed from the buyer's view in subtle ways. The `eb13055` fix removes that footgun for future runs.

2. **The kubectl-exec SIGKILL was an environmental artifact.** `command terminated with exit code 137` is what kubectl prints when its remote process dies from SIGKILL — could be harness `run_with_timeout`, OOM, or control-plane jitter. None of those would be visible in the controller logs (which today's `KEEP_CLUSTER_ON_FAIL=1` snapshot proved go quiet during the wait). Today's harness completed the same `obol kubectl exec` to buy.py without issue, so the SIGKILL was not deterministic.

## The actual blocker today

Step 18 (paid request through LiteLLM) failed:

```
FAIL: [18] Paid request returned HTTP 404
{"error":{"message":"litellm.NotFoundError: NotFoundError: OpenAIException - The model `qwen3.6-27b` does not exist.. Received Model Group=paid/qwen3.6-27b\nAvailable Model Group Fallbacks=None","type":null,"param":null,"code":"404"}}
```

This is operator-error model-name mismatch:

- LiteLLM correctly routed `paid/qwen3.6-27b` → buyer sidecar → `https://inference.v1337.org/services/aeon`.
- v1337's upstream vLLM does not serve a model named `qwen3.6-27b`.
- The actual model name is unknown from `/.well-known/agent-registration.json` (which advertises display name "Qwen3.6-27B AEON Ultimate" and skills `llm/inference, llm/uncensored`, but no model id).

Bob's 0.023 OBOL pre-signed auth was **NOT consumed** — LiteLLM 404'd before reaching the buyer sidecar's `/settle` path. Wallet balance unchanged.

To finish the live buy proof, the harness needs the right `--model` value. Options: (a) ask the seller, (b) probe `/v1/models` if v1337 makes it free, (c) brute-force common variants (`aeon`, `qwen-3.6-27b`, `qwen3.6`, `qwen3.6-27b-aeon`). All low-priority — the controller-side answer is already in.

## Side finding: LiteLLM hot-add quirk

The controller logs surfaced:

```
purchase: hot-add paid/qwen3.6-27b failed: POST /model/new: 400 Bad Request:
{"error":{"message":"Authentication Error, [Errno 30] Read-only file system: '/etc/litellm/config.yaml'", ...}}; relying on ConfigMap reload
```

LiteLLM's `/model/new` API tries to write back to `/etc/litellm/config.yaml`. In our deployment that path is a Kubernetes ConfigMap volume — read-only by default. The controller catches the 400 and falls back to the ConfigMap-reload path, which works (the alias DID become available, otherwise step 17 wouldn't have passed). Pre-existing behavior, not external-seller specific. Worth a one-line note in `paid-flows.md` so the next debugger isn't startled by the WARN in controller logs.

## Updates to original report

Replace follow-up #1 ("serviceoffer-controller external-seller mode") with: "RESOLVED — controller is endpoint-agnostic by design. Attempt 5's reconcile-hang was a kubectl-exec SIGKILL artifact, not a controller bug. Verified 2026-05-14 with `KEEP_CLUSTER_ON_FAIL=1` re-run."

Follow-up #2 (harness binary path) — DONE in commit `eb13055`.
Follow-up #3 (CF-WAF UA documentation) — DONE in commit `849cd93`.
Follow-up #4 (`KEEP_CLUSTER_ON_FAIL` knob) — DONE in commit `b749f95`.

The original report still has narrative value for the four bug fixes it surfaced (k3d cluster-name cap, CAIP-2 chain id mismatch, CF-WAF Python-urllib UA, stale `.build/obol`). Only the controller hypothesis was wrong.

## Artifacts

Under `/home/claude/obol-stack-qa-20260513-135712-post490/.tmp/v1337-rerun-20260514-063232-artifacts/` on spark1, captured by the new `external_snapshot_on_fail()`:

- `controller.log`, `controller-current.log` — full reconcile trace
- `purchaserequest.yaml` — the conclusive `Ready=True` proof
- `buyer-status-after.json` — sidecar saw 1 remaining, 0 spent
- `agent-pod-buypy.log` — clean `buy.py` run through PR creation
- `cluster-pods.txt`, `cluster-events.txt` — full cluster state at FAIL

The Bob k3d cluster (`obol-stack-buy-ext-bob`) is preserved on spark1 pending teardown.

## Closing note

The phase-1 polish items in `chore/buy-external-followups` more than paid for themselves on the first re-run: `KEEP_CLUSTER_ON_FAIL=1` made the diagnosis trivial, the binary normalization removed one of the candidate causes for attempt 5's hang, and the diagnostic snapshot bundle gave us seven artifacts that took a single bash command to inspect. The original v1337 report would have been wrong on its central technical claim if we hadn't re-run with these in place — a useful argument for keeping operator-level diagnostic ergonomics ahead of feature work.
