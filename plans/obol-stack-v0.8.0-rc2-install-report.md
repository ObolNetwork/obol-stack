# Obol Stack v0.8.0-rc2 Installation Report

**Machine:** Intel Xeon E-2288G (8c/16t), 32GB RAM, no GPU, 1.6TB disk, Ubuntu Linux
**Previous version:** Fresh install (purged v0.8.0-rc1)
**Target version:** v0.8.0-rc2
**Date:** 2026-04-09
**Install method:** Claude Code assisted

---

## Fixed from v0.8.0-rc1

- **Ollama prompt non-interactive crash** — installer no longer prompts for Ollama install. Detects existing install cleanly.
- **Frontend chat works** — sending a message to the agent in the Obol Stack frontend now returns a real response. The model name bug from rc1 is resolved.
- **ServiceOffer reconciliation** — new `serviceoffer-controller` in the `x402` namespace reconciles offers to `READY: True` automatically (was stuck in rc1).
- **`obol sell http` end-to-end** — creates ServiceOffer, tunnel, payment gate middleware, and HTTPRoute. Returns proper HTTP 402 with x402 payment requirements over Cloudflare tunnel. **Fully working.**
- **Bootstrap attempts to fix Ollama bind address** — tries to apply `OLLAMA_HOST=0.0.0.0` automatically (still requires sudo, but at least it tries).

---

## Issues Encountered

### 1. Bootstrap PATH prompt crashes in non-interactive shells

**Severity:** Medium — new in rc2 (replaces the rc1 Ollama prompt crash with a similar bug elsewhere).
**What happened:** The installer detects `~/.local/bin` is already on `PATH` but still tries to prompt about modifying `.bashrc`, then crashes on `/dev/tty`.
**Error:** `/dev/fd/63: line 1637: /dev/tty: No such device or address`
**Workaround:** Re-run after the binary is downloaded — it's already installed by the time it crashes.
**Suggested fix:** Skip the PATH prompt entirely if `~/.local/bin` is already on `PATH`. Same fix pattern as the rc1 Ollama prompt fix.

---

### 2. Port 80/443 conflict still blocks cluster creation

**Severity:** High — carried over from rc0/rc1, **still not fixed** despite PR #318 mentioning "443 connectivity issues".
**Error:** `failed to bind host port 0.0.0.0:443/tcp: address already in use` (Tailscale binds 443).
**Workaround:** Edit `~/.config/obol/k3d.yaml` — remove the `80:80` and `443:443` port mappings, keep `8080` and `8443`.
**Downstream impact:** This workaround later breaks `obol sell register` (see issue #6) because the CLI assumes port 80 is bound for eRPC.

---

### 3. Ollama unreachable from cluster (endpoint IP wrong)

**Severity:** High — carried over from rc0/rc1, **partial fix** in rc2.

**3a. Bind address** — bootstrap now tries to set `OLLAMA_HOST=0.0.0.0` via `sudo systemctl`. Improvement, but fails silently in non-interactive mode and prints "non-fatal" warning.

**3b. Endpoint IP hardcoded** — still hardcoded to `172.17.0.1` in the cluster manifests, but the actual k3d gateway is `172.18.0.1`. **Not fixed.**
**Workaround:** `obol kubectl patch endpoints ollama -n llm --type='json' -p='[{"op":"replace","path":"/subsets/0/addresses/0/ip","value":"172.18.0.1"}]'`

---

### 4. Volume permissions block OpenClaw onboarding entirely

**Severity:** High — carried over from rc0/rc1, **still not fixed.** Now blocks the install at a worse point.
**What happened:** `obol stack up` got further than rc1 — it created the cluster, deployed all infrastructure, and started trying to provision the OpenClaw wallet. Then failed:
**Error:** `keystore provisioning failed: write keystore: open /home/clawd/.local/share/obol/openclaw-obol-agent/remote-signer-keystores/...json: permission denied`
**Impact:** Default OpenClaw instance is **never created** during `obol stack up`. The user has to manually run `obol openclaw onboard` after fixing permissions, which generates a new deployment ID (e.g. `awake-glider`), invalidating any prior `/etc/hosts` entries.
**Workaround:** `sudo chown -R $(id -u):$(id -g) ~/.local/share/obol/` then `obol openclaw onboard`.
**Suggested fix:** Run an init container as root with `chown` before the OpenClaw container starts, or use `securityContext.fsGroup`.

---

### 5. `obol sell inference` still fails on Linux — Secure Enclave

**Severity:** High — carried over from rc1, **not fixed**.
**What happened:** Same Apple Secure Enclave check at the end of `obol sell inference`. Everything before it succeeds (ServiceOffer, tunnel) but the inference proxy never starts.
**Error:** `enclave SIP check failed: enclave: Secure Enclave not supported on this platform`
**Workaround:** Use `obol sell http` with Ollama as the upstream service — works fully.
**Suggested fix:** Fall back to the in-cluster `remote-signer` (already deployed) for non-Apple platforms.

---

### 6. `obol sell register` direct registration assumes port 80

**Severity:** Medium — new in rc2.
**What happened:** When trying to register an agent on ERC-8004 directly via the remote-signer, the CLI tries to reach the eRPC service at `http://localhost/rpc/base-sepolia` (port 80). Since we had to remap Traefik to port 8080 (issue #2), this fails immediately.
**Error:** `connect to base-sepolia via eRPC: dial tcp 127.0.0.1:80: connect: connection refused`
**Suggested fix:** Either honor the actual Traefik port from the k3d config, or proxy the eRPC call through `obol kubectl` instead of assuming a host port.

---

### 7. Sponsored ERC-8004 registration service unreachable

**Severity:** Low/External — new in rc2, possibly transient.
**What happened:** `obol sell register --chain mainnet --sponsored` reached out to `https://sponsored.howto8004.com/api/register` which timed out.
**Error:** `sponsor request failed: Post "https://sponsored.howto8004.com/api/register": context deadline exceeded`
**Note:** May be a transient infra issue with the sponsorship service.

---

### 8. Frontend has no `/sell` UI page

**Severity:** Medium — new in rc2.
**What happened:** The new monetization features ship in rc2 with backend reconciliation working (controller, API routes), but there's no UI page for it. `/sell` returns 404. Only `/api/sell/list` exists.
**Impact:** Users can't see, manage, or create sell offers from the Obol Stack frontend — they have to use the CLI.
**Suggested fix:** Add a `/sell` page that displays `obol sell list` results and allows creating new offers.

---

## Summary

| # | Issue | Severity | Status vs rc1 |
|---|-------|----------|---------------|
| 1 | Bootstrap PATH prompt non-interactive crash | Medium | New (replaces fixed rc1 Ollama crash) |
| 2 | Port 80/443 conflict | High | Not fixed |
| 3 | Ollama unreachable (bind + endpoint IP) | High | Partial fix on bind, IP still wrong |
| 4 | Volume permissions block OpenClaw onboarding | High | Worse — now blocks default agent setup |
| 5 | `obol sell inference` Secure Enclave | High | Not fixed |
| 6 | `obol sell register` assumes port 80 | Medium | New (downstream of #2) |
| 7 | Sponsored ERC-8004 service unreachable | Low | New, possibly transient |
| 8 | No `/sell` UI page in frontend | Medium | New |

---

## What Works End-to-End

**This is the first release where the core sell flow actually works.** Verified:

- `obol sell http` creates a fully reconciled ServiceOffer with payment gate
- Cloudflare tunnel exposes the offer at a public URL
- Hitting the public endpoint returns proper HTTP 402 with x402 payment requirements:
  - Asset: USDC on Base Sepolia (`0x036CbD53842c5426634e7929541eC2318f3dCF7e`)
  - Pay-to: agent's auto-generated wallet
  - Price: as specified (e.g. 0.001 USDC per request)
- The new `serviceoffer-controller` correctly reconciles offers through all stages: ModelReady → UpstreamHealthy → PaymentGateReady → RoutePublished → Ready
- Frontend chat with the in-cluster OpenClaw agent works (using `ollama/qwen3:0.6b` for tool-calling on CPU)

**4 high-severity issues persist** from earlier releases. None of them are in the new code — they're all in the install/bootstrap path or in the platform compatibility layer (Secure Enclave, port assumptions, file ownership). The actual product (sell, payment gate, tunnel, controller) is in good shape.
