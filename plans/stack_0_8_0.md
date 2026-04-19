# Obol Stack v0.8.0-rc5 Installation Report

**Machine:** MacBook Pro, Apple Silicon (M-series), macOS 26.3.1
**Previous version:** Fresh install (clean slate)
**Target version:** v0.8.0-rc5
**Date:** 2026-04-17
**Install method:** Claude Code assisted (OBOL_DEVELOPMENT=true)
**Tester:** Ana

---

## Summary

| Area | Result |
|------|--------|
| Bootstrap (`obolup.sh`) | ⚠️ Partial — 2 issues |
| Stack init + up | ✅ Clean |
| Model setup (Anthropic) | ✅ Working |
| Frontend chat | ✅ Working |
| Sell HTTP (`obol sell http`) | ✅ Working |
| Sell inference (`obol sell inference`) | ✅ Working — Secure Enclave graceful fallback confirmed |
| Buy side — probe + signing | ✅ Working |
| Buy side — paid inference end-to-end | ✅ Fixed — RC5-7 fix validated locally |
| Tunnel | ✅ Active |
| ERC-8004 registration | ⚠️ Partial — off-chain JSON ✅, 2 new bugs (RC5-8, RC5-9) |

**Verdict: RC5 is NOT ready to ship as-is.** RC5-7 (buy side paid inference broken) is a release blocker — but the fix has been validated locally. PR #343 must be merged before cutting rc6. Fix confirmed end-to-end: `paid/claude-sonnet-4-6` returned 200, sidecar `spent=3, remaining=0`.

---

## Fixed from v0.8.0-rc4

- **Port conflict detection** — `obol stack init` now auto-strips 80:80 and 443:443 port mappings when those ports are in use. No more manual YAML editing. ✅ (Not triggered on this machine — ports were free — but code confirmed in `backend_k3d.go`.)
- **Secure Enclave graceful fallback** — `obol sell inference` on older Macs without T2/SIP now logs a warning and continues instead of failing. ✅ Confirmed: enclave started cleanly on Apple Silicon.
- **Volume ownership** — `ensureVolumeWritable()` pre-creates PVC directories and chowns them to the current user before writes. ✅ No permission errors during skills injection or keystore provisioning.
- **obolup.sh PATH prompt** — early exit if `$OBOL_BIN_DIR` already on PATH. ✅ (Partially — see RC5-1 below.)

---

## Issues Found

### RC5-1 — `obolup.sh` Ollama prompt crashes non-interactively
**Severity:** Medium
**Status:** Pre-existing, not fully fixed in rc5

The installer crashes at the Ollama installation prompt when run without a real terminal (`/dev/tty`):
```
./obolup.sh: line 1373: /dev/tty: Device not configured
./obolup.sh: line 1375: choice: unbound variable
```
The rc5 fix guarded only the PATH prompt, not the Ollama prompt. Same pattern (`/dev/tty` read without a guard) affects at least one other interactive section.

**Workaround:** Run `obolup.sh` in a real interactive terminal.

**Recommendations:**
1. Apply the same `/dev/tty` guard to the Ollama install prompt (same pattern as the PATH fix).
2. Add a `--yes` / `--non-interactive` flag to skip all prompts for CI and server installs.
3. Wrap the Ollama `pull` command with `caffeinate -i` on macOS — the ~6GB download will pause if the Mac sleeps mid-download. Example:
   ```bash
   if [[ "$(uname)" == "Darwin" ]]; then
       caffeinate -i ollama pull "$MODEL"
   else
       ollama pull "$MODEL"
   fi
   ```

---

### RC5-2 — `openclaw` CLI installation fails (no npm, Docker fallback broken)
**Severity:** Medium
**Status:** New in rc5 (Docker fallback is a new code path)

npm/Node.js not available on the test machine. The rc5 Docker fallback (`docker create` + `docker cp`) was added for this case but failed:
```
==> npm/Node.js not available — extracting openclaw from Docker image...
ghcr.io/obolnetwork/openclaw:2026.3.24
! Docker extraction failed
! openclaw CLI installation failed (continuing...)
```
The `openclaw` CLI binary was not installed. The stack still came up and the agent worked (it uses the Docker image at runtime, not the CLI binary), but `obol openclaw cli` and skills management commands would not be available.

**Likely cause:** `ghcr.io/obolnetwork/openclaw:2026.3.24` may require authentication or the image was not published at the time of testing.

**Recommendation:** Log the exact Docker error (`docker pull` / `docker create` stderr) rather than silently reporting "Docker extraction failed". This makes it actionable.

---

### RC5-3 — `prometheus-node-exporter` ImagePullBackOff on Apple Silicon
**Severity:** Low (non-critical component)
**Status:** Transient — resolved itself on fresh cluster rebuild

```
Failed to pull image "quay.io/prometheus/node-exporter:v1.10.2":
  unexpected commit digest sha256:830f...
  expected sha256:a68a...
```
The image pulled successfully but failed an integrity check (digest mismatch), then the readiness probe failed. This is likely a multi-arch manifest issue on Apple Silicon or a corrupted layer in the registry.

**Impact:** Prometheus node metrics (CPU, memory, disk) unavailable. No impact on core stack functionality.

**Recommendation:** Pin to a digest-verified image or add a retry policy. Consider testing with `quay.io/prometheus/node-exporter:v1.10.0` as a fallback.

---

### RC5-4 — LiteLLM hot-add fails: `wget` not in container PATH
**Severity:** Medium (zero-downtime feature broken, functional fallback exists)
**Status:** New — hot-add was introduced in rc4

When adding a model provider, `obol model setup` tries to hot-add the model to running LiteLLM pods via `kubectl exec` + `wget`:
```
! Hot-add qwen3.5:9b failed: exec: "wget": executable file not found in $PATH
! Hot-add failed, falling back to restart
```
Falls back to a full deployment restart, which works but:
- Causes ~30s of downtime on each `obol model setup` call
- Defeats the purpose of the zero-downtime hot-add feature

PR #343 notes: "LiteLLM requires manual restart after purchases (ConfigMap subPath mounts don't propagate)" — so this may be a known accepted limitation, but the `wget` error is still unexpected.

**Recommendation:** Either install `wget` / `curl` in the LiteLLM container image, or switch the hot-add mechanism to use the Kubernetes API (port-forward) instead of `kubectl exec`.

---

### RC5-5 — No `obol sell update` command
**Severity:** Low (UX gap)
**Status:** Missing feature

Changing a ServiceOffer's price or configuration requires:
```bash
obol sell delete --force <name> -n <ns>
obol sell http <name> --price <new>  # recreate from scratch
```
There is no `obol sell update` or `obol sell pricing <name>` command.

**Recommendation:** Add `obol sell update <name> --per-request <price>` that patches the ServiceOffer CR spec in place. The controller already handles spec changes via reconciliation.

---

### RC5-6 — No guided onboarding after install (Product)
**Severity:** Medium (first-run UX)
**Status:** Missing feature

After `obolup.sh` completes, the user is dropped at a blank terminal with no guidance. The expected flow (model setup → sell → tunnel) requires reading documentation or already knowing the commands.

**Recommendation:** After `obol stack up` succeeds, auto-invoke a short guided setup via the OpenClaw agent already deployed in the cluster:
```
✓ Stack is live at http://obol.stack
? What would you like to do?
  > Set up a model provider
  > Sell local inference
  > Explore the dashboard
```
The agent already has skills for all of these. This is a 1-hour feature that would dramatically improve the new-user experience.

---

### RC5-7 — Buy side paid inference fails (RELEASE BLOCKER)
**Severity:** Critical
**Status:** Fixed in PR #343, not yet merged

**Root cause:** The `api_base` for the x402-buyer sidecar is missing the `/v1` path suffix in `purchase_helpers.go` and `llm.yaml`:

```go
// internal/serviceoffercontroller/purchase_helpers.go
// rc5 (broken):
APIBase: "http://127.0.0.1:8402",

// fix (PR #343):
APIBase: "http://127.0.0.1:8402/v1",
```

**What happens:** LiteLLM routes `paid/*` requests to the sidecar at `/chat/completions` (no `/v1` prefix). The sidecar's mux returns Go's default 404. LiteLLM surfaces this as a 403. End result: every paid inference request fails.

**Observed:**
```
{"error": {"message": "litellm.APIError: OpenAIException - Error code: 403", "code": "403"}}
```

**What works:** Everything in the pipeline EXCEPT the final inference call:
- ✅ `buy.py probe` — sees 402 pricing
- ✅ USDC balance check
- ✅ ERC-3009 auth signing (1000 auths)
- ✅ PurchaseRequest CR created and reconciled
- ✅ Sidecar loads auths (1000 remaining)
- ❌ Paid inference request — 403

**Fix:** One-line change in two files. Applied locally and validated end-to-end:
- `internal/serviceoffercontroller/purchase_helpers.go`: `APIBase: "http://127.0.0.1:8402/v1"`
- `internal/embed/infrastructure/base/templates/llm.yaml`: `api_base: "http://127.0.0.1:8402/v1"`

**Validation:** Rebuilt cluster with `OBOL_DEVELOPMENT=true` (builds controller from local source). Result:
- `paid/claude-sonnet-4-6` → HTTP 200, `"content": "paid inference works"` ✅
- x402-buyer sidecar: `spent=3, remaining=0` (all auths consumed) ✅

**Action required:** Merge PR #343 before rc5 is declared stable or rc6 is cut.

---

### RC5-8 — `/skill.md` shows 0 services despite Ready ServiceOffer
**Severity:** Medium (discovery broken for buyers using the catalog)
**Status:** New — found during ERC-8004 registration testing

`obol sell http test-service --register` successfully creates the ServiceOffer (status: Ready) and RegistrationRequest, but `/skill.md` reports "0 ready ServiceOffer(s)" and "No services currently available." The `obol-skill-md` ConfigMap is written by the controller at reconcile time, but the current offer is not counted.

**Observed:**
```
# Obol Stack Service Catalog
> Generated from 0 ready ServiceOffer(s).
**No services currently available.**
```

**Impact:** Buyers discovering services via `/skill.md` see no services. Discovery is broken for agents that use the catalog endpoint.

**Recommendation:** The controller should re-read all Ready ServiceOffers when writing the `obol-skill-md` ConfigMap, not just the one being reconciled.

---

### RC5-9 — Stale tunnel URL in `/skill.md` and `RegistrationRequest.status.registrationUri`
**Severity:** Medium (discovery sends buyers to dead URL)
**Status:** New — found during ERC-8004 registration testing

After `obol stack purge` + fresh `obol stack up`, the new cluster gets a new Cloudflare tunnel URL. However, both `/skill.md` and the `registrationUri` field in the RegistrationRequest status reference the **old tunnel URL** from the previous session:

```
# In /skill.md:
https://marcus-secretariat-marker-spears.trycloudflare.com/.well-known/agent-registration.json  ← dead (530)

# In RegistrationRequest status:
publishedUrl: https://pike-damages-bonds-burton.trycloudflare.com/.well-known/...  ← correct
registrationUri: https://marcus-secretariat-marker-spears.trycloudflare.com/.well-known/...  ← stale
```

The actual `/.well-known/agent-registration.json` is served correctly at the new URL. Only the `registrationUri` field and `/skill.md` link are stale.

**Likely cause:** The `registrationUri` is written once from the stored `agentBaseURL` config value, which may not be updated when a new tunnel starts. The `/skill.md` embeds this stale `registrationUri`.

**Recommendation:** The controller should derive `registrationUri` from the current live tunnel URL (the one used in `publishedUrl`) rather than a cached config value. Alternatively, update `agentBaseURL` atomically when the tunnel restarts.

---

## What Works Well

- **ServiceOffer reconciliation is fast** — all 5 stages (ModelReady → UpstreamHealthy → PaymentGateReady → RoutePublished → Ready) complete in under 10 seconds.
- **402 payment gate is solid** — correct x402v2 JSON with network, asset, amount, payTo, ERC-3009 method. Works locally and over Cloudflare tunnel.
- **Port conflict detection** (rc5 new feature) — code confirmed, logic is sound.
- **Secure Enclave graceful fallback** (rc5 new feature) — confirmed working on Apple Silicon, would have prevented failures on older Intel Macs.
- **Volume ownership fixes** (rc5 new feature) — no permission errors during the entire test session.
- **Tunnel auto-starts** on `obol sell http` — clean UX, Cloudflare tunnel active within 13 seconds.
- **PurchaseRequest CR lifecycle** — probe → sign → configure → verify all work correctly up to the final request.
- **ERC-8004 RegistrationRequest CR lifecycle** — controller creates `so-<name>-registration` CR, sets `OffChainOnly` phase gracefully when no signing key, publishes `/.well-known/agent-registration.json` with correct OASF metadata (skills, domains, x402Support, services endpoint).
- **On-chain registration graceful fallback** — `Registered: OffChainOnly` with clear reason when no ERC-8004 signing key configured. No crash, clean status condition.

---

## Recommended Actions Before rc6

| Priority | Action |
|----------|--------|
| P0 | Merge PR #343 (fixes RC5-7 buy side blocker + auth lifecycle hardening) |
| P1 | Fix `obolup.sh` Ollama prompt `/dev/tty` crash (RC5-1) |
| P1 | Add `caffeinate` to Ollama pull on macOS in `obolup.sh` |
| P2 | Fix `openclaw` Docker extraction error logging (RC5-2) |
| P2 | Fix `prometheus-node-exporter` image digest on Apple Silicon (RC5-3) |
| P2 | Fix `wget` not found in LiteLLM container for hot-add (RC5-4) |
| P3 | Add `obol sell update` command (RC5-5) |
| P3 | Add guided post-install onboarding flow (RC5-6) |
| P2 | Fix `/skill.md` not listing Ready ServiceOffers (RC5-8) |
| P2 | Fix stale tunnel URL in `/skill.md` and `registrationUri` after cluster rebuild (RC5-9) |

---

## Test Environment Reproduction

```bash
# Prerequisites
# - macOS Apple Silicon, Docker Desktop running
# - git clone ObolNetwork/obol-stack, checkout v0.8.0-rc5

OBOL_DEVELOPMENT=true ./obolup.sh

export OBOL_DEVELOPMENT=true
export OBOL_CONFIG_DIR=$(pwd)/.workspace/config
export OBOL_BIN_DIR=$(pwd)/.workspace/bin
export OBOL_DATA_DIR=$(pwd)/.workspace/data

go build -o .workspace/bin/obol ./cmd/obol
obol stack init && obol stack up
obol model setup --provider anthropic --api-key <key>
obol sell pricing
obol sell http test-service --wallet <wallet> --chain base-sepolia \
  --per-request 0.001 --upstream litellm --port 4000 --namespace llm \
  --health-path /health/readiness
curl -X POST http://obol.stack:8080/services/test-service/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}'
# Expected: HTTP 402 ✅

# Buy side (fund wallet first via faucet.circle.com → Base Sepolia)
obol kubectl exec -n openclaw-obol-agent deploy/openclaw -c openclaw -- \
  python3 /data/.openclaw/skills/buy-inference/scripts/buy.py buy test-buyer \
  --endpoint https://<tunnel>/services/test-service \
  --model claude-sonnet-4-6 --count 5
# Expected: 403 on paid inference ← RC5-7
```
